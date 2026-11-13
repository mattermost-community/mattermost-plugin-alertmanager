package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type teamRow struct {
	Team  string
	Count int
}

type amDriftGroup struct {
	AMURL string
	Names []string
}

// inventoryPageData is everything inventory.html renders.
type inventoryPageData struct {
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
}

// inventoryForm is the route tester's submitted state.
//
// Three modes:
//
//	simulate     → read-only, walk AM route tree against labels
//	webhook-test → POST a hardcoded test payload to each target's
//	               webhook URL (tests MM side only, bypasses AM)
//	end-to-end   → POST a synthetic alert to AM, AM templates +
//	               delivers (tests the full chain)
//
// The target is a type (`all`, `group`, `individual`) plus a value; channel and
// team narrow the receivers for webhook-test and end-to-end (simulate walks the
// route tree at the AM level).
type inventoryForm struct {
	mode     string
	typ      string
	value    string
	channel  string
	team     string
	severity string
	extra    string
}

// parseInventoryForm reads the fields via FormValue so the side-effecting modes
// can arrive as a POST body (see the CL-20 gate in runInventoryForm); read-only
// simulate still works as a GET query.
func parseInventoryForm(r *http.Request) inventoryForm {
	f := inventoryForm{
		mode:     strings.TrimSpace(r.FormValue("simulate_mode")),
		typ:      strings.TrimSpace(r.FormValue("simulate_type")),
		value:    strings.TrimSpace(r.FormValue("simulate_value")),
		channel:  strings.TrimSpace(r.FormValue("simulate_channel")),
		team:     strings.TrimSpace(r.FormValue("simulate_team")),
		severity: strings.TrimSpace(r.FormValue("simulate_severity")),
		extra:    strings.TrimSpace(r.FormValue("simulate_extra")),
	}
	// Severity is a knob only for end-to-end (it sets the severity the
	// synthetic alert fires at). Webhook-test ignores it, and simulate routes
	// on the `runbook` label, not severity — a severity-only simulate just
	// falls through to the default route and confuses people. So drop the
	// dedicated severity outside end-to-end; an admin who genuinely has
	// severity-based routes still simulates them via the extra-labels box.
	if f.mode != "end-to-end" {
		f.severity = ""
	}
	return f
}

func (f inventoryForm) submitted() bool {
	return f.mode != "" || f.typ != "" || f.value != "" || f.channel != "" || f.team != "" || f.severity != "" || f.extra != ""
}

// inventoryGroupMode reads the grouping mode (channel|team|am, default channel)
// from group= via FormValue so it survives the CSRF-driven POST swap (which
// drops the query string) the same way the simulate_* fields do.
func inventoryGroupMode(r *http.Request) string {
	mode := r.FormValue("group")
	if mode != "team" && mode != "am" {
		return "channel"
	}
	return mode
}

// probeInventoryAMs probes each unique AM URL once, populating the
// reachability cache. The probe is cached for amReachabilityTTL — repeat page
// loads within the TTL window don't re-ping. Up to maxParallelAMCalls probes run
// at once, so the page waits for the slowest Alertmanager rather than the sum.
func (p *Plugin) probeInventoryAMs(configs []alertConfig) map[string]amReachabilityEntry {
	urls := uniqueAMURLs(configs)
	entries := make([]amReachabilityEntry, len(urls))
	sem := make(chan struct{}, maxParallelAMCalls)
	var wg sync.WaitGroup
	for i, url := range urls {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			entries[i] = p.probeAMReachability(url)
		})
	}
	wg.Wait()
	amStatus := make(map[string]amReachabilityEntry, len(urls))
	for i, url := range urls {
		amStatus[url] = entries[i]
	}
	return amStatus
}

// buildInventoryRows builds one row per receiver and resolves channel→team URL
// paths for clickable links. p.API.GetChannelByName needs the team ID, but
// configs only carry the team SLUG, so each team slug is resolved once.
func (p *Plugin) buildInventoryRows(configs []alertConfig, amStatus map[string]amReachabilityEntry) []inventoryRow {
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
	return rows
}

// inventoryTeamRollup counts receivers per team, sorted by team name.
func inventoryTeamRollup(configs []alertConfig) []teamRow {
	counts := make(map[string]int)
	for _, c := range configs {
		counts[c.Team]++
	}
	rows := make([]teamRow, 0, len(counts))
	for t, n := range counts {
		rows = append(rows, teamRow{t, n})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Team < rows[j].Team })
	return rows
}

// reconcilerBanner summarises the last reconcile run for the health banner.
func (p *Plugin) reconcilerBanner() (status string, healthy bool) {
	lastRun, lastPruned := p.reconcileStatus()
	if lastRun.IsZero() {
		return "scheduled, first cycle pending — appears within ~5 minutes of plugin activation", false
	}
	age := time.Since(lastRun)
	// Healthy if within 2x the configured interval (= 10 min by default).
	return fmt.Sprintf("%s ago (pruned %d on last run)", humanDuration(age), lastPruned), age < 10*time.Minute
}

