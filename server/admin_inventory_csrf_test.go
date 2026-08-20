package main

import (
	"net/http"
	"testing"
)

// TestSimSideEffectAllowed is the CL-20 gate: the side-effecting simulate modes
// run only on a POST that ALSO carries X-Requested-With: XMLHttpRequest (so a
// cross-site form/link can't trigger them even if the platform forwarded the
// session); read-only modes run on any method with any header.
func TestSimSideEffectAllowed(t *testing.T) {
	const xhr = "XMLHttpRequest"
	cases := []struct {
		method        string
		requestedWith string
		mode          string
		want          bool
	}{
		// Side-effecting: need POST *and* the XHR header.
		{http.MethodPost, xhr, "webhook-test", true},
		{http.MethodPost, xhr, "end-to-end", true},
		{http.MethodPost, "", "webhook-test", false},    // POST but no XHR header (plain form)
		{http.MethodPost, "fetch", "end-to-end", false}, // wrong header value
		{http.MethodGet, xhr, "webhook-test", false},    // GET even with header
		{http.MethodGet, "", "end-to-end", false},       // the cross-site link case
		{http.MethodHead, xhr, "end-to-end", false},
		{http.MethodPut, xhr, "webhook-test", false},
		// Read-only modes are never gated, regardless of method/header.
		{http.MethodGet, "", "simulate", true},
		{http.MethodGet, "", "", true},
	}
	for _, tc := range cases {
		if got := simSideEffectAllowed(tc.method, tc.requestedWith, tc.mode); got != tc.want {
			t.Errorf("simSideEffectAllowed(%q, %q, %q) = %v, want %v", tc.method, tc.requestedWith, tc.mode, got, tc.want)
		}
	}
}
