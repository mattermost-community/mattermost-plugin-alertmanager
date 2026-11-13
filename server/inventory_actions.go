package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	amconfig "github.com/prometheus/alertmanager/config"
	"github.com/prometheus/alertmanager/dispatch"
)

// simSideEffectAllowed reports whether a simulate mode may run given the request
// method and its X-Requested-With header. The side-effecting modes (webhook-test,
// end-to-end) require BOTH a POST and X-Requested-With: XMLHttpRequest (CL-20).
//
// Requiring POST alone would lean entirely on the platform stripping
// Mattermost-User-Id when a POST fails CSRF — a Mattermost internal we can't
// verify from here. Also requiring X-Requested-With makes the gate self-contained:
// a cross-site <form> or top-level navigation cannot set a custom header, and the
// page's own Fire button sends it via fetch. Read-only modes are unrestricted.
// Extracted so the gate is unit-testable without the full HTTP handler.
func simSideEffectAllowed(method, requestedWith, mode string) bool {
	if mode == "webhook-test" || mode == "end-to-end" {
		return method == http.MethodPost && requestedWith == "XMLHttpRequest"
	}
	return true
}

// inventorySimResult is what the inventory page's route-simulator
// section shows. Empty Mode = no simulation was requested; the form
// renders without a result block.
type inventorySimResult struct {
	Mode      string // "", "ok", "fell-through", "error"
	Message   string
	AMURL     string
	Labels    map[string]string // sorted for stable rendering
	Receivers []string          // for Mode == "ok", the matched receivers
	DefaultRx string            // for Mode == "fell-through"

	// MatrixRow is set when this result is one row of a matrix
	// (simulate-all-runbooks mode). Holds the runbook slug this
	// row represents — used by the template to render a table.
	MatrixRow string
}

// parseEndToEndExtraLabels splits the admin page form's free-text
// extra-labels field into a label map for the synthetic alert. Input
// is space-separated `key=value` pairs (same shape the simulate path
// accepts). Lenient parser — silently skips malformed pairs since the
// form input is operator-supplied and a strict reject would block the
// fire on a typo. Strict validation happens at the simulate path
// (which uses parseSimulateLabels). For end-to-end, AM will reject
// invalid labels at the API boundary anyway.
func parseEndToEndExtraLabels(extra string) map[string]string {
	if extra == "" {
		return nil
	}
	labels := map[string]string{}
	for pair := range strings.FieldsSeq(extra) {
		eq := strings.IndexByte(pair, '=')
		if eq < 1 || eq == len(pair)-1 {
			continue
		}
		labels[pair[:eq]] = pair[eq+1:]
	}
	if len(labels) == 0 {
		return nil
	}
	return labels
}

// buildSimulateLabelsInput composes the dropdown + free-text values
// into the space-separated `key=value` shape that
// parseSimulateLabels accepts. Empty dropdown values are skipped;
// the free-text extra is appended as-is so power users can pass
// any labels they want (namespace, pod, app, etc.).
func buildSimulateLabelsInput(runbook, severity, extra string) string {
	var parts []string
	if runbook != "" {
		parts = append(parts, "runbook="+runbook)
	}
	if severity != "" {
		parts = append(parts, "severity="+severity)
	}
	if extra != "" {
		parts = append(parts, extra)
	}
	return strings.Join(parts, " ")
}

// inventoryActionResult captures the outcome of a side-effect
// action from the admin page form (webhook-test or end-to-end fire).
// Different shape than inventorySimResult because the per-target
// summary needs to include success/fail status, not route matches.
type inventoryActionResult struct {
	Mode    string         // "" / "webhook-test" / "end-to-end" / "error"
	Channel string         // channel scope if filtered
	Items   []actionResult // per-receiver / per-runbook outcome
	Summary string         // overall summary (e.g., "fired 6 of 6")
	Error   string         // if Mode == "error"
}

type actionResult struct {
	Name   string // receiver name OR runbook slug, depending on mode
	OK     bool
	Detail string // success-detail or error message
}

