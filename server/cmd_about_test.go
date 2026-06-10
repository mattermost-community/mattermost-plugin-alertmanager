package main

import (
	"testing"
)

// TestCountDistinctAMs verifies the dedup count shown in the about
// command. Empty URLs are skipped — they represent partially configured
// receivers and shouldn't inflate the backend count.
func TestCountDistinctAMs(t *testing.T) {
	cases := []struct {
		name    string
		configs []alertConfig
		want    int
	}{
		{"empty", nil, 0},
		{"all same URL", []alertConfig{
			{AlertManagerURL: "http://am1"},
			{AlertManagerURL: "http://am1"},
		}, 1},
		{"all different URLs", []alertConfig{
			{AlertManagerURL: "http://am1"},
			{AlertManagerURL: "http://am2"},
			{AlertManagerURL: "http://am3"},
		}, 3},
		{"empty URLs are ignored", []alertConfig{
			{AlertManagerURL: ""},
			{AlertManagerURL: "http://am1"},
			{AlertManagerURL: ""},
		}, 1},
		{"mix", []alertConfig{
			{AlertManagerURL: "http://am1"},
			{AlertManagerURL: "http://am2"},
			{AlertManagerURL: "http://am1"},
			{AlertManagerURL: ""},
		}, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := countDistinctAMs(tc.configs)
			if got != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

// TestOrDash and TestOrFallback cover the small display helpers used in
// the about output. Trivial but they hide a subtle bug: returning the
// raw value when empty would render as a confusing blank line in chat.
func TestOrDash(t *testing.T) {
	if got := orDash(""); got != "—" {
		t.Fatalf("empty should render as em-dash, got %q", got)
	}
	if got := orDash("hello"); got != "hello" {
		t.Fatalf("non-empty should pass through, got %q", got)
	}
}

func TestOrFallback(t *testing.T) {
	if got := orFallback("", "_unset_"); got != "_unset_" {
		t.Fatalf("empty should use fallback, got %q", got)
	}
	if got := orFallback("https://mm.example.com", "_unset_"); got != "`https://mm.example.com`" {
		t.Fatalf("non-empty should be backtick-wrapped, got %q", got)
	}
}

func TestPresence(t *testing.T) {
	yesGot := presence(true, "configured", "not set")
	if yesGot != ":white_check_mark: configured" {
		t.Fatalf("present case wrong: %q", yesGot)
	}
	noGot := presence(false, "configured", "not set")
	if noGot != "not set" {
		t.Fatalf("absent case wrong: %q", noGot)
	}
}
