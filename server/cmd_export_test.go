package main

import (
	"strings"
	"testing"
)

// TestRedactOtherChannelsInDiff pins the rule that ONLY context
// lines from receivers NOT in the calling channel get redacted.
// Own-channel receivers and addition lines (`+ ` prefix) stay
// un-redacted so the operator can copy-paste their additions.
func TestRedactOtherChannelsInDiff(t *testing.T) {
	t.Run("other channel's api_url and password get redacted", func(t *testing.T) {
		diff := `  receivers:
    - name: other-team-receiver
      slack_configs:
        - api_url: 'http://mm.example.com/hooks/secret123'
          password: 'plaintext'
    - name: own-channel-receiver
      slack_configs:
        - api_url: 'http://mm.example.com/hooks/abc'
`
		own := map[string]bool{"own-channel-receiver": true}
		got := redactOtherChannelsInDiff(diff, own)

		// Other channel's URL + password redacted
		if !strings.Contains(got, "<REDACTED>") {
			t.Fatalf("expected redaction marker in output, got:\n%s", got)
		}
		if strings.Contains(got, "hooks/secret123") {
			t.Fatalf("other channel's webhook URL leaked through:\n%s", got)
		}
		if strings.Contains(got, "plaintext") {
			t.Fatalf("other channel's password leaked through:\n%s", got)
		}
		// Own receiver's URL preserved
		if !strings.Contains(got, "hooks/abc") {
			t.Fatalf("own channel's webhook URL should be preserved but is missing:\n%s", got)
		}
	})

	t.Run("addition lines (`+ ` prefix) never redacted", func(t *testing.T) {
		diff := `+ - name: own-addition
+   slack_configs:
+     - api_url: 'http://mm.example.com/hooks/my-new-url'
`
		got := redactOtherChannelsInDiff(diff, nil)
		if !strings.Contains(got, "hooks/my-new-url") {
			t.Fatalf("addition URL should never be redacted, got:\n%s", got)
		}
	})

	t.Run("global-level secrets are redacted; non-secret global passes through (F-004)", func(t *testing.T) {
		diff := `  global:
    smtp_from: alerts@example.com
    smtp_auth_password: 'global-pw-leak'
    slack_api_url: 'https://hooks.slack.com/services/global-secret'
    victorops_api_key: 'vo-key-leak'
  receivers:
    - name: webhook-other
      webhook_configs:
        - url: 'https://internal/hook-leak'
      msteams_configs:
        - authorization:
            credentials: 'bearer-leak'
      slack_configs:
        - http_config:
            oauth2:
              client_secret: 'oauth-leak'
  route:
    receiver: default
`
		got := redactOtherChannelsInDiff(diff, nil)
		for _, secret := range []string{"global-pw-leak", "global-secret", "vo-key-leak", "hook-leak", "bearer-leak", "oauth-leak"} {
			if strings.Contains(got, secret) {
				t.Fatalf("secret %q leaked through the diff (F-004):\n%s", secret, got)
			}
		}
		// Non-secret global content and route structure still visible.
		if !strings.Contains(got, "alerts@example.com") {
			t.Fatalf("non-secret global content should pass through:\n%s", got)
		}
		if !strings.Contains(got, "receiver: default") {
			t.Fatalf("route structure should pass through:\n%s", got)
		}
	})

	t.Run("proxy_url credentials are redacted (F-004)", func(t *testing.T) {
		diff := `  receivers:
    - name: other-team
      slack_configs:
        - http_config:
            proxy_url: 'http://proxyuser:proxypass@proxy.example.com:8080'
`
		got := redactOtherChannelsInDiff(diff, nil)
		if strings.Contains(got, "proxypass") || strings.Contains(got, "proxyuser") {
			t.Fatalf("proxy_url credentials leaked:\n%s", got)
		}
	})

	t.Run("block-scalar secrets incl. continuation lines are redacted (F-003)", func(t *testing.T) {
		diff := `  receivers:
    - name: other-team
      webhook_configs:
        - url: |
            https://mattermost.example.com/hooks/block-scalar-secret
          send_resolved: true
`
		got := redactOtherChannelsInDiff(diff, nil)
		if strings.Contains(got, "block-scalar-secret") {
			t.Fatalf("block-scalar secret leaked on a continuation line:\n%s", got)
		}
		if !strings.Contains(got, "send_resolved") {
			t.Fatalf("sibling key after the block scalar was over-redacted (block not bounded by key column):\n%s", got)
		}
	})

	t.Run("vendor tokens (service_key, routing_key) also redacted", func(t *testing.T) {
		diff := `  receivers:
    - name: pagerduty-other-team
      pagerduty_configs:
        - service_key: 'pd-secret-abc'
          routing_key: 'pd-routing-xyz'
`
		got := redactOtherChannelsInDiff(diff, nil)
		if strings.Contains(got, "pd-secret-abc") {
			t.Fatalf("service_key leaked, got:\n%s", got)
		}
		if strings.Contains(got, "pd-routing-xyz") {
			t.Fatalf("routing_key leaked, got:\n%s", got)
		}
	})

	t.Run("flow-style YAML secrets are redacted", func(t *testing.T) {
		// Alertmanager accepts flow-style ({ } / [ ]); the line-anchored block-style
		// regex can't see keys that aren't at the start of the line. All three secrets
		// below sit inside flow collections and must still be masked.
		diff := `  receivers:
    - name: other-team
      webhook_configs: [{url: "https://mm.example/hooks/flow-secret", send_resolved: true}]
      http_config: {proxy_url: "http://fuser:fpass@proxy.example:8080"}
      pagerduty_configs: [{service_key: flow-pd-key}]
`
		got := redactOtherChannelsInDiff(diff, nil)
		for _, secret := range []string{"flow-secret", "fuser", "fpass", "flow-pd-key"} {
			if strings.Contains(got, secret) {
				t.Fatalf("flow-style secret %q leaked through the diff:\n%s", secret, got)
			}
		}
		// Non-secret flow keys on the same line stay visible (only the value is masked).
		if !strings.Contains(got, "send_resolved") {
			t.Fatalf("non-secret flow key over-redacted:\n%s", got)
		}
	})

	t.Run("own-channel flow-style secrets are NOT redacted", func(t *testing.T) {
		// The caller needs their own additions/context intact to paste.
		diff := `  receivers:
    - name: own-team
      webhook_configs: [{url: "https://mm.example/hooks/keep-me"}]
`
		got := redactOtherChannelsInDiff(diff, map[string]bool{"own-team": true})
		if !strings.Contains(got, "keep-me") {
			t.Fatalf("own-channel flow-style URL should be preserved:\n%s", got)
		}
	})
}

