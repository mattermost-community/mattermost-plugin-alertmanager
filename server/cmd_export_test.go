package main

import (
	"strings"
	"testing"
)

// TestDetectCRDMode verifies that detectCRDMode recognises Prometheus Operator
// namespaced receiver names (<ns>/<alertmanagerconfig-name>/<receiver-name>)
// and returns false for flat AM configs where receivers use the plugin's
// native naming format.
func TestDetectCRDMode(t *testing.T) {
	t.Run("flat config returns false", func(t *testing.T) {
		loaded := `receivers:
- name: "null"
- name: database-connectivity-loss--team-channel
`
		own := map[string]bool{"database-connectivity-loss--team-channel": true}
		if detectCRDMode(loaded, own) {
			t.Fatal("expected flat mode, got CRD mode")
		}
	})

	t.Run("CRD-namespaced receiver returns true", func(t *testing.T) {
		loaded := `receivers:
- name: "null"
- name: monitoring/mattermost-alertmanager-abc123/database-connectivity-loss--team-channel
`
		own := map[string]bool{"database-connectivity-loss--team-channel": true}
		if !detectCRDMode(loaded, own) {
			t.Fatal("expected CRD mode, got flat mode")
		}
	})

	t.Run("namespaced receiver for a different channel does not trigger CRD mode for this channel", func(t *testing.T) {
		loaded := `receivers:
- name: monitoring/mattermost-alertmanager-abc123/high-cpu-usage--other-channel
- name: database-connectivity-loss--team-channel
`
		// ownNames only has the database receiver — the CRD one belongs to other-channel
		own := map[string]bool{"database-connectivity-loss--team-channel": true}
		if detectCRDMode(loaded, own) {
			t.Fatal("CRD mode should only trigger when THIS channel's receivers have namespaced names")
		}
	})

	t.Run("empty config returns false", func(t *testing.T) {
		if detectCRDMode("", map[string]bool{"foo": true}) {
			t.Fatal("expected false for empty config")
		}
	})
}

// TestReceiverLoadedInAM verifies that receiverLoadedInAM handles both flat
// and CRD-namespaced name matching.
func TestReceiverLoadedInAM(t *testing.T) {
	loaded := `receivers:
- name: "null"
- name: bridge-fallback
- name: monitoring/mattermost-alertmanager-abc123/database-connectivity-loss--team-channel
- name: plain-receiver--team-channel
`

	t.Run("flat name matches directly", func(t *testing.T) {
		if !receiverLoadedInAM(loaded, "plain-receiver--team-channel", false) {
			t.Fatal("expected flat receiver to be found")
		}
	})

	t.Run("flat name not present returns false", func(t *testing.T) {
		if receiverLoadedInAM(loaded, "missing-receiver--team-channel", false) {
			t.Fatal("expected missing receiver to return false")
		}
	})

	t.Run("CRD mode matches by third path segment", func(t *testing.T) {
		if !receiverLoadedInAM(loaded, "database-connectivity-loss--team-channel", true) {
			t.Fatal("expected CRD-namespaced receiver to be found via segment match")
		}
	})

	t.Run("CRD mode without matching segment returns false", func(t *testing.T) {
		if receiverLoadedInAM(loaded, "not-present--team-channel", true) {
			t.Fatal("expected non-matching name to return false in CRD mode")
		}
	})

	t.Run("CRD mode off: namespaced name not matched by flat check", func(t *testing.T) {
		// "database-connectivity-loss--team-channel" appears only as the third
		// segment of a namespaced name — a flat check shouldn't match it.
		if receiverLoadedInAM(loaded, "database-connectivity-loss--team-channel", false) {
			t.Fatal("flat mode should not match via CRD path segment")
		}
	})

	t.Run("shorter channel name does not match a longer receiver", func(t *testing.T) {
		// Substring matching used to treat x--sre-alerts as already loaded
		// when AM only had x--sre-alerts-prod, and the diff dropped the add.
		prefixLoaded := "receivers:\n- name: plain-receiver--team-channel-prod\n"
		if receiverLoadedInAM(prefixLoaded, "plain-receiver--team-channel", false) {
			t.Fatal("prefix of a longer receiver name must not count as loaded")
		}
		if receiverLoadedInAM(prefixLoaded, "plain-receiver--team-channel", true) {
			t.Fatal("CRD mode must not prefix-match a flat longer name")
		}
	})
}

