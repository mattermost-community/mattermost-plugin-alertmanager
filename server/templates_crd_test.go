package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// sampleCRDSpecs builds a fallback + one real runbook receiver sharing a webhook
// secret, mirroring the plugin's one-shared-webhook-per-group model.
func sampleCRDSpecs() []crdReceiverSpec {
	return []crdReceiverSpec{
		{
			slug:       "", // fallback: parent-route catch-all, no sub-route
			name:       "security-fallback",
			secretName: "alertmanager-webhook-security",
			channel:    "security-alerts",
			iconURL:    "https://mm.example/plugins/com.mattermost.alertmanager/public/alertmanager-logo.png",
		},
		{
			slug:              "unexpected-container-image",
			name:              "unexpected-container-image--team-security-alerts",
			secretName:        "alertmanager-webhook-security",
			channel:           "security-alerts",
			runbookDefaultURL: "https://mm.example/plugins/com.mattermost.alertmanager/public/runbooks/unexpected-container-image.html",
			iconURL:           "https://mm.example/plugins/com.mattermost.alertmanager/public/alertmanager-logo.png",
		},
	}
}

// TestRenderAlertmanagerConfigIsValidYAML proves the generated CRD parses as
// well-formed YAML. The title:/text: block scalars contain Alertmanager Go
// templates ({{ .Status }} etc.) that must survive as literal strings, not
// break the document — a round-trip unmarshal is the cheapest proof.
func TestRenderAlertmanagerConfigIsValidYAML(t *testing.T) {
	out := renderAlertmanagerConfig("mattermost-alertmanager-security", "monitoring", "security-fallback", sampleCRDSpecs())

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("generated AlertmanagerConfig is not valid YAML: %v\n---\n%s", err, out)
	}

	if got := doc["apiVersion"]; got != TargetAlertmanagerConfigAPIVersion {
		t.Errorf("apiVersion = %v, want %s", got, TargetAlertmanagerConfigAPIVersion)
	}
	if got := doc["kind"]; got != "AlertmanagerConfig" {
		t.Errorf("kind = %v, want AlertmanagerConfig", got)
	}
}

// TestRenderCRDReceiverParity checks the shared body landed at the CRD's deeper
// nesting (parity with the file format, just re-indented). If these columns
// drift the operator rejects the manifest, so pin them explicitly.
func TestRenderCRDReceiverParity(t *testing.T) {
	out := renderAlertmanagerConfig("cr", "monitoring", "security-fallback", sampleCRDSpecs())

	checks := map[string]string{
		"secret-ref apiURL":         "        - apiURL:\n            name: alertmanager-webhook-security\n            key: url",
		"camelCased sendResolved":   "\n          sendResolved: true",
		"color key at 10 spaces":    "\n          color: '{{ if eq .Status \"firing\" }}",
		"title body at 12 spaces":   "\n            {{- if eq .Status \"firing\" -}}",
		"sub-route with continue":   "      - matchers: [{name: runbook, value: \"unexpected-container-image\", matchType: \"=\"}]\n        receiver: unexpected-container-image--team-security-alerts\n        continue: true",
		"parent =~ matcher":         "value: \"^(unexpected-container-image)$\"",
		"per-runbook runbook URL":   "runbooks/unexpected-container-image.html",
	}
	for name, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("%s: generated CRD missing expected fragment:\n%s\n---full---\n%s", name, want, out)
		}
	}
}

// TestRenderWebhookSecretIsValidYAML proves the companion Secret is well-formed
// and keeps the URL as a stringData value.
func TestRenderWebhookSecretIsValidYAML(t *testing.T) {
	out := renderWebhookSecret("alertmanager-webhook-security", "monitoring", "https://mm.example/hooks/abc123")

	var sec struct {
		Kind       string            `yaml:"kind"`
		StringData map[string]string `yaml:"stringData"`
	}
	if err := yaml.Unmarshal([]byte(out), &sec); err != nil {
		t.Fatalf("generated Secret is not valid YAML: %v\n%s", err, out)
	}
	if sec.Kind != "Secret" {
		t.Errorf("kind = %q, want Secret", sec.Kind)
	}
	if sec.StringData["url"] != "https://mm.example/hooks/abc123" {
		t.Errorf("stringData.url = %q, want the webhook URL", sec.StringData["url"])
	}
}
