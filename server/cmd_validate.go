package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/christopherfickess/mattermost-plugin-alertmanager/server/alertmanager"
	"github.com/mattermost/mattermost/server/public/model"
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

	webhookTest := containsFlag(rest, "--webhook-test")
	endToEnd := containsFlag(rest, "--end-to-end")
	rest = stripFlags(rest, "--webhook-test", "--end-to-end")

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
		ok          bool
		statusText  string
		configBody  string // raw YAML text from AM, used by check (b)
		configError string
	}
	amStatus := make(map[string]statusCache)
	for _, c := range scoped {
		if _, seen := amStatus[c.AlertManagerURL]; seen {
			continue
		}
		amStatus[c.AlertManagerURL] = doValidateAMStatus(c.AlertManagerURL)
	}

	type rowResult struct {
		Name             string
		AMReach          string // "✓" / "✗ <error>"
		LoadedInAM       string
		WebhookAccepts   string // empty if check skipped
		EndToEndAlertID  string
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

		// (d) End-to-end alert via AM — opt-in
		if endToEnd && st.ok {
			alertID, err := postValidateSyntheticAlert(c.AlertManagerURL, receiverBaseSlug(c.Name))
			if err != nil {
				r.EndToEndAlertID = "✗ " + err.Error()
			} else {
				r.EndToEndAlertID = "✓ alert id " + alertID
			}
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

// containsFlag returns true if the args list includes the exact flag.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
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
	ok          bool
	statusText  string
	configBody  string
	configError string
}) {
	if amURL == "" {
		out.statusText = "no AM URL configured"
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, amURL+"/api/v2/status", nil)
	if err != nil {
		out.statusText = "bad URL"
		return
	}
	resp, err := alertmanager.Client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			out.statusText = "timeout"
		} else {
			out.statusText = "unreachable"
		}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		out.statusText = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return
	}

	var body struct {
		Config struct {
			Original string `json:"original"`
		} `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		out.ok = true // AM responded, we just couldn't parse the config
		out.statusText = "ok"
		out.configError = "could not parse AM status JSON: " + err.Error()
		return
	}
	out.ok = true
	out.statusText = "ok"
	out.configBody = body.Config.Original
	return
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
	defer resp.Body.Close()

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
// We use a 30s-from-now `endsAt` so the alert auto-resolves quickly
// and doesn't linger. labels include `test=validate` so it's
// distinguishable from real traffic.
func postValidateSyntheticAlert(amURL, runbookSlug string) (string, error) {
	startsAt := time.Now().UTC().Format(time.RFC3339)
	endsAt := time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339)
	payload := []map[string]any{
		{
			"labels": map[string]string{
				"alertname": "ValidateSyntheticTest",
				"runbook":   runbookSlug,
				"severity":  "warning",
				"test":      "validate",
				"source":    "alertmanager-plugin-validate",
			},
			"annotations": map[string]string{
				"summary":     "Synthetic alert from /alertmanager validate",
				"description": "This is a validate diagnostic — if you see this in the channel, AM → MM delivery works end-to-end. Auto-resolves in ~30 seconds.",
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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// AM doesn't return an alert ID — it returns 200 with empty body.
	// Use the runbook slug as the identifier for the user.
	return runbookSlug, nil
}
