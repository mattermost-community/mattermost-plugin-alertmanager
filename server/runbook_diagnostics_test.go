package main

import (
	"strings"
	"testing"
)

// TestExtractQuickDiagnostics drives the markdown parser against the
// shapes it actually encounters: a real runbook with the section
// present, an MD body without the section, and the boundary cases
// where the limit caps the result or the next H2 heading ends it.
func TestExtractQuickDiagnostics(t *testing.T) {
	t.Run("real runbook returns three blocks", func(t *testing.T) {
		// Use the actual embedded high-cpu-usage runbook — this
		// catches regressions where the runbook content drifts from
		// what the parser expects (e.g., heading text changed).
		got := loadQuickDiagnosticsForSlug("high-cpu-usage")
		if len(got) != 3 {
			t.Fatalf("expected 3 diagnostic blocks, got %d", len(got))
		}
		if got[0].Lang != "bash" {
			t.Fatalf("first block: expected lang=bash, got %q", got[0].Lang)
		}
		if !strings.Contains(got[0].Code, "kubectl top") {
			t.Fatalf("first block missing expected content: %q", got[0].Code)
		}
		if got[1].Lang != "promql" {
			t.Fatalf("second block: expected lang=promql, got %q", got[1].Lang)
		}
	})

	t.Run("missing section returns empty slice", func(t *testing.T) {
		md := []byte(`# Title

## Some other heading

Content with no quick diagnostics section.
`)
		got := extractQuickDiagnostics(md)
		if len(got) != 0 {
			t.Fatalf("expected empty slice, got %d blocks", len(got))
		}
	})

	t.Run("limit caps at three blocks even if more are present", func(t *testing.T) {
		md := []byte("## Quick diagnostics\n\n" +
			"```bash\nfirst\n```\n\n" +
			"```bash\nsecond\n```\n\n" +
			"```bash\nthird\n```\n\n" +
			"```bash\nfourth\n```\n\n" +
			"```bash\nfifth\n```\n")
		got := extractQuickDiagnostics(md)
		if len(got) != 3 {
			t.Fatalf("expected 3 blocks, got %d", len(got))
		}
		if got[2].Code != "third" {
			t.Fatalf("third block content wrong: %q", got[2].Code)
		}
	})

	t.Run("next H2 heading ends the section", func(t *testing.T) {
		md := []byte("## Quick diagnostics\n\n" +
			"```bash\nfirst\n```\n\n" +
			"## Severity & urgency\n\n" +
			"```bash\nshould not be picked up\n```\n")
		got := extractQuickDiagnostics(md)
		if len(got) != 1 {
			t.Fatalf("expected 1 block (section ended at next H2), got %d", len(got))
		}
		if got[0].Code != "first" {
			t.Fatalf("block content wrong: %q", got[0].Code)
		}
	})

	t.Run("language hint is captured", func(t *testing.T) {
		md := []byte("## Quick diagnostics\n\n" +
			"```promql\nrate(foo[5m])\n```\n")
		got := extractQuickDiagnostics(md)
		if len(got) != 1 {
			t.Fatalf("expected 1 block, got %d", len(got))
		}
		if got[0].Lang != "promql" {
			t.Fatalf("expected lang=promql, got %q", got[0].Lang)
		}
	})

	t.Run("multiline code content preserves newlines", func(t *testing.T) {
		md := []byte("## Quick diagnostics\n\n" +
			"```sql\nSELECT *\nFROM pg_stat_statements\nLIMIT 5;\n```\n")
		got := extractQuickDiagnostics(md)
		if len(got) != 1 {
			t.Fatalf("expected 1 block, got %d", len(got))
		}
		expected := "SELECT *\nFROM pg_stat_statements\nLIMIT 5;"
		if got[0].Code != expected {
			t.Fatalf("multiline code wrong:\nexpected: %q\nactual:   %q", expected, got[0].Code)
		}
	})
}

// TestSubstituteLabelPlaceholders pins the placeholder-to-template
// rewrite. The contract is: known labels become AM Go-template
// directives; unknown ones pass through (an angle-bracketed token
// in a shell or SQL command is far more likely to be content than
// an intentional placeholder).
func TestSubstituteLabelPlaceholders(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"single known label", "host=<instance>", "host={{ .Labels.instance }}"},
		{"multiple known labels", "kubectl logs -n <namespace> <pod>", "kubectl logs -n {{ .Labels.namespace }} {{ .Labels.pod }}"},
		{"unknown label passes through", "<unknown>", "<unknown>"},
		{"mixed known and unknown", "<instance> <unknown>", "{{ .Labels.instance }} <unknown>"},
		{"uppercase is not a placeholder", "<INSTANCE>", "<INSTANCE>"},
		{"angle brackets in shell expression untouched", "if [ $x -lt 5 ]; then echo '<5'; fi", "if [ $x -lt 5 ]; then echo '<5'; fi"},
		{"empty input", "", ""},
		{"no placeholders", "psql -c 'SELECT 1'", "psql -c 'SELECT 1'"},
		{"shell comments left alone", "# <namespace> is the alert's namespace\nkubectl get pod -n <namespace>", "# <namespace> is the alert's namespace\nkubectl get pod -n {{ .Labels.namespace }}"},
		{"SQL comments left alone", "-- <instance> comes from the alert\nSELECT * FROM pg_stat_activity WHERE host = '<instance>';", "-- <instance> comes from the alert\nSELECT * FROM pg_stat_activity WHERE host = '{{ .Labels.instance }}';"},
		{"indented comment still skipped", "   # <pod> here\nkubectl logs <pod>", "   # <pod> here\nkubectl logs {{ .Labels.pod }}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := substituteLabelPlaceholders(tc.in)
			if got != tc.want {
				t.Fatalf("substitute: expected %q, got %q", tc.want, got)
			}
		})
	}
}

