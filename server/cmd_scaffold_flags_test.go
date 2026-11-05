package main

import (
	"strings"
	"testing"
)

// TestEnforceReceiverCap is the CL-25 quota guard: exactly hitting the cap is
// allowed, one over is rejected, and the boundary is inclusive (`>` not `>=`).
func TestEnforceReceiverCap(t *testing.T) {
	cases := []struct {
		name     string
		existing int
		adding   int
		wantErr  bool
	}{
		{"well under", 10, 5, false},
		{"exactly at cap", maxReceivers - 1, 1, false},
		{"one over", maxReceivers, 1, true},
		{"bulk add crosses the cap", maxReceivers - 5, 30, true},
		{"already at cap, add one", maxReceivers, 1, true},
		{"empty add at cap is fine", maxReceivers, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := enforceReceiverCap(tc.existing, tc.adding)
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error for existing=%d adding=%d, got nil", tc.existing, tc.adding)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for existing=%d adding=%d: %v", tc.existing, tc.adding, err)
			}
			if err != nil && !strings.Contains(err.Error(), "receiver limit reached") {
				t.Fatalf("error should name the limit, got: %v", err)
			}
		})
	}
}
