package main

import (
	"net/http"
	"testing"
)

// TestSimSideEffectAllowed is the CL-20 gate: the side-effecting simulate modes
// run only on POST (so Mattermost's CSRF check applies); read-only modes run on
// any method.
func TestSimSideEffectAllowed(t *testing.T) {
	cases := []struct {
		method string
		mode   string
		want   bool
	}{
		{http.MethodGet, "webhook-test", false},
		{http.MethodPost, "webhook-test", true},
		{http.MethodGet, "end-to-end", false},
		{http.MethodPost, "end-to-end", true},
		{http.MethodHead, "end-to-end", false},
		{http.MethodPut, "webhook-test", false},
		// Read-only modes are never gated.
		{http.MethodGet, "simulate", true},
		{http.MethodGet, "", true},
	}
	for _, tc := range cases {
		if got := simSideEffectAllowed(tc.method, tc.mode); got != tc.want {
			t.Errorf("simSideEffectAllowed(%q, %q) = %v, want %v", tc.method, tc.mode, got, tc.want)
		}
	}
}
