package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	pmodel "github.com/prometheus/common/model"

	"github.com/mattermost/mattermost-plugin-alertmanager/server/alertmanager"

	amconfig "github.com/prometheus/alertmanager/config"
	"github.com/prometheus/alertmanager/dispatch"
)

// cmd_validate.go owns /alertmanager validate — an end-to-end diagnostic
// that confirms each link in the alerting pipeline is wired correctly.
// Channel-scoped (only checks receivers bound to the current channel).
//
// Three independent checks per receiver, two opt-in side-effect tests:
//
//   (a) AM reachability             — GET /api/v2/status, expect 200
//   (b) Receiver loaded in AM       — fetch AM config, search for `name: X`
//   (c) Webhook accepts POSTs       — opt-in via --webhook-test;
//                                     posts a visible test message
//                                     to the channel
//   (d) End-to-end alert delivery   — opt-in via --end-to-end;
//                                     fires a synthetic alert through
//                                     AM, user watches channel for arrival
//
// Default (no flags): only checks (a) and (b). No side effects in chat.
// Opt-in flags trigger side effects so admins choose their level of
// intrusiveness.

// handleValidate is the entry point for /alertmanager validate.
// Usage:
//
//	/alertmanager validate                              # all receivers in this channel
//	/alertmanager validate <name>                       # one receiver by name
//	/alertmanager validate <set>                        # only receivers in a runbook set (compute, application, etc.)
//	/alertmanager validate all                          # alias of no-arg
//	/alertmanager validate --webhook-test               # also POST visible test message per receiver
//	/alertmanager validate --end-to-end                 # also fire synthetic alert through AM
//	/alertmanager validate compute --end-to-end         # combine set filter + flag
//
// Set names take precedence over receiver names (same dispatch rule
// as /alertmanager remove). Runbook slugs don't collide with set
// names so the precedence is safe.
func (p *Plugin) handleValidate(args *model.CommandArgs) (string, error) {
	if err := p.requireChannelTeamAdmin(args.UserId, args.ChannelId); err != nil {
		return err.Error(), nil
	}

	fields := strings.Fields(args.Command)
	rest := fields[2:]

	// --simulate is a sub-mode of validate: instead of probing real
	// receivers, walk the AM route tree with the given label set and
	// report which receivers an alert with those labels would actually
	// dispatch to. Everything AFTER --simulate is the label list, so
	// extract it first and short-circuit the regular validate path.
	if idx := indexOfFlag(rest, "--simulate"); idx >= 0 {
		return p.handleValidateSimulate(args, rest[idx+1:])
	}

	webhookTest := containsFlag(rest, "--webhook-test")
	endToEnd := containsFlag(rest, "--end-to-end")

	// --severity=<value> drives the end-to-end fire matrix.
	// Accepted: warning, critical, info, all. `all` fires four
	// synthetic alerts per receiver (warning + critical + info +
	// resolved). Empty/missing defaults to warning. Validated by
	// expandSeverityForFire — empty return slice = invalid value.
	severityFlag, rest := extractFlagValue(rest, "--severity=")
	rest = stripFlags(rest, "--webhook-test", "--end-to-end")
	fireSpecs := expandSeverityForFire(severityFlag)
	if endToEnd && fireSpecs == nil {
		return fmt.Sprintf(":x: Invalid `--severity=%s` value. Accepted: `warning`, `critical`, `info`, `all`.", severityFlag), nil
	}

	// First positional, if any, is either a set name or a receiver name.
	var target string
	if len(rest) >= 1 {
		target = strings.ToLower(rest[0])
	}

	scoped := p.configsForCurrentChannel(args)
	if len(scoped) == 0 {
		return ":information_source: No receivers bound to this channel — nothing for validate to check.", nil
	}

	switch {
	case target == "" || target == "all":
		// No filter — check every receiver in this channel.
	case isKnownSet(target):
		// Filter to receivers whose base slug is in this set.
		setSlugs := scaffoldSets[target]
		baseSet := make(map[string]bool, len(setSlugs))
		for _, s := range setSlugs {
			baseSet[s] = true
		}
		var filtered []alertConfig
		for _, c := range scoped {
			if baseSet[receiverBaseSlug(c.Name)] {
				filtered = append(filtered, c)
			}
		}
		if len(filtered) == 0 {
			return fmt.Sprintf(":information_source: No `%s`-set receivers bound to this channel.", target), nil
		}
		scoped = filtered
	default:
		// Treat as receiver name. Smart-resolve so short-form names
		// work in the current channel.
		all := p.getConfiguration().AlertConfigs
		resolved := resolveReceiverName(all, rest[0], args.ChannelId, p)
		var matched []alertConfig
		for _, c := range scoped {
			if c.Name == resolved {
				matched = append(matched, c)
				break
			}
		}
		if len(matched) == 0 {
			return fmt.Sprintf("Receiver `%s` is not bound to this channel.", rest[0]), nil
		}
		scoped = matched
	}

	// Run the cheap checks (a, b) on each receiver. AM status is the
	// same for all receivers bound to the same AM URL — cache the
	// status response per unique AM URL.
	type statusCache struct {
		ok         bool
		statusText string
		configBody string // raw YAML text from AM, used by check (b)
	}
	amStatus := make(map[string]statusCache)
	for _, c := range scoped {
		if _, seen := amStatus[c.AlertManagerURL]; seen {
			continue
		}
		amStatus[c.AlertManagerURL] = doValidateAMStatus(c.AlertManagerURL)
	}

	type rowResult struct {
		Name            string
		AMReach         string // "✓" / "✗ <error>"
		LoadedInAM      string
		WebhookAccepts  string // empty if check skipped
		EndToEndAlertID string
	}
	results := make([]rowResult, 0, len(scoped))
	for _, c := range scoped {
		r := rowResult{Name: c.Name}

		// (a) AM reach
		st := amStatus[c.AlertManagerURL]
		if st.ok {
			r.AMReach = "✓"
		} else {
			r.AMReach = "✗ " + st.statusText
		}

		// (b) Receiver loaded in AM — only meaningful if (a) passed
		if st.ok {
			needle := "name: " + c.Name
			if strings.Contains(st.configBody, needle) {
				r.LoadedInAM = "✓"
			} else {
				r.LoadedInAM = "✗ not found in AM config"
			}
		} else {
			r.LoadedInAM = "— (AM unreachable)"
		}

		// (c) Webhook accepts POST — opt-in
		if webhookTest {
			webhookURL := p.webhookURLForReceiver(c)
			if err := postValidateTestMessage(webhookURL, c.Name); err != nil {
				r.WebhookAccepts = "✗ " + err.Error()
			} else {
				r.WebhookAccepts = "✓ (test post sent to channel)"
			}
		}

		// (d) End-to-end alert via AM — opt-in. Fires one synthetic per
		// fireSpec. When --severity=all, this is 4 alerts per receiver
		// (warning + critical + info + resolved); otherwise 1. Per-spec
		// errors get aggregated into a single status string.
		if endToEnd && st.ok {
			r.EndToEndAlertID = fireSyntheticMatrix(c.AlertManagerURL, receiverBaseSlug(c.Name), fireSpecs)
		} else if endToEnd {
			r.EndToEndAlertID = "— (AM unreachable)"
		}

		results = append(results, r)
	}

	// Audit log — validate invocation is a privileged read.
	p.auditLog("validate.run", args.UserId, target, args.ChannelId, fmt.Sprintf("checked=%d webhook_test=%v end_to_end=%v", len(results), webhookTest, endToEnd))

	// Render the report.
	var b strings.Builder
	b.WriteString(":medical_symbol: **Validate report for receivers in this channel**\n\n")

	header := "| Receiver | AM reach | Loaded in AM |"
	divider := "|---|---|---|"
	if webhookTest {
		header += " Webhook accepts |"
		divider += "---|"
	}
	if endToEnd {
		header += " End-to-end alert |"
		divider += "---|"
	}
	b.WriteString(header + "\n")
	b.WriteString(divider + "\n")
	for _, r := range results {
		line := fmt.Sprintf("| `%s` | %s | %s |", r.Name, r.AMReach, r.LoadedInAM)
		if webhookTest {
			line += " " + r.WebhookAccepts + " |"
		}
		if endToEnd {
			line += " " + r.EndToEndAlertID + " |"
		}
		b.WriteString(line + "\n")
	}

	b.WriteString("\n")
	b.WriteString("**Legend:** ✓ = passed · ✗ = failed · — = skipped\n\n")
	if !webhookTest {
		b.WriteString("_Run with_ `--webhook-test` _to also POST a marked test message to each webhook (visible in this channel)._\n")
	}
	if !endToEnd {
		b.WriteString("_Run with_ `--end-to-end` _to also fire a synthetic alert through Alertmanager — watch the channel for delivery within ~30s._\n")
	}
	return b.String(), nil
}

