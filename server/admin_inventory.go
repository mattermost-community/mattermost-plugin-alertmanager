package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	amconfig "github.com/prometheus/alertmanager/config"
	"github.com/prometheus/alertmanager/dispatch"
)

// admin_inventory.go owns the /admin/inventory endpoint: a sysadmin-gated
// HTML report grouping plugin-managed receivers by channel / team / AM
// URL with optional CSV export, live AM reachability dots, reconciler
// health banner, and client-side search.
//
// Split from http.go for size — the template + grouping logic + CSV
// path totals a few hundred LOC, more than the autocomplete endpoints
// it would otherwise live next to.

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
		_ = cw.Write([]string{c.Name, c.Team, c.Channel, c.AlertManagerURL})
	}
}

// inventoryGroup represents one collapsible section on the inventory
// page. Group key (channel / team / AM URL) varies by ?group= param.
type inventoryGroup struct {
	// Label is what appears in the group header.
	Label string
	// SubLabel is a smaller secondary line (e.g., team name for a channel group).
	SubLabel string
	// ChannelURLPath, when non-empty, makes the group label a clickable
	// link to the channel in Mattermost (only set for channel-grouped view).
	ChannelURLPath string
	// AMReachable / AMStatus are populated for AM-URL-grouped view OR
	// for the per-receiver row in other views. Empty AMStatus = not probed.
	AMReachable bool
	AMStatus    string
	Receivers   []inventoryRow
}

// inventoryRow is one row in a group's table.
type inventoryRow struct {
	Name            string
	Team            string
	Channel         string
	ChannelURLPath  string // clickable link target ("/team/channels/channelname"), empty if unresolvable
	AlertManagerURL string
	AMReachable     bool
	AMStatus        string
	LoadedInAM      bool // receiver name found in AM's loaded YAML

	// HealthLabel + HealthClass are pre-computed for the template
	// so the row can render an actionable status badge instead of
	// only an ambiguous colored dot. See computeHealth().
	HealthLabel string // "OK", "Not in AM YAML", "AM unreachable: timeout", etc.
	HealthClass string // "ok" | "warn" | "bad" — drives CSS color
}

// computeHealth fills HealthLabel + HealthClass based on the row's
// AM reachability + LoadedInAM state. Lets the template render the
// actual reason instead of just a colored dot.
//
// Precedence (worst first):
//  1. AM unreachable → "AM unreachable: <reason>"  (bad / red)
//  2. AM reachable but receiver not in YAML → "Not in AM YAML"  (warn / orange)
//  3. Healthy → "OK"  (ok / green)
//
// The "Not in AM YAML" case is the silent-failure mode: plugin thinks
// the receiver exists, but the user forgot to paste the assembled YAML
// into alertmanager.yml (or reloaded AM without picking it up).
func (r *inventoryRow) computeHealth() {
	if r.AlertManagerURL == "" {
		r.HealthLabel = "No AM URL configured"
		r.HealthClass = "warn"
		return
	}
	if !r.AMReachable {
		reason := r.AMStatus
		if reason == "" {
			reason = "unknown"
		}
		r.HealthLabel = "AM unreachable: " + reason
		r.HealthClass = "bad"
		return
	}
	if !r.LoadedInAM {
		r.HealthLabel = "Not in AM YAML"
		r.HealthClass = "warn"
		return
	}
	r.HealthLabel = "OK"
	r.HealthClass = "ok"
}

