package main

import (
	"io/fs"
	"regexp"
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

var backtickedName = regexp.MustCompile("`([a-z_]+)`")

// packAlertLabels is the label set a pack page promises its alert carries,
// read from its "## Required Prometheus labels" section.
func packAlertLabels(t *testing.T, slug string) map[string]bool {
	t.Helper()
	data, err := root.RunbooksFS.ReadFile("runbooks/" + packRunbookDir + "/" + slug + ".md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(data), "## Required Prometheus labels")
	if !ok {
		t.Fatalf("%s.md has no ## Required Prometheus labels section", slug)
	}
	section, _, _ = strings.Cut(section, "\n## ")
	labels := map[string]bool{}
	for _, m := range backtickedName.FindAllStringSubmatch(section, -1) {
		if labelPlaceholderAllowlist[m[1]] {
			labels[m[1]] = true
		}
	}
	return labels
}

func packRuleExprs(t *testing.T) map[string]string {
	t.Helper()
	exprs := map[string]string{}
	for _, file := range []string{mattermostOperatorRulesFile, mattermostPostgresRulesFile} {
		raw, err := root.SamplesFS.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Groups []struct {
				Rules []struct{ Alert, Expr string }
			}
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, g := range doc.Groups {
			for _, r := range g.Rules {
				exprs[r.Alert] = r.Expr
			}
		}
	}
	return exprs
}

const installationLabel = "installation_mattermost_com_installation"

var (
	byClause = regexp.MustCompile(`\bby\s*\(([^)]*)\)`)
	// Label-changing PromQL the check cannot reason about: without (...),
	// aggregations with no leading by (...) (sum(x) by (y) included), and
	// functions that drop or rewrite labels.
	unsupportedAggregation = regexp.MustCompile(`\bwithout\s*\(|\b(sum|min|max|avg|count|group|stddev|stdvar|topk|bottomk|quantile|count_values|limitk|limit_ratio|absent|absent_over_time|label_replace|label_join|scalar|vector)\s*\(`)
)

// packCommandLabels is the set of allowlisted labels the inline command uses.
func packCommandLabels(slug string) map[string]bool {
	used := map[string]bool{}
	for _, blk := range loadPackQuickDiagnostics(slug) {
		for line := range strings.SplitSeq(blk.Code, "\n") {
			if isCommentLine(line) {
				continue
			}
			for _, m := range labelPlaceholderRegex.FindAllStringSubmatch(line, -1) {
				if labelPlaceholderAllowlist[m[1]] {
					used[m[1]] = true
				}
			}
		}
	}
	return used
}

// TestPackCommandsUseOnlyAlertLabels: an inline command may only use labels
// the alert carries, or Alertmanager renders the placeholder as "". The page's
// label list is checked against the rule, so it cannot claim a label the
// rule's series lack or aggregate away.
func TestPackCommandsUseOnlyAlertLabels(t *testing.T) {
	exprs := packRuleExprs(t)
	for alert, slug := range packRunbookSlugs {
		expr, ok := exprs[alert]
		if !ok || strings.TrimSpace(expr) == "" {
			t.Errorf("%s has no rule in %s or %s", alert, mattermostOperatorRulesFile, mattermostPostgresRulesFile)
			continue
		}
		labels := packAlertLabels(t, slug)
		if len(labels) == 0 {
			t.Errorf("%s.md lists no allowlisted labels", slug)
		}
		if m := unsupportedAggregation.FindString(expr); m != "" {
			t.Errorf("%s uses %q; extend this test before relying on its labels", alert, m)
		}
		// Only series scraped by the Mattermost ServiceMonitor carry the
		// installation label, and every such rule selects on it.
		if labels[installationLabel] && !strings.Contains(expr, installationLabel) {
			t.Errorf("%s.md lists %s, but the %s rule never selects it", slug, installationLabel, alert)
		}
		for _, m := range byClause.FindAllStringSubmatch(expr, -1) {
			kept := map[string]bool{}
			for l := range strings.SplitSeq(m[1], ",") {
				kept[strings.TrimSpace(l)] = true
			}
			for l := range labels {
				if !kept[l] {
					t.Errorf("%s.md lists %s, but %s aggregates it away: by (%s)", slug, l, alert, m[1])
				}
			}
		}
		for l := range packCommandLabels(slug) {
			if !labels[l] {
				t.Errorf("%s.md inline command uses <%s>, which %s does not carry", slug, l, alert)
			}
		}
	}
}

var packLabelValues = map[string]string{
	"namespace":       "ns-prod",
	"pod":             "pod-6f9c-x",
	installationLabel: "chat-main",
}

// packAlertSample is the alert as Alertmanager would deliver it: only the
// labels the page says the rule keeps.
func packAlertSample(t *testing.T, alert, slug string) sanAlert {
	t.Helper()
	labels := sanKV{"alertname": alert}
	for l := range packAlertLabels(t, slug) {
		v, ok := packLabelValues[l]
		if !ok {
			t.Fatalf("no test value for label %q listed in %s.md", l, slug)
		}
		labels[l] = v
	}
	return sanAlert{Labels: labels}
}

// assertPackCommandFilled checks the inline command itself, not the whole
// post, since the Details section prints label values too.
func assertPackCommandFilled(t *testing.T, out, slug string) {
	t.Helper()
	_, cmd, ok1 := strings.Cut(out, "**Quick diagnostics:**")
	_, cmd, ok2 := strings.Cut(cmd, "```")
	cmd, _, ok3 := strings.Cut(cmd, "```")
	if !ok1 || !ok2 || !ok3 || strings.TrimSpace(cmd) == "" {
		t.Errorf("%s: no fenced inline command in:\n%s", slug, out)
		return
	}
	for l := range packCommandLabels(slug) {
		if !strings.Contains(cmd, packLabelValues[l]) {
			t.Errorf("%s: <%s> was not filled in:\n%s", slug, l, cmd)
		}
	}
	for line := range strings.SplitSeq(cmd, "\n") {
		if isCommentLine(line) {
			continue
		}
		// Any tag, not just allowlisted ones, so a shrunk allowlist fails here.
		for _, m := range labelPlaceholderRegex.FindAllStringSubmatch(line, -1) {
			if !knownLiteralPlaceholders[m[1]] {
				t.Errorf("%s: raw placeholder <%s> left in %q", slug, m[1], line)
			}
		}
	}
}

func executePackText(t *testing.T, receiverYAML string, alert sanAlert) string {
	t.Helper()
	return executePackTemplate(t, extractTextTemplate(t, receiverYAML), alert)
}

func executePackTemplate(t *testing.T, text string, alert sanAlert) string {
	t.Helper()
	// Alertmanager parses notification templates with missingkey=zero, so an
	// absent annotation reads as "".
	tmpl, err := template.New("text").Option("missingkey=zero").Funcs(amLikeFuncMap).Parse(text)
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
			out := executePackText(t, block, packAlertSample(t, alert, slug))
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
			assertPackCommandFilled(t, out, slug)
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
	var doc []struct {
		SlackConfigs []struct{ Text string } `yaml:"slackConfigs"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("pack CRD receiver is not valid YAML: %v\n%s", err, out)
	}
	if len(doc) != 1 || len(doc[0].SlackConfigs) != 1 {
		t.Fatalf("expected one receiver with one slackConfig:\n%s", out)
	}
	text := doc[0].SlackConfigs[0].Text
	for alert, slug := range packRunbookSlugs {
		got := executePackTemplate(t, text, packAlertSample(t, alert, slug))
		assertPackCommandFilled(t, got, slug)
		if !strings.Contains(got, "/runbooks/mattermost-operator/"+slug+".html)") {
			t.Errorf("CRD receiver has no link to %s for %s:\n%s", slug, alert, got)
		}
		if strings.Count(got, "**Quick diagnostics:**") != 1 {
			t.Errorf("CRD receiver wants one diagnostics block for %s:\n%s", alert, got)
		}
	}
}

func TestPackReceiverIgnoresUnsafeRunbookURL(t *testing.T) {
	block := renderReceiverYAMLForKind(packReceiverSlug, "https://mm.example/hooks/x", "alerts", testPackRunbookDefaultURL, "https://mm.example/icon.png", kindPack)
	for _, bad := range []string{"javascript:alert(1)", "https://ok](https://evil)", "/admin_console", "https://ok.example/x\nINJECT"} {
		out := executePackText(t, block, sanAlert{
			Labels:      sanKV{"alertname": "MattermostServerDown"},
			Annotations: sanKV{"runbook_url": bad},
		})
		if !strings.Contains(out, "[runbook](https://mm.example/plugins/com.mattermost.alertmanager/public/runbooks/mattermost-operator/mattermost-server-down.html)") {
			t.Errorf("runbook_url %q should fall back to the pack page:\n%s", bad, out)
		}
		leaked := strings.Contains(out, "evil") || strings.Contains(out, "javascript") ||
			strings.Contains(out, "INJECT") || strings.Contains(out, "admin_console")
		if strings.Count(out, "](") != 1 || leaked {
			t.Errorf("runbook_url %q leaked into the post:\n%s", bad, out)
		}
	}
}

func TestCustomReceiverStillHasNoRunbookContent(t *testing.T) {
	block := renderReceiverYAMLForKind("my-alerts", "https://mm.example/hooks/x", "alerts", "", "https://mm.example/icon.png", kindCustom)
	if strings.Contains(block, "mattermost-operator/") || strings.Contains(block, "**Quick diagnostics:**") {
		t.Errorf("custom receiver picked up pack runbook content:\n%s", block)
	}
}
