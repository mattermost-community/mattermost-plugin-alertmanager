package main

import (
	"encoding/json"
	"fmt"
)

// resolveReceiverName takes a user-supplied receiver name and returns
// the actual stored name, accepting either the full suffixed form
// (high-cpu-usage--alert-slo-channel) or the short base slug
// (high-cpu-usage). For short-name lookups, prefers a match in the
// current channel; falls back to the unsuffixed legacy form anywhere.
// Returns the original input unchanged if no match found — the caller
// then surfaces a "not found" error.
//
// The plugin pointer is needed to resolve the current channel's slug
// from its ID, which is what the receiver name is suffixed with.
// receiverNameInScope reports whether name is among the channel-scoped
// receivers. remove/rotate use it to confirm a resolved name really belongs to
// the invocation channel before mutating it: resolveReceiverName returns its raw
// input unchanged on a miss, which could coincide with another channel's full
// receiver name, so an exact-match against the global list downstream would
// otherwise act cross-channel (CL-02).
func receiverNameInScope(scoped []alertConfig, name string) bool {
	for _, c := range scoped {
		if c.Name == name {
			return true
		}
	}
	return false
}

func resolveReceiverName(all []alertConfig, supplied, channelID string, p *Plugin) string {
	// 1. Exact match — covers full suffixed names AND legacy unsuffixed names
	for _, c := range all {
		if c.Name == supplied {
			return c.Name
		}
	}
	// 2. Short-name match scoped to current team + channel. Team is part of
	// the receiver name now, so the candidate needs the team slug too.
	if ch, appErr := p.API.GetChannel(channelID); appErr == nil {
		if team, teamErr := p.API.GetTeam(ch.TeamId); teamErr == nil {
			candidate := receiverNameForChannel(supplied, team.Name, ch.Name)
			for _, c := range all {
				if c.Name == candidate {
					return c.Name
				}
			}
		}
	}
	// 3. Short-name fallback against base slug across all entries
	for _, c := range all {
		if receiverBaseSlug(c.Name) == supplied {
			return c.Name
		}
	}
	return supplied
}

// resolveChannelID maps a stored team-slug + channel-slug to the channel ID.
// Rotate uses it to find the channel behind an existing receiver. It never
// creates a channel: a receiver whose channel was deleted fails here.
func (p *Plugin) resolveChannelID(teamSlug, channelSlug string) (string, error) {
	team, appErr := p.API.GetTeamByName(teamSlug)
	if appErr != nil {
		return "", fmt.Errorf("get team %q: %w", teamSlug, appErr)
	}
	channel, appErr := p.API.GetChannelByName(team.Id, channelSlug, false)
	if appErr != nil {
		return "", fmt.Errorf("get channel %q: %w", channelSlug, appErr)
	}
	return channel.Id, nil
}

// maxConfigWriteAttempts caps the compare-and-set retry loop in
// updateConfigsAtomic. Contention on the receiver list is admin-initiated and
// rare; a handful of attempts absorbs a genuine cross-pod race without spinning.
const maxConfigWriteAttempts = 5

// updateConfigsAtomic performs a cluster-safe read-modify-write of the receiver
// list (CL-24) and returns the list as it was BEFORE the change and AFTER it.
//
// configWriteMu only serializes writers within ONE pod. In HA, slash commands
// run on whichever pod serves the request, a KV write does NOT fire
// OnConfigurationChange on the other pods, and SavePluginConfig/KVSet is a blind
// overwrite — so two pods each computing from their own stale in-memory snapshot
// would lose an update. This reads the list straight from KV, applies transform
// to that fresh state, and commits with KVCompareAndSet (the write lands only if
// KV still holds exactly what we read); on a lost race it reloads and retries.
//
// transform must be a PURE function of the list it is handed — it may run several
// times, so it must not perform side effects (create/delete webhooks etc.). Do
// those before (incorporating the result into the transform) or after, keyed off
// the returned before/after lists.
//
// The caller MUST hold configWriteMu across its whole RMW; the guard panics
// otherwise. Holding it keeps intra-pod writers from needlessly losing CAS races
// to each other — CAS then only ever retries on a genuine cross-pod conflict.
func (p *Plugin) updateConfigsAtomic(transform func(current []alertConfig) ([]alertConfig, error)) (before, after []alertConfig, err error) {
	// Guard: TryLock succeeds only when the mutex is unlocked, so a success here
	// means nobody holds it — a caller forgot to lock. Fail loud rather than
	// silently reopen the race.
	if p.configWriteMu.TryLock() {
		p.configWriteMu.Unlock()
		panic("updateConfigsAtomic called without configWriteMu held — lock configWriteMu across the full read-modify-write")
	}

	for range maxConfigWriteAttempts {
		oldBytes, appErr := p.API.KVGet(kvKeyAlertConfigs)
		if appErr != nil {
			return nil, nil, fmt.Errorf("read receiver list from KV: %w", appErr)
		}
		current, perr := parseAlertConfigs(string(oldBytes))
		if perr != nil {
			return nil, nil, fmt.Errorf("parse current receiver list: %w", perr)
		}

		next, terr := transform(current)
		if terr != nil {
			return nil, nil, terr
		}

		newBytes, merr := json.MarshalIndent(next, "", "  ")
		if merr != nil {
			return nil, nil, fmt.Errorf("marshal: %w", merr)
		}
		// Validate before persisting so a bad write can't corrupt durable state.
		parsed, verr := parseAlertConfigs(string(newBytes))
		if verr != nil {
			return nil, nil, fmt.Errorf("validation: %w", verr)
		}

		// Commit only if KV still holds exactly what we read. A nil oldBytes (fresh
		// install — KVGet returns nil, never an empty slice, for an absent key)
		// makes this an insert-if-absent, which is what we want.
		set, appErr := p.API.KVCompareAndSet(kvKeyAlertConfigs, oldBytes, newBytes)
		if appErr != nil {
			return nil, nil, fmt.Errorf("persist receiver list to KV: %w", appErr)
		}
		if !set {
			continue // another pod wrote between our read and write — reload + retry
		}

		// Refresh THIS node's in-memory config from the committed write, then tell
		// peers to reload from KV — a KV write does not fire OnConfigurationChange
		// on other nodes, so without the broadcast they'd serve a stale list. The
		// config-map-backed settings are untouched by this write.
		//
		// The broadcast runs in a short-lived goroutine (fire-and-forget): callers
		// invoke updateConfigsAtomic while holding configWriteMu, and
		// PublishPluginClusterEvent is a cluster send we don't want to run under
		// that lock. It's best-effort anyway (this node is already fresh; peers
		// also converge via the reconciler), and the reload event is idempotent, so
		// racing/reordered sends are harmless.
		p.applyAlertConfigsToMemory(parsed)
		go p.broadcastConfigReload()
		return current, parsed, nil
	}
	return nil, nil, fmt.Errorf("receiver list is being modified concurrently; please retry")
}
