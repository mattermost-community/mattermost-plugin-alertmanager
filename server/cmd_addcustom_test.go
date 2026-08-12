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