// renderInventoryHTML is the main page renderer. Reads ?group= for the
// grouping mode (channel|team|am, default channel).
func (p *Plugin) renderInventoryHTML(w http.ResponseWriter, r *http.Request, configs []alertConfig) {
	groupMode := r.URL.Query().Get("group")
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
			row.LoadedInAM = entry.LoadedInAM(c.Name)
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
	// (or rotated a webhook via /alertmanager rotate without
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
	for amURL, entry := range amStatus {
		if !entry.Reachable || entry.ConfigBody == "" {
			continue
		}
		amNames := extractAMReceiverNames(entry.ConfigBody)
		pluginSet := pluginNamesPerAM[amURL]
		var driftNames []string
		for _, n := range amNames {
			if !pluginSet[n] {
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
	simMode := strings.TrimSpace(r.URL.Query().Get("simulate_mode"))
	simType := strings.TrimSpace(r.URL.Query().Get("simulate_type"))
	simValue := strings.TrimSpace(r.URL.Query().Get("simulate_value"))
	simChannel := strings.TrimSpace(r.URL.Query().Get("simulate_channel"))
	simTeam := strings.TrimSpace(r.URL.Query().Get("simulate_team"))
	simSeverity := strings.TrimSpace(r.URL.Query().Get("simulate_severity"))
	simExtra := strings.TrimSpace(r.URL.Query().Get("simulate_extra"))

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
		case "webhook-test":
			simActionResult = p.runInventoryWebhookTest(targetSlugs, filteredConfigs)
		case "end-to-end":
			simActionResult = p.runInventoryEndToEnd(targetSlugs, simSeverity, simExtra, filteredConfigs, amStatus)
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
		err := postValidateTestMessage(webhookURL, ac.Name)
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
		// see which combinations landed when --severity=all multiplies
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
				item.Detail = err.Error()
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
// Same engine as the /alertmanager validate --simulate command — just
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

// groupInventory bucketizes rows by the chosen mode. Result is sorted
// for deterministic output.
func groupInventory(rows []inventoryRow, mode string) []inventoryGroup {
	buckets := make(map[string][]inventoryRow)
	for _, r := range rows {
		key := groupKey(r, mode)
		buckets[key] = append(buckets[key], r)
	}

	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	groups := make([]inventoryGroup, 0, len(buckets))
	for _, key := range keys {
		entries := buckets[key]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		g := inventoryGroup{
			Label:     key,
			Receivers: entries,
		}
		switch mode {
		case "channel":
			// Channels are unique per team in MM, but receivers in the
			// same channel always share a team — pull from first entry.
			g.SubLabel = "Team: " + entries[0].Team
			g.ChannelURLPath = entries[0].ChannelURLPath
		case "team":
			g.SubLabel = fmt.Sprintf("%d receivers", len(entries))
		case "am":
			// AM URL grouping: surface reachability on the group header itself.
			if len(entries) > 0 {
				g.AMReachable = entries[0].AMReachable
				g.AMStatus = entries[0].AMStatus
			}
			g.SubLabel = fmt.Sprintf("%d receivers", len(entries))
		}
		groups = append(groups, g)
	}
	return groups
}

func groupKey(r inventoryRow, mode string) string {
	switch mode {
	case "team":
		return r.Team
	case "am":
		if r.AlertManagerURL == "" {
			return "(no AM URL)"
		}
		return r.AlertManagerURL
	default:
		return r.Channel
	}
}

func countDistinctChannels(configs []alertConfig) int {
	seen := make(map[string]bool)
	for _, c := range configs {
		seen[c.Channel] = true
	}
	return len(seen)
}

// humanDuration formats a duration as a short human-readable string
// for the reconciler health banner.
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// inventoryTemplate is the rich HTML shell for /admin/inventory.
// Includes:
//   - Reconciler health banner (top)
//   - Per-team rollup
//   - Group-by toggle (channel / team / AM URL)
//   - CSV export link
//   - Client-side search box
//   - Per-AM reachability dots (when grouping by AM)
//   - Clickable channel names (when grouping by channel)
//   - Fixed-width columns for cross-group alignment
var inventoryTemplate = template.Must(template.New("inventory").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>Alertmanager Plugin Inventory</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; color: #1a1a1a; background: #f7f7f8; padding: 32px; margin: 0; }
        h1 { margin: 0 0 4px; }
        .meta { color: #666; font-size: 13px; margin-bottom: 16px; }
        .health { display: inline-block; padding: 6px 12px; border-radius: 4px; font-size: 13px; margin-bottom: 16px; }
        .health.ok { background: #d4f5dd; color: #16753a; }
        .health.warn { background: #ffe9c2; color: #8a5a00; }
        .summary { background: white; padding: 16px 20px; border-radius: 6px; box-shadow: 0 1px 3px rgba(0,0,0,0.06); margin-bottom: 24px; max-width: 1200px; }
        .summary h3 { margin: 0 0 8px; font-size: 13px; text-transform: uppercase; letter-spacing: 0.04em; color: #666; }
        .summary-list { display: flex; flex-wrap: wrap; gap: 8px 24px; font-size: 14px; }
        .controls { display: flex; gap: 16px; align-items: center; margin-bottom: 16px; max-width: 1200px; flex-wrap: wrap; }
        .controls input[type=search] { padding: 8px 12px; border: 1px solid #ccc; border-radius: 4px; font-size: 14px; flex: 1; min-width: 250px; }
        .controls .toggle { display: flex; gap: 4px; }
        .controls .toggle a { padding: 6px 12px; background: white; border: 1px solid #ccc; border-radius: 4px; text-decoration: none; color: #1a1a1a; font-size: 13px; }
        .controls .toggle a.active { background: #1d3a8c; color: white; border-color: #1d3a8c; }
        .controls .csv-link { padding: 6px 12px; background: white; border: 1px solid #ccc; border-radius: 4px; text-decoration: none; color: #1a1a1a; font-size: 13px; }
        .group { background: white; border-radius: 6px; box-shadow: 0 1px 3px rgba(0,0,0,0.08); margin-bottom: 24px; overflow: hidden; max-width: 1200px; }
        .group-header { background: #1d3a8c; color: white; padding: 12px 20px; display: flex; justify-content: space-between; align-items: center; }
        .group-header h2 { margin: 0; font-size: 16px; }
        .group-header h2 a { color: white; text-decoration: none; }
        .group-header h2 a:hover { text-decoration: underline; }
        .group-header .sub { font-size: 12px; opacity: 0.8; }
        .dot { display: inline-block; width: 10px; height: 10px; border-radius: 50%; margin-right: 6px; vertical-align: middle; }
        .dot.ok { background: #4ade80; }
        .dot.bad { background: #ef4444; }
        .dot.warn { background: #f59e0b; }
        .status { display: inline-block; padding: 2px 8px; border-radius: 10px; font-size: 11px; font-weight: 500; margin-left: 8px; vertical-align: middle; white-space: nowrap; }
        .status.ok { background: #d4f5dd; color: #16753a; }
        .status.warn { background: #ffe9c2; color: #8a5a00; }
        .status.bad { background: #fde2e2; color: #9b2222; }
        .status.drift { background: #ffe9c2; color: #8a5a00; }
        .drift-group { background: white; border-radius: 6px; box-shadow: 0 1px 3px rgba(0,0,0,0.08); margin-bottom: 24px; overflow: hidden; max-width: 1200px; border-left: 4px solid #f59e0b; }
        .drift-group .group-header { background: #8a5a00; }
        .drift-group .group-header .sub { opacity: 0.9; }
        .legend { font-size: 13px; color: #555; margin-bottom: 16px; max-width: 1200px; background: white; padding: 12px 16px; border-radius: 6px; box-shadow: 0 1px 3px rgba(0,0,0,0.06); }
        .legend .row { display: flex; align-items: flex-start; gap: 8px; padding: 4px 0; }
        .legend .status { margin-left: 0; margin-right: 0; flex-shrink: 0; min-width: 140px; text-align: center; }
        .legend .desc { line-height: 1.4; }
        table { width: 100%; border-collapse: collapse; table-layout: fixed; }
        th, td { padding: 10px 20px; text-align: left; font-size: 14px; border-bottom: 1px solid #eee; vertical-align: top; word-break: break-all; }
        th:first-child, td:first-child { width: 55%; }
        th:last-child, td:last-child { width: 45%; }
        th { background: #f2f3f5; font-weight: 600; }
        tr:last-child td { border-bottom: none; }
        tr.hidden { display: none; }
        code { background: #eef0f3; padding: 2px 6px; border-radius: 3px; font-family: "SFMono-Regular", Menlo, Monaco, monospace; font-size: 12px; word-break: break-all; }
        .empty { background: white; padding: 32px; text-align: center; color: #888; border-radius: 6px; max-width: 1200px; }
    </style>
</head>
<body>
    <h1>Alertmanager Plugin Inventory</h1>
    <div class="meta">{{.Total}} receivers across {{.Channels}} channels · plugin v{{.Version}}</div>

    <div class="health {{if .ReconcilerHealthy}}ok{{else}}warn{{end}}">
        <strong>Reconciler:</strong> {{.ReconcilerStatus}}
    </div>

    {{if .TeamRollup}}
    <div class="summary">
        <h3>By team</h3>
        <div class="summary-list">
            {{range .TeamRollup}}<span><strong>{{.Team}}:</strong> {{.Count}}</span>{{end}}
        </div>
    </div>
    {{end}}

    <div class="summary" style="background: #f8fafc; border-left: 4px solid #1d3a8c;">
        <h3>:mag: Route tester</h3>
        <div style="font-size: 13px; color: #555; margin-bottom: 12px;">Three modes for verifying your alerting pipeline:
            <ul style="margin: 4px 0 8px 20px; padding: 0;">
                <li><strong>Simulate</strong> — walks Alertmanager's loaded route tree, reports matched receivers. Read-only.</li>
                <li><strong>Webhook test</strong> — POSTs a hardcoded test payload to each receiver's Mattermost webhook. Bypasses Alertmanager. Tests the MM side only.</li>
                <li><strong>End-to-end</strong> — fires a synthetic alert through Alertmanager. Tests the full chain. Real chat posts result.</li>
            </ul>
        </div>
        <form method="get" action="" style="display: flex; flex-direction: column; gap: 10px;">
            <input type="hidden" name="group" value="{{.GroupMode}}">
            <div style="display: flex; gap: 12px; align-items: flex-end; flex-wrap: wrap;">
                <div style="display: flex; flex-direction: column; gap: 4px;">
                    <span style="font-size: 11px; font-weight: 600; color: #666; text-transform: uppercase; letter-spacing: 0.04em;">Mode</span>
                    <select name="simulate_mode" style="padding: 6px 8px; border: 1px solid #ccc; border-radius: 4px; font-size: 13px;">
                        <option value="simulate" {{if or (eq "simulate" .SimulateMode) (eq "" .SimulateMode)}}selected{{end}}>Simulate (read-only)</option>
                        <option value="webhook-test" {{if eq "webhook-test" .SimulateMode}}selected{{end}}>Webhook test</option>
                        <option value="end-to-end" {{if eq "end-to-end" .SimulateMode}}selected{{end}}>End-to-end</option>
                    </select>
                </div>
                <div style="display: flex; flex-direction: column; gap: 4px;">
                    <span style="font-size: 11px; font-weight: 600; color: #666; text-transform: uppercase; letter-spacing: 0.04em;">Type</span>
                    <select name="simulate_type" style="padding: 6px 8px; border: 1px solid #ccc; border-radius: 4px; font-size: 13px; min-width: 140px;">
                        <option value="">(pick type)</option>
                        <option value="all" {{if eq "all" .SimulateType}}selected{{end}}>All runbooks</option>
                        <option value="group" {{if eq "group" .SimulateType}}selected{{end}}>Group</option>
                        <option value="individual" {{if eq "individual" .SimulateType}}selected{{end}}>Individual</option>
                    </select>
                </div>
                <div style="display: flex; flex-direction: column; gap: 4px;">
                    <span style="font-size: 11px; font-weight: 600; color: #666; text-transform: uppercase; letter-spacing: 0.04em;">Target</span>
                    <select name="simulate_value" style="padding: 6px 8px; border: 1px solid #ccc; border-radius: 4px; font-size: 13px; min-width: 200px;">
                        <option value="">(pick type first)</option>
                    </select>
                </div>
                <div style="display: flex; flex-direction: column; gap: 4px;">
                    <span style="font-size: 11px; font-weight: 600; color: #666; text-transform: uppercase; letter-spacing: 0.04em;">Team</span>
                    <select name="simulate_team" style="padding: 6px 8px; border: 1px solid #ccc; border-radius: 4px; font-size: 13px; min-width: 160px;">
                        <option value="">(any team)</option>
                        {{range .TeamOptions}}<option value="{{.}}" {{if eq . $.SimulateTeam}}selected{{end}}>{{.}}</option>{{end}}
                    </select>
                </div>
                <div style="display: flex; flex-direction: column; gap: 4px;">
                    <span style="font-size: 11px; font-weight: 600; color: #666; text-transform: uppercase; letter-spacing: 0.04em;">Channel</span>
                    <select name="simulate_channel" style="padding: 6px 8px; border: 1px solid #ccc; border-radius: 4px; font-size: 13px; min-width: 200px;">
                        <option value="">(any channel)</option>
                        {{range .ChannelOptions}}<option value="{{.}}" {{if eq . $.SimulateChannel}}selected{{end}}>{{.}}</option>{{end}}
                    </select>
                </div>
                <div id="sim-severity-field" style="display: flex; flex-direction: column; gap: 4px;">
                    <span style="font-size: 11px; font-weight: 600; color: #666; text-transform: uppercase; letter-spacing: 0.04em;">Severity</span>
                    <select name="simulate_severity" style="padding: 6px 8px; border: 1px solid #ccc; border-radius: 4px; font-size: 13px;">
                        <option value="">(none)</option>
                        <option value="warning" {{if eq "warning" .SimulateSeverity}}selected{{end}}>warning</option>
                        <option value="critical" {{if eq "critical" .SimulateSeverity}}selected{{end}}>critical</option>
                        <option value="info" {{if eq "info" .SimulateSeverity}}selected{{end}}>info</option>
                        <option value="all" {{if eq "all" .SimulateSeverity}}selected{{end}}>all (end-to-end only — fires warning+critical+info+resolved)</option>
                    </select>
                </div>
            </div>
            <input type="text" name="simulate_extra" value="{{.SimulateExtra}}" placeholder="Additional labels (optional): severity=critical namespace=billing pod=api-7d9 service=checkout" style="padding: 8px 12px; border: 1px solid #ccc; border-radius: 4px; font-size: 13px; font-family: SFMono-Regular, Menlo, Monaco, monospace;">
            <div>
                <button type="submit" style="padding: 8px 16px; background: #1d3a8c; color: white; border: none; border-radius: 4px; font-size: 13px; cursor: pointer;">Run</button>
                <a href="?group={{.GroupMode}}" style="margin-left: 8px; padding: 8px 12px; font-size: 13px; color: #666; text-decoration: none;">Reset</a>
                {{if or (eq "webhook-test" .SimulateMode) (eq "end-to-end" .SimulateMode)}}
                <span style="margin-left: 12px; font-size: 12px; color: #9b2222;">:warning: This mode posts real messages to MM — pick a test channel.</span>
                {{end}}
            </div>
        </form>

        {{if .SimulateAction.Mode}}
        <div style="margin-top: 16px; padding: 0; background: white; border-radius: 4px; border: 1px solid #ddd; overflow: hidden;">
            {{if eq .SimulateAction.Mode "error"}}
            <div style="padding: 12px 16px;">
                <div style="font-weight: 600; color: #9b2222; margin-bottom: 8px;">:x: Action failed</div>
                <div style="font-size: 13px;">{{.SimulateAction.Error}}</div>
            </div>
            {{else}}
            <div style="padding: 10px 16px; background: #f2f3f5; font-weight: 600; font-size: 13px;">
                {{if eq .SimulateAction.Mode "webhook-test"}}Webhook test results{{else}}End-to-end fire results{{end}}{{if .SimulateAction.Summary}} — {{.SimulateAction.Summary}}{{end}}
            </div>
            <table style="margin: 0;">
                <thead>
                    <tr><th style="width: 50%;">{{if eq .SimulateAction.Mode "webhook-test"}}Receiver{{else}}Runbook{{end}}</th><th style="width: 50%;">Result</th></tr>
                </thead>
                <tbody>
                    {{range .SimulateAction.Items}}
                    <tr>
                        <td><code>{{.Name}}</code></td>
                        <td>{{if .OK}}<span class="status ok">OK</span>{{else}}<span class="status bad">FAIL</span>{{end}} {{.Detail}}</td>
                    </tr>
                    {{end}}
                </tbody>
            </table>
            {{end}}
        </div>
        {{end}}

        {{if .SimulateMatrix}}
        <div style="margin-top: 16px; padding: 0; background: white; border-radius: 4px; border: 1px solid #ddd; overflow: hidden;">
            <div style="padding: 10px 16px; background: #f2f3f5; font-weight: 600; font-size: 13px;">
                Coverage matrix — every shipped runbook simulated{{if .SimulateSeverity}} with <code>severity={{.SimulateSeverity}}</code>{{end}}{{if .SimulateExtra}} plus <code>{{.SimulateExtra}}</code>{{end}}
            </div>
            <table style="margin: 0;">
                <thead>
                    <tr><th style="width: 35%;">Runbook</th><th style="width: 65%;">Would dispatch to</th></tr>
                </thead>
                <tbody>
                    {{range .SimulateMatrix}}
                    <tr>
                        <td><code>{{.MatrixRow}}</code></td>
                        <td>
                            {{if eq .Mode "ok"}}
                                {{range .Receivers}}<code>{{.}}</code> {{end}}
                            {{else if eq .Mode "fell-through"}}
                                <span class="status warn">fell through</span> default: <code>{{.DefaultRx}}</code>
                            {{else if eq .Mode "error"}}
                                <span class="status bad">error</span> {{.Message}}
                            {{end}}
                        </td>
                    </tr>
                    {{end}}
                </tbody>
            </table>
        </div>
        {{end}}

        {{if .SimulateResult.Mode}}
        <div style="margin-top: 16px; padding: 12px 16px; background: white; border-radius: 4px; border: 1px solid #ddd;">
            {{if eq .SimulateResult.Mode "ok"}}
                <div style="font-weight: 600; color: #16753a; margin-bottom: 8px;">:white_check_mark: Would dispatch to {{len .SimulateResult.Receivers}} receiver(s)</div>
                <div style="font-size: 12px; color: #666; margin-bottom: 8px;">Against Alertmanager: <code>{{.SimulateResult.AMURL}}</code></div>
                <div style="font-size: 13px; margin-bottom: 4px;"><strong>Receivers:</strong></div>
                <ul style="margin: 0 0 8px 16px; padding: 0;">
                    {{range .SimulateResult.Receivers}}<li><code>{{.}}</code></li>{{end}}
                </ul>
            {{else if eq .SimulateResult.Mode "fell-through"}}
                <div style="font-weight: 600; color: #8a5a00; margin-bottom: 8px;">:warning: No sub-route matched — alert would fall through to default</div>
                <div style="font-size: 12px; color: #666; margin-bottom: 8px;">Against Alertmanager: <code>{{.SimulateResult.AMURL}}</code></div>
                <div style="font-size: 13px;"><strong>Default receiver:</strong> <code>{{.SimulateResult.DefaultRx}}</code></div>
                <div style="font-size: 12px; color: #666; margin-top: 8px;">If you expected this alert to hit a specific runbook receiver, check that your rule emits the <code>runbook=&lt;slug&gt;</code> label (or whatever label your <code>route.routes[].matchers:</code> block keys on).</div>
            {{else if eq .SimulateResult.Mode "error"}}
                <div style="font-weight: 600; color: #9b2222; margin-bottom: 8px;">:x: Simulation failed</div>
                <div style="font-size: 13px;">{{.SimulateResult.Message}}</div>
            {{end}}
            {{if .SimulateResult.Labels}}
            <div style="font-size: 12px; color: #666; margin-top: 8px;"><strong>Input labels:</strong>
                {{range $k, $v := .SimulateResult.Labels}}<code>{{$k}}={{$v}}</code> {{end}}
            </div>
            {{end}}
        </div>
        {{end}}
    </div>

    <div class="legend">
        <div class="row"><span class="status ok">OK</span><span class="desc">Receiver is loaded in AM and AM is reachable.</span></div>
        <div class="row"><span class="status warn">Not in AM YAML</span><span class="desc">Receiver in plugin config but missing from AM's loaded config (paste the latest receivers.yml + reload AM).</span></div>
        <div class="row"><span class="status bad">AM unreachable</span><span class="desc">Plugin can't reach the Alertmanager URL (network, TLS, or AM down).</span></div>
        <div class="row"><span class="status drift">AM-only</span><span class="desc">Loaded in AM but NOT tracked by the plugin (someone hand-edited alertmanager.yml). Shown in the orange "AM-only receivers" section below.</span></div>
    </div>

    <div class="controls">
        <input type="search" id="filter" placeholder="Filter (matches receiver name, team, channel, AM URL)…" />
        <div class="toggle">
            <a href="?group=channel" class="{{if eq .GroupMode "channel"}}active{{end}}">By channel</a>
            <a href="?group=team" class="{{if eq .GroupMode "team"}}active{{end}}">By team</a>
            <a href="?group=am" class="{{if eq .GroupMode "am"}}active{{end}}">By Alertmanager</a>
        </div>
        <a class="csv-link" href="{{.CSVHref}}">Download CSV</a>
    </div>

    {{if .AMDrift}}
    {{range .AMDrift}}
    <div class="drift-group">
        <div class="group-header">
            <h2>⚠ AM-only receivers</h2>
            <div class="sub">{{.AMURL}} · {{len .Names}} receiver(s) loaded in AM but not tracked by the plugin. Usually means someone hand-edited alertmanager.yml outside the plugin lifecycle, OR a /alertmanager rotate ran but the new YAML wasn't pasted in. Investigate before they go stale.</div>
        </div>
        <table>
            <thead>
                <tr><th>Receiver name (AM-side)</th><th>Plugin status</th></tr>
            </thead>
            <tbody>
                {{range .Names}}
                <tr class="receiver-row">
                    <td><code>{{.}}</code></td>
                    <td><span class="status drift">AM-only</span> <em>(no plugin entry)</em></td>
                </tr>
                {{end}}
            </tbody>
        </table>
    </div>
    {{end}}
    {{end}}

    {{if .Groups}}
    {{range .Groups}}
    <div class="group">
        <div class="group-header">
            <h2>
                {{if eq $.GroupMode "am"}}<span class="dot {{if .AMReachable}}ok{{else}}bad{{end}}"></span>{{end}}
                {{if .ChannelURLPath}}<a href="{{.ChannelURLPath}}">~{{.Label}}</a>{{else}}{{if eq $.GroupMode "channel"}}~{{end}}{{.Label}}{{end}}
            </h2>
            <div class="sub">{{.SubLabel}}{{if and (eq $.GroupMode "am") .AMStatus}} · {{.AMStatus}}{{end}}</div>
        </div>
        <table>
            <thead>
                <tr><th>Receiver</th><th>Alertmanager URL</th></tr>
            </thead>
            <tbody>
                {{range .Receivers}}
                <tr class="receiver-row">
                    <td>
                        <code>{{.Name}}</code>
                        <span class="status {{.HealthClass}}">{{.HealthLabel}}</span>
                    </td>
                    <td>
                        {{if .AlertManagerURL -}}
                        <code>{{.AlertManagerURL}}</code>
                        {{- else}}<em>(none)</em>{{end}}
                    </td>
                </tr>
                {{end}}
            </tbody>
        </table>
    </div>
    {{end}}
    {{else}}
    <div class="empty">No receivers registered.</div>
    {{end}}

    <script>
        // Client-side filter: hide receiver rows whose text doesn't
        // include the query (case-insensitive substring match). Group
        // headers stay visible even when all their rows are filtered
        // out — they show as empty tables. Simpler than hiding empty
        // groups; the user can see at a glance which groups had matches.
        document.getElementById('filter').addEventListener('input', function (e) {
            const q = e.target.value.toLowerCase();
            document.querySelectorAll('.receiver-row').forEach(function (row) {
                const text = row.textContent.toLowerCase();
                if (q === '' || text.indexOf(q) !== -1) {
                    row.classList.remove('hidden');
                } else {
                    row.classList.add('hidden');
                }
            });
        });

        // Two-stage cascade for the route tester form.
        //
        // Stage 1: Type → Value. When the admin picks Type=group,
        // the Value dropdown swaps to the list of groups. Type=individual
        // → list of runbook slugs. Type=all → Value is disabled
        // (irrelevant; the whole runbook set is the target).
        //
        // Stage 2: (Type, Value) → Channel. Only channels with at
        // least one receiver matching the chosen (type, value) get
        // shown. Empty selection or "all" type → every channel with
        // any plugin receiver.
        //
        // All three dropdowns preserve their server-rendered initial
        // values across the cascade so a deep-linked form (bookmarked
        // ?simulate_type=...&simulate_value=...&simulate_channel=...)
        // loads with the picks intact.
        const targetChannels = {{.TargetChannelsJSON}};
        const teamChannels = {{.TeamChannelsJSON}};
        const allGroups = {{.GroupOptionsJSON}};
        const allSlugs = {{.RunbookSlugsJSON}};
        const initialType = {{.SimulateType | printf "%q"}};
        const initialValue = {{.SimulateValue | printf "%q"}};
        const initialChannel = {{.SimulateChannel | printf "%q"}};
        const initialTeam = {{.SimulateTeam | printf "%q"}};

        const typeSel = document.querySelector('select[name="simulate_type"]');
        const valueSel = document.querySelector('select[name="simulate_value"]');
        const channelSel = document.querySelector('select[name="simulate_channel"]');
        const teamSel = document.querySelector('select[name="simulate_team"]');

        function refreshValueOptions() {
            if (!typeSel || !valueSel) return;
            const t = typeSel.value;
            const desired = valueSel.value || initialValue;
            let opts;
            if (t === "all") {
                opts = '<option value="">(no further selection)</option>';
                valueSel.disabled = true;
            } else if (t === "group") {
                opts = '<option value="">(pick a group)</option>';
                for (const g of allGroups) {
                    const sel = g === desired ? ' selected' : '';
                    opts += '<option value="' + g + '"' + sel + '>' + g + '</option>';
                }
                valueSel.disabled = false;
            } else if (t === "individual") {
                opts = '<option value="">(pick a runbook)</option>';
                for (const s of allSlugs) {
                    const sel = s === desired ? ' selected' : '';
                    opts += '<option value="' + s + '"' + sel + '>' + s + '</option>';
                }
                valueSel.disabled = false;
            } else {
                opts = '<option value="">(pick type first)</option>';
                valueSel.disabled = true;
            }
            valueSel.innerHTML = opts;
        }

        function computeTargetKey() {
            if (!typeSel || !valueSel) return "";
            const t = typeSel.value;
            if (t === "all") return "all";
            if (t === "group" && valueSel.value) return "group:" + valueSel.value;
            if (t === "individual" && valueSel.value) return "individual:" + valueSel.value;
            return "";
        }

        function refreshChannelOptions() {
            if (!channelSel) return;
            const key = computeTargetKey();
            let channels = (targetChannels[key] || targetChannels[""] || []);
            // Narrow to the selected team's channels — channel names repeat
            // across teams, so without this the dropdown mixes them.
            const team = teamSel ? teamSel.value : "";
            if (team && teamChannels[team]) {
                const allowed = teamChannels[team];
                channels = channels.filter(function(c) { return allowed.indexOf(c) !== -1; });
            }
            const desired = channelSel.value || initialChannel;
            let opts = '<option value="">(any channel)</option>';
            for (const c of channels) {
                const sel = c === desired ? ' selected' : '';
                opts += '<option value="' + c + '"' + sel + '>' + c + '</option>';
            }
            channelSel.innerHTML = opts;
        }

        if (typeSel && valueSel && channelSel) {
            typeSel.addEventListener('change', function() {
                refreshValueOptions();
                refreshChannelOptions();
            });
            valueSel.addEventListener('change', refreshChannelOptions);
            if (teamSel) teamSel.addEventListener('change', refreshChannelOptions);
            // Initial render — restore server-rendered values, then
            // populate downstream dropdowns based on them.
            if (initialType) typeSel.value = initialType;
            if (teamSel && initialTeam) teamSel.value = initialTeam;
            refreshValueOptions();
            refreshChannelOptions();
        }

        // Severity is an end-to-end-only knob (see server comment). Show the
        // field only in that mode; disable it otherwise so it never submits —
        // a severity in simulate/webhook-test is misleading (routes key on the
        // runbook label). Admins who really want severity in a simulate put it
        // in the extra-labels box.
        const modeSel = document.querySelector('select[name="simulate_mode"]');
        const sevField = document.getElementById('sim-severity-field');
        const sevSel = document.querySelector('select[name="simulate_severity"]');
        function refreshSeverityVisibility() {
            if (!modeSel || !sevField || !sevSel) return;
            const show = modeSel.value === 'end-to-end';
            sevField.style.display = show ? '' : 'none';
            sevSel.disabled = !show;
        }
        if (modeSel) {
            modeSel.addEventListener('change', refreshSeverityVisibility);
            refreshSeverityVisibility();
        }
    </script>
</body>
</html>
`))
