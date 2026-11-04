package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/prometheus/alertmanager/api/v2/models"

	"github.com/mattermost/mattermost-plugin-alertmanager/server/alertmanager"
)

func TestParseSilenceDuration(t *testing.T) {
	hour := time.Hour
	cases := []struct {
		name    string
		raw     string
		scope   silenceScope
		want    time.Duration
		wantErr bool
	}{
		{name: "missing", scope: silenceScopeAlert, wantErr: true},
		{name: "30m alert", raw: "30m", scope: silenceScopeAlert, want: 30 * time.Minute},
		{name: "30mins group", raw: "30mins", scope: silenceScopeGroup, want: 30 * time.Minute},
		{name: "1 hour words", raw: "4hours", scope: silenceScopeAlert, want: 4 * hour},
		{name: "72h group", raw: "72h", scope: silenceScopeGroup, want: 72 * hour},
		{name: "73h rejected", raw: "73h", scope: silenceScopeAlert, wantErr: true},
		{name: "90m rejected", raw: "90m", scope: silenceScopeAlert, wantErr: true},
		{name: "fraction rejected", raw: "1.5h", scope: silenceScopeAlert, wantErr: true},
		{name: "all 30m", raw: "30m", scope: silenceScopeAll, want: 30 * time.Minute},
		{name: "all 1h", raw: "1h", scope: silenceScopeAll, want: hour},
		{name: "all 1hr", raw: "1hr", scope: silenceScopeAll, want: hour},
		{name: "all 2h rejected", raw: "2h", scope: silenceScopeAll, wantErr: true},
		{name: "all 72h rejected", raw: "72h", scope: silenceScopeAll, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSilenceDuration(tc.raw, tc.scope)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %s", got)
			}
		})
	}
}

func TestSilenceTimeRejectedListsAllowedTimes(t *testing.T) {
	_, err := parseSilenceDuration("96h", silenceScopeGroup)
	if err == nil {
		t.Fatal("expected 96h to be rejected")
	}
	msg := silenceTimeRejected("96h", silenceScopeGroup, err)
	for _, want := range []string{"96h", "30m", "1h", "72h", "all", "HighCPUUsage", "compute"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("reply missing %q:\n%s", want, msg)
		}
	}
}

func TestClassifySilenceTarget(t *testing.T) {
	scope, name, err := classifySilenceTarget("compute")
	if err != nil || scope != silenceScopeGroup || name != "compute" {
		t.Fatalf("compute: scope=%d name=%q err=%v", scope, name, err)
	}
	scope, _, err = classifySilenceTarget("all")
	if err != nil || scope != silenceScopeAll {
		t.Fatalf("all: scope=%d err=%v", scope, err)
	}
	scope, name, err = classifySilenceTarget("HighCPUUsage")
	if err != nil || scope != silenceScopeAlert || name != "HighCPUUsage" {
		t.Fatalf("alert: scope=%d name=%q err=%v", scope, name, err)
	}
	if _, _, err := classifySilenceTarget("high cpu"); err == nil {
		t.Fatal("expected a spaced name to be rejected")
	}
}

func alertFor(t *testing.T, runbook string) string {
	t.Helper()
	for alert, rb := range shippedAlertRunbooks() {
		if rb == runbook {
			return alert
		}
	}
	t.Fatalf("no shipped alert for runbook %s", runbook)
	return ""
}

func planTestConfigs() []alertConfig {
	return []alertConfig{
		{Name: "high-cpu-usage--team-chan", AlertManagerURL: "http://am-a"},
		{Name: "mattermost-api-high-latency--team-chan", AlertManagerURL: "http://am-b"},
		{Name: "custom-hook--team-chan", AlertManagerURL: "http://am-c", Kind: kindCustom},
		{Name: packReceiverSlug + "--team-chan", AlertManagerURL: "http://am-d", Kind: kindPack, Pack: packReceiverSlug},
	}
}

func TestSilencePlanGroupUsesChannelRunbooks(t *testing.T) {
	jobs, err := silencePlan(silenceScopeGroup, "compute", planTestConfigs())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || len(jobs[0].configs) != 1 || jobs[0].configs[0].Name != "high-cpu-usage--team-chan" {
		t.Fatalf("jobs = %+v", jobs)
	}
	if m := jobs[0].matchers; len(m) != 1 || !m[0].Regex || m[0].Name != "runbook" {
		t.Fatalf("matchers = %+v", m)
	}
	if _, err := silencePlan(silenceScopeGroup, "database", planTestConfigs()); err == nil {
		t.Fatal("expected a group with no receivers in the channel to fail")
	}
}

func TestSilencePlanCoversPackAlerts(t *testing.T) {
	for _, scope := range []struct {
		name   string
		scope  silenceScope
		target string
	}{
		{"all", silenceScopeAll, "all"},
		{"mattermost group", silenceScopeGroup, mattermostGroup},
	} {
		t.Run(scope.name, func(t *testing.T) {
			jobs, err := silencePlan(scope.scope, scope.target, planTestConfigs())
			if err != nil {
				t.Fatal(err)
			}
			var pack *silenceJob
			for i := range jobs {
				if jobs[i].matchers[0].Name == "alertname" {
					pack = &jobs[i]
				}
			}
			if pack == nil || !pack.matchers[0].Regex || len(pack.configs) != 1 || pack.configs[0].AlertManagerURL != "http://am-d" {
				t.Fatalf("want one alertname silence on the pack backend, got %+v", jobs)
			}
		})
	}

	jobs, err := silencePlan(silenceScopeGroup, "compute", planTestConfigs())
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.matchers[0].Name == "alertname" {
			t.Fatal("a compute silence must not reach the pack's alerts")
		}
	}
}

