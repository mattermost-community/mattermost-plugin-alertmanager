package main

import (
	"strings"
	"testing"

	pmodel "github.com/prometheus/common/model"

	amconfig "github.com/prometheus/alertmanager/config"
	"github.com/prometheus/alertmanager/dispatch"
)

// TestParseSimulateLabels pins the input contract for `--simulate
// <key>=<value>` args. Operators typing at 3am benefit from clear
// error messages on bad input — each case below should produce a
// specific, actionable error or a clean label set.
func TestParseSimulateLabels(t *testing.T) {
	t.Run("empty input is an error", func(t *testing.T) {
		_, err := parseSimulateLabels(nil)
		if err == nil {
			t.Fatal("expected error for empty input")
		}
	})

	t.Run("single key=value pair parses", func(t *testing.T) {
		ls, err := parseSimulateLabels([]string{"runbook=high-cpu-usage"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := string(ls["runbook"]); got != "high-cpu-usage" {
			t.Fatalf("expected runbook=high-cpu-usage, got runbook=%q", got)
		}
	})

	t.Run("multiple labels parse", func(t *testing.T) {
		ls, err := parseSimulateLabels([]string{
			"runbook=high-cpu-usage",
			"severity=critical",
			"namespace=monitoring",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(ls) != 3 {
			t.Fatalf("expected 3 labels, got %d", len(ls))
		}
	})

	t.Run("missing equals sign is rejected", func(t *testing.T) {
		_, err := parseSimulateLabels([]string{"no-equals-here"})
		if err == nil {
			t.Fatal("expected error for arg without =")
		}
		if !strings.Contains(err.Error(), "no-equals-here") {
			t.Fatalf("error should name the bad arg, got: %v", err)
		}
	})

	t.Run("empty key is rejected", func(t *testing.T) {
		_, err := parseSimulateLabels([]string{"=value"})
		if err == nil {
			t.Fatal("expected error for empty key")
		}
	})

	t.Run("empty value is rejected", func(t *testing.T) {
		// Trailing `=` with nothing after.
		_, err := parseSimulateLabels([]string{"key="})
		if err == nil {
			t.Fatal("expected error for empty value")
		}
	})

	t.Run("values can contain = signs (only first = splits)", func(t *testing.T) {
		// A real Prometheus label value can include =, especially in
		// strings rendered from PromQL output. Split on the FIRST =.
		ls, err := parseSimulateLabels([]string{"query=a=b=c"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := string(ls["query"]); got != "a=b=c" {
			t.Fatalf("expected value a=b=c, got %q", got)
		}
	})
}

// TestSimulationAgainstSampleConfig drives the dispatch.NewRoute
// path end-to-end against a realistic AM config: alerts with the
// `runbook=high-cpu-usage` label should land on the matching
// receiver; alerts without should hit the default.
//
// Guards against future regressions in the dispatch wrapper — if
// the upstream library changes its Match semantics or the route
// tree construction, this test catches it before runtime.
func TestSimulationAgainstSampleConfig(t *testing.T) {
	sampleYAML := `route:
  receiver: catchall
  routes:
    - matchers: [runbook="high-cpu-usage"]
      receiver: high-cpu-usage--alerts
      continue: true
    - matchers: [runbook="high-http-error-rate"]
      receiver: high-http-error-rate--alerts
      continue: true

receivers:
  - name: catchall
  - name: high-cpu-usage--alerts
  - name: high-http-error-rate--alerts
`
	cfg, err := amconfig.Load(sampleYAML)
	if err != nil {
		t.Fatalf("sample config failed to parse: %v", err)
	}
	mainRoute := dispatch.NewRoute(cfg.Route, nil)

	t.Run("alert with known runbook label routes to specific receiver", func(t *testing.T) {
		labels := pmodel.LabelSet{"runbook": "high-cpu-usage", "severity": "warning"}
		matches := mainRoute.Match(labels)
		if len(matches) == 0 {
			t.Fatal("expected at least one match")
		}
		found := false
		for _, m := range matches {
			if m.RouteOpts.Receiver == "high-cpu-usage--alerts" {
				found = true
				break
			}
		}
		if !found {
			var names []string
			for _, m := range matches {
				names = append(names, m.RouteOpts.Receiver)
			}
			t.Fatalf("expected high-cpu-usage--alerts in matches, got: %v", names)
		}
	})

	t.Run("alert without runbook label falls to catchall via no sub-route match", func(t *testing.T) {
		labels := pmodel.LabelSet{"alertname": "FooBar", "severity": "warning"}
		matches := mainRoute.Match(labels)
		// When no sub-routes match, the AM dispatch logic returns
		// either an empty slice OR the root route. Either way the
		// effective receiver is the root's receiver ("catchall").
		// Our handler treats no-matches as "would fall to default."
		for _, m := range matches {
			if m.RouteOpts.Receiver != "catchall" {
				t.Fatalf("expected only catchall in matches when no sub-route applies, got %q", m.RouteOpts.Receiver)
			}
		}
	})
}

// TestExpandSeverityForFire pins the contract for the --severity flag:
// what each accepted value expands to, which strings are invalid, and
// what the `all` matrix looks like.
func TestExpandSeverityForFire(t *testing.T) {
	t.Run("empty defaults to warning (backwards compat)", func(t *testing.T) {
		got := expandSeverityForFire("")
		if len(got) != 1 || got[0].Severity != "warning" || got[0].Resolved {
			t.Fatalf("expected [warning,firing], got %+v", got)
		}
	})

	t.Run("warning is single firing", func(t *testing.T) {
		got := expandSeverityForFire("warning")
		if len(got) != 1 || got[0].Severity != "warning" || got[0].Resolved {
			t.Fatalf("expected [warning,firing], got %+v", got)
		}
	})

	t.Run("critical is single firing", func(t *testing.T) {
		got := expandSeverityForFire("critical")
		if len(got) != 1 || got[0].Severity != "critical" || got[0].Resolved {
			t.Fatalf("expected [critical,firing], got %+v", got)
		}
	})

	t.Run("info is single firing", func(t *testing.T) {
		got := expandSeverityForFire("info")
		if len(got) != 1 || got[0].Severity != "info" || got[0].Resolved {
			t.Fatalf("expected [info,firing], got %+v", got)
		}
	})

	t.Run("all expands to 4 specs in order", func(t *testing.T) {
		got := expandSeverityForFire("all")
		if len(got) != 4 {
			t.Fatalf("expected 4 specs, got %d (%+v)", len(got), got)
		}
		// Order matters — firing severities ascending then resolved.
		// Caller relies on this for the chat-history reading order.
		want := []syntheticFireSpec{
			{Severity: "warning", Resolved: false},
			{Severity: "critical", Resolved: false},
			{Severity: "info", Resolved: false},
			{Severity: "info", Resolved: true},
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("spec[%d] = %+v, want %+v", i, got[i], want[i])
			}
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		gotAll := expandSeverityForFire("ALL")
		gotCritical := expandSeverityForFire("Critical")
		if len(gotAll) != 4 {
			t.Errorf("ALL should expand same as all, got %d specs", len(gotAll))
		}
		if len(gotCritical) != 1 || gotCritical[0].Severity != "critical" {
			t.Errorf("Critical should normalize to critical, got %+v", gotCritical)
		}
	})

	t.Run("whitespace trimmed", func(t *testing.T) {
		got := expandSeverityForFire("  warning  ")
		if len(got) != 1 || got[0].Severity != "warning" {
			t.Errorf("expected [warning], got %+v", got)
		}
	})

	t.Run("unknown value returns nil so caller surfaces error", func(t *testing.T) {
		got := expandSeverityForFire("emergency")
		if got != nil {
			t.Errorf("expected nil for unknown, got %+v", got)
		}
	})
}
