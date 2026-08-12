package main

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-alertmanager/server/alertmanager"
)

// amReachabilityTTL is how long a reachability probe result is cached
// before the next probe runs. Tuned to balance "fresh signal" against
// "don't hammer AM when an admin reloads the inventory page."
const amReachabilityTTL = 60 * time.Second

// amProbeTimeout caps a single reachability probe. Short enough that a
// dead AM doesn't stretch an inventory page render into the painful range.
const amProbeTimeout = 3 * time.Second

// amReachabilityEntry is the cached result of one probe attempt.
// Stored in Plugin.amReachabilityCache keyed by AM URL.
type amReachabilityEntry struct {
	// Reachable is the result of the last probe; false on any error.
	Reachable bool
	// Status is a short human-readable string ("ok", "timeout",
	// "connection refused", "404", etc.) for surfacing in the UI.
	Status string
	// CheckedAt is when we last probed; results past TTL get re-probed.
	CheckedAt time.Time
	// ConfigBody is the raw YAML text of AM's currently-loaded config,
	// fetched from /api/v2/status. Used to confirm individual
	// receivers are loaded in AM ("loaded" indicator on the inventory
	// page, doctor check b). Empty when probe fails OR when AM
	// returned data we couldn't parse.
	ConfigBody string
	// receivers indexes the receiver names present in ConfigBody by their
	// canonical (operator-prefix-stripped) name. The value is true when a
	// canonical name appeared ONLY in operator-prefixed form — i.e. the
	// receiver is loaded via an AlertmanagerConfig CRD rather than flat YAML.
	// Built once at probe time so per-receiver lookups are O(1).
	receivers map[string]bool
}

// LoadedInAM reports whether a given receiver name appears in the AM-side
// config the probe captured, and whether it is present only via the Prometheus
// Operator's AlertmanagerConfig prefixing.
//
// Matching is canonical: the Prometheus Operator renames receivers to
// "<namespace>/<alertmanagerconfig-name>/<receiver>" when it merges an
// AlertmanagerConfig CRD into alertmanager.yaml, so a plain substring check for
// the bare name would miss CRD-managed receivers and wrongly report them "Not
// in AM YAML". We strip that prefix before comparing.
//
// Returns (false, false) when the probe failed or the config wasn't parsed.
func (e amReachabilityEntry) LoadedInAM(receiverName string) (loaded, viaOperator bool) {
	if e.receivers == nil {
		return false, false
	}
	// receiverName is the plugin's own name (never operator-prefixed), so it is
	// already canonical and can key the index directly.
	viaOperator, ok := e.receivers[receiverName]
	return ok, viaOperator
}

// canonicalReceiverName strips the Prometheus Operator's
// "<namespace>/<alertmanagerconfig-name>/" prefix, returning the bare receiver
// name and whether a prefix was present. Flat-YAML names (no "/") come back
// unchanged with viaOperator=false.
func canonicalReceiverName(name string) (canonical string, viaOperator bool) {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:], true
	}
	return name, false
}

// indexAMReceivers parses the receiver names out of an AM-loaded config body
// and indexes them by canonical name. A canonical name maps to true only when
// every occurrence was operator-prefixed, so a receiver also present in flat
// form is never mislabelled "via operator".
func indexAMReceivers(configBody string) map[string]bool {
	idx := map[string]bool{}
	for _, raw := range extractAMReceiverNames(configBody) {
		canonical, viaOperator := canonicalReceiverName(raw)
		if existing, ok := idx[canonical]; ok {
			idx[canonical] = existing && viaOperator
		} else {
			idx[canonical] = viaOperator
		}
	}
	return idx
}

// receiverLoadedIn reports whether receiverName is present in an AM-loaded
// config body, canonicalizing operator-prefixed names. Convenience wrapper for
// callers that hold a raw config body rather than a probe entry (e.g. the
// validate command).
func receiverLoadedIn(configBody, receiverName string) (loaded, viaOperator bool) {
	viaOperator, ok := indexAMReceivers(configBody)[receiverName]
	return ok, viaOperator
}

