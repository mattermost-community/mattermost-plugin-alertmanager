package main

import (
	"testing"
)

// TestReceiverNameForChannel pins the suffixing pattern used to keep
// receiver names unique across channels. The double-hyphen separator
// is load-bearing: it has to be unambiguous when parsed back out by
// receiverBaseSlug.
func TestReceiverNameForChannel(t *testing.T) {
	cases := []struct {
		slug, channel, want string
	}{
		{"high-cpu-usage", "alerts", "high-cpu-usage--alerts"},
		{"high-cpu-usage", "alert-slo-channel", "high-cpu-usage--alert-slo-channel"},
		{"pod-not-ready", "ops", "pod-not-ready--ops"},
	}

	for _, tc := range cases {
		t.Run(tc.slug+"/"+tc.channel, func(t *testing.T) {
			got := receiverNameForChannel(tc.slug, tc.channel)
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// TestReceiverBaseSlug covers the inverse: extracting the runbook slug
// portion from a channel-suffixed receiver name. Crucial for the
// runbook URL fallback — that lookup is keyed by slug, not by full
// receiver name, so getting this parse wrong silently routes users to
// the wrong docs.
func TestReceiverBaseSlug(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// Channel-suffixed: the slug portion before the first `--`.
		{"high-cpu-usage--alerts", "high-cpu-usage"},
		{"high-cpu-usage--alert-slo-channel", "high-cpu-usage"},
		{"pod-not-ready--ops", "pod-not-ready"},

		// Unsuffixed names pass through unchanged. Treated as a legacy
		// shape — receivers created before channel-suffixing existed
		// keep working without rewrite.
		{"high-cpu-usage", "high-cpu-usage"},
		{"plain", "plain"},

		// Pathological: only-separator inputs. We return the part
		// before `--` since that's the documented contract. An empty
		// string here means the caller fed us garbage, but the
		// function shouldn't panic.
		{"--alerts", "--alerts"},
		{"slug--", "slug"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := receiverBaseSlug(tc.input)
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
