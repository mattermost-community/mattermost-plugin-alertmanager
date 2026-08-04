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
	doc := string(body)

	for _, want := range []string{
		TargetPrometheusOperatorVersion,
		TargetAlertmanagerConfigAPIVersion,
		TargetPrometheusRuleAPIVersion,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/COMPATIBILITY.md is missing target constant %q — update the table to match server/crd_versions.go", want)
		}
	}
}
