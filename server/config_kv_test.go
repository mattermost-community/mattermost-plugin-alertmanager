package main

import (
	"bytes"
	"testing"

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
}

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
