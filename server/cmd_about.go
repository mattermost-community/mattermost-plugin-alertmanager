package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/hako/durafmt"
	"github.com/mattermost/mattermost/server/public/model"
)

// handleAbout renders a single-screen "what is this plugin and what's
// it doing right now" summary. Bundles four signals an operator usually
// pieces together by clicking through System Console + multiple slash
// commands:
//
//   - Build identity (version, plugin ID)
//   - Configured settings (WebhookHost, YAML TTL, CA bundle present?,
//     metrics token present?) — values redacted; only presence shown
//   - Live state (receivers in this channel vs org-wide, distinct AM
//     backends, reconciler last-run timestamp + pruned count)
//   - Jump-off links (admin inventory page, embedded docs index)
//
// Open to all users in the channel. Doesn't expose secrets — token
// values are shown as "configured" / "not set", never the raw value.
func (p *Plugin) handleAbout(args *model.CommandArgs) string {
	cfg := p.getConfiguration()
	siteURL := p.publicSiteURL()
	channelScoped := p.configsForCurrentChannel(args)

	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		":satellite: **Mattermost Alertmanager plugin %s**\n\n",
		Manifest.Version,
	))
	b.WriteString(fmt.Sprintf("- **Plugin ID:** `%s`\n", Manifest.Id))
	b.WriteString(fmt.Sprintf("- **Mattermost site URL:** %s\n\n", orDash(siteURL)))

	// Live state — the operationally interesting block.
	b.WriteString("**Receivers**\n")
	b.WriteString(fmt.Sprintf("- In this channel: **%d**\n", len(channelScoped)))
	b.WriteString(fmt.Sprintf("- Org-wide (all channels): **%d**\n", len(cfg.AlertConfigs)))
	b.WriteString(fmt.Sprintf("- Distinct Alertmanager backends: **%d**\n\n", countDistinctAMs(cfg.AlertConfigs)))

	// Reconciler health — same data the inventory page banner shows.
	lastRun, pruned := p.reconcileStatus()
	b.WriteString("**Reconciler** (background orphan-prune janitor)\n")
	if lastRun.IsZero() {
		b.WriteString("- Last run: _never since plugin start — will fire within 5 minutes_\n\n")
	} else {
		b.WriteString(fmt.Sprintf(
			"- Last run: %s ago, pruned %d receiver(s)\n\n",
			durafmt.Parse(time.Since(lastRun)).LimitFirstN(2).String(), pruned,
		))
	}

	// Configured settings — presence-only for the secret-bearing ones.
	b.WriteString("**Configured settings** (System Console → Plugins → Alertmanager)\n")
	b.WriteString(fmt.Sprintf("- WebhookHost override: %s\n", orFallback(cfg.WebhookHost, "_unset (using Mattermost SiteURL)_")))
	b.WriteString(fmt.Sprintf("- Assembled YAML TTL: **%d hour(s)**\n", cfg.AssembledYAMLTTLHours))
	b.WriteString(fmt.Sprintf("- Alertmanager CA bundle: %s\n", presence(cfg.AlertManagerCABundle != "", "configured", "_not set — system trust store in use_")))
	b.WriteString(fmt.Sprintf("- Metrics endpoint token: %s\n\n", presence(cfg.MetricsToken != "", "configured (`/plugins/"+Manifest.Id+"/metrics` reachable)", "_not set — `/metrics` returns 404_")))

	// Jump-off links — built from SiteURL so they're clickable.
	b.WriteString("**Links**\n")
	if siteURL != "" {
		b.WriteString(fmt.Sprintf("- Admin inventory page: %s/plugins/%s/admin/inventory\n", siteURL, Manifest.Id))
	}
	b.WriteString("- Docs in chat: `/alertmanager docs` (tab through topics)\n")
	b.WriteString("- Runbooks in chat: `/alertmanager docs slash_commands` for the full reference\n")
	b.WriteString("- Validate a receiver: `/alertmanager validate all`\n")

	return b.String()
}

// publicSiteURL returns the Mattermost SiteURL with trailing slash
// stripped. Returns empty string if config or SiteURL is unset — the
// caller is responsible for handling the empty case gracefully (we hide
// the inventory-page link when SiteURL isn't known since the URL would
// be broken).
func (p *Plugin) publicSiteURL() string {
	cfg := p.API.GetConfig()
	if cfg == nil || cfg.ServiceSettings.SiteURL == nil {
		return ""
	}
	return strings.TrimRight(*cfg.ServiceSettings.SiteURL, "/")
}

// countDistinctAMs counts unique AlertManagerURL values across the
// registered configs. Empty URLs are ignored — they represent partially
// configured receivers.
func countDistinctAMs(configs []alertConfig) int {
	seen := make(map[string]bool)
	for _, c := range configs {
		if c.AlertManagerURL == "" {
			continue
		}
		seen[c.AlertManagerURL] = true
	}
	return len(seen)
}

// orDash returns s unchanged or "—" if empty. Used to render unset
// values legibly in markdown.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// orFallback returns s if non-empty, otherwise the fallback string.
// Used to surface a meaningful default explanation when a setting is
// unset (e.g. "using Mattermost SiteURL" when WebhookHost is blank).
func orFallback(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return "`" + s + "`"
}

// presence collapses a boolean into one of two display strings — used
// for settings where we want to confirm the value is configured
// without revealing the value itself (tokens, CA bundles).
func presence(have bool, yes, no string) string {
	if have {
		return ":white_check_mark: " + yes
	}
	return no
}
