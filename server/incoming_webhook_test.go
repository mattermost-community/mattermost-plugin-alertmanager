package main

import (
	"errors"
	"strings"
	"testing"
)

// TestWebhookDeleteError pins that a webhook-delete failure never carries the raw
// hook ID in its message — the error is logged via err.Error() at sites that
// redact the dedicated field, so a raw ID here would silently undo CL-13. The
// underlying cause is still wrapped (errors.Is), and the redacted fingerprint is
// present for correlation.
func TestWebhookDeleteError(t *testing.T) {
	const id = "abcdef0123456789abcdef0123456789"
	cause := errors.New("boom from Client4")

	err := webhookDeleteError(id, cause)
	if strings.Contains(err.Error(), id) {
		t.Fatalf("delete error leaks the raw webhook ID: %q", err.Error())
	}
	if !strings.Contains(err.Error(), redactHookID(id)) {
		t.Fatalf("delete error should carry the redacted fingerprint: %q", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Fatalf("delete error must wrap the underlying cause")
	}
}

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
