package alertmanager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"

	"github.com/prometheus/alertmanager/api/v2/models"
)

// ptrDateTime wraps a time.Time as *strfmt.DateTime — the swagger model
// uses pointers to strfmt.DateTime for required date fields, so test
// fixtures need two layers (convert + take address).
func ptrDateTime(t time.Time) *strfmt.DateTime {
	d := strfmt.DateTime(t)
	return &d
}

func TestResolved(t *testing.T) {
	// Zero value (EndsAt == nil) — Resolved should report false
	// because we don't know when (or whether) it ends.
	s := &models.GettableSilence{}
	assert.False(t, Resolved(s))

	// Ends one minute from now — not yet resolved.
	s.EndsAt = ptrDateTime(time.Now().Add(time.Minute))
	assert.False(t, Resolved(s))

	// Ended one minute ago — resolved.
	s.EndsAt = ptrDateTime(time.Now().Add(-1 * time.Minute))
	assert.True(t, Resolved(s))
}

// TestExpireSilenceValidatesSilenceID guards the F-002 sink where a
// caller-controlled silenceID is interpolated straight into the request
// URL (alertmanagerURL + "/api/v2/silence/" + silenceID). An unvalidated
// silenceID is as much a path-control vector as the base-URL half of
// F-002 — a query string, fragment, or dot-segment traversal smuggled in
// via silenceID reaches the wire same as a hostile base URL would.
//
// Uses stdlib assertions (not testify) per project convention, even
// though this package's other test uses testify.
func TestExpireSilenceValidatesSilenceID(t *testing.T) {
	// Valid-shape IDs must proceed past validation and actually reach the
	// network — a local httptest server proves that with a real (but
	// fast, in-process) round trip instead of just checking the error
	// text, and avoids the httpRetry backoff loop a genuinely unreachable
	// address would trigger.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	t.Run("valid UUID-shaped ID is accepted past validation", func(t *testing.T) {
		if err := ExpireSilence("550e8400-e29b-41d4-a716-446655440000", ts.URL, "", ""); err != nil {
			t.Fatalf("ExpireSilence with a valid UUID silence ID returned an error: %v", err)
		}
	})
	t.Run("valid ULID-shaped ID is accepted past validation", func(t *testing.T) {
		if err := ExpireSilence("01ARZ3NDEKTSV4RRFFQ69G5FAV", ts.URL, "", ""); err != nil {
			t.Fatalf("ExpireSilence with a valid ULID silence ID returned an error: %v", err)
		}
	})

	// Rejection cases must fail on the ID-format check alone, before any
	// network call is attempted. Point them at a base URL nothing is
	// listening on — if a rejection case ever regressed past validation,
	// it would surface as a network/connection error here instead of the
	// "invalid silence ID" / "cannot be empty" message asserted below,
	// making the regression obvious rather than silently passing (and
	// without dragging in httpRetry's up-to-30s backoff loop, since a
	// passing test never reaches the network at all).
	const unreachableAM = "http://127.0.0.1:0"

	rejectCases := []struct {
		name      string
		silenceID string
		wantFrag  string
	}{
		{"empty ID is rejected", "", "silence ID cannot be empty"},
		{"query string suffix is rejected", "x?a=b", "invalid silence ID"},
		{"path traversal is rejected", "../../v1/kv/secret", "invalid silence ID"},
		{"fragment suffix is rejected", "x#y", "invalid silence ID"},
		{"65-character ID exceeds the length cap and is rejected", strings.Repeat("a", 65), "invalid silence ID"},
	}

	for _, tc := range rejectCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ExpireSilence(tc.silenceID, unreachableAM, "", "")
			if err == nil {
				t.Fatalf("ExpireSilence(%q, ...) = nil error, want a validation error", tc.silenceID)
			}
			if !strings.Contains(err.Error(), tc.wantFrag) {
				t.Fatalf("ExpireSilence(%q, ...) error = %q, want it to contain %q (a network error here would mean validation didn't run first)",
					tc.silenceID, err.Error(), tc.wantFrag)
			}
		})
	}
}
