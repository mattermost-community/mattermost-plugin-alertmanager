package main

import (
	"io/fs"
	"sort"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

const testPackRunbookDefaultURL = "https://mm.example/plugins/com.mattermost.alertmanager/public/runbooks/mattermost-server.html"

// TestPackRunbooksCoverRoutedAlerts is the drift guard: every alert the pack
// route claims has a runbook page, every page belongs to an alert, and every
// page has a command for the inline diagnostics.
func TestPackRunbooksCoverRoutedAlerts(t *testing.T) {
	routed, err := packRoutedAlertnames()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(routed)
	if got := sortedPackAlertnames(); strings.Join(got, ",") != strings.Join(routed, ",") {
		t.Errorf("packRunbookSlugs keys differ from the routed alerts\n got: %v\nwant: %v", got, routed)
	}

	paths, err := fs.Glob(root.RunbooksFS, "runbooks/"+packRunbookDir+"/*.md")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]bool{}
	for _, p := range paths {
		files[strings.TrimSuffix(strings.TrimPrefix(p, "runbooks/"+packRunbookDir+"/"), ".md")] = true
	}
	slugs := map[string]bool{}
	for alert, slug := range packRunbookSlugs {
		slugs[slug] = true
		if !files[slug] {
			t.Errorf("%s maps to %s.md, which does not exist", alert, slug)
			continue
		}
		if len(loadPackQuickDiagnostics(slug)) != 1 {
			t.Errorf("%s.md has no command under ## Quick diagnostics", slug)
		}
	}
	for f := range files {
		if !slugs[f] {
			t.Errorf("runbooks/%s/%s.md belongs to no pack alert", packRunbookDir, f)
		}
	}
}

// Pack pages sit under runbooks/ but must never become receivers.
func TestPackRunbooksAreNotReceivers(t *testing.T) {
	for _, slug := range loadRunbookSlugs() {
		if _, err := root.RunbooksFS.ReadFile("runbooks/" + slug + ".md"); err != nil {
			t.Errorf("receiver slug %q has no top-level runbook: %v", slug, err)
		}
	}
}

func executePackText(t *testing.T, receiverYAML string, alert sanAlert) string {
	t.Helper()
	// Alertmanager parses notification templates with missingkey=zero, so an
	// absent annotation reads as "".
	tmpl, err := template.New("text").Option("missingkey=zero").Funcs(amLikeFuncMap).Parse(extractTextTemplate(t, receiverYAML))
	if err != nil {
		t.Fatalf("pack text template does not parse: %v", err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, struct{ Alerts []sanAlert }{[]sanAlert{alert}}); err != nil {
		t.Fatalf("executing pack text template: %v", err)
	}
	return sb.String()
}

func TestPackReceiverLinksEachAlert(t *testing.T) {
	block := renderReceiverYAMLForKind(packReceiverSlug, "https://mm.example/hooks/x", "alerts", testPackRunbookDefaultURL, "https://mm.example/icon.png", kindPack)

	cfg := "route:\n  receiver: " + packReceiverSlug + "\nreceivers:\n" + block
	if err := validateMergedConfig(cfg); err != nil {
		t.Fatalf("pack receiver is not valid Alertmanager config: %v\n%s", err, cfg)
	}

	for alert, slug := range packRunbookSlugs {
		t.Run(alert, func(t *testing.T) {
			out := executePackText(t, block, sanAlert{Labels: sanKV{
				"alertname": alert,
				"namespace": "mm-prod",
				"pod":       "mm-6f9c-x",
				"installation_mattermost_com_installation": "mm",
			}})
			want := "**Runbook:** [runbook](https://mm.example/plugins/com.mattermost.alertmanager/public/runbooks/mattermost-operator/" + slug + ".html)"
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in:\n%s", want, out)
			}
			if strings.Count(out, "**Runbook:**") != 1 {
				t.Errorf("want exactly one runbook line:\n%s", out)
			}
			if !strings.Contains(out, "**Quick diagnostics:**\n\n1.\n```") || strings.Contains(out, "\n2.\n") {
				t.Errorf("want exactly one inline command:\n%s", out)
			}
			if !strings.Contains(out, "mm-prod") {
				t.Errorf("label placeholders were not filled in:\n%s", out)
			}
		})
	}
}

func TestPackReceiverPrefersRunbookURLAnnotation(t *testing.T) {
	block := renderReceiverYAMLForKind(packReceiverSlug, "https://mm.example/hooks/x", "alerts", testPackRunbookDefaultURL, "https://mm.example/icon.png", kindPack)
	out := executePackText(t, block, sanAlert{
		Labels:      sanKV{"alertname": "MattermostServerDown"},
		Annotations: sanKV{"runbook_url": "https://wiki.example/mm-down"},
	})
	if !strings.Contains(out, "[runbook](https://wiki.example/mm-down)") || strings.Contains(out, "mattermost-server-down.html") {
		t.Errorf("runbook_url annotation should win:\n%s", out)
	}
}

func TestPackReceiverSkipsUnknownAlert(t *testing.T) {
	block := renderReceiverYAMLForKind(packReceiverSlug, "https://mm.example/hooks/x", "alerts", testPackRunbookDefaultURL, "https://mm.example/icon.png", kindPack)
	out := executePackText(t, block, sanAlert{Labels: sanKV{"alertname": "SomethingElse"}})
	if strings.Contains(out, "**Runbook:**") || strings.Contains(out, "**Quick diagnostics:**") {
		t.Errorf("an alert outside the pack must get no runbook content:\n%s", out)
	}
}

func TestPackCRDReceiverLinksEachAlert(t *testing.T) {
	out := renderCRDReceiver(crdReceiverSpec{
		slug:              packReceiverSlug,
		name:              packReceiverSlug + "--team-alerts",
		secretName:        "alertmanager-webhook",
		channel:           "alerts",
		runbookDefaultURL: testPackRunbookDefaultURL,
		iconURL:           "https://mm.example/icon.png",
		kind:              kindPack,
	})
	var doc []map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("pack CRD receiver is not valid YAML: %v\n%s", err, out)
	}
	for _, slug := range packRunbookSlugs {
		if !strings.Contains(out, "/runbooks/mattermost-operator/"+slug+".html") {
			t.Errorf("CRD receiver has no link to %s", slug)
		}
	}
	if strings.Count(out, "**Quick diagnostics:**") != len(packRunbookSlugs) {
		t.Errorf("want one diagnostics branch per alert, got %d", strings.Count(out, "**Quick diagnostics:**"))
	}
}

func TestCustomReceiverStillHasNoRunbookContent(t *testing.T) {
	block := renderReceiverYAMLForKind("my-alerts", "https://mm.example/hooks/x", "alerts", "", "https://mm.example/icon.png", kindCustom)
	if strings.Contains(block, "mattermost-operator/") || strings.Contains(block, "**Quick diagnostics:**") {
		t.Errorf("custom receiver picked up pack runbook content:\n%s", block)
	}
}
