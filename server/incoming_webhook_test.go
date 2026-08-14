package main

import (
	"strings"
	"testing"
)

// TestRedactHookID pins the CL-13 log-redaction contract: the raw webhook ID (a
// bearer token) never appears in the fingerprint, the fingerprint is stable for
// a given ID (so log lines correlate), distinct IDs differ, and empty is labeled.
func TestRedactHookID(t *testing.T) {
	const id = "abcdef0123456789abcdef0123456789"

	got := redactHookID(id)
	if strings.Contains(got, id) {
		t.Fatalf("redacted form leaks the raw webhook ID: %q", got)
	}
	if !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("expected sha256: prefix, got %q", got)
	}
	if got != redactHookID(id) {
		t.Fatalf("fingerprint not stable for the same ID")
	}
	if redactHookID("different-id") == got {
		t.Fatalf("distinct IDs produced the same fingerprint")
	}
	if redactHookID("") != "(none)" {
		t.Fatalf("empty ID should render as (none), got %q", redactHookID(""))
	}
}
