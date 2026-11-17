package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// The /admin/inventory endpoint: a sysadmin-gated HTML report of plugin-managed
// receivers with CSV export. Row grouping lives in inventory_rows.go, the
// simulation and webhook-test actions in inventory_actions.go, and the page
// markup in inventory.html.

// handleAdminInventory dispatches to HTML or CSV renderer based on
// the `format` query param. Sysadmin-gated by header.
func (p *Plugin) handleAdminInventory(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.client.User.HasPermissionTo(userID, model.PermissionManageSystem) {
		http.Error(w, "forbidden: this endpoint requires system_admin", http.StatusForbidden)
		return
	}

	configs := p.getConfiguration().AlertConfigs
	if r.URL.Query().Get("format") == "csv" {
		p.renderInventoryCSV(w, configs)
		return
	}
	p.renderInventoryHTML(w, r, configs)
}

// renderInventoryCSV streams a simple CSV: name, team, channel, am_url.
// One row per receiver. Useful for offline audit / spreadsheet workflows.
//
// Webhook ID is deliberately NOT included — same reasoning as the HTML
// view, secrets stay out of bulk export.
func (p *Plugin) renderInventoryCSV(w http.ResponseWriter, configs []alertConfig) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="alertmanager-inventory.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()

	_ = cw.Write([]string{"name", "team", "channel", "alertmanager_url"})
	// Sort for deterministic output across reloads.
	sorted := make([]alertConfig, len(configs))
	copy(sorted, configs)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Channel != sorted[j].Channel {
			return sorted[i].Channel < sorted[j].Channel
		}
		return sorted[i].Name < sorted[j].Name
	})
	for _, c := range sorted {
		_ = cw.Write([]string{csvSafe(c.Name), csvSafe(c.Team), csvSafe(c.Channel), csvSafe(c.AlertManagerURL)})
	}
}

