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
