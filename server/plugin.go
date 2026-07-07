package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

var Manifest = root.Manifest

// Plugin is the runtime state for the Mattermost Alertmanager bridge.
//
// configuration is held by pointer and atomically swapped — never mutated
// in place. The plugin does NOT run an inbound webhook receiver — alert
// delivery happens directly via Mattermost's native incoming webhooks
// (created by this plugin via the IncomingWebhook API, owned by the bot).
type Plugin struct {
	plugin.MattermostPlugin

	client *pluginapi.Client

	configuration     *configuration
	configurationLock sync.RWMutex

	// BotUserID is the bot account that owns the channels we auto-create
	// and is the Username/IconURL override on the webhooks we register.
	// The webhook's user_id field is actually set to the calling sysadmin
	// (see incoming_webhook.go for why), but display overrides make
	// posts appear as this bot regardless.
	BotUserID string

	// stopReconciler halts the periodic webhook-orphan reconciler
	// goroutine started in OnActivate. nil if not started or already
	// stopped. Written in OnActivate, read+nil'd in OnDeactivate —
	// Mattermost guarantees these calls are serialized per plugin
	// instance, so no additional synchronization is needed.
	stopReconciler func()

	// configWriteMu serializes config read-modify-write cycles across
	// concurrent callers (slash commands + background reconciler). The
	// lock must be held from the initial getConfiguration read through
	// the saveConfigs call to prevent lost updates. See saveConfigs.
	configWriteMu sync.Mutex

	// reconcilerStatusLock guards reconcilerLastRun and
	// reconcilerLastPruned. These are read by the admin inventory
	// page and written by each reconcileOrphans cycle.
	reconcilerStatusLock sync.Mutex
	reconcilerLastRun    time.Time
	reconcilerLastPruned int

	// amReachabilityCache is a sync.Map keyed by Alertmanager URL.
	// Values are *amReachabilityEntry. Probes are deduped per URL
	// and results cached for amReachabilityTTL — keeps the admin
	// inventory page from N-pinging on every render.
	amReachabilityCache sync.Map
}

// OnActivate is called once at plugin start. Ensures the bot exists, mints
// a PAT for the bot so we can make Client4 calls against ourselves for
// IncomingWebhook CRUD, and registers slash commands.
func (p *Plugin) OnActivate() error {
	p.client = pluginapi.NewClient(p.API, p.Driver)

	botID, err := p.client.Bot.EnsureBot(&model.Bot{
		Username:    "alertmanagerbot",
		DisplayName: "Alertmanager Bot",
		Description: "Posts alerts from Prometheus Alertmanager. Created by the Alertmanager plugin.",
	}, pluginapi.ProfileImagePath(filepath.Join("assets", "alertmanager-logo.png")))
	if err != nil {
		return fmt.Errorf("ensure bot: %w", err)
	}
	p.BotUserID = botID

	command, err := p.getCommand()
	if err != nil {
		return fmt.Errorf("get command: %w", err)
	}
	if err := p.API.RegisterCommand(command); err != nil {
		return fmt.Errorf("register command: %w", err)
	}

	// Force-enable the integration override settings. Webhook posts
	// embed Username/IconURL override fields (see incoming_webhook.go),
	// but Mattermost only honors them when these server-level toggles
	// are on. Off by default in some installs, which makes posts
	// render as the calling sysadmin instead of @alertmanagerbot.
	// We flip them on at activation so the plugin's documented behavior
	// is the actual behavior.
	if err := p.ensureIntegrationOverrides(); err != nil {
		p.API.LogWarn("could not enable integration override settings; webhook posts may display as the creating sysadmin instead of @alertmanagerbot. Enable manually in System Console → Integrations → Integration Management.",
			"err", err.Error())
	}

	// Start the orphan-webhook reconciler. Prunes plugin config entries
	// whose underlying Mattermost incoming webhook has been deleted
	// out-of-band (System Console). See reconciler.go for details.
	p.stopReconciler = p.startReconciler()

	return nil
}

