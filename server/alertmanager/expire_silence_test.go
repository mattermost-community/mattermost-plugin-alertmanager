package alertmanager

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSilenceUUID = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

// TestExpireSilenceRejectsNonUUID is the core CL-08 regression: any ID that
// isn't a UUID — traversal, query injection, empty, malformed — must be rejected
// BEFORE a request goes out, so an attacker-controlled path segment never
// reaches the wire with the basic-auth header attached.
func TestExpireSilenceRejectsNonUUID(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	bad := []string{
		"",
		"../../../-/reload",
		testSilenceUUID + "/../../-/reload",
		testSilenceUUID + "?x=y",
		"not-a-uuid",
		"6ba7b810-9dad-11d1-80b4", // too short
		"g" + testSilenceUUID[1:], // non-hex char
	}
	for _, id := range bad {
		hit = false
		if err := ExpireSilence(id, srv.URL, "user", "pass"); err == nil {
			t.Errorf("id %q: expected rejection, got nil error", id)
		}
		if hit {
			t.Errorf("id %q: request reached the server despite an invalid ID", id)
		}
	}
}

// TestExpireSilenceValidUUID confirms a well-formed UUID hits the expected path
// and that a non-200 response does NOT reflect the response body (no oracle).
func TestExpireSilenceValidUUID(t *testing.T) {
	// httptest servers listen on loopback, which the SSRF dial guard blocks by
	// default — allowlist it so these tests exercise real request behavior
	// (simulates an admin who allowlisted a same-host Alertmanager).
	SetAllowedNets([]*net.IPNet{mustCIDR("127.0.0.0/8"), mustCIDR("::1/128")})
	t.Cleanup(func() { SetAllowedNets(nil) })

	t.Run("200 succeeds and targets the silence path", func(t *testing.T) {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		if err := ExpireSilence(testSilenceUUID, srv.URL, "", ""); err != nil {
			t.Fatalf("valid UUID unexpectedly failed: %v", err)
		}
		if want := "/api/v2/silence/" + testSilenceUUID; gotPath != want {
			t.Fatalf("request path = %q, want %q", gotPath, want)
		}
	})

	t.Run("non-200 error does not leak the response body", func(t *testing.T) {
		const secret = "SECRET-INTERNAL-CONFIG-abc123"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(secret))
		}))
		defer srv.Close()

		err := ExpireSilence(testSilenceUUID, srv.URL, "", "")
		if err == nil {
			t.Fatal("expected an error on a 500 response")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error reflected the response body (disclosure oracle): %v", err)
		}
	})
}

// TestExpireSilenceDoesNotFollowRedirect verifies RefuseRedirect: a
// path-normalizing 301 must not be followed (which would re-attach credentials),
// so the redirect target is never reached and the caller sees a non-200.
func TestExpireSilenceDoesNotFollowRedirect(t *testing.T) {
	// Allowlist loopback so the httptest front/target servers are reachable
	// through the SSRF-guarded client (see TestExpireSilenceValidUUID).
	SetAllowedNets([]*net.IPNet{mustCIDR("127.0.0.0/8"), mustCIDR("::1/128")})
	t.Cleanup(func() { SetAllowedNets(nil) })

	var followed bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed = true
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v2/silence/"+testSilenceUUID, http.StatusMovedPermanently)
	}))
	defer front.Close()

	err := ExpireSilence(testSilenceUUID, front.URL, "user", "pass")
	if err == nil {
		t.Fatal("expected a non-200 error from the unfollowed redirect")
	}
	if followed {
		t.Fatal("client followed the redirect — RefuseRedirect not applied")
	}
}