// amOnlyReceivers finds receivers that exist in AM's loaded config but have no
// matching plugin entry. Means someone hand-edited alertmanager.yml outside the
// plugin's lifecycle (or rotated a webhook via /alertmanager config rotate
// without pasting the new YAML into AM yet).
//
// Computed per AM URL because each backend has its own loaded config. Limited
// to reachable backends — we can't drift-check against AM if we couldn't fetch
// its config in this cycle.
func (p *Plugin) amOnlyReceivers(configs []alertConfig, amStatus map[string]amReachabilityEntry) []amDriftGroup {
	pluginNamesPerAM := make(map[string]map[string]bool)
	for _, c := range configs {
		if pluginNamesPerAM[c.AlertManagerURL] == nil {
			pluginNamesPerAM[c.AlertManagerURL] = make(map[string]bool)
		}
		pluginNamesPerAM[c.AlertManagerURL][c.Name] = true
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
	return amDrift
}

// runInventoryForm runs the route tester action the form asked for.
func (p *Plugin) runInventoryForm(r *http.Request, f inventoryForm, configs []alertConfig, amStatus map[string]amReachabilityEntry) (result inventorySimResult, matrix []inventorySimResult, action inventoryActionResult) {
	if !f.submitted() {
		return result, nil, action
	}
	// Decode (type, value) into a list of slugs.
	targetSlugs := decodeTargetSelection(f.typ, f.value)
	filteredConfigs := filterConfigsByChannel(configs, f.team, f.channel)

	switch f.mode {
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
		case !simSideEffectAllowed(r.Method, r.Header.Get("X-Requested-With"), f.mode):
			action = inventoryActionResult{
				Mode:  "error",
				Error: "This action changes state (posts to webhooks / fires alerts). Run it with the Fire button, not by opening a link — the request must be a POST issued by the page (CSRF protection).",
			}
		case f.mode == "webhook-test":
			action = p.runInventoryWebhookTest(targetSlugs, filteredConfigs)
		default:
			action = p.runInventoryEndToEnd(targetSlugs, f.severity, f.extra, filteredConfigs, amStatus)
		}
	default:
		// simulate (default)
		switch {
		case len(targetSlugs) > 1:
			matrix = p.runInventorySimulationMatrixForSlugs(targetSlugs, f.severity, f.extra, configs, amStatus)
		case len(targetSlugs) == 1:
			result = p.runInventorySimulation(buildSimulateLabelsInput(targetSlugs[0], f.severity, f.extra), configs, amStatus)
		case f.severity != "" || f.extra != "":
			// No slugs selected — sim with severity/extra alone
			result = p.runInventorySimulation(buildSimulateLabelsInput("", f.severity, f.extra), configs, amStatus)
		default:
			result = inventorySimResult{Mode: "error", Message: "Pick a target type + group/runbook OR supply labels via the extras field."}
		}
	}
	return result, matrix, action
}

// inventoryChannelOptions lists the distinct channel names for the Channel dropdown.
func inventoryChannelOptions(configs []alertConfig) []string {
	set := make(map[string]bool)
	for _, c := range configs {
		set[c.Channel] = true
	}
	return sortedKeys(set)
}

// inventoryTeamChannels maps each team to its channels, for the JS cascade
// (pick a team → narrow channels).
func inventoryTeamChannels(configs []alertConfig) map[string][]string {
	sets := make(map[string]map[string]bool)
	for _, c := range configs {
		if sets[c.Team] == nil {
			sets[c.Team] = make(map[string]bool)
		}
		sets[c.Team][c.Channel] = true
	}
	out := make(map[string][]string, len(sets))
	for t, set := range sets {
		out[t] = sortedKeys(set)
	}
	return out
}

// inventoryGroupOptions lists group names from scaffoldSets, excluding the
// "all" alias so it isn't confused with the matrix-mode target type.
func inventoryGroupOptions() []string {
	groups := make([]string, 0, len(scaffoldSets))
	for g := range scaffoldSets {
		if g == "all" {
			continue
		}
		groups = append(groups, g)
	}
	sort.Strings(groups)
	return groups
}

// inventoryScriptData is the JSON the page script consumes. G203 false
// positives: every value is json.Marshal output of server-controlled data
// (channel list, group options, embedded runbook slugs — never user input).
// json.Marshal escapes `<` by default, preventing `</script>` breakouts.
// template.JS here just tells html/template to treat the bytes as a JS literal
// (skip the wrap-in-quotes pass) — which is what we want because the literal
// IS the JSON.
type inventoryScriptData struct {
	targetChannels template.JS
	groups         template.JS
	slugs          template.JS
	teamChannels   template.JS
}

func newInventoryScriptData(configs []alertConfig, groupOptions []string, slugs []string) inventoryScriptData {
	// For each target value the channel dropdown can take, list channels that
	// actually have at least one matching receiver so the options don't include
	// channels where the chosen target has no presence. Computed server-side,
	// embedded as JSON, applied by a small JS handler at page load + on target
	// dropdown change.
	targetChannels, _ := json.Marshal(buildTargetChannelMap(configs, groupOptions, slugs))
	groups, _ := json.Marshal(groupOptions)
	slugsJSON, _ := json.Marshal(slugs)
	teamChannels, _ := json.Marshal(inventoryTeamChannels(configs))
	return inventoryScriptData{
		targetChannels: template.JS(targetChannels), //nolint:gosec // G203: pre-escaped by json.Marshal (HTML escape default)
		groups:         template.JS(groups),         //nolint:gosec // G203: pre-escaped by json.Marshal (HTML escape default)
		slugs:          template.JS(slugsJSON),      //nolint:gosec // G203: pre-escaped by json.Marshal (HTML escape default)
		teamChannels:   template.JS(teamChannels),   //nolint:gosec // G203: pre-escaped by json.Marshal (HTML escape default)
	}
}