// TestFormatQuickDiagnosticsForAlertWithPlaceholders confirms the
// substitution lands in the rendered diagnostics block — guards
// against a refactor that wires the parser without wiring the
// substitution.
func TestFormatQuickDiagnosticsForAlertWithPlaceholders(t *testing.T) {
	blocks := []quickDiagnostic{
		{Lang: "bash", Code: "psql host=<instance> -c 'SELECT 1;'"},
	}
	got := formatQuickDiagnosticsForAlert(blocks)
	if !strings.Contains(got, "{{ .Labels.instance }}") {
		t.Fatalf("expected AM template directive in output, got: %s", got)
	}
	if strings.Contains(got, "<instance>") {
		t.Fatalf("expected <instance> to be replaced, but it's still present: %s", got)
	}
}

// TestFormatQuickDiagnosticsForAlert pins the chat-output rendering
// so a future template tweak doesn't silently change the on-call
// experience.
func TestFormatQuickDiagnosticsForAlert(t *testing.T) {
	t.Run("empty input returns empty string", func(t *testing.T) {
		if got := formatQuickDiagnosticsForAlert(nil); got != "" {
			t.Fatalf("expected empty string for nil input, got %q", got)
		}
	})

	t.Run("blocks render as numbered fenced code blocks", func(t *testing.T) {
		blocks := []quickDiagnostic{
			{Lang: "bash", Code: "kubectl top pods"},
			{Lang: "promql", Code: "rate(x[5m])"},
		}
		got := formatQuickDiagnosticsForAlert(blocks)
		// Header present
		if !strings.HasPrefix(got, "**Quick diagnostics:**") {
			t.Fatalf("missing header in output: %q", got)
		}
		// Each block numbered and fenced
		if !strings.Contains(got, "1.\n```bash\nkubectl top pods\n```") {
			t.Fatalf("first block not formatted correctly: %q", got)
		}
		if !strings.Contains(got, "2.\n```promql\nrate(x[5m])\n```") {
			t.Fatalf("second block not formatted correctly: %q", got)
		}
	})
}

// TestRenderReceiverYAML_WithDiagnostics confirms the full integration:
// loading a real runbook's diagnostics + substituting into the YAML
// template produces output that includes the diagnostic commands and
// remains valid as YAML structure.
func TestRenderReceiverYAML_WithDiagnostics(t *testing.T) {
	out := renderReceiverYAML(
		"high-cpu-usage--alerts",
		"https://mm.example/hooks/abc",
		"alerts",
		"https://mm.example/runbooks/high-cpu-usage.html",
		"https://mm.example/icon.png",
	)

	// The diagnostics block from the runbook should be embedded.
	if !strings.Contains(out, "**Quick diagnostics:**") {
		t.Fatalf("rendered YAML missing diagnostics header.\nOutput:\n%s", out)
	}
	if !strings.Contains(out, "kubectl top pods") {
		t.Fatalf("rendered YAML missing expected diagnostic command.\nOutput:\n%s", out)
	}

	// Indentation: every non-first line of the diagnostics block
	// must start at column 9 (8 spaces) to align with the YAML
	// literal block.
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "kubectl") || strings.HasPrefix(line, "**Quick diagnostics") || strings.HasPrefix(line, "1.") {
			t.Fatalf("line lacks YAML block indent — would break YAML parse:\n%q", line)
		}
	}
}

// TestRenderReceiverYAML_NoRunbook handles the case where the receiver
// name doesn't map to a known runbook slug (legacy entries created
// before the channel-suffix pattern, or hand-edited names). The
// renderer must still produce valid YAML with no diagnostics block.
func TestRenderReceiverYAML_NoRunbook(t *testing.T) {
	out := renderReceiverYAML(
		"nonexistent-runbook--alerts",
		"https://mm.example/hooks/abc",
		"alerts",
		"https://mm.example/runbooks/nonexistent-runbook.html",
		"https://mm.example/icon.png",
	)

	if strings.Contains(out, "**Quick diagnostics:**") {
		t.Fatalf("expected no diagnostics block for unknown runbook, got one")
	}
	// Sanity: the rest of the template still rendered.
	if !strings.Contains(out, "name: nonexistent-runbook--alerts") {
		t.Fatalf("baseline template rendering broken:\n%s", out)
	}
}
