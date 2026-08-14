package main

import (
	"regexp"
	"sort"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

// The types below reproduce the slice of Alertmanager's notification data model
// that the receiver `text:` template touches: `.Alerts` (ranged), each alert's
// `.Labels` / `.Annotations` (map access like `.Labels.alertname`), and
// `.Labels.SortedPairs` (a method on AM's KV type, not a map key). Reproducing
// them here lets the test execute the emitted template exactly as Alertmanager
// would at delivery, so we prove the CL-06 sanitizer neutralizes injection in
// the real rendered output — not just that a substring is present.
type sanKV map[string]string

type sanPair struct{ Name, Value string }

// SortedPairs mirrors github.com/prometheus/alertmanager/template KV.SortedPairs.
func (k sanKV) SortedPairs() []sanPair {
	keys := make([]string, 0, len(k))
	for key := range k {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]sanPair, len(keys))
	for i, key := range keys {
		pairs[i] = sanPair{Name: key, Value: k[key]}
	}
	return pairs
}

type sanAlert struct {
	Labels      sanKV
	Annotations sanKV
}

// amLikeFuncMap reproduces the Alertmanager template funcs the notification
// body relies on. reReplaceAll is AM's own (regexp-backed) helper; toUpper is in
// AM's funcmap too. printf is a text/template builtin, so it needs no entry.
var amLikeFuncMap = template.FuncMap{
	"reReplaceAll": func(pattern, repl, text string) string {
		return regexp.MustCompile(pattern).ReplaceAllString(text, repl)
	},
	"toUpper": strings.ToUpper,
}

// extractTextTemplate pulls the slack_configs[0].text block out of a rendered
// file-format receiver — that string is the live Alertmanager Go template.
func extractTextTemplate(t *testing.T, receiverYAML string) string {
	t.Helper()
	var recv []struct {
		SlackConfigs []struct {
			Text string `yaml:"text"`
		} `yaml:"slack_configs"`
	}
	if err := yaml.Unmarshal([]byte(receiverYAML), &recv); err != nil {
		t.Fatalf("rendered receiver is not valid YAML: %v\n%s", err, receiverYAML)
	}
	if len(recv) != 1 || len(recv[0].SlackConfigs) != 1 {
		t.Fatalf("expected one receiver with one slack_config, got %#v", recv)
	}
	return recv[0].SlackConfigs[0].Text
}

// TestReceiverTextSanitizesHostileAlert is the CL-06 regression: it renders a
// receiver, executes the emitted `text:` template against an alert whose labels
// and annotations carry markdown/shell injection, and asserts none of it
// survives into the on-call post. Parsing the template also proves the emitted
// reReplaceAll directives are valid AM template syntax (the real risk of the
// escaping) — a broken directive would fail Parse and light this test up.
func TestReceiverTextSanitizesHostileAlert(t *testing.T) {
	// Slug with no runbook file → QUICK_DIAGNOSTICS is empty, keeping the test
	// focused on the label/annotation sinks rather than diagnostics rendering.
	yamlOut := renderReceiverYAML("no-such-runbook--team-chan", "https://mm.example/hooks/x", "alerts", "https://mm.example/runbook", "https://mm.example/icon.png")
	textTmpl := extractTextTemplate(t, yamlOut)

	tmpl, err := template.New("text").Funcs(amLikeFuncMap).Parse(textTmpl)
	if err != nil {
		t.Fatalf("emitted text template does not parse (would break AM rendering): %v\n%s", err, textTmpl)
	}

	hostile := struct{ Alerts []sanAlert }{
		Alerts: []sanAlert{{
			Labels: sanKV{
				"alertname": "AL[x](https://evil)ERT",
				"severity":  "critical",
				// A label value whose own backtick would otherwise close the code span.
				"zzz_evil": "V1`V2",
			},
			Annotations: sanKV{
				"description":   "D1\nD2 [click](https://evil) `id`",
				"runbook_url":   "https://ok](https://evil)",
				"dashboard_url": "https://grafana`x`",
			},
		}},
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, hostile); err != nil {
		t.Fatalf("executing emitted text template failed: %v", err)
	}
	out := sb.String()

	// Structural invariants — the template itself never emits these sequences, so
	// any occurrence is injected content that slipped through sanitization.
	if strings.Contains(out, "](") {
		t.Errorf("disguised markdown link survived (found `](`):\n%s", out)
	}
	if strings.Contains(out, "$(") {
		t.Errorf("shell command substitution survived (found `$(`):\n%s", out)
	}
	// Newline injection: the description's two halves must land on one line.
	if !strings.Contains(out, "D1D2") {
		t.Errorf("newline in description was not stripped (expected D1D2 adjacent):\n%s", out)
	}
	// Code-span breakout: the data-supplied backtick must be gone, keeping the
	// value inside its span. The literal span backticks the template emits stay.
	if strings.Contains(out, "V1`V2") {
		t.Errorf("backtick in label value survived (code-span breakout):\n%s", out)
	}
	if strings.Contains(out, "`id`") {
		t.Errorf("backtick-wrapped token from description survived:\n%s", out)
	}
	if strings.Contains(out, "grafana`") {
		t.Errorf("backtick in dashboard_url survived:\n%s", out)
	}

	// Benign content must still render (sanitizer strips, it doesn't blank fields).
	if !strings.Contains(out, "`V1V2`") {
		t.Errorf("sanitized label value lost its code span:\n%s", out)
	}
	// Brackets are intentionally NOT stripped (only parens are — that alone breaks
	// the `[text](url)` link syntax), so the alertname keeps its `[x]` but lost the
	// `(...)` that would have made it a link.
	if !strings.Contains(out, "AL[x]https://evilERT") {
		t.Errorf("sanitized alertname not rendered as expected:\n%s", out)
	}
}

// TestCRDFallbackReceiverSanitizerWired proves the CRD fallback receiver — which
// carries its own copy of the label/annotation sinks — routes through the shared
// sanitizer. The full shared body is execution-tested via the file path above;
// here we just confirm the fallback path emits the sanitize directive rather
// than a bare interpolation.
func TestCRDFallbackReceiverSanitizerWired(t *testing.T) {
	spec := crdReceiverSpec{name: "fallback--team-chan", secretName: "sec", channel: "alerts", iconURL: "https://mm.example/icon.png"}
	out := renderCRDFallbackReceiver(spec)
	if strings.Contains(out, "**Alert:** {{ .Labels.alertname }}") {
		t.Fatalf("CRD fallback receiver still emits an unsanitized alertname:\n%s", out)
	}
	if !strings.Contains(out, `reReplaceAll "[`) {
		t.Fatalf("CRD fallback receiver did not expand the sanitizer directive:\n%s", out)
	}
}
