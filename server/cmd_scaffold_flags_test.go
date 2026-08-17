package main

import (
	"reflect"
	"strings"
	"testing"
)

// TestExtractBoolFlag pins the --private parser: the flag is stripped from the
// remaining args wherever it appears (so it can't be mistaken for a positional
// team/channel/name), presence is reported correctly, and the order of the other
// args is preserved.
func TestExtractBoolFlag(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		flag        string
		wantPresent bool
		wantRest    []string
	}{
		{"absent", []string{"team", "chan", "url"}, "--private", false, []string{"team", "chan", "url"}},
		{"at end", []string{"team", "chan", "url", "--private"}, "--private", true, []string{"team", "chan", "url"}},
		{"in middle", []string{"team", "--private", "chan", "url"}, "--private", true, []string{"team", "chan", "url"}},
		{"at start", []string{"--private", "team", "chan"}, "--private", true, []string{"team", "chan"}},
		{"duplicated: still present, both stripped", []string{"--private", "team", "--private", "chan"}, "--private", true, []string{"team", "chan"}},
		{"empty args", nil, "--private", false, []string{}},
		{"order preserved", []string{"c", "a", "b", "--private"}, "--private", true, []string{"c", "a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			present, rest := extractBoolFlag(tc.args, tc.flag)
			if present != tc.wantPresent {
				t.Errorf("present = %v, want %v", present, tc.wantPresent)
			}
			if !reflect.DeepEqual(rest, tc.wantRest) {
				t.Errorf("rest = %#v, want %#v", rest, tc.wantRest)
			}
		})
	}
}

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
