package main

import (
	"strings"
	"testing"
)

func TestValidateCustomReceiverName(t *testing.T) {
	// Pull a real runbook slug and a real category-set name at runtime so the
	// collision cases don't hardcode catalog specifics.
	var runbookSlug string
	if s := runbookSlugs(); len(s) > 0 {
		runbookSlug = s[0]
	}
	var categoryName string
	for k := range scaffoldSets {
		categoryName = k
		break
	}

	cases := []struct {
		name     string
		raw      string
		wantErr  bool
		wantFull string // only checked when wantErr is false
	}{
		{"valid", "slo-burn-rate", false, "slo-burn-rate--ops-alerts"},
		{"lowercased", "SLO-Burn-Rate", false, "slo-burn-rate--ops-alerts"},
		{"empty", "", true, ""},
		{"whitespace only", "   ", true, ""},
		{"contains double dash", "foo--bar", true, ""},
		{"too long", strings.Repeat("a", 200), true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			full, err := validateCustomReceiverName(tc.raw, "ops", "alerts")
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateCustomReceiverName(%q) err = %v, wantErr = %v", tc.raw, err, tc.wantErr)
			}
			if !tc.wantErr && full != tc.wantFull {
				t.Fatalf("validateCustomReceiverName(%q) full = %q, want %q", tc.raw, full, tc.wantFull)
			}
		})
	}

	if runbookSlug != "" {
		t.Run("rejects a runbook slug", func(t *testing.T) {
			if _, err := validateCustomReceiverName(runbookSlug, "ops", "alerts"); err == nil {
				t.Fatalf("expected error for runbook slug %q, got nil", runbookSlug)
			}
		})
	}
	if categoryName != "" {
		t.Run("rejects a category set name", func(t *testing.T) {
			if _, err := validateCustomReceiverName(categoryName, "ops", "alerts"); err == nil {
				t.Fatalf("expected error for category %q, got nil", categoryName)
			}
		})
	}
}

func TestAssembleRoutesYAMLCustom(t *testing.T) {
	const runbookRecv = "high-cpu-usage--ops-alerts"
	const customRecv = "slo-burn-rate--ops-alerts"

	t.Run("custom-only emits a commented stub and no live routes: key", func(t *testing.T) {
		out := assembleRoutesYAML([]alertConfig{{Name: customRecv, Custom: true}})
		if !strings.Contains(out, "# Custom (non-runbook) receivers") {
			t.Fatalf("expected custom stub header, got:\n%s", out)
		}
		if !strings.Contains(out, "#    receiver: "+customRecv) {
			t.Fatalf("expected commented custom receiver line, got:\n%s", out)
		}
		if strings.Contains(out, "\nroutes:\n") {
			t.Fatalf("custom-only output must not emit a bare `routes:` key, got:\n%s", out)
		}
		if strings.Contains(out, "[runbook=") {
			t.Fatalf("custom receiver must not get a live runbook matcher, got:\n%s", out)
		}
	})

	t.Run("mixed emits live runbook route plus commented custom stub", func(t *testing.T) {
		out := assembleRoutesYAML([]alertConfig{
			{Name: runbookRecv},
			{Name: customRecv, Custom: true},
		})
		if !strings.Contains(out, "\nroutes:\n") {
			t.Fatalf("expected live routes: key, got:\n%s", out)
		}
		if !strings.Contains(out, `[runbook="high-cpu-usage"]`) || !strings.Contains(out, "    receiver: "+runbookRecv) {
			t.Fatalf("expected live runbook route, got:\n%s", out)
		}
		if !strings.Contains(out, "#    receiver: "+customRecv) {
			t.Fatalf("expected commented custom stub, got:\n%s", out)
		}
	})

	t.Run("runbook-only is unchanged (no custom section)", func(t *testing.T) {
		out := assembleRoutesYAML([]alertConfig{{Name: runbookRecv}})
		if strings.Contains(out, "# Custom (non-runbook) receivers") {
			t.Fatalf("runbook-only output must not include a custom section, got:\n%s", out)
		}
		if !strings.Contains(out, "    receiver: "+runbookRecv) {
			t.Fatalf("expected live runbook route, got:\n%s", out)
		}
	})
}

