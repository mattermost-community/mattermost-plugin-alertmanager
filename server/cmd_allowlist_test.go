package main

import (
	"strings"
	"testing"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

func TestNormalizeRegistryPrefix(t *testing.T) {
	got, err := normalizeRegistryPrefix("GHCR.IO/MyOrg")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ghcr.io/myorg/" {
		t.Fatalf("got %q", got)
	}
	if _, quoteErr := normalizeRegistryPrefix(`ghcr.io/myorg/");`); quoteErr == nil {
		t.Fatal("expected a prefix with a quote to be rejected")
	}
	if _, spaceErr := normalizeRegistryPrefix("ghcr.io/my org/"); spaceErr == nil {
		t.Fatal("expected a prefix with a space to be rejected")
	}
	digest, err := normalizeRegistryPrefix("sha256:")
	if err != nil || digest != "sha256:" {
		t.Fatalf("digest prefix = %q err=%v", digest, err)
	}
}

func TestParseImageAllowlistDedupesAndNormalizes(t *testing.T) {
	got, err := parseImageAllowlist("ghcr.io/myorg/\nregistry.example.com:5000/team\n,ghcr.io/myorg/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "ghcr.io/myorg/" || got[1] != "registry.example.com:5000/team/" {
		t.Fatalf("got %#v", got)
	}
}

func TestApplyImageAllowlistInsertsEscapedPrefix(t *testing.T) {
	raw, err := root.SamplesFS.ReadFile("samples/prometheus-rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := applyImageAllowlist(string(raw), []string{"ghcr.io/myorg/"})
	if err != nil {
		t.Fatal(err)
	}
	want := `|ghcr\\.io/myorg/|sha256:).*"`
	if !strings.Contains(got, want) {
		t.Fatalf("escaped prefix not inserted before the digest alternative")
	}
	if strings.Count(got, want) != 1 {
		t.Fatalf("prefix inserted %d times", strings.Count(got, want))
	}
	unchanged, err := applyImageAllowlist(string(raw), nil)
	if err != nil || unchanged != string(raw) {
		t.Fatal("empty extras should leave the sample unchanged")
	}
}

func TestParseShippedImagePrefixes(t *testing.T) {
	raw, err := root.SamplesFS.ReadFile("samples/prometheus-rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseShippedImagePrefixes(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !containsPrefix(got, "registry.mattermost.com/") || !containsPrefix(got, "sha256:") {
		t.Fatalf("shipped prefixes = %#v", got)
	}
	if strings.Contains(strings.Join(got, " "), `\\`) {
		t.Fatalf("shipped prefixes still contain PromQL escaping: %#v", got)
	}
}
