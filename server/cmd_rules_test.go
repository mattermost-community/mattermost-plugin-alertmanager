package main

import (
	"strings"
	"testing"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

func TestFilterPrometheusRulesKeepsTheGroup(t *testing.T) {
	raw, err := root.SamplesFS.ReadFile("samples/prometheus-rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	filtered, n, err := filterPrometheusRules(string(raw), scaffoldSets["compute"])
	if err != nil {
		t.Fatal(err)
	}
	if n < len(scaffoldSets["compute"]) {
		t.Fatalf("compute produced %d rules, want at least %d", n, len(scaffoldSets["compute"]))
	}
	if !strings.Contains(filtered, "runbook: high-cpu-usage") {
		t.Fatal("compute filter dropped high-cpu-usage")
	}
	if strings.Contains(filtered, "runbook: high-http-error-rate") {
		t.Fatal("compute filter kept an application rule")
	}
	manifest := buildPrometheusRuleManifest(filtered, "monitoring", prometheusRuleObject+"-compute")
	if !strings.Contains(manifest, "name: mattermost-alertmanager-rules-compute") {
		t.Fatal("per-group object name was not set")
	}
	if !strings.Contains(manifest, "apiVersion: "+TargetPrometheusRuleAPIVersion) {
		t.Fatal("manifest is not a PrometheusRule")
	}
}

func TestFullPrometheusRuleDownloadCoversEveryGroup(t *testing.T) {
	raw, err := root.SamplesFS.ReadFile("samples/prometheus-rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	manifest := buildPrometheusRuleManifest(string(raw), "monitoring", prometheusRuleObject)
	for _, name := range []string{
		"compute",
		"application",
		"database",
		"storage",
		"networking",
		"observability",
		"security",
		"mattermost-alertmanager-mattermost",
	} {
		if !strings.Contains(manifest, "name: "+name) {
			t.Fatalf("full CRD is missing group %s", name)
		}
	}
	for group, slugs := range scaffoldSets {
		if slugs == nil {
			continue
		}
		for _, slug := range slugs {
			if !strings.Contains(string(raw), "runbook: "+slug) {
				t.Errorf("group %s slug %s has no sample rule", group, slug)
			}
		}
	}
}
