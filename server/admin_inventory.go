package main

import (
	"encoding/csv"
	"net/http"
	"sort"

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

// renderInventoryHTML is the main page renderer.
func (p *Plugin) renderInventoryHTML(w http.ResponseWriter, r *http.Request, configs []alertConfig) {
	groupMode := inventoryGroupMode(r)
	amStatus := p.probeInventoryAMs(configs)
	groups := groupInventory(p.buildInventoryRows(configs, amStatus), groupMode)

	// Per-team rollup (always shown at top, independent of group mode).
	teamRows := inventoryTeamRollup(configs)
	teamOptions := make([]string, len(teamRows))
	for i, t := range teamRows {
		teamOptions[i] = t.Team
	}

	reconcilerStatus, reconcilerHealthy := p.reconcilerBanner()
	amDrift := p.amOnlyReceivers(configs, amStatus)

	form := parseInventoryForm(r)
	simResult, simMatrix, simActionResult := p.runInventoryForm(r, form, configs, amStatus)

	groupOptions := inventoryGroupOptions()
	slugs := runbookSlugs()
	script := newInventoryScriptData(configs, groupOptions, slugs)

	data := inventoryPageData{
		Total:              len(configs),
		Channels:           countDistinctChannels(configs),
		Groups:             groups,
		TeamRollup:         teamRows,
		AMDrift:            amDrift,
		ReconcilerStatus:   reconcilerStatus,
		ReconcilerHealthy:  reconcilerHealthy,
		GroupMode:          groupMode,
		Version:            Manifest.Version,
		SimulateMode:       form.mode,
		SimulateType:       form.typ,
		SimulateValue:      form.value,
		SimulateChannel:    form.channel,
		SimulateTeam:       form.team,
		SimulateSeverity:   form.severity,
		SimulateExtra:      form.extra,
		SimulateResult:     simResult,
		SimulateMatrix:     simMatrix,
		SimulateAction:     simActionResult,
		RunbookSlugs:       slugs,
		ChannelOptions:     inventoryChannelOptions(configs),
		GroupOptions:       groupOptions,
		TeamOptions:        teamOptions,
		TargetChannelsJSON: script.targetChannels,
		GroupOptionsJSON:   script.groups,
		RunbookSlugsJSON:   script.slugs,
		TeamChannelsJSON:   script.teamChannels,
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
