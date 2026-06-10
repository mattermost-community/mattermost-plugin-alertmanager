package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// ServeHTTP backs the dynamic-list autocomplete endpoints used by the
// /alertmanager scaffold and /alertmanager add slash commands. Mattermost
// invokes these URLs as the user tabs through arguments; we return JSON
// lists of suggestions.
//
// Auth: every request must carry the Mattermost-User-Id header (Mattermost
// sets it for in-session calls). The endpoints are read-only and only
// return data the calling user already has access to via the plugin API,
// so we don't need a stronger guard than presence of the header.
func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	// /metrics uses bearer-token auth (set via MetricsToken setting),
	// not the Mattermost-User-Id header — Prometheus scrapes from
	// outside MM's auth context.
	if r.URL.Path == "/metrics" {
		p.handleMetrics(w, r)
		return
	}

	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		http.Error(w, "unauthorized: missing Mattermost-User-Id header", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/autocomplete/teams":
		p.handleAutocompleteTeams(w, r, userID)
	case "/autocomplete/channels":
		p.handleAutocompleteChannels(w, r, userID)
	case "/admin/inventory":
		p.handleAdminInventory(w, r, userID)
	default:
		http.NotFound(w, r)
	}
}

// handleMetrics emits Prometheus-format metrics about the plugin's
// state — receiver counts grouped by channel, last reconcile timestamp,
// auto-delete sweep counters.
//
// Auth: bearer token from MetricsToken setting. Endpoint is disabled
// (returns 404) when the token is empty. Prometheus configures the
// token in its scrape_configs `authorization` block.
//
// Why bearer token instead of Mattermost-User-Id: Prometheus scrapes
// from outside MM's auth context, so we need an auth scheme that
// doesn't require a logged-in user. Rotation = changing the setting.
func (p *Plugin) handleMetrics(w http.ResponseWriter, r *http.Request) {
	token := p.getConfiguration().MetricsToken
	if token == "" {
		http.NotFound(w, r)
		return
	}
	supplied := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if supplied != token {
		http.Error(w, "unauthorized: bad or missing bearer token", http.StatusUnauthorized)
		return
	}

	configs := p.getConfiguration().AlertConfigs
	byChannel := make(map[string]int)
	for _, c := range configs {
		byChannel[c.Channel]++
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// emit drops the (int, error) return that fmt.Fprintf produces — the
	// metrics endpoint has no recovery path for a write failure (the
	// scrape just retries next interval), and the writes are pure
	// fire-and-forget. Wrapping in this closure keeps each metric line
	// readable while satisfying errcheck.
	emit := func(format string, args ...any) {
		_, _ = fmt.Fprintf(w, format, args...)
	}

	emit("# HELP alertmanager_plugin_receivers_total Number of receivers registered in plugin config, grouped by destination channel.\n")
	emit("# TYPE alertmanager_plugin_receivers_total gauge\n")
	if len(byChannel) == 0 {
		emit("alertmanager_plugin_receivers_total 0\n")
	} else {
		for ch, n := range byChannel {
			emit("alertmanager_plugin_receivers_total{channel=%q} %d\n", ch, n)
		}
	}

	emit("# HELP alertmanager_plugin_receivers_grand_total Total receivers across all channels.\n")
	emit("# TYPE alertmanager_plugin_receivers_grand_total gauge\n")
	emit("alertmanager_plugin_receivers_grand_total %d\n", len(configs))

	emit("# HELP alertmanager_plugin_channels_total Number of distinct channels with at least one registered receiver.\n")
	emit("# TYPE alertmanager_plugin_channels_total gauge\n")
	emit("alertmanager_plugin_channels_total %d\n", len(byChannel))

	// YAML janitor TTL setting — useful for monitoring that the
	// setting hasn't drifted from the security policy expectation.
	emit("# HELP alertmanager_plugin_yaml_ttl_hours Configured TTL (hours) for DM'd YAML files. 0 = auto-delete disabled.\n")
	emit("# TYPE alertmanager_plugin_yaml_ttl_hours gauge\n")
	emit("alertmanager_plugin_yaml_ttl_hours %d\n", p.getConfiguration().AssembledYAMLTTLHours)

	// Build timestamp signal — the metric is constant, but its
	// presence in the scrape lets you detect plugin uptime/restarts.
	emit("# HELP alertmanager_plugin_info Plugin info — version label, value always 1.\n")
	emit("# TYPE alertmanager_plugin_info gauge\n")
	emit("alertmanager_plugin_info{version=%q,plugin_id=%q} 1\n", Manifest.Version, Manifest.Id)
}

// handleAdminInventory is now implemented in admin_inventory.go.

// handleAutocompleteTeams returns the teams the calling user is a member
// of as AutocompleteListItem JSON. Item is the team URL slug (the value
// Mattermost inserts into the command line); HelpText is the display name
// shown next to it in the typeahead.
//
// Limiting to user-member teams is deliberate: showing the user teams
// they can't join would be misleading for an autocomplete that's about to
// be used to bind a receiver to a channel in that team.
func (p *Plugin) handleAutocompleteTeams(w http.ResponseWriter, _ *http.Request, userID string) {
	teams, appErr := p.API.GetTeamsForUser(userID)
	if appErr != nil {
		respondAutocompleteError(w, fmt.Sprintf("failed to list teams: %v", appErr))
		return
	}
	items := make([]model.AutocompleteListItem, 0, len(teams))
	for _, t := range teams {
		items = append(items, model.AutocompleteListItem{
			Item:     t.Name,
			HelpText: t.DisplayName,
		})
	}
	respondAutocompleteItems(w, items)
}

// handleAutocompleteChannels returns public channels for the team the
// user has already typed in an earlier argument. Mattermost includes the
// partial command line in the `parsed` query param; we extract the team
// slug from it and call GetPublicChannelsForTeam.
//
// Why public channels rather than channels-the-user-is-in:
//   - Sysadmins scaffolding receivers may bind webhooks to channels they
//     aren't currently members of.
//   - Auto-creation of the destination channel happens in /alertmanager
//     add and scaffold, so the user can still type a channel name that
//     doesn't exist yet — autocomplete is a suggestion, not a gate.
//
// Private channels are intentionally not in the list: surfacing them via
// an open autocomplete would leak channel names to users who can't
// otherwise see them.
func (p *Plugin) handleAutocompleteChannels(w http.ResponseWriter, r *http.Request, userID string) {
	parsed := r.URL.Query().Get("parsed")
	teamSlug := extractTeamSlugFromParsed(parsed)

	// Empty team slug = user hasn't filled the team arg yet. Return a
	// single placeholder item so the typeahead has something to render and
	// the user gets a hint about ordering.
	if teamSlug == "" {
		respondAutocompleteItems(w, []model.AutocompleteListItem{
			{
				Item:     "_fill-team-first_",
				HelpText: "Fill in the team argument first — channels are listed per team",
			},
		})
		return
	}

	team, appErr := p.API.GetTeamByName(teamSlug)
	if appErr != nil {
		respondAutocompleteItems(w, []model.AutocompleteListItem{
			{
				Item:     "_team-not-found_",
				HelpText: fmt.Sprintf("Team `%s` doesn't exist — fix the team arg and re-tab", teamSlug),
			},
		})
		return
	}

	// Page 0 / 200 covers nearly every team. Teams with more than 200
	// public channels can't reasonably use typeahead anyway, and the
	// command still accepts free-text channel names.
	channels, appErr := p.API.GetPublicChannelsForTeam(team.Id, 0, 200)
	if appErr != nil {
		respondAutocompleteError(w, fmt.Sprintf("failed to list channels for team %s: %v", teamSlug, appErr))
		return
	}

	items := make([]model.AutocompleteListItem, 0, len(channels))
	for _, c := range channels {
		items = append(items, model.AutocompleteListItem{
			Item:     c.Name,
			HelpText: c.DisplayName,
		})
	}
	respondAutocompleteItems(w, items)

	// Suppress unused-import linter complaint about userID; it's there for
	// future per-user filtering and to match the dispatcher signature.
	_ = userID
}

// extractTeamSlugFromParsed reads the team slug out of the partial
// command line Mattermost passes in the autocomplete callback.
//
//	/alertmanager add <team> <channel> <am-url> [set]
//
// Returns "" if no usable team slug is present yet.
func extractTeamSlugFromParsed(parsed string) string {
	fields := strings.Fields(parsed)
	if len(fields) < 3 {
		return ""
	}
	// fields[0] is the trigger (e.g. "/alertmanager"); fields[1] is the
	// subcommand. Only `add` takes team+channel args.
	if fields[1] != "add" {
		return ""
	}
	return fields[2]
}

func respondAutocompleteItems(w http.ResponseWriter, items []model.AutocompleteListItem) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

func respondAutocompleteError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode([]model.AutocompleteListItem{
		{Item: "_error_", HelpText: msg},
	})
}