// TestExtractReceiverBlocks verifies that extractReceiverBlocks correctly
// extracts multi-line receiver blocks matching the filter, leaving others out.
func TestExtractReceiverBlocks(t *testing.T) {
	loaded := `global:
  resolve_timeout: 5m
receivers:
- name: "null"
- name: keep-me--team-channel
  slack_configs:
  - api_url: 'https://mm.example/hooks/abc'
    channel: '#alerts'
- name: skip-me--other-channel
  slack_configs:
  - api_url: 'https://mm.example/hooks/xyz'
route:
  receiver: "null"
`

	t.Run("extracts only matching receivers", func(t *testing.T) {
		keep := map[string]bool{"keep-me--team-channel": true}
		out := extractReceiverBlocks(loaded, func(n string) bool { return keep[n] })

		if !strings.Contains(out, "keep-me--team-channel") {
			t.Fatalf("expected keep-me in output:\n%s", out)
		}
		if strings.Contains(out, "skip-me--other-channel") {
			t.Fatalf("skip-me should not appear in output:\n%s", out)
		}
		if strings.Contains(out, `"null"`) {
			t.Fatalf("null receiver should not appear in output:\n%s", out)
		}
		// The full multi-line block should be present
		if !strings.Contains(out, "api_url") {
			t.Fatalf("expected slack_configs block to be included:\n%s", out)
		}
	})

	t.Run("CRD-namespaced receiver extracted via keep function", func(t *testing.T) {
		crdLoaded := `receivers:
- name: monitoring/mattermost-alertmanager-abc123/keep-me--team-channel
  slack_configs:
  - api_url: 'https://mm.example/hooks/abc'
- name: monitoring/mattermost-alertmanager-abc123/skip-me--other-channel
  slack_configs:
  - api_url: 'https://mm.example/hooks/xyz'
`
		own := map[string]bool{"keep-me--team-channel": true}
		out := extractReceiverBlocks(crdLoaded, func(name string) bool {
			if own[name] {
				return true
			}
			parts := strings.SplitN(name, "/", 3)
			return len(parts) == 3 && own[parts[2]]
		})
		if !strings.Contains(out, "keep-me--team-channel") {
			t.Fatalf("expected CRD-namespaced keep-me in output:\n%s", out)
		}
		if strings.Contains(out, "skip-me--other-channel") {
			t.Fatalf("skip-me should not appear:\n%s", out)
		}
	})

	t.Run("no receivers block returns empty", func(t *testing.T) {
		out := extractReceiverBlocks("route:\n  receiver: foo\n", func(string) bool { return true })
		if out != "" {
			t.Fatalf("expected empty for config with no receivers block, got:\n%s", out)
		}
	})

	t.Run("filter matches none returns empty", func(t *testing.T) {
		out := extractReceiverBlocks(loaded, func(string) bool { return false })
		if out != "" {
			t.Fatalf("expected empty when filter matches nothing, got:\n%s", out)
		}
	})
}

