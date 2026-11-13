package main

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/plugin"
)

// ServeHTTP serves the Prometheus /metrics endpoint, the admin inventory page,
// and the validate typeahead list.
//
// Auth: /metrics uses a bearer token. Everything else must carry the
// Mattermost-User-Id header (Mattermost sets it for in-session calls).
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
	case "/admin/inventory":
		p.handleAdminInventory(w, r, userID)
	case validateTargetsPath:
		p.handleAutocompleteValidateTargets(w, r, userID)
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
	if subtle.ConstantTimeCompare([]byte(supplied), []byte(token)) != 1 {
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

	// Emit the receiver count WITHOUT a per-channel label. The /metrics endpoint
	// is bearer-protected but typically reachable by observability users who are
	// NOT members of these Mattermost channels, so raw channel names as labels
	// leak topology past MM's ACLs (finding: metrics expose raw channel names).
	// Aggregate counts below carry the operational signal without the names.
	emit("# HELP alertmanager_plugin_receivers_total Total receivers registered in plugin config.\n")
	emit("# TYPE alertmanager_plugin_receivers_total gauge\n")
	emit("alertmanager_plugin_receivers_total %d\n", len(configs))

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