// ensureIntegrationOverrides checks Mattermost's server-level toggles
// that govern whether webhooks can override the post's displayed
// username + icon. If either is off, flips it on and logs.
//
// Implications of doing this from a plugin:
//   - It's a server-wide setting change, affecting every webhook in
//     this Mattermost install, not just the plugin's webhooks. That
//     is acceptable because the toggles exist specifically for this
//     pattern — no integration that wires up Username/IconURL would
//     want them off.
//   - Audit log records the change as the plugin acting on behalf of
//     the system. Visible in logs as a config update event.
//   - Idempotent: when both are already on, this is a single GetConfig
//     call with no SaveConfig.
func (p *Plugin) ensureIntegrationOverrides() error {
	cfg := p.API.GetConfig()
	if cfg == nil {
		return fmt.Errorf("plugin API returned no server config")
	}

	enabled := func(b *bool) bool { return b != nil && *b }
	usernameOn := enabled(cfg.ServiceSettings.EnablePostUsernameOverride)
	iconOn := enabled(cfg.ServiceSettings.EnablePostIconOverride)
	if usernameOn && iconOn {
		return nil
	}

	t := true
	cfg.ServiceSettings.EnablePostUsernameOverride = &t
	cfg.ServiceSettings.EnablePostIconOverride = &t
	if appErr := p.API.SaveConfig(cfg); appErr != nil {
		return appErr
	}
	p.API.LogInfo("Enabled integration override toggles (EnablePostUsernameOverride, EnablePostIconOverride). Webhook posts will now render with the @alertmanagerbot identity instead of the creating sysadmin.",
		"was_username_on", usernameOn, "was_icon_on", iconOn)
	return nil
}

func (p *Plugin) OnDeactivate() error {
	if p.stopReconciler != nil {
		p.stopReconciler()
		p.stopReconciler = nil
	}
	return nil
}

// getConfiguration returns the current snapshot. Treat as immutable;
// mutation requires Clone + setConfiguration.
func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &configuration{}
	}
	return p.configuration
}

// setConfiguration atomically swaps the active configuration.
func (p *Plugin) setConfiguration(c *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()
	p.configuration = c
}

// requireSystemAdmin returns nil if the user has system_admin privilege,
// otherwise an error for echoing to the user. Used by commands that
// affect org-wide state (e.g., reconcile).
func (p *Plugin) requireSystemAdmin(userID string) error {
	if p.client == nil {
		return fmt.Errorf("plugin not fully initialized")
	}
	if !p.client.User.HasPermissionTo(userID, model.PermissionManageSystem) {
		return fmt.Errorf("this command requires system_admin privilege")
	}
	return nil
}

// requireChannelTeamAdmin returns nil if the user is either a
// system_admin OR a team_admin of the channel's team. Used by mutating
// commands that operate on a specific channel — team admins can manage
// their own team's receivers without bottlenecking on the sysadmin.
//
// Access is team-scoped, NOT channel-scoped: a team_admin can manage
// receivers in any channel of their team, including channels they are
// not a member of (even private ones). This is intentional — team admins
// are trusted to administer their team's infrastructure configuration.
// A team_admin in team A cannot reach team B's channels. Sysadmins
// bypass the team check entirely.
func (p *Plugin) requireChannelTeamAdmin(userID, channelID string) error {
	if p.client == nil {
		return fmt.Errorf("plugin not fully initialized")
	}
	if p.client.User.HasPermissionTo(userID, model.PermissionManageSystem) {
		return nil
	}
	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil {
		return fmt.Errorf("could not resolve channel: %w", appErr)
	}
	member, appErr := p.API.GetTeamMember(channel.TeamId, userID)
	if appErr != nil {
		return fmt.Errorf("you must be a member of this channel's team")
	}
	// team_admin role is space-separated in the Roles string.
	if slices.Contains(strings.Fields(member.Roles), "team_admin") {
		return nil
	}
	return fmt.Errorf("this command requires team_admin (in this channel's team) or system_admin privilege")
}

// auditLog emits a structured plugin audit record for a mutating
// action via Mattermost's LogAuditRec API. These records land in the
// MM audit log (separate from regular plugin logs) so security teams
// can filter "what did the alertmanager plugin do" without grepping
// through MM's noisy PAT-create/revoke entries.
//
// Also emits a LogInfo line so the action shows up in regular plugin
// logs alongside the audit log entry.
func (p *Plugin) auditLog(action, userID, receiverName, channelID, result string) {
	p.API.LogInfo("alertmanager-plugin-audit",
		"action", action, "user_id", userID,
		"receiver", receiverName, "channel_id", channelID,
		"result", result,
	)
	rec := &model.AuditRecord{
		EventName: "alertmanager." + action,
		Status:    result,
		Actor: model.AuditEventActor{
			UserId: userID,
		},
		EventData: model.AuditEventData{
			ObjectType: "alertmanager-receiver",
			Parameters: map[string]any{
				"receiver":   receiverName,
				"channel_id": channelID,
			},
		},
		Meta: map[string]any{
			"plugin_id": Manifest.Id,
		},
	}
	p.API.LogAuditRec(rec)
}