// isKnownSet returns true when name matches a known runbook set
// (compute, application, etc.). The scaffoldSets map's nil-value
// entries (`all`) aren't considered "known sets" for validate's
// filtering purposes — `all` is handled as the no-filter case in
// handleValidate's switch.
func isKnownSet(name string) bool {
	slugs, ok := scaffoldSets[name]
	return ok && slugs != nil
}

// handleValidateSimulate walks AM's loaded route tree against a
// supplied label set and reports which receiver(s) would actually
// receive a real alert carrying those labels. Mirrors the semantics
// of `amtool config routes test` — the question is "given my
// Prometheus rule's labels, where would the resulting alert land?"
//
// With no label args, prints a preset list (one common runbook per
// line) so the operator has copy-pasteable starting points instead
// of staring at "Usage: ...". Empty arglist is a discoverability
// path, not an error.
//
// Reads AM's loaded config from the cached probe (no fresh HTTP
// call needed unless the cache is stale — same path
// /alertmanager validate's loaded-in-AM check uses).
func (p *Plugin) handleValidateSimulate(args *model.CommandArgs, simulateArgs []string) (string, error) {
	scoped := p.configsForCurrentChannel(args)
	if len(scoped) == 0 {
		return ":information_source: No receivers bound to this channel — can't simulate routing without a backing Alertmanager.", nil
	}

	if len(simulateArgs) == 0 {
		return formatSimulatePresets(), nil
	}

	labels, parseErr := parseSimulateLabels(simulateArgs)
	if parseErr != nil {
		return fmt.Sprintf(":x: %v\n\nUsage: `/alertmanager validate --simulate <key>=<value> [<key>=<value> ...]`\n\nExample: `/alertmanager validate --simulate runbook=high-cpu-usage severity=warning`", parseErr), nil
	}

	// Same-AM assumption: all receivers in a channel point at one AM
	// (the inventory page enforces this implicitly). Pick the first
	// receiver's AM URL; if a channel actually has receivers across
	// multiple AMs, we'd want a per-AM simulation — out of scope here.
	amURL := scoped[0].AlertManagerURL
	entry := p.probeAMReachability(amURL)
	if !entry.Reachable {
		return fmt.Sprintf(":x: Alertmanager at `%s` is unreachable (%s). Can't simulate without a live config.", amURL, entry.Status), nil
	}
	if entry.ConfigBody == "" {
		return fmt.Sprintf(":x: Alertmanager at `%s` responded but didn't return its loaded config in /api/v2/status (older AM version?). Can't simulate without it.", amURL), nil
	}

	cfg, err := amconfig.Load(entry.ConfigBody)
	if err != nil {
		return fmt.Sprintf(":x: AM's loaded config doesn't parse: `%v`. Simulation is unreliable on broken configs.", err), nil
	}
	if cfg.Route == nil {
		return ":x: AM's loaded config has no `route:` block — nothing to simulate.", nil
	}

	mainRoute := dispatch.NewRoute(cfg.Route, nil)
	matches := mainRoute.Match(labels)

	var b strings.Builder
	b.WriteString(":mag: **Route simulation result**\n\n")
	b.WriteString(fmt.Sprintf("Against Alertmanager: `%s`\n\n", amURL))
	b.WriteString("**Input alert labels:**\n")
	// Render labels in sorted key order so the same input always
	// produces the same output (label maps iterate randomly otherwise).
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "- `%s` = `%s`\n", k, string(labels[pmodel.LabelName(k)]))
	}
	b.WriteString("\n")

	if len(matches) == 0 {
		b.WriteString(":warning: **NO sub-routes matched.** Alert would fall through to the default receiver:\n\n")
		fmt.Fprintf(&b, "- `%s`\n\n", cfg.Route.Receiver)
		b.WriteString("If you expected this alert to hit a specific runbook receiver, check that your rule emits the `runbook=<slug>` label (or whatever label your `route.routes[].matchers:` block keys on).")
		return b.String(), nil
	}

	fmt.Fprintf(&b, "**Would dispatch to %d receiver(s):**\n", len(matches))
	for _, m := range matches {
		fmt.Fprintf(&b, "- `%s`\n", m.RouteOpts.Receiver)
	}

	// Cross-check: do those receivers exist in AM's loaded config?
	// In principle they should (we just parsed it), but AM allows
	// routes that reference undefined receivers in some edge cases.
	loaded := extractAMReceiverNames(entry.ConfigBody)
	loadedSet := make(map[string]bool, len(loaded))
	for _, n := range loaded {
		loadedSet[n] = true
	}
	for _, m := range matches {
		if !loadedSet[m.RouteOpts.Receiver] {
			fmt.Fprintf(&b, "\n:warning: Receiver `%s` is referenced by a route but NOT defined in AM's receivers list — alerts dispatched to it will fail. Fix the receiver definition or the route's `receiver:` field.\n", m.RouteOpts.Receiver)
		}
	}

	return b.String(), nil
}