// TestExtractRouteItems verifies that extractRouteItems returns only the route
// blocks whose receiver: field belongs to the specified channel.
func TestExtractRouteItems(t *testing.T) {
	loaded := `route:
  receiver: "null"
  routes:
  - matchers: [runbook="database-connectivity-loss"]
    receiver: database-connectivity-loss--team-channel
    continue: true
  - matchers: [alertname="Watchdog"]
    receiver: watchdog
  - matchers: [runbook="connection-pool-saturation"]
    receiver: connection-pool-saturation--team-channel
    continue: true
  group_wait: 30s
receivers:
- name: "null"
`

	own := map[string]bool{
		"database-connectivity-loss--team-channel": true,
		"connection-pool-saturation--team-channel": true,
	}

	t.Run("extracts routes matching channel receivers", func(t *testing.T) {
		items := extractRouteItems(loaded, own, false)
		joined := strings.Join(items, "\n")

		if !strings.Contains(joined, "database-connectivity-loss") {
			t.Fatalf("expected database route in output:\n%s", joined)
		}
		if !strings.Contains(joined, "connection-pool-saturation") {
			t.Fatalf("expected connection-pool route in output:\n%s", joined)
		}
		// Watchdog belongs to a different receiver — must not appear
		if strings.Contains(joined, "watchdog") {
			t.Fatalf("watchdog route should not appear:\n%s", joined)
		}
	})

	t.Run("CRD mode matches namespaced receiver in route", func(t *testing.T) {
		crdLoaded := `route:
  receiver: "null"
  routes:
  - matchers: [runbook="database-connectivity-loss"]
    receiver: monitoring/mattermost-alertmanager-abc123/database-connectivity-loss--team-channel
    continue: true
  - matchers: [alertname="Watchdog"]
    receiver: watchdog
`
		items := extractRouteItems(crdLoaded, own, true)
		joined := strings.Join(items, "\n")
		if !strings.Contains(joined, "database-connectivity-loss") {
			t.Fatalf("expected CRD-namespaced route in output:\n%s", joined)
		}
		if strings.Contains(joined, "watchdog") {
			t.Fatalf("watchdog route should not appear:\n%s", joined)
		}
	})

	t.Run("no route block returns nil", func(t *testing.T) {
		items := extractRouteItems("receivers:\n- name: foo\n", own, false)
		if len(items) > 0 {
			t.Fatalf("expected empty result for config with no route block, got:\n%v", items)
		}
	})
}

