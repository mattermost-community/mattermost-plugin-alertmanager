package main

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// fakeKVAPI implements the KV-store methods updateConfigsAtomic /
// loadAlertConfigsFromKV use. Embedding the interface satisfies the full method
// set; only KVGet/KVCompareAndSet are exercised. casFailuresRemaining lets a test
// simulate a cross-pod CAS conflict: KVCompareAndSet reports a lost race that
// many times before actually committing, so the retry loop can be observed.
type fakeKVAPI struct {
	plugin.API
	store                map[string][]byte
	casFailuresRemaining int
	casCalls             int

	mu              sync.Mutex // guards publishedEvents (broadcast fires from a goroutine)
	publishedEvents []string   // event IDs broadcast via PublishPluginClusterEvent
}

// PublishPluginClusterEvent records the broadcast so tests can assert peers are
// notified to reload after a committed KV write. Thread-safe: updateConfigsAtomic
// fires the broadcast in a goroutine.
func (f *fakeKVAPI) PublishPluginClusterEvent(ev model.PluginClusterEvent, _ model.PluginClusterEventSendOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishedEvents = append(f.publishedEvents, ev.Id)
	return nil
}

// publishedEventIDs returns a copy of the broadcast event IDs seen so far.
func (f *fakeKVAPI) publishedEventIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.publishedEvents...)
}

// Logging/audit no-ops so paths that log (e.g. repair's auditLog) don't nil-deref.
func (f *fakeKVAPI) LogInfo(string, ...any)               {}
func (f *fakeKVAPI) LogWarn(string, ...any)               {}
func (f *fakeKVAPI) LogError(string, ...any)              {}
func (f *fakeKVAPI) LogAuditRec(*model.AuditRecord)       {}
func (f *fakeKVAPI) KVSet(string, []byte) *model.AppError { return nil }

func (f *fakeKVAPI) KVGet(key string) ([]byte, *model.AppError) {
	return f.store[key], nil // absent key returns nil, treated as empty
}

func (f *fakeKVAPI) KVCompareAndSet(key string, oldValue, newValue []byte) (bool, *model.AppError) {
	f.casCalls++
	if f.casFailuresRemaining > 0 {
		f.casFailuresRemaining--
		return false, nil // simulate another pod having written first
	}
	if !bytes.Equal(f.store[key], oldValue) {
		return false, nil
	}
	f.store[key] = newValue
	return true, nil
}

