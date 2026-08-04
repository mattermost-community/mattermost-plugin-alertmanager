package main

import (
	"strings"
	"testing"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

// TestCompatibilityDocMatchesConstants guards docs/COMPATIBILITY.md against
// drifting from the CRD target constants in crd_versions.go. The constants are
// the single source of truth; the doc table must quote each verbatim so a
// version bump can't silently leave the published table stale.
func TestCompatibilityDocMatchesConstants(t *testing.T) {
	body, err := root.DocsFS.ReadFile("docs/COMPATIBILITY.md")
	if err != nil {
		t.Fatalf("COMPATIBILITY.md not embedded: %v", err)
	}
	// Scope to the "## Current target" section and require each value
	// backticked, as the table renders them. Two reasons over a whole-doc
	// Contains: (1) a stale table can't be masked by the value appearing in
	// prose, and (2) the backtick delimiters stop "monitoring.coreos.com/v1"
	// from matching as a substring of ".../v1alpha1".
	section := currentTargetSection(string(body))
	if section == "" {
		t.Fatal(`docs/COMPATIBILITY.md is missing the "## Current target" section`)
	}
	for _, want := range []string{
		TargetPrometheusOperatorVersion,
		TargetAlertmanagerConfigAPIVersion,
		TargetPrometheusRuleAPIVersion,
	} {
		if !strings.Contains(section, "`"+want+"`") {
			t.Errorf("docs/COMPATIBILITY.md \"## Current target\" section is missing backticked %q — update the table to match server/crd_versions.go", want)
		}
	}
}

// currentTargetSection returns the body of the "## Current target" section (up
// to the next "## " heading), or "" if the heading is absent.
func currentTargetSection(doc string) string {
	const heading = "## Current target"
	i := strings.Index(doc, heading)
	if i < 0 {
		return ""
	}
	rest := doc[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		return rest[:j]
	}
	return rest
}
