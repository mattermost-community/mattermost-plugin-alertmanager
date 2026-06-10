package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/christopherfickess/mattermost-plugin-alertmanager/server/alertmanager"
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
}

// LoadedInAM reports whether a given receiver name appears in the
// AM-side config the probe captured. Substring match — AM's config
// uses `name: <receivername>` so a literal contains check is enough.
// Returns false when probe failed or config wasn't parsed.
func (e amReachabilityEntry) LoadedInAM(receiverName string) bool {
	if e.ConfigBody == "" {
		return false
	}
	return strings.Contains(e.ConfigBody, "name: "+receiverName)
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
	}
	return entry
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