// csvSafe neutralizes spreadsheet formula injection independently of URL
// validation — a different output context with different metacharacters (CL-21).
// A cell whose first character is one a spreadsheet may treat as a formula lead
// (= + - @, or a leading TAB/CR) is prefixed with a single quote so Excel/Sheets
// render it as literal text rather than evaluating it (e.g. =HYPERLINK(...) that
// exfiltrates the row on open). Applied to every exported cell, not just the URL,
// since the defense is cheap and the sink is the spreadsheet, not the value.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// renderInventoryHTML is the main page renderer. Reads the grouping mode
// (channel|team|am, default channel) from group= — via FormValue so it survives
// the CSRF-driven POST swap (which drops the query string) the same way the
// simulate_* fields do.
func (p *Plugin) renderInventoryHTML(w http.ResponseWriter, r *http.Request, configs []alertConfig) {
	groupMode := r.FormValue("group")
	if groupMode != "team" && groupMode != "am" {
		groupMode = "channel"
	}

	// Probe each unique AM URL once, populating the reachability cache.
	// The probe is cached for amReachabilityTTL — repeat page loads
	// within the TTL window don't re-ping.
	amStatus := make(map[string]amReachabilityEntry)
	for _, url := range uniqueAMURLs(configs) {
		amStatus[url] = p.probeAMReachability(url)
	}

	// Build rows + resolve channel→team URL paths for clickable links.
	// p.API.GetChannelByName needs the team ID, but configs only carry
	// the team SLUG. Resolve each team slug → team ID once.
	teamIDs := make(map[string]string)
	getTeamID := func(slug string) string {
		if id, ok := teamIDs[slug]; ok {
			return id
		}
		if t, appErr := p.API.GetTeamByName(slug); appErr == nil {
			teamIDs[slug] = t.Id
			return t.Id
		}
		teamIDs[slug] = ""
		return ""
	}

	rows := make([]inventoryRow, 0, len(configs))
	for _, c := range configs {
		row := inventoryRow{
			Name:            c.Name,
			Team:            c.Team,
			Channel:         c.Channel,
			AlertManagerURL: c.AlertManagerURL,
		}
		// Mattermost channel URLs are /<team>/channels/<channel-name>.
		// Without the team we can't form a valid URL.
		if getTeamID(c.Team) != "" {
			row.ChannelURLPath = "/" + c.Team + "/channels/" + c.Channel
		}
		if entry, ok := amStatus[c.AlertManagerURL]; ok {
			row.AMReachable = entry.Reachable
			row.AMStatus = entry.Status
			row.LoadedInAM, row.LoadedViaOperator = entry.LoadedInAM(c.Name)
		}
		row.computeHealth()
		rows = append(rows, row)
	}

	// Group based on mode.
	groups := groupInventory(rows, groupMode)

	// Per-team rollup (always shown at top, independent of group mode).
	teamCounts := make(map[string]int)
	for _, c := range configs {
		teamCounts[c.Team]++
	}
	type teamRow struct {
		Team  string
		Count int
	}
	teamRows := make([]teamRow, 0, len(teamCounts))
	for t, n := range teamCounts {
		teamRows = append(teamRows, teamRow{t, n})
	}
	sort.Slice(teamRows, func(i, j int) bool { return teamRows[i].Team < teamRows[j].Team })

	// Reconciler health for the banner.
	lastRun, lastPruned := p.reconcileStatus()
	reconcilerStatus := "scheduled, first cycle pending — appears within ~5 minutes of plugin activation"
	reconcilerHealthy := false
	if !lastRun.IsZero() {
		age := time.Since(lastRun)
		reconcilerStatus = fmt.Sprintf("%s ago (pruned %d on last run)", humanDuration(age), lastPruned)
		// Healthy if within 2x the configured interval (= 10 min by default).
		reconcilerHealthy = age < 10*time.Minute
	}

	// Inverse drift detection: receivers that exist in AM's loaded
	// config but have no matching plugin entry. Means someone
	// hand-edited alertmanager.yml outside the plugin's lifecycle
	// (or rotated a webhook via /alertmanager config rotate without
	// pasting the new YAML into AM yet).
	//
	// Computed per AM URL because each backend has its own loaded
	// config. Limited to reachable backends — we can't drift-check
	// against AM if we couldn't fetch its config in this cycle.
	pluginNamesPerAM := make(map[string]map[string]bool)
	for _, c := range configs {
		if pluginNamesPerAM[c.AlertManagerURL] == nil {
			pluginNamesPerAM[c.AlertManagerURL] = make(map[string]bool)
		}
		pluginNamesPerAM[c.AlertManagerURL][c.Name] = true
	}
	type amDriftGroup struct {
		AMURL string
		Names []string
	}
	var amDrift []amDriftGroup
	// Acknowledged names are receivers another system created on purpose.
	// A KV read failure shows them as AM-only again rather than hiding
	// every warning.
	acked := map[string]bool{}
	if names, aerr := p.loadAcknowledgedReceivers(); aerr != nil {
		p.API.LogWarn("could not read acknowledged receivers; AM-only list will include them", "err", aerr.Error())
	} else {
		acked = acknowledgedSet(names)
	}
	for amURL, entry := range amStatus {
		if !entry.Reachable || entry.ConfigBody == "" {
			continue
		}
		amNames := extractAMReceiverNames(entry.ConfigBody)
		pluginSet := pluginNamesPerAM[amURL]
		var driftNames []string
		for _, n := range amNames {
			// Compare on the canonical name so a plugin receiver the operator
			// renamed to "<ns>/<config>/<name>" isn't mistaken for a hand-edited
			// AM-only receiver. Display the original (prefixed) name either way.
			canonical, _ := canonicalReceiverName(n)
			// Skip Alertmanager built-in receivers — these are always present in
			// every AM deployment and are never created by the plugin. Flagging
			// them as AM-only trains operators to ignore the warning entirely, which
			// destroys its signal value for real drift (feedback item 10, gap 3).
			if canonical == "null" || canonical == "blackhole" {
				continue
			}
			// Skip plugin-generated fallback receivers — created as a side
			// effect of CRD export but not stored as individual alertConfig
			// entries. Their base name (without -fallback) is plugin-tracked,
			// so we know they're plugin-owned. Flagging them as AM-only
			// destroys the warning's signal value for real drift.
			if base, ok := strings.CutSuffix(canonical, "-fallback"); ok {
				// Handle the "-N-fallback" disambiguation suffix form:
				// "my-channel-2-fallback" → "my-channel".
				if dashIdx := strings.LastIndex(base, "-"); dashIdx != -1 {
					if _, err := strconv.Atoi(base[dashIdx+1:]); err == nil {
						base = base[:dashIdx]
					}
				}
				if pluginSet[base] {
					continue
				}
			}
			if acked[canonical] {
				continue
			}
			if !pluginSet[canonical] {
				driftNames = append(driftNames, n)
			}
		}
		if len(driftNames) > 0 {
			amDrift = append(amDrift, amDriftGroup{AMURL: amURL, Names: driftNames})
		}
	}
	sort.Slice(amDrift, func(i, j int) bool { return amDrift[i].AMURL < amDrift[j].AMURL })

	// Route tester inputs. Three modes:
	//   simulate     → read-only, walk AM route tree against labels
	//   webhook-test → POST a hardcoded test payload to each target's
	//                  webhook URL (tests MM side only, bypasses AM)
	//   end-to-end   → POST a synthetic alert to AM, AM templates +
	//                  delivers (tests the full chain)
	//
	// Target uses a single dropdown with optgroups separating
	// "Groups" from "Individual runbooks." The encoded value is
	// `all`, `group:<name>`, or `individual:<slug>` — the prefix
	// disambiguates without needing a separate type field.
	//
	// channel filters the target receiver list to those in a specific
	// channel (only relevant for webhook-test + end-to-end; simulate
	// walks the route tree at the AM level).
	// Read via FormValue so the side-effecting modes can arrive as a POST body
	// (see the CL-20 gate below); read-only simulate still works as a GET query.
	simMode := strings.TrimSpace(r.FormValue("simulate_mode"))
	simType := strings.TrimSpace(r.FormValue("simulate_type"))
	simValue := strings.TrimSpace(r.FormValue("simulate_value"))
	simChannel := strings.TrimSpace(r.FormValue("simulate_channel"))
	simTeam := strings.TrimSpace(r.FormValue("simulate_team"))
	simSeverity := strings.TrimSpace(r.FormValue("simulate_severity"))
	simExtra := strings.TrimSpace(r.FormValue("simulate_extra"))

	// Severity is a knob only for end-to-end (it sets the severity the
	// synthetic alert fires at). Webhook-test ignores it, and simulate routes
	// on the `runbook` label, not severity — a severity-only simulate just
	// falls through to the default route and confuses people. So drop the
	// dedicated severity outside end-to-end; an admin who genuinely has
	// severity-based routes still simulates them via the extra-labels box.
	if simMode != "end-to-end" {
		simSeverity = ""
	}

	formSubmitted := simMode != "" || simType != "" || simValue != "" || simChannel != "" || simTeam != "" || simSeverity != "" || simExtra != ""

	var simResult inventorySimResult
	var simMatrix []inventorySimResult
	var simActionResult inventoryActionResult

	if formSubmitted {
		// Decode (type, value) into a list of slugs.
		targetSlugs := decodeTargetSelection(simType, simValue)
		filteredConfigs := filterConfigsByChannel(configs, simTeam, simChannel)

		switch simMode {
		case "webhook-test", "end-to-end":
			// CL-20: these modes cause side effects — webhook-test POSTs to real
			// incoming webhooks, end-to-end fires synthetic alerts. Mattermost
			// exempts GET from CSRF validation for plugin routes (and strips
			// Referer), so a GET-driven link would let an attacker trigger these
			// with a logged-in sysadmin's ambient session. Require POST, which the
			// platform DOES CSRF-check (it only sets Mattermost-User-Id after that
			// check passes). The page's Fire button submits these as a POST with
			// X-Requested-With; a cross-site link cannot.
			switch {
			case !simSideEffectAllowed(r.Method, r.Header.Get("X-Requested-With"), simMode):
				simActionResult = inventoryActionResult{
					Mode:  "error",
					Error: "This action changes state (posts to webhooks / fires alerts). Run it with the Fire button, not by opening a link — the request must be a POST issued by the page (CSRF protection).",
				}
			case simMode == "webhook-test":
				simActionResult = p.runInventoryWebhookTest(targetSlugs, filteredConfigs)
			default:
				simActionResult = p.runInventoryEndToEnd(targetSlugs, simSeverity, simExtra, filteredConfigs, amStatus)
			}
		default:
			// simulate (default)
			switch {
			case len(targetSlugs) > 1:
				simMatrix = p.runInventorySimulationMatrixForSlugs(targetSlugs, simSeverity, simExtra, configs, amStatus)
			case len(targetSlugs) == 1:
				simResult = p.runInventorySimulation(buildSimulateLabelsInput(targetSlugs[0], simSeverity, simExtra), configs, amStatus)
			case simSeverity != "" || simExtra != "":
				// No slugs selected — sim with severity/extra alone
				simResult = p.runInventorySimulation(buildSimulateLabelsInput("", simSeverity, simExtra), configs, amStatus)
			default:
				simResult = inventorySimResult{Mode: "error", Message: "Pick a target type + group/runbook OR supply labels via the extras field."}
			}
		}
	}

	// Enumerate distinct channel names for the Channel dropdown.
	channelSet := make(map[string]bool)
	for _, c := range configs {
		channelSet[c.Channel] = true
	}
	channelOptions := make([]string, 0, len(channelSet))
	for c := range channelSet {
		channelOptions = append(channelOptions, c)
	}
	sort.Strings(channelOptions)

	// Teams that have receivers — the by-team scope dropdown. Channel names
	// repeat across teams, so scoping by team disambiguates the channel
	// dropdown and targets webhook-test/end-to-end at the right team.
	teamOptions := make([]string, 0, len(teamCounts))
	for t := range teamCounts {
		teamOptions = append(teamOptions, t)
	}
	sort.Strings(teamOptions)

	// team → its channels, for the JS cascade (pick a team → narrow channels).
	teamChannelSet := make(map[string]map[string]bool)
	for _, c := range configs {
		if teamChannelSet[c.Team] == nil {
			teamChannelSet[c.Team] = make(map[string]bool)
		}
		teamChannelSet[c.Team][c.Channel] = true
	}
	teamChannels := make(map[string][]string, len(teamChannelSet))
	for t, chset := range teamChannelSet {
		chs := make([]string, 0, len(chset))
		for ch := range chset {
			chs = append(chs, ch)
		}
		sort.Strings(chs)
		teamChannels[t] = chs
	}
	teamChannelsJSON, _ := json.Marshal(teamChannels)

	// Enumerate group names from scaffoldSets (excluding the "all"
	// alias so we don't confuse it with the matrix-mode target type).
	groupOptions := make([]string, 0, len(scaffoldSets))
	for g := range scaffoldSets {
		if g == "all" {
			continue
		}
		groupOptions = append(groupOptions, g)
	}
	sort.Strings(groupOptions)

	// Build the target → channels map for the JS-driven channel
	// dropdown. For each target value the dropdown can take, list
	// channels that actually have at least one matching receiver
	// so the channel options don't include channels where the
	// chosen target has no presence. Computed server-side, embedded
	// as JSON, applied by a small JS handler at page load + on
	// target dropdown change.
	targetChannels := buildTargetChannelMap(configs, groupOptions, runbookSlugs())
	targetChannelsJSON, _ := json.Marshal(targetChannels)
	// Also serialize the bare list of groups + slugs for the JS
	// type-cascading: when the admin picks Type=group, the secondary
	// dropdown swaps to show this list; same for Type=individual.
	groupsJSON, _ := json.Marshal(groupOptions)
	slugsJSON, _ := json.Marshal(runbookSlugs())

	data := struct {
		Total              int
		Channels           int
		Groups             []inventoryGroup
		TeamRollup         []teamRow
		AMDrift            []amDriftGroup
		ReconcilerStatus   string
		ReconcilerHealthy  bool
		GroupMode          string
		Version            string
		CSVHref            string
		SimulateMode       string
		SimulateType       string
		SimulateValue      string
		SimulateChannel    string
		SimulateTeam       string
		SimulateSeverity   string
		SimulateExtra      string
		SimulateResult     inventorySimResult
		SimulateMatrix     []inventorySimResult
		SimulateAction     inventoryActionResult
		RunbookSlugs       []string
		ChannelOptions     []string
		GroupOptions       []string
		TeamOptions        []string
		TargetChannelsJSON template.JS
		GroupOptionsJSON   template.JS
		RunbookSlugsJSON   template.JS
		TeamChannelsJSON   template.JS
	}{
		Total:             len(configs),
		Channels:          countDistinctChannels(configs),
		Groups:            groups,
		TeamRollup:        teamRows,
		AMDrift:           amDrift,
		ReconcilerStatus:  reconcilerStatus,
		ReconcilerHealthy: reconcilerHealthy,
		GroupMode:         groupMode,
		Version:           Manifest.Version,
		SimulateMode:      simMode,
		SimulateType:      simType,
		SimulateValue:     simValue,
		SimulateChannel:   simChannel,
		SimulateTeam:      simTeam,
		SimulateSeverity:  simSeverity,
		SimulateExtra:     simExtra,
		SimulateResult:    simResult,
		SimulateMatrix:    simMatrix,
		SimulateAction:    simActionResult,
		RunbookSlugs:      runbookSlugs(),
		ChannelOptions:    channelOptions,
		GroupOptions:      groupOptions,
		TeamOptions:       teamOptions,
		// G203 false positives: targetChannelsJSON/groupsJSON/slugsJSON are
		// already json.Marshal output of server-controlled data (channel
		// list, group options, embedded runbook slugs — never user input).
		// json.Marshal defaults to SetEscapeHTML(true), so `<` is already
		// emitted as `<`, preventing `</script>` breakouts. template.JS
		// here just tells html/template to treat the bytes as a JS literal
		// (skip the wrap-in-quotes pass) — which is what we want because
		// the literal IS the JSON.
		TargetChannelsJSON: template.JS(targetChannelsJSON), //nolint:gosec // G203: pre-escaped by json.Marshal (HTML escape default)
		GroupOptionsJSON:   template.JS(groupsJSON),         //nolint:gosec // G203: pre-escaped by json.Marshal (HTML escape default)
		RunbookSlugsJSON:   template.JS(slugsJSON),          //nolint:gosec // G203: pre-escaped by json.Marshal (HTML escape default)
		TeamChannelsJSON:   template.JS(teamChannelsJSON),   //nolint:gosec // G203: pre-escaped by json.Marshal (HTML escape default)
		// Relative URL: browser resolves against the current URL,
		// which is /plugins/<id>/admin/inventory. Using `?format=csv`
		// gives the right target. Earlier bug: using r.URL.Path
		// emitted `/admin/inventory?format=csv` which the browser
		// interpreted as starting from MM root, not the plugin mount.
		CSVHref: "?format=csv",
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := inventoryTemplate.Execute(w, data); err != nil {
		p.API.LogWarn("admin-inventory: template render failed", "err", err.Error())
	}
}