// buildTargetChannelMap returns a map from each possible target
// dropdown value to the list of channels that actually host at
// least one matching receiver. Used by the admin page's JS to
// filter the Channel dropdown so it only offers channels where the
// chosen target has presence. Empty list = no channel has any
// matching receiver (dropdown collapses to just "(any channel)").
//
// Keys:
//
//	""             — any target (= all channels with plugin receivers)
//	"all"          — same as ""
//	"group:<name>" — channels with at least one receiver in that group
//	"individual:<slug>" — channels with that specific receiver
func buildTargetChannelMap(configs []alertConfig, groups, slugs []string) map[string][]string {
	out := make(map[string][]string)

	// Per-slug → set of channels
	slugChannels := make(map[string]map[string]bool, len(slugs))
	for _, c := range configs {
		base := receiverBaseSlug(c.Name)
		if slugChannels[base] == nil {
			slugChannels[base] = make(map[string]bool)
		}
		slugChannels[base][c.Channel] = true
	}

	// All channels with any receiver — used for the "all" + empty
	// keys + as the union basis for groups.
	allChannelsSet := make(map[string]bool)
	for _, ch := range slugChannels {
		for c := range ch {
			allChannelsSet[c] = true
		}
	}
	allChannels := sortedKeys(allChannelsSet)
	out[""] = allChannels
	out["all"] = allChannels

	// Per-individual entries.
	for _, slug := range slugs {
		out["individual:"+slug] = sortedKeys(slugChannels[slug])
	}

	// Per-group entries — union of channels across the group's slugs.
	for _, group := range groups {
		setSlugs, ok := scaffoldSets[group]
		if !ok {
			continue
		}
		union := make(map[string]bool)
		for _, slug := range setSlugs {
			for c := range slugChannels[slug] {
				union[c] = true
			}
		}
		out["group:"+group] = sortedKeys(union)
	}

	return out
}

// sortedKeys returns the keys of a string-bool map in sorted order.
// Handles nil maps — returns nil rather than empty slice — so the
// JSON output is `null` for empty entries (JS treats `null` and
// missing key the same).
func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// decodeTargetSelection turns the (Type, Value) dropdown pair into
// a flat list of runbook slugs:
//
//	type=all                → all 30 slugs (value ignored)
//	type=group, value=X     → scaffoldSets[X]
//	type=individual, value=X → [X]
//	anything else           → nil (caller falls back to label-only sim)
//
// Two-dropdown cascade lets the secondary dropdown's contents
// reflect what was picked in the primary — JS swaps it client-side,
// the backend just trusts the (type, value) pair it receives.
func decodeTargetSelection(typ, value string) []string {
	switch typ {
	case "all":
		return runbookSlugs()
	case "group":
		if value == "" {
			return nil
		}
		if slugs, ok := scaffoldSets[value]; ok && len(slugs) > 0 {
			out := make([]string, len(slugs))
			copy(out, slugs)
			sort.Strings(out)
			return out
		}
	case "individual":
		if value != "" {
			return []string{value}
		}
	}
	return nil
}

// filterConfigsByChannel scopes the receiver list by team and/or channel.
// Both empty = no filtering. Team matters because channel names repeat across
// teams (`town-square` is in every team) — filtering on channel name alone
// would target the wrong team's receivers for webhook-test / end-to-end.
func filterConfigsByChannel(configs []alertConfig, team, channel string) []alertConfig {
	if team == "" && channel == "" {
		return configs
	}
	out := make([]alertConfig, 0, len(configs))
	for _, c := range configs {
		if team != "" && c.Team != team {
			continue
		}
		if channel != "" && c.Channel != channel {
			continue
		}
		out = append(out, c)
	}
	return out
}

// runInventorySimulationMatrixForSlugs runs a simulation per slug
// from the supplied list (instead of all 30). Used when the admin
// picks a Group target — we get a coverage table scoped to just
// that group.
func (p *Plugin) runInventorySimulationMatrixForSlugs(slugs []string, severity, extra string, configs []alertConfig, amStatus map[string]amReachabilityEntry) []inventorySimResult {
	out := make([]inventorySimResult, 0, len(slugs))
	for _, slug := range slugs {
		input := buildSimulateLabelsInput(slug, severity, extra)
		row := p.runInventorySimulation(input, configs, amStatus)
		row.MatrixRow = slug
		out = append(out, row)
	}
	return out
}

