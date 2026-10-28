package main

import (
	"strings"
	"testing"
	"time"
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

func TestSilencePlanGroupUsesChannelRunbooks(t *testing.T) {
	configs := []alertConfig{
		{Name: "high-cpu-usage--team-chan", AlertManagerURL: "http://am"},
		{Name: "mattermost-api-high-latency--team-chan", AlertManagerURL: "http://am"},
		{Name: "custom-hook--team-chan", AlertManagerURL: "http://am", Custom: true},
	}
	got, matchers, err := silencePlan(silenceScopeGroup, "compute", configs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "high-cpu-usage--team-chan" {
		t.Fatalf("configs = %+v", got)
	}
	if len(matchers) != 1 || !matchers[0].Regex || matchers[0].Name != "runbook" {
		t.Fatalf("matchers = %+v", matchers)
	}
	if _, _, err := silencePlan(silenceScopeGroup, "database", configs); err == nil {
		t.Fatal("expected a group with no receivers in the channel to fail")
	}
}
