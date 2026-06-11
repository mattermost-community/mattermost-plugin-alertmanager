package main

import (
	"strings"
	"testing"
)

// TestBuildDiffAgainstLoaded covers the unified-diff-style merger
// for /alertmanager export --diff-against-loaded. The function does
// textual insertion of additions into a captured AM YAML body —
// these tests pin the insertion points so a future regression
// doesn't silently start emitting the additions in the wrong block.
func TestBuildDiffAgainstLoaded(t *testing.T) {
	t.Run("insert receivers + routes at correct block ends", func(t *testing.T) {
		loaded := `global:
  smtp_from: alerts@example.com

receivers:
  - name: pagerduty-team-1
    pagerduty_configs:
      - service_key: abc
  - name: email-team-2
    email_configs:
      - to: ops@example.com

route:
  receiver: pagerduty-team-1
  routes:
    - matchers: [team="db"]
      receiver: pagerduty-team-1
`
		newRecvs := `- name: high-cpu-usage--alerts
  slack_configs:
    - api_url: 'https://mm.example/hooks/abc'
`
		newRoutes := `- matchers: [runbook="high-cpu-usage"]
  receiver: high-cpu-usage--alerts
  continue: true
`
		diff := buildDiffAgainstLoaded(loaded, newRecvs, newRoutes)

		// Both addition markers should appear
		if !strings.Contains(diff, "+ # ---- plugin additions: receivers ----") {
			t.Fatalf("receivers addition marker missing.\nOutput:\n%s", diff)
		}
		if !strings.Contains(diff, "+ # ---- plugin additions: routes ----") {
			t.Fatalf("routes addition marker missing.\nOutput:\n%s", diff)
		}
		// Added lines carry the + prefix
		if !strings.Contains(diff, "+ - name: high-cpu-usage--alerts") {
			t.Fatalf("added receiver line missing + prefix.\nOutput:\n%s", diff)
		}
		// Context lines carry the 2-space prefix
		if !strings.Contains(diff, "  receivers:") {
			t.Fatalf("context line for receivers: missing 2-space prefix.\nOutput:\n%s", diff)
		}
	})

	t.Run("receivers insertion lands before next top-level key", func(t *testing.T) {
		// The receivers: block runs from line 4 to the route: line.
		// The insertion should be on the line that starts `route:`,
		// pushing route: down by N lines.
		loaded := `receivers:
  - name: existing
route:
  receiver: existing
`
		newRecvs := `- name: added-receiver
`
		diff := buildDiffAgainstLoaded(loaded, newRecvs, "")

		// Find the marker and confirm it appears before the route: context line.
		markerIdx := strings.Index(diff, "+ # ---- plugin additions: receivers ----")
		routeIdx := strings.Index(diff, "  route:")
		if markerIdx == -1 {
			t.Fatalf("marker missing")
		}
		if routeIdx == -1 {
			t.Fatalf("route: context line missing")
		}
		if markerIdx > routeIdx {
			t.Fatalf("receivers addition marker (%d) should appear before route: (%d)", markerIdx, routeIdx)
		}
	})

	t.Run("receivers at EOF (no following top-level key)", func(t *testing.T) {
		loaded := `route:
  receiver: existing
receivers:
  - name: existing
`
		newRecvs := `- name: added
`
		diff := buildDiffAgainstLoaded(loaded, newRecvs, "")

		if !strings.Contains(diff, "+ - name: added") {
			t.Fatalf("addition missing from EOF-receivers case.\nOutput:\n%s", diff)
		}
	})

	t.Run("missing receivers: block falls back to manual-merge note", func(t *testing.T) {
		// Unusual but possible: an AM YAML where the only receiver
		// definition is under a custom key, or a partial config.
		loaded := `global:
  smtp_from: alerts@example.com
`
		newRecvs := `- name: orphan
`
		diff := buildDiffAgainstLoaded(loaded, newRecvs, "")
		if !strings.Contains(diff, "couldn't find `receivers:` block") {
			t.Fatalf("fallback note missing for receivers-less YAML.\nOutput:\n%s", diff)
		}
	})

	t.Run("no additions returns the loaded YAML in context form", func(t *testing.T) {
		loaded := `receivers:
  - name: existing
`
		diff := buildDiffAgainstLoaded(loaded, "", "")
		// Should have no + lines.
		for _, line := range strings.Split(diff, "\n") {
			if strings.HasPrefix(line, "+ ") {
				t.Fatalf("expected no additions, found: %q", line)
			}
		}
	})
}