// parseSimulateLabels turns CLI-style `key=value` args into a
// Prometheus label set. Validates each pair and returns a clear
// error on malformed input — operators typing this at 3am benefit
// from "got `foo` (no `=`)" over a generic parse failure.
func parseSimulateLabels(args []string) (pmodel.LabelSet, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no labels supplied")
	}
	ls := pmodel.LabelSet{}
	for _, a := range args {
		eq := strings.IndexByte(a, '=')
		if eq < 1 || eq == len(a)-1 {
			return nil, fmt.Errorf("invalid label %q (expected `key=value`)", a)
		}
		name := pmodel.LabelName(a[:eq])
		value := pmodel.LabelValue(a[eq+1:])
		// LegacyValidation matches the historical IsValid() rules: starts
		// with [A-Za-z_], rest [A-Za-z0-9_]. The newer UTF8Validation
		// would accept Unicode but would break Alertmanager v0.x backends.
		if !pmodel.LegacyValidation.IsValidLabelName(string(name)) {
			return nil, fmt.Errorf("invalid label name %q (must match Prometheus label name rules: starts with [A-Za-z_], rest [A-Za-z0-9_])", a[:eq])
		}
		ls[name] = value
	}
	return ls, nil
}

// formatSimulatePresets returns a list of runbook-slug starter
// expressions the operator can copy-paste. Discovered from the
// embedded runbook list — kept in sync with whatever runbooks
// ship in the plugin without a separate registry to maintain.
func formatSimulatePresets() string {
	slugs := runbookSlugs()
	var b strings.Builder
	b.WriteString(":mag: **Route simulation — pick a starter to try**\n\n")
	b.WriteString("Run with one or more `key=value` labels to see which receiver an alert with those labels would route to. Use one of these runbook slugs as a starting point:\n\n")
	b.WriteString("```\n")
	for _, slug := range slugs {
		fmt.Fprintf(&b, "/alertmanager validate --simulate runbook=%s\n", slug)
	}
	b.WriteString("```\n\n")
	b.WriteString("Or supply your own label set with anything Prometheus emits:\n\n")
	b.WriteString("- `/alertmanager validate --simulate severity=critical service=api-gateway`\n")
	b.WriteString("- `/alertmanager validate --simulate alertname=PodCrashLoopBackOff namespace=billing`\n")
	b.WriteString("- `/alertmanager validate --simulate runbook=high-cpu-usage severity=warning`\n\n")
	b.WriteString("The simulation walks Alertmanager's currently-loaded route tree (fetched live via `/api/v2/status`) — no synthetic alert is actually fired, so this is safe to run as often as you want.")
	return b.String()
}