// TestRenderReceiverYAMLForKindCustom is C2: a custom receiver must NOT render
// the plugin runbook fallback link (which 404s), while a standard receiver still
// does. The annotation-based runbook line is preserved for both.
func TestRenderReceiverYAMLForKindCustom(t *testing.T) {
	const fallbackURL = "https://mm.example.com/plugins/x/public/runbooks/slo-burn-rate.html"

	standard := renderReceiverYAMLForKind("high-cpu-usage--ops-alerts", "http://wh", "alerts",
		"https://mm.example.com/plugins/x/public/runbooks/high-cpu-usage.html", "http://icon", false)
	if !strings.Contains(standard, "public/runbooks/high-cpu-usage.html") {
		t.Fatalf("standard receiver must include the runbook fallback link, got:\n%s", standard)
	}

	custom := renderReceiverYAMLForKind("slo-burn-rate--ops-alerts", "http://wh", "alerts",
		fallbackURL, "http://icon", true)
	if strings.Contains(custom, fallbackURL) {
		t.Fatalf("custom receiver must NOT include the runbook fallback link, got:\n%s", custom)
	}
	// The plugin-side placeholders must all be resolved (no leftover tokens).
	if strings.Contains(custom, "{{RUNBOOK") {
		t.Fatalf("custom render left an unresolved runbook placeholder:\n%s", custom)
	}
	// Alert-time runbook_url annotation support is retained (the AM conditional).
	if !strings.Contains(custom, ".Annotations.runbook_url") {
		t.Fatalf("custom render should still honor an alert's runbook_url annotation:\n%s", custom)
	}
}

// TestRenderAlertmanagerConfigCustom is C4: the CRD export must not auto-generate
// a runbook matcher for a custom receiver, must keep it out of the parent gate,
// but must still emit the receiver + a commented stub.
func TestRenderAlertmanagerConfigCustom(t *testing.T) {
	specs := []crdReceiverSpec{
		{name: "alerts-fallback", channel: "alerts", secretName: "s"}, // slug=="" fallback
		{slug: "slo-burn-rate", name: "slo-burn-rate--ops-alerts", channel: "alerts", secretName: "s", custom: true},
		{
			slug: "high-cpu-usage", name: "high-cpu-usage--ops-alerts", channel: "alerts", secretName: "s",
			runbookDefaultURL: "https://mm.example.com/rb/high-cpu-usage.html",
		},
	}
	out := renderAlertmanagerConfig("cr", "monitoring", "alerts-fallback", specs)

	// The standard receiver still gets its live runbook matcher.
	if !strings.Contains(out, `{name: runbook, value: "high-cpu-usage", matchType: "="}`) {
		t.Fatalf("standard receiver should keep its runbook matcher, got:\n%s", out)
	}
	// The custom receiver must NOT get an auto runbook matcher...
	if strings.Contains(out, `{name: runbook, value: "slo-burn-rate", matchType: "="}`) {
		t.Fatalf("custom receiver must NOT get an auto runbook matcher, got:\n%s", out)
	}
	// ...must be kept out of the parent =~ gate...
	if strings.Contains(out, "slo-burn-rate|") || strings.Contains(out, "|slo-burn-rate") || strings.Contains(out, "^(slo-burn-rate)$") {
		t.Fatalf("custom slug must not appear in the parent matcher, got:\n%s", out)
	}
	// ...but the receiver itself must still be defined, with a commented stub.
	if !strings.Contains(out, "slo-burn-rate--ops-alerts") {
		t.Fatalf("custom receiver must still be emitted, got:\n%s", out)
	}
	if !strings.Contains(out, "#    receiver: slo-burn-rate--ops-alerts") {
		t.Fatalf("expected a commented custom-route stub for the custom receiver, got:\n%s", out)
	}
}

// TestRenderAlertmanagerConfigCustomOnlyIsInert guards the C5 regression: a
// custom-only webhook group (the normal add-custom case) must NOT emit an empty
// "^()$" parent matcher, which Alertmanager would treat as matching every alert
// with no runbook label and route broad traffic into the group's fallback.
func TestRenderAlertmanagerConfigCustomOnlyIsInert(t *testing.T) {
	specs := []crdReceiverSpec{
		{name: "alerts-fallback", channel: "alerts", secretName: "s"}, // slug=="" fallback
		{slug: "slo-burn-rate", name: "slo-burn-rate--ops-alerts", channel: "alerts", secretName: "s", custom: true},
	}
	out := renderAlertmanagerConfig("cr", "monitoring", "alerts-fallback", specs)

	if strings.Contains(out, `value: "^()$"`) {
		t.Fatalf("custom-only group must not emit an empty ^()$ parent matcher (captures all no-runbook alerts):\n%s", out)
	}
	if !strings.Contains(out, "__mm_no_runbook_routes__") {
		t.Fatalf("custom-only group should use the never-matching sentinel parent matcher:\n%s", out)
	}
	// spec.route.routes must be an explicit empty array, not null (schema types it
	// as an array — a comment-only body would decode to null and fail validation).
	if !strings.Contains(out, "routes: []") {
		t.Fatalf("custom-only group must emit `routes: []`, not a null routes body:\n%s", out)
	}
}