// TestValidateMergedConfig pins the contract that schema-invalid
// merged YAML gets rejected by the alertmanager/config library —
// which is what AM itself uses at reload time. If this passes,
// the operator can trust the "Validation: ok" badge in the DM.
func TestValidateMergedConfig(t *testing.T) {
	t.Run("empty input is no-op", func(t *testing.T) {
		if err := validateMergedConfig(""); err != nil {
			t.Fatalf("empty input should return nil, got %v", err)
		}
	})

	t.Run("complete valid config passes", func(t *testing.T) {
		yaml := `route:
  receiver: default
receivers:
  - name: default
`
		if err := validateMergedConfig(yaml); err != nil {
			t.Fatalf("expected valid config to pass, got %v", err)
		}
	})

	t.Run("undefined receiver in route is rejected", func(t *testing.T) {
		yaml := `route:
  receiver: does-not-exist
receivers:
  - name: default
`
		err := validateMergedConfig(yaml)
		if err == nil {
			t.Fatal("expected error for undefined receiver reference, got nil")
		}
		if !strings.Contains(err.Error(), "does-not-exist") && !strings.Contains(err.Error(), "undefined receiver") {
			t.Fatalf("expected error to mention undefined receiver, got: %v", err)
		}
	})

	t.Run("malformed YAML is rejected", func(t *testing.T) {
		yaml := `route:
  receiver: default
  routes:
    - not a valid route block
`
		if err := validateMergedConfig(yaml); err == nil {
			t.Fatal("expected error for malformed YAML, got nil")
		}
	})
}