// indexOfFlag returns the position of the named flag in args, or -1
// if absent. Used by --simulate, where everything AFTER the flag is
// the label list (key=value pairs) and we need to peel those off
// from any preceding positionals.
func indexOfFlag(args []string, flag string) int {
	for i, a := range args {
		if a == flag {
			return i
		}
	}
	return -1
}

// containsFlag returns true if the args list includes the exact flag.
func containsFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

// stripFlags removes the named flags from the args list, returning
// only positional args. Used after containsFlag to leave the receiver
// name behind.
func stripFlags(args []string, flags ...string) []string {
	skip := make(map[string]bool, len(flags))
	for _, f := range flags {
		skip[f] = true
	}
	out := make([]string, 0, len(args))
	for _, a := range args {
		if skip[a] {
			continue
		}
		out = append(out, a)
	}
	return out
}

// doValidateAMStatus performs check (a) AND fetches the AM config body
// used by check (b). Combined into one call because both pull from
// the same /api/v2/status endpoint — one HTTP round-trip per unique
// AM URL.
//
// Result struct captures both halves so the caller can branch per
// receiver without re-pinging.
func doValidateAMStatus(amURL string) (out struct {
	ok         bool
	statusText string
	configBody string
},
) {
	if amURL == "" {
		out.statusText = "no AM URL configured"
		return out
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, amURL+"/api/v2/status", nil)
	if err != nil {
		out.statusText = "bad URL"
		return out
	}
	resp, err := alertmanager.Client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			out.statusText = "timeout"
		} else {
			out.statusText = "unreachable"
		}
		return out
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		out.statusText = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return out
	}

	var body struct {
		Config struct {
			Original string `json:"original"`
		} `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		// AM responded but we couldn't parse the config body. Treat as
		// reachable (check a passes) but check b will be inconclusive
		// since configBody stays empty.
		out.ok = true
		out.statusText = "ok"
		return out
	}
	out.ok = true
	out.statusText = "ok"
	out.configBody = body.Config.Original
	return out
}

// postValidateTestMessage POSTs a clearly-marked test message directly
// to the receiver's webhook URL. Confirms (from the plugin's network
// perspective) that the URL is valid and MM accepts the post.
//
// The message is visible in the channel — that's the cost of running
// check (c). User opts in by passing --webhook-test.
func postValidateTestMessage(webhookURL, receiverName string) error {
	payload := map[string]any{
		"text":     fmt.Sprintf(":mag: Validate check: this is a test message confirming the `%s` receiver's webhook is reachable. Safe to delete.", receiverName),
		"username": "alertmanagerbot",
	}
	body, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("bad URL: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// postValidateSyntheticAlert fires a synthetic alert through AM's
// /api/v2/alerts endpoint with labels that should match the receiver's
// route (assuming the user's routes match on `runbook: <slug>`). AM
// renders it through its template + posts to the receiver's webhook.
// User watches the channel for delivery.
//
// Firing alerts: startsAt=now, endsAt=now+30s — AM fires immediately
// and auto-resolves after ~30s.
//
// Resolved alerts (resolved=true): startsAt=now-60s, endsAt=now-1s —
// AM accepts the alert as already-resolved and sends the resolved
// notification path (green sidebar, [✓ RESOLVED:] title prefix). Use
// for visual matrix testing alongside firing alerts.
//
// labels include `test=validate` so synthetic traffic is
// distinguishable from real. severity defaults to "warning" when
// empty. extraLabels merge in on top of the defaults — caller-supplied
// keys override the helper's own (so e.g. a caller can override
// `alertname` or `severity` from the admin form, but not the
// `test=validate` / `source=...` markers since those are appended
// last to keep the synthetic-alert marker authoritative).
func postValidateSyntheticAlert(amURL, runbookSlug, severity string, extraLabels map[string]string, resolved bool) (string, error) {
	if severity == "" {
		severity = "warning"
	}
	// Per-severity alertname so AM groups each severity into its own
	// notification. Without this, --severity=all collapses
	// warning + critical + info into one (3 firing) group post since
	// AM's standard group_by includes alertname. Each spec needs a
	// distinct alertname to materialize as a distinct chat post.
	titleSeverity := strings.ToUpper(severity[:1]) + severity[1:]
	alertname := "ValidateSyntheticTest" + titleSeverity
	summary := fmt.Sprintf("Synthetic %s alert from /alertmanager validate", severity)
	description := fmt.Sprintf("Validate diagnostic at %s severity — if you see this in the channel, AM → MM delivery works end-to-end. Auto-resolves in ~30 seconds.", severity)
	if resolved {
		// Resolved gets its own alertname too, plus startsAt/endsAt
		// in the past below — the combination makes AM route this as
		// a resolved-state notification independent of any firing
		// counterpart.
		alertname = "ValidateSyntheticTestResolved"
		summary = "Resolved synthetic alert from /alertmanager validate"
		description = "Resolved-state diagnostic — verifies the [✓ RESOLVED:] title and green sidebar render correctly."
	}

	labels := map[string]string{
		"alertname": alertname,
		"runbook":   runbookSlug,
	}
	maps.Copy(labels, extraLabels)
	// Markers come last so they always win — guarantees the alert is
	// identifiable as synthetic even if a caller passed conflicting keys.
	labels["severity"] = severity
	labels["test"] = "validate"
	labels["source"] = "alertmanager-plugin-validate"

	var startsAt, endsAt string
	if resolved {
		// startsAt in the past + endsAt in the past = AM treats this
		// as a resolved alert and immediately fires the resolved
		// notification (no firing path).
		startsAt = time.Now().Add(-60 * time.Second).UTC().Format(time.RFC3339)
		endsAt = time.Now().Add(-1 * time.Second).UTC().Format(time.RFC3339)
	} else {
		startsAt = time.Now().UTC().Format(time.RFC3339)
		endsAt = time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339)
	}

	payload := []map[string]any{
		{
			"labels": labels,
			"annotations": map[string]string{
				"summary":     summary,
				"description": description,
			},
			"startsAt": startsAt,
			"endsAt":   endsAt,
		},
	}
	body, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, amURL+"/api/v2/alerts", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("bad URL: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := alertmanager.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST to AM failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// AM doesn't return an alert ID — it returns 200 with empty body.
	// Use the runbook slug as the identifier for the user.
	return runbookSlug, nil
}

// expandSeverityForFire turns a --severity flag value into the ordered
// list of (severity, resolved) tuples to fire. Single severities fire
// one alert; "all" fires four — warning, critical, info, plus a
// resolved alert. Empty string defaults to warning (backwards compat
// with the v1.0.2 single-fire behavior).
//
// The "all" expansion is deliberately ordered firing-first then
// resolved so the chat history reads in increasing severity intensity
// followed by the lifecycle's natural conclusion.
func expandSeverityForFire(value string) []syntheticFireSpec {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "all":
		return []syntheticFireSpec{
			{Severity: "warning", Resolved: false},
			{Severity: "critical", Resolved: false},
			{Severity: "info", Resolved: false},
			{Severity: "info", Resolved: true},
		}
	case "critical", "warning", "info":
		return []syntheticFireSpec{{Severity: normalized, Resolved: false}}
	case "":
		return []syntheticFireSpec{{Severity: "warning", Resolved: false}}
	default:
		// Unknown — caller validates against this; we return empty so
		// the caller can surface a helpful error rather than firing the
		// wrong thing.
		return nil
	}
}

// syntheticFireSpec is one alert AM should receive — a (severity,
// resolved) pair. The full `all` matrix is four of these.
type syntheticFireSpec struct {
	Severity string
	Resolved bool
}

// fireSyntheticMatrix fires one synthetic alert per fireSpec against
// the given AM + runbook, returning a single status string suitable
// for the validate report's End-to-end column. Aggregates per-spec
// outcomes — partial-failure cases show `✓ 3 of 4` so the operator
// can see something landed without paging up the AM logs.
func fireSyntheticMatrix(amURL, runbookSlug string, specs []syntheticFireSpec) string {
	if len(specs) == 0 {
		return "— (no severity)"
	}
	ok := 0
	var firstErr string
	for _, s := range specs {
		_, err := postValidateSyntheticAlert(amURL, runbookSlug, s.Severity, nil, s.Resolved)
		if err != nil {
			if firstErr == "" {
				firstErr = err.Error()
			}
			continue
		}
		ok++
	}
	if ok == len(specs) {
		if len(specs) == 1 {
			return "✓ alert id " + runbookSlug
		}
		return fmt.Sprintf("✓ %d/%d (warning+critical+info+resolved)", ok, len(specs))
	}
	if ok == 0 {
		return "✗ " + firstErr
	}
	return fmt.Sprintf("⚠ %d/%d — first error: %s", ok, len(specs), firstErr)
}