// probeAMReachability returns the cached reachability status for an
// Alertmanager URL, refreshing the cache if past TTL. Safe for
// concurrent callers — the underlying sync.Map handles per-key
// serialization; multiple admins viewing the inventory page in the
// same TTL window all share one probe result.
//
// Uses the alertmanager package's HTTP client so CA bundle settings
// apply here too.
func (p *Plugin) probeAMReachability(amURL string) amReachabilityEntry {
	if amURL == "" {
		return amReachabilityEntry{Reachable: false, Status: "(no URL)", CheckedAt: time.Now()}
	}

	if v, ok := p.amReachabilityCache.Load(amURL); ok {
		entry := v.(*amReachabilityEntry)
		if time.Since(entry.CheckedAt) < amReachabilityTTL {
			return *entry
		}
	}

	// Cache miss or expired — probe fresh.
	entry := doAMProbe(amURL)
	p.amReachabilityCache.Store(amURL, &entry)
	return entry
}

// doAMProbe makes a single GET against AM's /api/v2/status endpoint
// and classifies the result. Bigger response than /-/healthy because
// it includes the loaded AM config — the inventory page uses that
// data to show per-receiver "loaded in AM" indicators, so one probe
// answers two questions.
func doAMProbe(amURL string) amReachabilityEntry {
	ctx, cancel := context.WithTimeout(context.Background(), amProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, amURL+"/api/v2/status", nil)
	if err != nil {
		return amReachabilityEntry{Reachable: false, Status: "bad URL", CheckedAt: time.Now()}
	}

	resp, err := alertmanager.Client.Do(req)
	if err != nil {
		status := "unreachable"
		if ctx.Err() == context.DeadlineExceeded {
			status = "timeout"
		}
		return amReachabilityEntry{Reachable: false, Status: status, CheckedAt: time.Now()}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return amReachabilityEntry{Reachable: false, Status: http.StatusText(resp.StatusCode), CheckedAt: time.Now()}
	}

	// Parse out the loaded config. JSON parse failure isn't fatal —
	// AM responded so it's reachable; we just can't say what's loaded.
	var body struct {
		Config struct {
			Original string `json:"original"`
		} `json:"config"`
	}
	entry := amReachabilityEntry{Reachable: true, Status: "ok", CheckedAt: time.Now()}
	if err := json.NewDecoder(resp.Body).Decode(&body); err == nil {
		entry.ConfigBody = body.Config.Original
		entry.receivers = indexAMReceivers(entry.ConfigBody)
	}
	return entry
}

// extractAMReceiverNames scans an Alertmanager-loaded YAML body for
// receiver names. Used by the inventory page's inverse drift check
// — the inverse of LoadedInAM. Given AM's `config.original` we want
// to know "which receiver names exist in AM that the plugin doesn't
// track," which is the answer to "did someone hand-edit
// alertmanager.yml outside the plugin?"
//
// Parsing is regex-based rather than YAML-parsing because the body
// AM returns is already validated YAML (AM wouldn't have loaded it
// otherwise). The pattern `^\s+-\s+name: <value>` is unique to
// receiver list entries — slack_configs sub-blocks lead with
// `api_url:` and route entries lead with `matchers:`, neither of
// which would match this regex.
func extractAMReceiverNames(configBody string) []string {
	re := regexp.MustCompile(`(?m)^\s+-\s+name:\s+([^\s]+)`)
	matches := re.FindAllStringSubmatch(configBody, -1)
	seen := make(map[string]bool)
	var out []string
	for _, m := range matches {
		// Trim wrapping quotes — YAML allows quoted or unquoted scalars.
		name := strings.Trim(m[1], `"'`)
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// uniqueAMURLs deduplicates the AlertManagerURL field across a list of
// alertConfigs. Used by the inventory page so the reachability probe
// runs once per distinct AM (not once per receiver bound to that AM).
func uniqueAMURLs(configs []alertConfig) []string {
	seen := make(map[string]bool)
	var urls []string
	for _, c := range configs {
		if c.AlertManagerURL == "" {
			continue
		}
		if seen[c.AlertManagerURL] {
			continue
		}
		seen[c.AlertManagerURL] = true
		urls = append(urls, c.AlertManagerURL)
	}
	return urls
}

// Make sure sync is used (the cache is on Plugin, not in this file —
// keep the import explicit in case a future refactor lifts the cache).
var _ sync.Map
