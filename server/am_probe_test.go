package main

import (
	"reflect"
	"testing"
)

// TestExtractAMReceiverNames pins the regex against shapes that
// actually appear in Alertmanager's loaded config body. Slack_configs
// and route entries use different leading keys (api_url, matchers)
// and shouldn't match — we want only the top-level receivers list.
func TestExtractAMReceiverNames(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "two receivers, no other content",
			in: `receivers:
  - name: foo
    slack_configs:
      - api_url: 'http://x'
  - name: bar
    pagerduty_configs:
      - service_key: abc
`,
			want: []string{"bar", "foo"},
		},
		{
			name: "route entries with - matchers: are not picked up",
			in: `route:
  receiver: foo
  routes:
    - matchers: [runbook="x"]
      receiver: foo
    - matchers: [runbook="y"]
      receiver: bar
receivers:
  - name: foo
    slack_configs: []
  - name: bar
    slack_configs: []
`,
			want: []string{"bar", "foo"},
		},
		{
			name: "slack_configs entries (starting with - api_url:) are not picked up",
			in: `receivers:
  - name: webhook-receiver
    slack_configs:
      - api_url: 'http://mm/hooks/abc'
      - api_url: 'http://mm/hooks/def'
`,
			want: []string{"webhook-receiver"},
		},
		{
			name: "quoted names get unquoted",
			in: `receivers:
  - name: "needs-quoting"
  - name: 'single-quoted'
  - name: plain
`,
			want: []string{"needs-quoting", "plain", "single-quoted"},
		},
		{
			name: "empty input returns empty slice",
			in:   ``,
			want: nil,
		},
		{
			name: "deduplicates if the same name appears twice (config bug)",
			in: `receivers:
  - name: foo
  - name: foo
  - name: bar
`,
			want: []string{"bar", "foo"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractAMReceiverNames(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

// TestCanonicalReceiverName pins the Prometheus Operator prefix-stripping:
// "<namespace>/<alertmanagerconfig-name>/<receiver>" collapses to the bare
// receiver name, while flat names pass through untouched.
func TestCanonicalReceiverName(t *testing.T) {
	cases := []struct {
		name            string
		in              string
		wantName        string
		wantViaOperator bool
	}{
		{"flat name", "high-cpu--ops-alerts", "high-cpu--ops-alerts", false},
		{"operator-prefixed", "monitoring/mm-alertmanager/high-cpu--ops-alerts", "high-cpu--ops-alerts", true},
		{"receiver slug contains no slash", "watchdog", "watchdog", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotVia := canonicalReceiverName(tc.in)
			if gotName != tc.wantName || gotVia != tc.wantViaOperator {
				t.Fatalf("canonicalReceiverName(%q) = (%q,%v), want (%q,%v)",
					tc.in, gotName, gotVia, tc.wantName, tc.wantViaOperator)
			}
		})
	}
}

// TestLoadedInAMCanonical is the core CRD fix: a receiver the operator renamed
// to "<ns>/<config>/<name>" must still report loaded, flagged viaOperator, so
// the inventory badge shows "OK · via operator" instead of "Not in AM YAML".
func TestLoadedInAMCanonical(t *testing.T) {
	// One flat receiver + two CRD-managed (operator-prefixed) receivers.
	body := `receivers:
  - name: flat-receiver--ops-alerts
  - name: monitoring/mm-alertmanager/high-cpu--ops-alerts
  - name: monitoring/mm-alertmanager/high-mem--ops-alerts
`
	entry := amReachabilityEntry{Reachable: true, ConfigBody: body, receivers: indexAMReceivers(body)}

	cases := []struct {
		name            string
		receiver        string
		wantLoaded      bool
		wantViaOperator bool
	}{
		{"flat receiver loads without operator flag", "flat-receiver--ops-alerts", true, false},
		{"crd receiver loads canonically, flagged via operator", "high-cpu--ops-alerts", true, true},
		{"other crd receiver", "high-mem--ops-alerts", true, true},
		{"absent receiver", "does-not-exist", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loaded, viaOperator := entry.LoadedInAM(tc.receiver)
			if loaded != tc.wantLoaded || viaOperator != tc.wantViaOperator {
				t.Fatalf("LoadedInAM(%q) = (%v,%v), want (%v,%v)",
					tc.receiver, loaded, viaOperator, tc.wantLoaded, tc.wantViaOperator)
			}
		})
	}
}

// TestIndexAMReceiversFlatWinsOverPrefixed ensures a receiver present in BOTH
// flat and operator-prefixed form is not mislabelled "via operator".
func TestIndexAMReceiversFlatWinsOverPrefixed(t *testing.T) {
	body := `receivers:
  - name: dup--ops-alerts
  - name: monitoring/mm-alertmanager/dup--ops-alerts
`
	entry := amReachabilityEntry{receivers: indexAMReceivers(body)}
	loaded, viaOperator := entry.LoadedInAM("dup--ops-alerts")
	if !loaded || viaOperator {
		t.Fatalf("LoadedInAM(dup) = (%v,%v), want (true,false)", loaded, viaOperator)
	}
}

// TestLoadedInAMNilIndex guards the failed-probe path (no config parsed).
func TestLoadedInAMNilIndex(t *testing.T) {
	entry := amReachabilityEntry{Reachable: false}
	if loaded, viaOperator := entry.LoadedInAM("anything"); loaded || viaOperator {
		t.Fatalf("nil index must report (false,false), got (%v,%v)", loaded, viaOperator)
	}
}