func TestSilencePlanAlertStaysInChannelRuleSet(t *testing.T) {
	configs := planTestConfigs()
	cpu := alertFor(t, "high-cpu-usage")

	jobs, err := silencePlan(silenceScopeAlert, cpu, configs)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || len(jobs[0].configs) != 1 || jobs[0].configs[0].AlertManagerURL != "http://am-a" {
		t.Fatalf("alert should be silenced only where its receiver sends it: %+v", jobs)
	}
	if m := jobs[0].matchers; len(m) != 1 || m[0].Name != "alertname" || m[0].Value != cpu || m[0].Regex {
		t.Fatalf("matchers = %+v", m)
	}

	packNames, err := packAlertnames()
	if err != nil {
		t.Fatal(err)
	}
	jobs, err = silencePlan(silenceScopeAlert, packNames[0], configs)
	if err != nil || len(jobs[0].configs) != 1 || jobs[0].configs[0].AlertManagerURL != "http://am-d" {
		t.Fatalf("pack alert should go to the pack receiver: %+v, %v", jobs, err)
	}

	for _, name := range []string{alertFor(t, "persistent-volume-full"), "SomeoneElsesAlert", "MyCustomAlert"} {
		if _, err := silencePlan(silenceScopeAlert, name, configs); err == nil {
			t.Errorf("%s is not in this channel's rule set and must be refused", name)
		}
	}
	if _, err := silencePlan(silenceScopeAlert, packNames[0], configs[:3]); err == nil {
		t.Error("a pack alert needs a pack receiver in the channel")
	}
}

func TestSilenceTouchesChannel(t *testing.T) {
	str := func(s string) *string { return &s }
	flag := func(b bool) *bool { return &b }
	silence := func(name, value string, regex, equal bool) *models.GettableSilence {
		return &models.GettableSilence{Silence: models.Silence{Matchers: models.Matchers{
			{Name: str(name), Value: str(value), IsRegex: flag(regex), IsEqual: flag(equal)},
		}}}
	}
	runbooks := []string{"high-cpu-usage"}
	alerts := []string{"HighCPUUsage"}

	cases := []struct {
		name string
		s    *models.GettableSilence
		want bool
	}{
		{"exact runbook", silence("runbook", "high-cpu-usage", false, true), true},
		{"other runbook", silence("runbook", "persistent-volume-full", false, true), false},
		{"exact alertname", silence("alertname", "HighCPUUsage", false, true), true},
		{"regex alertname", silence("alertname", "High.*", true, true), true},
		{"regex is anchored", silence("alertname", "CPU", true, true), false},
		{"regex that matches everything else", silence("alertname", "Other.*", true, true), false},
		{"negative matcher", silence("alertname", "HighCPUUsage", false, false), false},
		{"unrelated label", silence("namespace", "prod", false, true), false},
		{"invalid regex", silence("alertname", "(", true, true), false},
		{"oversized regex", silence("alertname", strings.Repeat("a", maxSilenceRegexLen+1), true, true), false},
		{"nil silence", nil, false},
	}
	for _, c := range cases {
		if got := silenceTouchesChannel(c.s, runbooks, alerts); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestListSilencesShowsOnlyThisChannelsSilences(t *testing.T) {
	const (
		mine      = "0c1b6c3e-5f0e-4c3a-9a55-2f9d1c7e8a10"
		byRunbook = "11111111-1111-4111-8111-111111111111"
		other     = "22222222-2222-4222-8222-222222222222"
		recorded  = "33333333-3333-4333-8333-333333333333"
	)
	silence := func(id, matcherName, matcherValue string) string {
		return `{"id":"` + id + `","status":{"state":"active"},"matchers":[{"name":"` + matcherName + `","value":"` + matcherValue + `","isRegex":false,"isEqual":true}],` +
			`"startsAt":"2026-01-01T00:00:00Z","endsAt":"2099-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z","createdBy":"x","comment":"c"}`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[" + strings.Join([]string{
			silence(byRunbook, "runbook", "high-cpu-usage"),
			silence(other, "runbook", "persistent-volume-full"),
			silence(recorded, "namespace", "prod"),
		}, ",") + "]"))
	}))
	t.Cleanup(srv.Close)

	_, ipNet, err := net.ParseCIDR("127.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	alertmanager.SetAllowedNets([]*net.IPNet{ipNet})
	t.Cleanup(func() { alertmanager.SetAllowedNets(nil) })

	p, _ := newSilenceKVPlugin()
	p.setConfiguration(newConfiguration([]alertConfig{
		{Name: "high-cpu-usage--ateam-alerts", Team: "ateam", Channel: "alerts", WebhookID: "h1", AlertManagerURL: srv.URL},
	}, "", 0, "", "", 0))
	if err = p.recordSilence(recorded, silenceRecord{ChannelID: "chanA", AMURL: srv.URL, CreatedBy: "u1"}, time.Hour); err != nil {
		t.Fatal(err)
	}

	out, err := p.handleListSilences(&model.CommandArgs{ChannelId: "chanA", UserId: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{byRunbook, recorded} {
		if !strings.Contains(out, id) {
			t.Errorf("silence %s applies to this channel and should be listed:\n%s", id, out)
		}
	}
	if strings.Contains(out, other) {
		t.Errorf("a silence for another channel's runbook must not be listed:\n%s", out)
	}
}