// TestUpdateConfigsAtomicRoundTrip is the CL-19 + CL-24 happy path: the receiver
// list (with its webhook IDs and Alertmanager passwords) persists to the plugin
// KV store — not a config-map key readable via GET /api/v4/config — via
// compare-and-set, round-trips back with the secrets intact, and refreshes the
// in-memory configuration (no OnConfigurationChange fires for a KV write).
func TestUpdateConfigsAtomicRoundTrip(t *testing.T) {
	api := &fakeKVAPI{store: map[string][]byte{}}
	p := &Plugin{}
	p.API = api

	entries := []alertConfig{{
		Name:      "high-cpu-usage--team-chan",
		Team:      "team",
		Channel:   "chan",
		WebhookID: "abc123",
		User:      "svc",
		Password:  "s3cret",
	}}

	p.configWriteMu.Lock()
	before, after, err := p.updateConfigsAtomic(func(current []alertConfig) ([]alertConfig, error) {
		return append(current, entries...), nil
	})
	p.configWriteMu.Unlock()
	if err != nil {
		t.Fatalf("updateConfigsAtomic: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("expected empty before-list on a fresh store, got %d", len(before))
	}
	if len(after) != 1 || after[0].Password != "s3cret" {
		t.Fatalf("after-list wrong: %#v", after)
	}

	if _, ok := api.store[kvKeyAlertConfigs]; !ok {
		t.Fatalf("receiver list not stored under KV key %q", kvKeyAlertConfigs)
	}
	if len(api.store) != 1 {
		t.Fatalf("expected exactly one KV key written, got %d: %v", len(api.store), api.store)
	}

	if got := p.getConfiguration().AlertConfigs; len(got) != 1 || got[0].WebhookID != "abc123" {
		t.Fatalf("in-memory config not refreshed after save: %#v", got)
	}

	// A committed write must broadcast a reload to peers (KV writes don't fire
	// OnConfigurationChange on other nodes). The broadcast is fire-and-forget
	// (off the write lock), so poll briefly for it.
	var events []string
	for range 200 {
		if events = api.publishedEventIDs(); len(events) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(events) != 1 || events[0] != clusterEventReloadConfigs {
		t.Fatalf("expected one %q cluster broadcast after commit, got %v", clusterEventReloadConfigs, events)
	}

	loaded, err := p.loadAlertConfigsFromKV()
	if err != nil {
		t.Fatalf("loadAlertConfigsFromKV: %v", err)
	}
	if len(loaded) != 1 || loaded[0].WebhookID != "abc123" || loaded[0].Password != "s3cret" || loaded[0].User != "svc" {
		t.Fatalf("round-trip lost secret data: %#v", loaded)
	}
}

// TestUpdateConfigsAtomicRetriesOnCASMiss is the CL-24 regression: when the CAS
// loses a race (another pod wrote first), the loop reloads and retries rather
// than clobbering the winner, and the transform re-runs against fresh state.
func TestUpdateConfigsAtomicRetriesOnCASMiss(t *testing.T) {
	api := &fakeKVAPI{store: map[string][]byte{}, casFailuresRemaining: 2}
	p := &Plugin{}
	p.API = api

	transformRuns := 0
	p.configWriteMu.Lock()
	_, after, err := p.updateConfigsAtomic(func(current []alertConfig) ([]alertConfig, error) {
		transformRuns++
		return []alertConfig{{Name: "high-cpu-usage--t-c", Team: "t", Channel: "c", WebhookID: "h"}}, nil
	})
	p.configWriteMu.Unlock()
	if err != nil {
		t.Fatalf("expected eventual success after CAS retries, got %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("expected the write to commit after retries, got %#v", after)
	}
	// Two simulated conflicts + one success = three CAS attempts, three transforms.
	if api.casCalls != 3 || transformRuns != 3 {
		t.Fatalf("expected 3 CAS calls and 3 transform runs, got casCalls=%d runs=%d", api.casCalls, transformRuns)
	}
}

// TestUpdateConfigsAtomicGivesUpUnderContention verifies the loop bounds itself:
// persistent CAS conflicts return an error rather than spinning forever.
func TestUpdateConfigsAtomicGivesUpUnderContention(t *testing.T) {
	api := &fakeKVAPI{store: map[string][]byte{}, casFailuresRemaining: 999}
	p := &Plugin{}
	p.API = api

	p.configWriteMu.Lock()
	_, _, err := p.updateConfigsAtomic(noopTransform)
	p.configWriteMu.Unlock()
	if err == nil {
		t.Fatal("expected an error under unrelenting CAS contention, got nil")
	}
	if api.casCalls != maxConfigWriteAttempts {
		t.Fatalf("expected exactly %d CAS attempts, got %d", maxConfigWriteAttempts, api.casCalls)
	}
}

// TestLoadAlertConfigsFromKVEmpty confirms an absent key is not an error — a
// fresh install (nothing added yet) loads an empty list, not a failure.
func TestLoadAlertConfigsFromKVEmpty(t *testing.T) {
	p := &Plugin{}
	p.API = &fakeKVAPI{store: map[string][]byte{}}

	got, err := p.loadAlertConfigsFromKV()
	if err != nil {
		t.Fatalf("unexpected error for empty KV: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list for absent key, got %#v", got)
	}
}

// TestClusterEventReloadsFromKV is the HA cross-pod regression: a KV write on one
// node does not fire OnConfigurationChange on peers, so peers only converge when
// they receive the reload cluster event. Simulates a peer whose in-memory list is
// empty while KV already holds a receiver written by another node.
func TestClusterEventReloadsFromKV(t *testing.T) {
	blob := `[{"name":"high-cpu--team-chan","team":"team","channel":"chan","webhookID":"hook1"}]`
	api := &fakeKVAPI{store: map[string][]byte{kvKeyAlertConfigs: []byte(blob)}}
	p := &Plugin{}
	p.API = api

	if got := p.getConfiguration().AlertConfigs; len(got) != 0 {
		t.Fatalf("precondition: peer should start with an empty in-memory list, got %d", len(got))
	}

	// Delivering the reload event syncs in-memory from KV.
	p.OnPluginClusterEvent(nil, model.PluginClusterEvent{Id: clusterEventReloadConfigs})
	if got := p.getConfiguration().AlertConfigs; len(got) != 1 || got[0].Name != "high-cpu--team-chan" {
		t.Fatalf("cluster event did not reload the receiver list from KV: %#v", got)
	}

	// An unrelated event ID must be ignored (no reload).
	api.store[kvKeyAlertConfigs] = []byte(`[]`)
	p.OnPluginClusterEvent(nil, model.PluginClusterEvent{Id: "unrelated-event"})
	if got := p.getConfiguration().AlertConfigs; len(got) != 1 {
		t.Fatalf("unrelated cluster event should be ignored, got %#v", got)
	}
}

// TestClusterEventReloadSerializesWithWrites guards that OnPluginClusterEvent
// takes configWriteMu, so a peer reload can't interleave with a local
// read-modify-write and clobber newer in-memory state with a stale KV snapshot.
// While the lock is held (simulating an in-flight local write) the reload must
// block; once released it must proceed.
func TestClusterEventReloadSerializesWithWrites(t *testing.T) {
	api := &fakeKVAPI{store: map[string][]byte{kvKeyAlertConfigs: []byte(`[]`)}}
	p := &Plugin{}
	p.API = api

	p.configWriteMu.Lock()
	done := make(chan struct{})
	go func() {
		p.OnPluginClusterEvent(nil, model.PluginClusterEvent{Id: clusterEventReloadConfigs})
		close(done)
	}()

	select {
	case <-done:
		p.configWriteMu.Unlock()
		t.Fatal("reload ran while configWriteMu was held — not serialized with local writes")
	case <-time.After(100 * time.Millisecond):
		// expected: blocked on the write lock
	}

	p.configWriteMu.Unlock()
	select {
	case <-done:
		// expected: proceeds once the lock is free
	case <-time.After(3 * time.Second):
		t.Fatal("reload did not proceed after configWriteMu was released")
	}
}