// runInventoryWebhookTest POSTs a hardcoded test payload directly
// to each target receiver's Mattermost webhook URL. Tests the
// MM-side of the pipeline (webhook auth + channel binding + post
// creation) without going through Alertmanager at all.
//
// Limited to receivers in the filtered configs (channel-scoped if
// the admin picked a channel; otherwise all). Matches a target
// slug if any receiver's name has that slug as its prefix (via
// receiverBaseSlug — handles the channel-suffix pattern).
func (p *Plugin) runInventoryWebhookTest(targetSlugs []string, configs []alertConfig) inventoryActionResult {
	res := inventoryActionResult{Mode: "webhook-test"}
	if len(targetSlugs) == 0 {
		res.Mode = "error"
		res.Error = "No target receivers selected. Pick a group/individual target first."
		return res
	}

	slugSet := make(map[string]bool, len(targetSlugs))
	for _, s := range targetSlugs {
		slugSet[s] = true
	}

	for _, ac := range configs {
		if !slugSet[receiverBaseSlug(ac.Name)] {
			continue
		}
		webhookURL := p.webhookURLForReceiver(ac)
		err := postValidateTestMessage(webhookURL, ac.WebhookID, ac.Name)
		item := actionResult{Name: ac.Name, OK: err == nil}
		if err == nil {
			item.Detail = "Posted test payload — check the channel for the message."
		} else {
			item.Detail = err.Error()
		}
		res.Items = append(res.Items, item)
	}

	if len(res.Items) == 0 {
		res.Mode = "error"
		res.Error = "No receivers matched the target selection + channel filter."
		return res
	}

	ok := 0
	for _, i := range res.Items {
		if i.OK {
			ok++
		}
	}
	res.Summary = fmt.Sprintf("Posted %d of %d webhook test payloads.", ok, len(res.Items))
	return res
}

// runInventoryEndToEnd POSTs synthetic alerts to AM for each target
// slug. AM then templates + delivers them via its loaded
// slack_configs. Tests the full Prometheus → AM → MM chain (minus
// Prometheus, since the plugin is the alert source here).
//
// One alert per slug; severity from form (default warning); extra
// labels from free text. All alerts include `source=admin-page-test`
// so they can be silenced later if the admin needs to suppress
// repeat tests.
func (p *Plugin) runInventoryEndToEnd(targetSlugs []string, severity, extra string, configs []alertConfig, amStatus map[string]amReachabilityEntry) inventoryActionResult {
	res := inventoryActionResult{Mode: "end-to-end"}
	if len(targetSlugs) == 0 {
		res.Mode = "error"
		res.Error = "No target runbooks selected. Pick a group/individual target first."
		return res
	}

	// Find an AM URL to fire against. Prefer one that's bound to a
	// receiver matching one of our target slugs (so AM actually has
	// a route for the alert we're firing).
	slugSet := make(map[string]bool, len(targetSlugs))
	for _, s := range targetSlugs {
		slugSet[s] = true
	}
	var amURL string
	for _, ac := range configs {
		if slugSet[receiverBaseSlug(ac.Name)] {
			if entry, ok := amStatus[ac.AlertManagerURL]; ok && entry.Reachable {
				amURL = ac.AlertManagerURL
				break
			}
		}
	}
	if amURL == "" {
		res.Mode = "error"
		res.Error = "No reachable Alertmanager backing the selected target receivers."
		return res
	}

	// Parse the free-text extra labels field into a map. Form input
	// is lenient — silently skip malformed pairs (no `=`, empty key,
	// empty value). The synthetic-alert markers in
	// postValidateSyntheticAlert override anything the operator types
	// for `test` / `source` / `alertname` / `runbook` / `severity`,
	// which is intentional: those identify the alert as synthetic.
	extraLabels := parseEndToEndExtraLabels(extra)

	// Expand severity dropdown to one or more (severity, resolved)
	// specs. "all" fires 4 alerts per slug (warning + critical + info +
	// resolved); single severities fire 1. Empty falls back to warning.
	specs := expandSeverityForFire(severity)
	if specs == nil {
		res.Mode = "error"
		res.Error = fmt.Sprintf("Invalid severity %q. Accepted: warning, critical, info, all.", severity)
		return res
	}

	for _, slug := range targetSlugs {
		// One actionResult row per (slug, spec) so the operator can
		// see which combinations landed when severity=all multiplies
		// the alert count.
		for _, s := range specs {
			specLabel := s.Severity
			if s.Resolved {
				specLabel += " (resolved)"
			}
			displayName := slug
			if len(specs) > 1 {
				displayName = fmt.Sprintf("%s [%s]", slug, specLabel)
			}
			alertID, err := postValidateSyntheticAlert(amURL, slug, s.Severity, extraLabels, s.Resolved)
			item := actionResult{Name: displayName, OK: err == nil}
			if err == nil {
				item.Detail = "Fired synthetic alert id " + alertID + " — watch the bound channels for delivery."
			} else {
				p.logAMError(err)
				item.Detail = amAdminErrorText(err)
			}
			res.Items = append(res.Items, item)
		}
	}

	ok := 0
	for _, i := range res.Items {
		if i.OK {
			ok++
		}
	}
	res.Summary = fmt.Sprintf("Fired %d of %d synthetic alerts to %s.", ok, len(res.Items), amURL)
	return res
}

