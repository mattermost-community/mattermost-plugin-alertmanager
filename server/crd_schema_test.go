package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCRDManifestKubeconform validates a generated AlertmanagerConfig + Secret
// against the real operator CRD schema, catching field/shape drift that the
// YAML round-trip tests (structure-only) can't. It SKIPS when kubeconform is
// not installed, so `go test` stays dependency-free locally — CI installs
// kubeconform and provides the schema (see .github/workflows/test.yml). Schemas
// come from the datreeio CRDs-catalog; `default` covers the core Secret.
func TestCRDManifestKubeconform(t *testing.T) {
	bin, err := exec.LookPath("kubeconform")
	if err != nil {
		t.Skip("kubeconform not installed; the validate-crd CI job runs this")
	}

	manifest := renderWebhookSecret("alertmanager-webhook-security", "monitoring", "https://mm.example/hooks/abc123") +
		"---\n" +
		renderAlertmanagerConfig("mattermost-alertmanager-security", "monitoring", "security-fallback", sampleCRDSpecs())

	dir := t.TempDir()
	path := filepath.Join(dir, "alertmanager-config.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	cmd := exec.Command(bin,
		"-strict",
		"-summary",
		"-schema-location", "default",
		"-schema-location", "https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json",
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("kubeconform rejected the generated manifest: %v\n%s", err, out)
	}
}