// TestBuildChannelScopedDiff verifies that the channel-scoped diff shows only
// this channel's receivers and routes — both existing (context lines) and new
// (+ addition lines).
func TestBuildChannelScopedDiff(t *testing.T) {
	loaded := `global:
  resolve_timeout: 5m
route:
  receiver: "null"
  routes:
  - matchers: [runbook="existing-runbook"]
    receiver: existing-runbook--team-channel
    continue: true
  - matchers: [alertname="Watchdog"]
    receiver: watchdog
  group_wait: 30s
receivers:
- name: "null"
- name: existing-runbook--team-channel
  slack_configs:
  - api_url: 'https://mm.example/hooks/existing'
- name: other-channel-receiver--other-channel
  slack_configs:
  - api_url: 'https://mm.example/hooks/other'
`

	own := map[string]bool{"existing-runbook--team-channel": true}

	t.Run("context lines show existing channel receiver and route", func(t *testing.T) {
		out := buildChannelScopedDiff(loaded, own, false, "", "")

		if !strings.Contains(out, "  - name: existing-runbook--team-channel") {
			t.Fatalf("expected existing receiver as context line:\n%s", out)
		}
		if !strings.Contains(out, "  - matchers:") {
			t.Fatalf("expected existing route as context line:\n%s", out)
		}
		// Other channel's receiver must not appear
		if strings.Contains(out, "other-channel-receiver") {
			t.Fatalf("other channel's receiver should not appear:\n%s", out)
		}
		// Watchdog route belongs to a different receiver — must not appear
		if strings.Contains(out, "watchdog") {
			t.Fatalf("watchdog route (different channel) should not appear:\n%s", out)
		}
	})

	t.Run("additions appear with + prefix", func(t *testing.T) {
		newRecvs := "- name: new-runbook--team-channel\n  slack_configs:\n  - api_url: 'https://mm.example/hooks/new'\n"
		entry := alertConfig{Name: "new-runbook--team-channel"}
		newRoutes := assembleRoutesYAML([]alertConfig{entry})

		out := buildChannelScopedDiff(loaded, own, false, newRecvs, newRoutes)

		if !strings.Contains(out, "+ - name: new-runbook--team-channel") {
			t.Fatalf("expected new receiver with + prefix:\n%s", out)
		}
		// assembleRoutesYAML indents route items 2 spaces ("  - matchers:"),
		// so after routesBodyOnly the addition lines read "+   - matchers:".
		if !strings.Contains(out, "+ # ---- plugin additions: routes ----") {
			t.Fatalf("expected route additions marker:\n%s", out)
		}
		if !strings.Contains(out, "new-runbook") {
			t.Fatalf("expected new-runbook in route additions:\n%s", out)
		}
		// Existing receiver still present as context
		if !strings.Contains(out, "  - name: existing-runbook--team-channel") {
			t.Fatalf("expected existing receiver as context (no + prefix):\n%s", out)
		}
	})

	t.Run("CRD mode: namespaced receiver shown as context", func(t *testing.T) {
		crdLoaded := `route:
  receiver: "null"
  routes:
  - matchers: [runbook="existing-runbook"]
    receiver: monitoring/mattermost-alertmanager-abc123/existing-runbook--team-channel
receivers:
- name: "null"
- name: monitoring/mattermost-alertmanager-abc123/existing-runbook--team-channel
  slack_configs:
  - api_url: 'https://mm.example/hooks/existing'
`
		out := buildChannelScopedDiff(crdLoaded, own, true, "", "")

		if !strings.Contains(out, "monitoring/mattermost-alertmanager-abc123/existing-runbook--team-channel") {
			t.Fatalf("expected CRD-namespaced receiver as context:\n%s", out)
		}
	})

	t.Run("no existing channel data shows (none) placeholders", func(t *testing.T) {
		emptyLoaded := `receivers:
- name: "null"
route:
  receiver: "null"
`
		out := buildChannelScopedDiff(emptyLoaded, own, false, "", "")
		if !strings.Contains(out, "(none)") {
			t.Fatalf("expected (none) placeholders when channel has nothing loaded:\n%s", out)
		}
	})
}

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
// for /alertmanager format diff. The function does
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

	// Regression: routes were inserted at routesEndIdx=0 (the first existing
	// route item) rather than after the last item. Verify that:
	//  (a) new routes appear AFTER all existing route items in the diff
	//  (b) the merged YAML is structurally valid (no duplicate routes: key)
	t.Run("routes insert after last existing route item, not before first", func(t *testing.T) {
		// 2-space indented routes: block, matching a typical AM config.
		loaded := `global:
  resolve_timeout: 5m
route:
  receiver: "null"
  routes:
  - matchers: [alertname="Watchdog"]
    receiver: watchdog
  group_wait: 10s
receivers:
- name: "null"
- name: watchdog
`
		// Use the full assembleRoutesYAML output (with routes: header + comments)
		// to reproduce the exact input that handleExportDiff passes.
		entry := alertConfig{Name: "high-cpu-usage--alerts-channel"}
		newRoutes := assembleRoutesYAML([]alertConfig{entry})
		// Include a matching receiver so AM's semantic validation passes too.
		newRecvs := `- name: high-cpu-usage--alerts-channel
  slack_configs:
  - api_url: 'http://mattermost.example/hooks/abc'
`

		_, merged := buildDiffAgainstLoaded(loaded, newRecvs, newRoutes)

		// The merged YAML must parse cleanly — before the fix it produced
		// "yaml: did not find expected '-' indicator" because the routes: key
		// was injected inside the route.routes: sequence.
		if err := validateMergedConfig(merged); err != nil {
			t.Fatalf("merged YAML fails validation: %v\nMerged:\n%s", err, merged)
		}

		// The existing Watchdog route must appear BEFORE the new addition.
		watchdogIdx := strings.Index(merged, "watchdog")
		additionIdx := strings.Index(merged, "high-cpu-usage")
		if watchdogIdx == -1 || additionIdx == -1 {
			t.Fatalf("expected both watchdog and high-cpu-usage in merged YAML\nMerged:\n%s", merged)
		}
		if watchdogIdx > additionIdx {
			t.Fatalf("new route appeared before existing watchdog route — insertion order wrong\nMerged:\n%s", merged)
		}
	})

	// Regression: nested routes: blocks inside a route item (e.g. kube-bridge-fallback
	// sub-routes) caused routesIndent to be overwritten with the nested block's indent,
	// making the insertion land inside the sub-route instead of at the top-level
	// route.routes: list. The !inRoutes guard on the entry condition fixes this.
	t.Run("nested sub-routes do not perturb top-level insertion point", func(t *testing.T) {
		loaded := `global:
  resolve_timeout: 5m
route:
  receiver: "null"
  routes:
  - receiver: bridge-fallback
    matchers:
    - alertname=~"^(PodNotReady|TargetDown)$"
    routes:
    - receiver: pod-not-ready
      matchers:
      - alertname="PodNotReady"
    - receiver: watchdog-silence
      matchers:
      - alertname="Watchdog"
    group_wait: 30s
    group_interval: 5m
  group_wait: 10s
receivers:
- name: "null"
- name: bridge-fallback
- name: pod-not-ready
- name: watchdog-silence
`
		entry := alertConfig{Name: "database-connectivity-loss--team-channel"}
		newRoutes := assembleRoutesYAML([]alertConfig{entry})
		newRecvs := `- name: database-connectivity-loss--team-channel
  slack_configs:
  - api_url: 'http://mattermost.example/hooks/abc'
`
		_, merged := buildDiffAgainstLoaded(loaded, newRecvs, newRoutes)

		if err := validateMergedConfig(merged); err != nil {
			t.Fatalf("merged YAML fails validation: %v\nMerged:\n%s", err, merged)
		}

		// The new route must appear at the top-level routes block, not inside the
		// bridge-fallback sub-route. Verify by checking that the new route is a
		// sibling of bridge-fallback (both at 2-space indent "- receiver:"), not
		// a child. The simplest proxy: the merged YAML must contain both top-level
		// sibling items; if the insertion went inside bridge-fallback, the
		// database receiver's route would be gated behind the alertname matcher
		// and AM would reject a runbook=database-connectivity-loss alert that has
		// no matching alertname.
		dbIdx := strings.Index(merged, "database-connectivity-loss")
		groupWait10Idx := strings.Index(merged, "group_wait: 10s")
		if dbIdx == -1 {
			t.Fatalf("database route missing from merged YAML\nMerged:\n%s", merged)
		}
		// The top-level group_wait: 10s is a sibling of routes:, so it should
		// appear AFTER the newly inserted database route (since we append at the
		// end of the top-level routes list, before the sibling keys).
		if groupWait10Idx != -1 && dbIdx > groupWait10Idx {
			t.Fatalf("database route appears after top-level group_wait — route was inserted inside sub-route block\nMerged:\n%s", merged)
		}
	})
}

