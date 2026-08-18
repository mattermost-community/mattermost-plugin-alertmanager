package main

import (
	"encoding/hex"
	"testing"
)

// TestGenerateMetricsTokenValue proves the minted token is the documented shape:
// 64 lowercase hex chars (32 random bytes), decodes cleanly, and is unique across
// calls (a static or short token would defeat the bearer-auth on /metrics).
func TestGenerateMetricsTokenValue(t *testing.T) {
	const wantLen = 64 // 32 bytes -> 64 hex chars, matches the setting placeholder

	seen := make(map[string]bool)
	for range 100 {
		token, err := generateMetricsTokenValue()
		if err != nil {
			t.Fatalf("generateMetricsTokenValue returned error: %v", err)
		}
		if len(token) != wantLen {
			t.Fatalf("token length = %d, want %d (%q)", len(token), wantLen, token)
		}
		if _, err := hex.DecodeString(token); err != nil {
			t.Fatalf("token is not valid hex: %q (%v)", token, err)
		}
		if seen[token] {
			t.Fatalf("duplicate token generated: %q", token)
		}
		seen[token] = true
	}
}