// TestBuildDiffAgainstLoaded covers the unified-diff-style merger
// for /alertmanager export --diff-against-loaded. The function does
// textual insertion of additions into a captured AM YAML body —
// these tests pin the insertion points so a future regression
// doesn't silently start emitting the additions in the wrong block.
func TestBuildDiffAgainstLoaded(t *testing.T) {
	t.Run("insert receivers + routes at correct block ends", func(t *testing.T) {
		loaded := `global:
  smtp_from: alerts@example.com

receivers:
  - name: pagerduty-team-1
    pagerduty_configs:
      - service_key: abc
  - name: email-team-2
    email_configs:
      - to: ops@example.com

route:
  receiver: pagerduty-team-1
  routes:
    - matchers: [team="db"]
      receiver: pagerduty-team-1
`
		newRecvs := `- name: high-cpu-usage--alerts
  slack_configs:
    - api_url: 'https://mm.example/hooks/abc'
`
		newRoutes := `- matchers: [runbook="high-cpu-usage"]
  receiver: high-cpu-usage--alerts
  continue: true
`
		diff, _ := buildDiffAgainstLoaded(loaded, newRecvs, newRoutes)

		// Both addition markers should appear
		if !strings.Contains(diff, "+ # ---- plugin additions: receivers ----") {
			t.Fatalf("receivers addition marker missing.\nOutput:\n%s", diff)
		}
		if !strings.Contains(diff, "+ # ---- plugin additions: routes ----") {
			t.Fatalf("routes addition marker missing.\nOutput:\n%s", diff)
		}
		// Added lines carry the + prefix
		if !strings.Contains(diff, "+ - name: high-cpu-usage--alerts") {
			t.Fatalf("added receiver line missing + prefix.\nOutput:\n%s", diff)
		}
		// Context lines carry the 2-space prefix
		if !strings.Contains(diff, "  receivers:") {
			t.Fatalf("context line for receivers: missing 2-space prefix.\nOutput:\n%s", diff)
		}
	})

	t.Run("receivers insertion lands before next top-level key", func(t *testing.T) {
		// The receivers: block runs from line 4 to the route: line.
		// The insertion should be on the line that starts `route:`,
		// pushing route: down by N lines.
		loaded := `receivers:
  - name: existing
route:
  receiver: existing
`
		newRecvs := `- name: added-receiver
`
		diff, _ := buildDiffAgainstLoaded(loaded, newRecvs, "")

		// Find the marker and confirm it appears before the route: context line.
		markerIdx := strings.Index(diff, "+ # ---- plugin additions: receivers ----")
		routeIdx := strings.Index(diff, "  route:")
		if markerIdx == -1 {
			t.Fatalf("marker missing")
		}
		if routeIdx == -1 {
			t.Fatalf("route: context line missing")
		}
		if markerIdx > routeIdx {
			t.Fatalf("receivers addition marker (%d) should appear before route: (%d)", markerIdx, routeIdx)
		}
	})

	t.Run("receivers at EOF (no following top-level key)", func(t *testing.T) {
		loaded := `route:
  receiver: existing
receivers:
  - name: existing
`
		newRecvs := `- name: added
`
		diff, _ := buildDiffAgainstLoaded(loaded, newRecvs, "")

		if !strings.Contains(diff, "+ - name: added") {
			t.Fatalf("addition missing from EOF-receivers case.\nOutput:\n%s", diff)
		}
	})

	t.Run("missing receivers: block falls back to manual-merge note", func(t *testing.T) {
		// Unusual but possible: an AM YAML where the only receiver
		// definition is under a custom key, or a partial config.
		loaded := `global:
  smtp_from: alerts@example.com
`
		newRecvs := `- name: orphan
`
		diff, _ := buildDiffAgainstLoaded(loaded, newRecvs, "")
		if !strings.Contains(diff, "couldn't find `receivers:` block") {
			t.Fatalf("fallback note missing for receivers-less YAML.\nOutput:\n%s", diff)
		}
	})

	t.Run("no additions returns the loaded YAML in context form", func(t *testing.T) {
		loaded := `receivers:
  - name: existing
`
		diff, _ := buildDiffAgainstLoaded(loaded, "", "")
		// Should have no + lines.
		for line := range strings.SplitSeq(diff, "\n") {
			if strings.HasPrefix(line, "+ ") {
				t.Fatalf("expected no additions, found: %q", line)
			}
		}
	})
}
