package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// fakeKVAPI implements only the KV-store methods of plugin.API for the CL-19
// round-trip test. Embedding the interface satisfies the full method set at
// compile time; any method other than KVGet/KVSet panics if called, which is
// fine because saveConfigsLocked / loadAlertConfigsFromKV touch only the KV
// store. Nothing here writes to a plugin config map — that's the point of CL-19.
type fakeKVAPI struct {
	plugin.API
	store map[string][]byte
}

func (f *fakeKVAPI) KVSet(key string, value []byte) *model.AppError {
	f.store[key] = value
	return nil
}

func (f *fakeKVAPI) KVGet(key string) ([]byte, *model.AppError) {
	return f.store[key], nil // absent key returns nil, which loadAlertConfigsFromKV treats as empty
}

// TestAlertConfigsKVRoundTrip is the CL-19 regression: the receiver list (with
// its webhook IDs and Alertmanager passwords) must persist to the plugin KV
// store — not a config-map key readable via GET /api/v4/config — and round-trip
// back with the secrets intact. It also confirms saveConfigsLocked refreshes the
// in-memory configuration itself, since a KV write fires no OnConfigurationChange.
func TestAlertConfigsKVRoundTrip(t *testing.T) {
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
	err := p.saveConfigsLocked(entries)
	p.configWriteMu.Unlock()
	if err != nil {
		t.Fatalf("saveConfigsLocked: %v", err)
	}

	// The list is under the KV key, and that is the ONLY key written — no config
	// map, no other keys that GET /api/v4/config could surface.
	if _, ok := api.store[kvKeyAlertConfigs]; !ok {
		t.Fatalf("receiver list not stored under KV key %q", kvKeyAlertConfigs)
	}
	if len(api.store) != 1 {
		t.Fatalf("expected exactly one KV key written, got %d: %v", len(api.store), api.store)
	}

	// saveConfigsLocked must refresh the in-memory config (KVSet fires no hook).
	if got := p.getConfiguration().AlertConfigs; len(got) != 1 || got[0].WebhookID != "abc123" {
		t.Fatalf("in-memory config not refreshed after save: %#v", got)
	}

	// Round-trips back through the loader with the secret fields intact.
	loaded, err := p.loadAlertConfigsFromKV()
	if err != nil {
		t.Fatalf("loadAlertConfigsFromKV: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 entry from KV, got %d", len(loaded))
	}
	if loaded[0].WebhookID != "abc123" || loaded[0].Password != "s3cret" || loaded[0].User != "svc" {
		t.Fatalf("round-trip lost secret data: %#v", loaded[0])
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
