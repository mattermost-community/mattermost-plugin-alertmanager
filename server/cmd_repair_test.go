package main

import (
	"strings"
	"testing"
)

// TestRepairAlertConfigsKV covers the F-002/F-003 recovery path: diagnose-only
// without --force, validate the snapshot before writing, and CAS on the corrupt
// bytes so a concurrent repair on another node can't be clobbered.
func TestRepairAlertConfigsKV(t *testing.T) {
	const valid = `[{"name":"high-cpu--t-c","team":"t","channel":"c","webhookID":"h1"}]`
	const corrupt = `{ not valid json`

	seedInMemory := func(p *Plugin) {
		p.setConfiguration(newConfiguration([]alertConfig{
			{Name: "high-cpu--t-c", Team: "t", Channel: "c", WebhookID: "h1"},
		}, "", 0, "", "", 0))
	}

	t.Run("valid KV needs no repair", func(t *testing.T) {
		api := &fakeKVAPI{store: map[string][]byte{kvKeyAlertConfigs: []byte(valid)}}
		p := &Plugin{}
		p.API = api
		if msg := p.repairAlertConfigsKV("u1", "c1", false); !strings.Contains(msg, "no repair needed") {
			t.Fatalf("got %q", msg)
		}
	})

	t.Run("corrupt KV without --force only diagnoses", func(t *testing.T) {
		api := &fakeKVAPI{store: map[string][]byte{kvKeyAlertConfigs: []byte(corrupt)}}
		p := &Plugin{}
		p.API = api
		msg := p.repairAlertConfigsKV("u1", "c1", false)
		if !strings.Contains(msg, "UNREADABLE") || !strings.Contains(msg, "--force") {
			t.Fatalf("got %q", msg)
		}
		if string(api.store[kvKeyAlertConfigs]) != corrupt {
			t.Fatal("diagnose must not modify KV")
		}
	})

	t.Run("--force rewrites from in-memory via CAS", func(t *testing.T) {
		api := &fakeKVAPI{store: map[string][]byte{kvKeyAlertConfigs: []byte(corrupt)}}
		p := &Plugin{}
		p.API = api
		seedInMemory(p)
		if msg := p.repairAlertConfigsKV("u1", "c1", true); !strings.Contains(msg, "Repaired") {
			t.Fatalf("got %q", msg)
		}
		if _, err := parseAlertConfigs(string(api.store[kvKeyAlertConfigs])); err != nil {
			t.Fatalf("KV should be valid after repair: %v", err)
		}
	})

	t.Run("--force aborts on CAS miss (F-002 concurrent change)", func(t *testing.T) {
		api := &fakeKVAPI{store: map[string][]byte{kvKeyAlertConfigs: []byte(corrupt)}, casFailuresRemaining: 1}
		p := &Plugin{}
		p.API = api
		seedInMemory(p)
		msg := p.repairAlertConfigsKV("u1", "c1", true)
		if !strings.Contains(msg, "changed while repairing") {
			t.Fatalf("expected CAS-miss abort, got %q", msg)
		}
		if string(api.store[kvKeyAlertConfigs]) != corrupt {
			t.Fatal("KV must be untouched after a CAS miss")
		}
	})

	t.Run("--force writes the SANITIZED snapshot, not the raw one (F-002)", func(t *testing.T) {
		api := &fakeKVAPI{store: map[string][]byte{kvKeyAlertConfigs: []byte(corrupt)}}
		p := &Plugin{}
		p.API = api
		// In-memory holds an un-sanitized URL (a stale ?query a pre-hardening build
		// might have accepted). parseAlertConfigs strips it; repair must persist the
		// sanitized form.
		p.setConfiguration(newConfiguration([]alertConfig{
			{Name: "probe--t-c", Team: "t", Channel: "c", AlertManagerURL: "http://am.example.com?x", WebhookID: "h1"},
		}, "", 0, "", "", 0))
		if msg := p.repairAlertConfigsKV("u1", "c1", true); !strings.Contains(msg, "Repaired") {
			t.Fatalf("got %q", msg)
		}
		written := string(api.store[kvKeyAlertConfigs])
		if strings.Contains(written, "?x") {
			t.Fatalf("repair wrote the UN-sanitized URL (F-002):\n%s", written)
		}
		if _, err := parseAlertConfigs(written); err != nil {
			t.Fatalf("written KV must be valid: %v", err)
		}
	})

	t.Run("--force applies the sanitized list to local memory (no wait for reload)", func(t *testing.T) {
		api := &fakeKVAPI{store: map[string][]byte{kvKeyAlertConfigs: []byte(corrupt)}}
		p := &Plugin{}
		p.API = api
		// In-memory holds an un-sanitized URL. After a force repair the writing
		// node must serve the SANITIZED value immediately, not keep the stale
		// snapshot until the next periodic reload.
		p.setConfiguration(newConfiguration([]alertConfig{
			{Name: "probe--t-c", Team: "t", Channel: "c", AlertManagerURL: "http://am.example.com?x", WebhookID: "h1"},
		}, "", 0, "", "", 0))
		if msg := p.repairAlertConfigsKV("u1", "c1", true); !strings.Contains(msg, "Repaired") {
			t.Fatalf("got %q", msg)
		}
		got := p.getConfiguration().AlertConfigs
		if len(got) != 1 {
			t.Fatalf("expected 1 in-memory receiver after repair, got %d", len(got))
		}
		if strings.Contains(got[0].AlertManagerURL, "?x") {
			t.Fatalf("local memory still holds the UN-sanitized URL after repair: %q", got[0].AlertManagerURL)
		}
	})

	t.Run("--force refuses an invalid in-memory snapshot (F-003)", func(t *testing.T) {
		api := &fakeKVAPI{store: map[string][]byte{kvKeyAlertConfigs: []byte(corrupt)}}
		p := &Plugin{}
		p.API = api
		// Duplicate names: newConfiguration accepts it, parseAlertConfigs rejects it.
		p.setConfiguration(newConfiguration([]alertConfig{
			{Name: "dup--t-c", Team: "t", Channel: "c", WebhookID: "h1"},
			{Name: "dup--t-c", Team: "t", Channel: "c", WebhookID: "h1"},
		}, "", 0, "", "", 0))
		msg := p.repairAlertConfigsKV("u1", "c1", true)
		if !strings.Contains(msg, "snapshot is itself invalid") {
			t.Fatalf("expected refuse-invalid-snapshot, got %q", msg)
		}
		if string(api.store[kvKeyAlertConfigs]) != corrupt {
			t.Fatal("KV must be untouched when the snapshot is invalid")
		}
	})
}
