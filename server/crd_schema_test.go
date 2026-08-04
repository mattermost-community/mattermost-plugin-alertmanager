package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCRDManifestKubeconform validates a generated AlertmanagerConfig against
// the operator's v1alpha1 CRD schema, catching field/shape drift the YAML
// round-trip tests (structure-only) can't.
//
// Offline by design: the schema is vendored at build/crd-schemas/ (see that
// dir's README) rather than fetched from the datreeio catalog at run time, so
// this works in air-gapped CI. Only the AlertmanagerConfig is checked — the
// companion Secret is core/v1 (would need the remote default schema store) and
// is already covered by TestRenderWebhookSecretIsValidYAML.
//
// SKIPS when kubeconform is absent so local `go test` stays dependency-free;
// the validate-crd CI job installs it (see .github/workflows/test.yml).
func TestCRDManifestKubeconform(t *testing.T) {
	bin, err := exec.LookPath("kubeconform")
	if err != nil {
		t.Skip("kubeconform not installed; the validate-crd CI job runs this")
	}

	manifest := renderAlertmanagerConfig("mattermost-alertmanager-security", "monitoring", "security-fallback", sampleCRDSpecs())

	dir := t.TempDir()
	path := filepath.Join(dir, "alertmanager-config.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// Tests run from the package dir (server/); the vendored schema is one up.
	schemaLoc := "../build/crd-schemas/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json"

	cmd := exec.Command(bin,
		"-strict",
		"-summary",
		"-schema-location", schemaLoc,
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("kubeconform rejected the generated AlertmanagerConfig: %v\n%s", err, out)
	}
}