// TestRoutesBodyOnly verifies that routesBodyOnly strips the comment preamble
// and "routes:" key that assembleRoutesYAML prepends, keeping only the bare
// YAML list items that can be spliced into an existing route.routes: block.
func TestRoutesBodyOnly(t *testing.T) {
	t.Run("strips preamble and routes: key", func(t *testing.T) {
		full := assembleRoutesYAML([]alertConfig{
			{Name: "high-cpu-usage--alerts-channel"},
			{Name: "disk-full--alerts-channel"},
		})
		body := routesBodyOnly(full)

		if strings.Contains(body, "routes:") {
			t.Fatalf("routesBodyOnly should strip the routes: key; got:\n%s", body)
		}
		if strings.Contains(body, "# Alertmanager routes") {
			t.Fatalf("routesBodyOnly should strip comment preamble; got:\n%s", body)
		}
		if !strings.Contains(body, "- matchers:") {
			t.Fatalf("routesBodyOnly should keep list items; got:\n%s", body)
		}
	})

	t.Run("empty input returns empty", func(t *testing.T) {
		if out := routesBodyOnly(""); out != "" {
			t.Fatalf("expected empty string, got %q", out)
		}
	})

	t.Run("all-custom receivers (no route items) returns empty", func(t *testing.T) {
		full := assembleRoutesYAML([]alertConfig{{Name: "my-custom--team-channel", Custom: true}})
		body := routesBodyOnly(full)
		if body != "" {
			t.Fatalf("expected empty body for all-custom input; got:\n%s", body)
		}
	})
}