// runInventorySimulation parses the form's label input, picks the
// first reachable AM, walks its loaded route tree against the
// labels, and returns a result struct the template can render.
// Same engine as the /alertmanager config validate simulate command — just
// a different entry point. Empty input is not an error: returns
// zero-value result so the page renders just the form.
func (p *Plugin) runInventorySimulation(input string, configs []alertConfig, amStatus map[string]amReachabilityEntry) inventorySimResult {
	if input == "" {
		return inventorySimResult{}
	}

	// Parse `key=value key=value ...` (space-separated, same shape
	// as the slash command).
	parts := strings.Fields(input)
	labels, err := parseSimulateLabels(parts)
	if err != nil {
		return inventorySimResult{
			Mode:    "error",
			Message: fmt.Sprintf("Bad label input: %v", err),
		}
	}

	// Pick a backend. The inventory page can have multiple AMs across
	// channels; for v1 we run the sim against the first reachable
	// one. If multiple AMs are in play and they have divergent route
	// trees, an admin can use the slash command from the channel
	// bound to the specific AM they care about.
	var amURL string
	var entry amReachabilityEntry
	for _, c := range configs {
		if e, ok := amStatus[c.AlertManagerURL]; ok && e.Reachable && e.ConfigBody != "" {
			amURL = c.AlertManagerURL
			entry = e
			break
		}
	}
	if amURL == "" {
		return inventorySimResult{
			Mode:    "error",
			Message: "No reachable Alertmanager found in the current inventory. Need at least one AM URL the plugin can fetch /api/v2/status from.",
		}
	}

	cfg, err := amconfig.Load(entry.ConfigBody)
	if err != nil {
		return inventorySimResult{
			Mode:    "error",
			Message: fmt.Sprintf("AM at %s loaded a config that doesn't parse: %v", amURL, err),
			AMURL:   amURL,
		}
	}
	if cfg.Route == nil {
		return inventorySimResult{
			Mode:    "error",
			Message: fmt.Sprintf("AM at %s has no route: block to walk.", amURL),
			AMURL:   amURL,
		}
	}

	mainRoute := dispatch.NewRoute(cfg.Route, nil)
	matches := mainRoute.Match(labels)

	// Convert labels to sorted display form for the template.
	labelDisplay := make(map[string]string, len(labels))
	for k, v := range labels {
		labelDisplay[string(k)] = string(v)
	}

	if len(matches) == 0 || (len(matches) == 1 && matches[0].RouteOpts.Receiver == cfg.Route.Receiver) {
		return inventorySimResult{
			Mode:      "fell-through",
			Message:   "No sub-route matched. Alert would fall through to the default receiver.",
			AMURL:     amURL,
			Labels:    labelDisplay,
			DefaultRx: cfg.Route.Receiver,
		}
	}

	receivers := make([]string, 0, len(matches))
	for _, m := range matches {
		receivers = append(receivers, m.RouteOpts.Receiver)
	}
	return inventorySimResult{
		Mode:      "ok",
		AMURL:     amURL,
		Labels:    labelDisplay,
		Receivers: receivers,
	}
}
