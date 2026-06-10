package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// This file owns the "plugin programmatically creates Mattermost incoming
// webhooks" subsystem. The plugin RPC API doesn't expose IncomingWebhook
// CRUD; we route through Client4 (the admin HTTP API).
//
// Authentication strategy: at slash-command time, the plugin mints an
// *ephemeral* personal access token for the calling sysadmin (who already
// has all the permissions we need), makes the Client4 calls, then revokes
// the PAT. The webhook ends up with user_id = calling admin, but its
// posts are rendered as @alertmanagerbot via the Username/IconURL
// override fields. So:
//
//   - The webhook keeps working after the admin is deactivated.
//     Mattermost's incoming webhook handler authenticates by hook-id,
//     not by owner-active-status.
//   - Posts always look like the bot regardless of who created the
//     webhook. (Requires System Console → Integrations → Integration
//     Management → "Enable Integrations to override usernames" and
//     "...icons" → true.)
//   - The PAT is short-lived (typically <100ms), revoked before the
//     slash command returns.
//
// Why not mint a PAT for the bot user: bots default to roles without
// manage_incoming_webhooks permission, and the plugin API doesn't expose
// UpdateRole / GetRoleByName to fix that. The calling sysadmin's PAT
// inherits their permissions, so it just works.

const (
	ephemeralTokenDescription = "Ephemeral token for the Alertmanager plugin's webhook management. Revoked immediately after use; safe to ignore if seen in audit logs."

	// webhookUsername and webhookIconURL are baked into every webhook
	// the plugin creates. Mattermost's webhook handler honors these as
	// overrides on each post, so output looks like @alertmanagerbot
	// even though the webhook's user_id is the calling admin.
	webhookUsername = "alertmanagerbot"
	webhookIconURL  = "/plugins/com.mattermost.alertmanager/public/alertmanager-logo.png"
)

// ephemeralClient4 mints a short-lived PAT for the given user, returns
// an authenticated Client4 plus a cleanup func that revokes the token.
// Callers must always defer cleanup() right after a successful call.
//
// The Client4 points at the local Mattermost process (localhost +
// ListenAddress) rather than SiteURL. The plugin runs inside the MM
// server process; routing its own API calls back through the public
// Ingress (which SiteURL points at in K8s) would be a network u-turn
// out through the cluster egress and back through the LB. Localhost
// is the same MM pod, no detour.
func (p *Plugin) ephemeralClient4(userID string) (*model.Client4, func(), error) {
	tok, appErr := p.API.CreateUserAccessToken(&model.UserAccessToken{
		UserId:      userID,
		Description: ephemeralTokenDescription,
	})
	if appErr != nil {
		return nil, nil, fmt.Errorf("mint ephemeral PAT for user %s: %w", userID, appErr)
	}

	cleanup := func() {
		if appErr := p.API.RevokeUserAccessToken(tok.Id); appErr != nil {
			p.API.LogWarn("failed to revoke ephemeral PAT (will linger until manually cleaned)",
				"tokenID", tok.Id, "err", appErr.Error())
		}
	}

	c := model.NewAPIv4Client(p.localBaseURL())
	c.SetToken(tok.Token)
	return c, cleanup, nil
}

// localBaseURL returns the URL the plugin should use to reach its own
// Mattermost process via Client4. Reads ServiceSettings.ListenAddress
// to compose http://localhost<addr>. Falls back to :8065 if not set
// (Mattermost's documented default).
//
// Why not SiteURL: in K8s deployments SiteURL is the public Ingress
// URL — routing plugin → MM-self calls through Ingress means
// pod → egress → LB → Ingress → pod, which is wasteful and breaks
// under restrictive NetworkPolicy. Localhost is always reachable
// because the plugin is in-process with MM.
func (p *Plugin) localBaseURL() string {
	listenAddr := ":8065"
	if cfg := p.API.GetConfig(); cfg != nil && cfg.ServiceSettings.ListenAddress != nil {
		if la := strings.TrimSpace(*cfg.ServiceSettings.ListenAddress); la != "" {
			listenAddr = la
		}
	}
	// ListenAddress is typically ":8065" or "0.0.0.0:8065" — strip a
	// leading wildcard host so we hit localhost specifically.
	if idx := strings.Index(listenAddr, ":"); idx > 0 {
		listenAddr = listenAddr[idx:]
	}
	return "http://localhost" + listenAddr
}

