package main

import (
	"encoding/csv"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
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

	data := struct {
		Total             int
		Channels          int
		Groups            []inventoryGroup
		TeamRollup        []teamRow
		AMDrift           []amDriftGroup
		ReconcilerStatus  string
		ReconcilerHealthy bool
		GroupMode         string
		Version           string
		CSVHref           string
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
    </script>
</body>
</html>
`))
