package main

import (
	"encoding/json"
	"errors"
	"fmt"
	neturl "net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

// rawConfiguration is what Mattermost's settings framework fills in. The
// AlertConfigsJSON field is the JSON-serialized array of alertConfig entries.
// Slash commands are the primary write path; the System Console field is the
// bulk-edit / GitOps fallback.
//
// WebhookHost is the optional override for the host:port portion of the
// Mattermost webhook URL when rendered into alertmanager.yml. See
// plugin.json settings_schema for the full rationale. Empty = fall back
// to SiteURL.
type rawConfiguration struct {
	AlertConfigsJSON      string
	WebhookHost           string
	AssembledYAMLTTLHours int
	AlertManagerCABundle  string
	MetricsToken          string
	WebhookRotationDays   int
}

// configuration is the parsed, validated, ready-to-serve plugin state.
// AlertConfigs is the active list; nameIndex provides O(1) lookup for
// slash commands that need to resolve an entry by name.
type configuration struct {
	AlertConfigs          []alertConfig
	WebhookHost           string
	AssembledYAMLTTLHours int
	AlertManagerCABundle  string
	MetricsToken          string
	WebhookRotationDays   int
	nameIndex             map[string]int
}

// alertConfig describes one Alertmanager backend bound to a Mattermost channel
// via a native incoming webhook.
//
// The plugin's job is to create the Mattermost incoming webhook (owned by the
// bot user, not the calling admin) and store its ID here. Alertmanager posts
// directly to that webhook via `slack_configs`; the plugin never sees the
// alert payload at runtime. The only runtime usage of these fields is for
// slash commands like /alertmanager render and /alertmanager alerts.
//
// Notably absent vs. the cpanato plugin: no Token field. Authentication of
// inbound alerts is whatever Mattermost's native incoming webhook system
// uses (the random hook-id in the URL). The plugin owns no shared secrets.
//
// Webhook sharing (v1.0.3+): multiple alertConfig entries may share a single
// WebhookID when they were created in the same /alertmanager add invocation.
// Group adds (e.g., `add ... compute`) produce N receivers all pointing at
// one Mattermost webhook. Individual slug adds (e.g., `add ... high-cpu-usage`)
// produce a single receiver with its own webhook. The GroupName field
// disambiguates and enables refcount-based cleanup on remove. Pre-v1.0.3
// receivers have empty GroupName and are treated as individual (each owns
// its webhook) for backwards compatibility.
type alertConfig struct {
	Name            string `json:"name"`
	Team            string `json:"team"`
	Channel         string `json:"channel"`
	AlertManagerURL string `json:"alertManagerURL,omitempty"`
	WebhookID       string `json:"webhookID"`

	// GroupName identifies the unit this receiver was created under.
	// Receivers sharing GroupName + Team + Channel also share WebhookID.
	// Values:
	//   - Category set keyword: "all", "compute", "application",
	//     "database", "networking", "observability", "storage"
	//   - Runbook slug: "high-cpu-usage", etc. — set when the receiver
	//     was created via an individual /alertmanager add <slug> call
	//   - Empty: legacy receiver from v1.0.0-v1.0.2 before group webhooks
	//     existed. Treated as individual for backwards compatibility.
	GroupName string `json:"groupName,omitempty"`

	// WebhookHostOverride lets a sysadmin pin a per-receiver host that
	// takes precedence over the global WebhookHost setting at YAML
	// render time. Set via /alertmanager add --webhook-host=<url>.
	// Use case: one Mattermost serving multiple K8s clusters where
	// each cluster's Alertmanager reaches MM via a different network
	// path. Empty = inherit global WebhookHost or fall back to SiteURL.
	WebhookHostOverride string `json:"webhookHostOverride,omitempty"`

	// Optional basic-auth credentials for outbound calls to Alertmanager's
	// REST API (used by /alertmanager alerts, silences, status). Not a
	// Mattermost user — these are service-account credentials for the
	// Alertmanager side. Leave empty unless your Alertmanager is behind an
	// auth proxy. NOT exposed via the /alertmanager add slash command;
	// set via System Console JSON edit if needed.
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`

	// LastRotatedAt is when the WebhookID was last (re)created via
	// /alertmanager add or /alertmanager rotate. Used by the rotation
	// reminder scheduler — when the configured WebhookRotationDays
	// elapses since this timestamp, the plugin DMs sysadmins to
	// suggest rotation. Zero value (== time.Time{}) is treated as
	// "rotated at plugin upgrade time" by the reconciler, so
	// existing receivers don't immediately fire reminders after the
	// feature is enabled.
	LastRotatedAt time.Time `json:"lastRotatedAt,omitzero"`

	// LastReminderAt is when the most-recent rotation-due reminder
	// was sent for this receiver. Used to throttle repeats — the
	// reconciler skips re-reminding for the same receiver until
	// reminderRepeatInterval has elapsed since this timestamp.
	// Reset on rotation along with LastRotatedAt.
	LastReminderAt time.Time `json:"lastReminderAt,omitzero"`

	// RotationRemindersEnabled is the per-receiver opt-in for the
	// rotation reminder system. Set true at creation time via the
	// optional `on` arg to /alertmanager add. When false (default),
	// the reconciler skips this receiver in its reminder check even
	// if the global WebhookRotationDays threshold passes. Two-tier
	// design: sysadmin sets the threshold globally; channel-team-admin
	// opts INTO rotation per channel at add time.
	RotationRemindersEnabled bool `json:"rotationRemindersEnabled,omitempty"`
}

// Names are user-facing identifiers — URL-safe so they can appear in slash
// command args and YAML output without escaping concerns.
var alertConfigNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// IsValid enforces per-entry invariants. Name validation runs first so
// downstream errors can reference it.
func (ac *alertConfig) IsValid() error {
	if !alertConfigNameRegex.MatchString(ac.Name) {
		return fmt.Errorf("invalid name %q: must be 1-64 chars, start with [a-z0-9], remainder [a-z0-9_-]", ac.Name)
	}
	if ac.Team == "" {
		return errors.New("must set team")
	}
	if ac.Channel == "" {
		return errors.New("must set channel")
	}
	if ac.WebhookID == "" {
		return errors.New("must set webhookID (the plugin creates this; do not set manually)")
	}
	if (ac.User == "") != (ac.Password == "") {
		return errors.New("user and password must both be set or both be empty")
	}
	return nil
}

// newConfiguration builds a configuration from validated entries and
// pre-computes the name index. Caller must have validated entries.
func newConfiguration(entries []alertConfig, webhookHost string, yamlTTLHours int, caBundle, metricsToken string, rotationDays int) *configuration {
	if yamlTTLHours < 0 {
		yamlTTLHours = 0
	}
	if rotationDays < 0 {
		rotationDays = 0
	}
	c := &configuration{
		AlertConfigs:          entries,
		WebhookHost:           strings.TrimRight(webhookHost, "/"),
		AssembledYAMLTTLHours: yamlTTLHours,
		AlertManagerCABundle:  caBundle,
		MetricsToken:          metricsToken,
		WebhookRotationDays:   rotationDays,
		nameIndex:             make(map[string]int, len(entries)),
	}
	for i, e := range entries {
		c.nameIndex[e.Name] = i
	}
	return c
}

// Clone deep-copies for safe atomic swap.
func (c *configuration) Clone() *configuration {
	cloned := make([]alertConfig, len(c.AlertConfigs))
	copy(cloned, c.AlertConfigs)
	return newConfiguration(cloned, c.WebhookHost, c.AssembledYAMLTTLHours, c.AlertManagerCABundle, c.MetricsToken, c.WebhookRotationDays)
}

// configMutex guards getConfiguration / setConfiguration. Embedded in Plugin
// rather than here to keep this file focused on the data model.

// validateWebhookHost rejects malformed WebhookHost values at config
// save time. Sanity-checks defense-in-depth — sysadmins are trusted,
// but typos shouldn't propagate to alertmanager.yml.
//
// Accepted forms: http[s]://host[:port] (no path).
// Empty string is valid (means "fall back to SiteURL").
func validateWebhookHost(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := neturl.Parse(raw)
	if err != nil {
		return fmt.Errorf("WebhookHost is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("WebhookHost must use http:// or https:// (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("WebhookHost has no host portion")
	}
	if u.Path != "" && u.Path != "/" {
		return fmt.Errorf("WebhookHost must be a host:port only, no path (got %q)", u.Path)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("WebhookHost cannot contain query string or fragment")
	}
	return nil
}

// parseAlertConfigs decodes and validates the JSON blob. Surfaces byte
// offsets on syntax errors and entry indices on validation errors so an
// admin staring at a multi-screen JSON blob can find the typo.
func parseAlertConfigs(blob string) ([]alertConfig, error) {
	blob = strings.TrimSpace(blob)
	if blob == "" {
		return nil, nil
	}

	var entries []alertConfig
	if err := json.Unmarshal([]byte(blob), &entries); err != nil {
		var syntaxErr *json.SyntaxError
		if errors.As(err, &syntaxErr) {
			return nil, fmt.Errorf("AlertConfigsJSON syntax error at byte offset %d: %w", syntaxErr.Offset, err)
		}
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return nil, fmt.Errorf("AlertConfigsJSON type error at byte offset %d (field %q): %w", typeErr.Offset, typeErr.Field, err)
		}
		return nil, fmt.Errorf("AlertConfigsJSON parse error: %w", err)
	}

	seenNames := make(map[string]struct{}, len(entries))
	// Track each webhookID's owner (team + channel + group) so we can
	// reject mismatches while allowing legitimate sharing across the
	// receivers in a single group/individual add invocation.
	type webhookOwner struct {
		team    string
		channel string
		group   string
	}
	seenWebhooks := make(map[string]webhookOwner, len(entries))
	for i := range entries {
		entries[i].AlertManagerURL = strings.TrimRight(entries[i].AlertManagerURL, "/")
		if err := entries[i].IsValid(); err != nil {
			return nil, fmt.Errorf("alertConfig[%d]: %w", i, err)
		}
		if _, dup := seenNames[entries[i].Name]; dup {
			return nil, fmt.Errorf("duplicate alertConfig name %q", entries[i].Name)
		}
		// WebhookID sharing constraint: receivers sharing a webhookID
		// must also share team + channel + groupName. Mismatches
		// indicate either operator error during a System Console
		// hand-edit or a bug in the plugin's own writes — reject at
		// parse time so the bad state can't activate.
		owner := webhookOwner{
			team:    entries[i].Team,
			channel: entries[i].Channel,
			group:   entries[i].GroupName,
		}
		if existing, seen := seenWebhooks[entries[i].WebhookID]; seen {
			if existing != owner {
				return nil, fmt.Errorf("alertConfig[%d] name=%q: webhookID %q is shared with a receiver in team=%q channel=%q group=%q; sharing requires matching team+channel+group (got team=%q channel=%q group=%q)",
					i, entries[i].Name, entries[i].WebhookID,
					existing.team, existing.channel, existing.group,
					owner.team, owner.channel, owner.group)
			}
		} else {
			seenWebhooks[entries[i].WebhookID] = owner
		}
		seenNames[entries[i].Name] = struct{}{}
	}
	return entries, nil
}

// configurationLock-aware helpers live on *Plugin.

// getConfiguration is defined as a method on *Plugin in plugin.go to keep
// the lock there with the lock state itself. Same for setConfiguration.

// OnConfigurationChange is the Mattermost hook for any settings update.
// Reads the JSON blob, validates, ensures destination teams exist (channels
// are auto-managed by the webhook system), and atomically swaps the runtime
// configuration.
//
// Does NOT call OnActivate — bot setup and command registration are
// one-time-per-process work owned by OnActivate.
func (p *Plugin) OnConfigurationChange() error {
	var raw rawConfiguration
	if err := p.API.LoadPluginConfiguration(&raw); err != nil {
		return fmt.Errorf("load plugin configuration: %w", err)
	}

	if err := validateWebhookHost(raw.WebhookHost); err != nil {
		return err
	}

	entries, err := parseAlertConfigs(raw.AlertConfigsJSON)
	if err != nil {
		return err
	}

	if p.API != nil {
		for _, ac := range entries {
			_, appErr := p.API.GetTeamByName(ac.Team)
			if appErr == nil {
				continue
			}
			// Tolerate transient errors (typically during early startup
			// before the API is fully ready). Hard-fail only on real 404s.
			if appErr.StatusCode == 404 {
				return fmt.Errorf("alertConfig %q: Mattermost team %q does not exist", ac.Name, ac.Team)
			}
			p.API.LogWarn("could not verify team existence (continuing)", "config", ac.Name, "team", ac.Team, "err", appErr.Error())
		}
	}

	p.setConfiguration(newConfiguration(entries, raw.WebhookHost, raw.AssembledYAMLTTLHours, raw.AlertManagerCABundle, raw.MetricsToken, raw.WebhookRotationDays))
	// Refresh the alertmanager package's HTTP client to use the new
	// CA bundle (if set). Applied on every config change so admins
	// can rotate certificates without a plugin restart.
	p.updateAlertmanagerHTTPClient(raw.AlertManagerCABundle)
	return nil
}

// Reflect import kept here to silence the unused-import error if Clone gets
// refactored. setConfiguration uses reflect.ValueOf below.
var _ = reflect.TypeOf

// Sync import is so that sync.RWMutex lives in this package. The actual
// mutex field is declared on Plugin in plugin.go; this reference makes the
// import explicit for code readers landing here first.
var _ sync.Locker = (*sync.Mutex)(nil)