// createIncomingWebhook registers a Mattermost incoming webhook in the
// destination channel. The hook is technically owned by the calling
// admin (since the PAT we minted is theirs), but Username/IconURL
// override fields ensure posts render as @alertmanagerbot.
//
// Returns the hook ID — that's the public identifier going into the URL
// path /hooks/<id>.
func (p *Plugin) createIncomingWebhook(callerUserID, channelID, displayName string) (string, error) {
	c, cleanup, err := p.ephemeralClient4(callerUserID)
	if err != nil {
		return "", err
	}
	defer cleanup()

	hook := &model.IncomingWebhook{
		ChannelId:   channelID,
		UserId:      callerUserID,
		DisplayName: displayName,
		Description: "Managed by the Mattermost Alertmanager plugin. Do not edit manually.",
		Username:    webhookUsername,
		IconURL:     webhookIconURL,
	}
	created, _, err := c.CreateIncomingWebhook(context.Background(), hook)
	if err != nil {
		return "", fmt.Errorf("create incoming webhook: %w", err)
	}
	return created.Id, nil
}

// deleteIncomingWebhook revokes a webhook by ID. Sysadmin role grants
// manage_others_incoming_webhooks so we can delete webhooks regardless
// of original owner. Tolerant of "already gone."
func (p *Plugin) deleteIncomingWebhook(callerUserID, hookID string) error {
	c, cleanup, err := p.ephemeralClient4(callerUserID)
	if err != nil {
		return err
	}
	defer cleanup()

	resp, err := c.DeleteIncomingWebhook(context.Background(), hookID)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("delete incoming webhook %q: %w", hookID, err)
	}
	return nil
}

// webhookURL returns the api_url for a hook ID using the global
// WebhookHost setting (or SiteURL fallback). Kept for the few sites
// that don't have an alertConfig to consult (e.g., legacy callers
// before the per-receiver override existed).
//
// Most callers should use webhookURLForReceiver, which honors the
// per-receiver WebhookHostOverride.
func (p *Plugin) webhookURL(hookID string) string {
	host := p.getConfiguration().WebhookHost
	if host == "" {
		host = p.siteURL()
	}
	return fmt.Sprintf("%s/hooks/%s", host, hookID)
}

// webhookURLForReceiver resolves the api_url with per-receiver
// precedence. Resolution order:
//
//  1. ac.WebhookHostOverride — set via /alertmanager add --webhook-host=...
//     for multi-cluster pattern C (each cluster has a different
//     network path to MM)
//  2. WebhookHost plugin setting — global default for the deployment
//  3. SiteURL — fallback for bare-metal where AM and MM share a host
//
// The resulting URL is what AM uses to POST alert notifications,
// NOT the URL the plugin uses for its own self-API calls (that's
// localBaseURL — always localhost) NOR the URL embedded in runbook
// links (that's runbookDefaultURL — always SiteURL).
func (p *Plugin) webhookURLForReceiver(ac alertConfig) string {
	host := ac.WebhookHostOverride
	if host == "" {
		host = p.getConfiguration().WebhookHost
	}
	if host == "" {
		host = p.siteURL()
	}
	return fmt.Sprintf("%s/hooks/%s", host, ac.WebhookID)
}

// runbookDefaultURL returns the plugin-hosted runbook page URL for a
// receiver slug — the URL the canonical template falls back to when
// the alert's `runbook_url` annotation isn't set.
//
// Points at the rendered .html file directly because Mattermost's
// plugin static file handler doesn't auto-resolve trailing slashes to
// index.html / home.html the way a normal web server does. Earlier
// "/<slug>/" pattern 404'd; "/<slug>.html" hits the real file.
//
// Always uses SiteURL, never WebhookHost. The runbook URL ends up in
// the chat post text and is clicked by users in their browsers — they
// reach MM through Ingress / public hostname, not the cluster-internal
// service.
func (p *Plugin) runbookDefaultURL(slug string) string {
	return fmt.Sprintf("%s/plugins/%s/public/runbooks/%s.html", p.siteURL(), Manifest.Id, slug)
}

// siteURL returns the configured Mattermost SiteURL with trailing
// slash trimmed, or a clearly-bogus placeholder if SiteURL isn't set.
// The placeholder shows up in rendered YAML so an admin pasting an
// unconfigured system's output sees what's missing.
func (p *Plugin) siteURL() string {
	siteURL := "https://CHANGE-ME-mattermost-site-url"
	if cfg := p.API.GetConfig(); cfg != nil && cfg.ServiceSettings.SiteURL != nil {
		if trimmed := strings.TrimRight(*cfg.ServiceSettings.SiteURL, "/"); trimmed != "" {
			siteURL = trimmed
		}
	}
	return siteURL
}
