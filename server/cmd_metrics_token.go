package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// The Prometheus /metrics endpoint is gated by the MetricsToken plugin setting.
// That setting is `secret: true`, so once set it can't be read back from System
// Console — and there's no "generate" button (this is a server-only plugin with
// no webapp UI). handleMetricsToken fills that gap: it mints a strong token,
// stores it, and reveals it ONCE to the invoking sysadmin along with a ready-to-
// paste Prometheus scrape_config. Regenerating rotates the token (any existing
// scraper must be updated), so a bare invocation only reports status — the
// destructive rotate requires the explicit `generate` argument.

// generateMetricsTokenValue returns a cryptographically-random 64-char hex token
// (32 bytes), matching the "random 64-character hex string" the setting's
// placeholder documents.
func generateMetricsTokenValue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// saveMetricsToken persists a new MetricsToken value WITHOUT disturbing the other
// System Console settings. SavePluginConfig replaces the whole plugin-config map,
// so we read the current settings first and write them all back with just the
// token changed. (The receiver list lives in the KV store since CL-19 and is not
// part of this map, so it's unaffected.)
func (p *Plugin) saveMetricsToken(token string) error {
	var raw rawConfiguration
	if err := p.API.LoadPluginConfiguration(&raw); err != nil {
		return fmt.Errorf("load current plugin configuration: %w", err)
	}
	// Preserve every other setting — SavePluginConfig replaces the whole map.
	// Build from rawConfiguration.toConfigMap so a future setting can't be
	// silently dropped by a stale key list here, then set just the token.
	cfg := raw.toConfigMap()
	cfg["metricstoken"] = token
	return p.client.Configuration.SavePluginConfig(cfg)
}

// metricsEndpointURL returns the full URL Prometheus scrapes.
func (p *Plugin) metricsEndpointURL() string {
	return fmt.Sprintf("%s/plugins/%s/metrics", p.siteURL(), Manifest.Id)
}

// handleMetricsToken generates/rotates the Prometheus metrics bearer token.
// Sysadmin-only (the metrics endpoint is a System-Console-level concern). A bare
// invocation reports status without rotating; `generate` mints a new token and
// reveals it once.
func (p *Plugin) handleMetricsToken(args *model.CommandArgs) (string, error) {
	if err := p.requireSystemAdmin(args.UserId); err != nil {
		return err.Error(), nil
	}

	fields := strings.Fields(args.Command)
	action := ""
	if len(fields) >= 3 {
		action = strings.ToLower(fields[2])
	}

	url := p.metricsEndpointURL()

	if action != "generate" {
		// Status only — never reveals or rotates. The token is secret:true, so we
		// can only report whether one is set, not its value.
		set := p.getConfiguration().MetricsToken != ""
		state := "_not set — the `/metrics` endpoint returns 404_"
		if set {
			state = "configured (endpoint enabled)"
		}
		return fmt.Sprintf(
			"**Metrics endpoint token:** %s\n\n**Endpoint:** `%s`\n\n"+
				"To mint a new token (or rotate the current one) and get a ready-to-paste "+
				"Prometheus config, run:\n\n```\n/alertmanager metrics-token generate\n```\n"+
				":warning: Generating **rotates** the token — any Prometheus already scraping this endpoint must be updated with the new value.",
			state, url,
		), nil
	}

	token, err := generateMetricsTokenValue()
	if err != nil {
		return fmt.Sprintf("Failed to generate token: %v", err), nil
	}
	if err := p.saveMetricsToken(token); err != nil {
		return fmt.Sprintf("Failed to save token: %v", err), nil
	}
	// Audit the rotation; never log the token value itself.
	p.auditLog("metrics.token.generated", args.UserId, "", args.ChannelId, "success")

	// The command response is ephemeral (visible only to you); the token is shown
	// here once — copy it now, it can't be read back from System Console.
	return fmt.Sprintf(
		":key: **Generated a new metrics bearer token.**\n\n"+
			":warning: This **rotated** the token — any existing Prometheus scrape config must be updated with the value below or it will start getting 401s.\n\n"+
			"**Token (shown once — copy it now):**\n```\n%s\n```\n\n"+
			"**Endpoint:** `%s`\n\n"+
			"**Prometheus `scrape_config`:**\n```yaml\n"+
			"scrape_configs:\n"+
			"  - job_name: mattermost-alertmanager-plugin\n"+
			"    metrics_path: /plugins/%s/metrics\n"+
			"    scheme: https   # http if your Mattermost isn't behind TLS\n"+
			"    static_configs:\n"+
			"      - targets: ['<your-mattermost-host>']\n"+
			"    authorization:\n"+
			"      type: Bearer\n"+
			"      credentials: %s\n"+
			"```",
		token, url, Manifest.Id, token,
	), nil
}
