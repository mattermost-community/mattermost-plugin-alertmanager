package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type inventoryPageAPI struct{ plugin.API }

func (inventoryPageAPI) GetTeamByName(name string) (*model.Team, *model.AppError) {
	return &model.Team{Id: "id-" + name, Name: name}, nil
}
func (inventoryPageAPI) KVGet(string) ([]byte, *model.AppError) { return nil, nil }
func (inventoryPageAPI) LogWarn(string, ...any)                 {}

const inventoryTestAMURL = "http://am.example:9093"

func inventoryTestPlugin() (*Plugin, []alertConfig) {
	p := &Plugin{}
	p.API = inventoryPageAPI{}
	configs := []alertConfig{
		{Name: "high-cpu-usage--ateam-alerts", Team: "ateam", Channel: "alerts", AlertManagerURL: inventoryTestAMURL},
		{Name: "disk-full--bteam-ops", Team: "bteam", Channel: "ops", AlertManagerURL: inventoryTestAMURL},
	}
	// Seed the probe cache so the page never touches the network. The config
	// carries one receiver the plugin doesn't know, to exercise AM-only drift.
	p.amReachabilityCache.Store(inventoryTestAMURL, &amReachabilityEntry{
		Reachable:  true,
		Status:     "ok",
		CheckedAt:  time.Now(),
		ConfigBody: "receivers:\n  - name: high-cpu-usage--ateam-alerts\n  - name: hand-edited\n  - name: \"null\"\n",
	})
	return p, configs
}

func renderInventoryPage(t *testing.T, method string, query url.Values) string {
	t.Helper()
	p, configs := inventoryTestPlugin()
	var req *http.Request
	if method == http.MethodPost {
		req = httptest.NewRequest(method, "/admin/inventory", strings.NewReader(query.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, "/admin/inventory?"+query.Encode(), nil)
	}
	rec := httptest.NewRecorder()
	p.renderInventoryHTML(rec, req, configs)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	return rec.Body.String()
}

func TestInventoryPageRenders(t *testing.T) {
	body := renderInventoryPage(t, http.MethodGet, url.Values{})
	for _, want := range []string{
		"Alertmanager Plugin Inventory",
		"high-cpu-usage--ateam-alerts",
		"disk-full--bteam-ops",
		"hand-edited",
		"/ateam/channels/alerts",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, ">null<") {
		t.Error("built-in null receiver must not be reported as AM-only")
	}
}

func TestInventoryPageGroupModeFallsBackToChannel(t *testing.T) {
	for mode, want := range map[string]string{"team": "team", "am": "am", "bogus": "channel", "": "channel"} {
		r := httptest.NewRequest(http.MethodGet, "/?group="+mode, nil)
		if got := inventoryGroupMode(r); got != want {
			t.Errorf("group=%q: got %q want %q", mode, got, want)
		}
	}
}

func TestInventoryFormDropsSeverityOutsideEndToEnd(t *testing.T) {
	q := url.Values{"simulate_mode": {"simulate"}, "simulate_severity": {"critical"}}
	f := parseInventoryForm(httptest.NewRequest(http.MethodGet, "/?"+q.Encode(), nil))
	if f.severity != "" {
		t.Errorf("severity kept for simulate: %q", f.severity)
	}
	if !f.submitted() {
		t.Error("form with a mode must count as submitted")
	}
	q.Set("simulate_mode", "end-to-end")
	f = parseInventoryForm(httptest.NewRequest(http.MethodGet, "/?"+q.Encode(), nil))
	if f.severity != "critical" {
		t.Errorf("severity dropped for end-to-end: %q", f.severity)
	}
}

func TestInventorySideEffectModesRejectGET(t *testing.T) {
	for _, mode := range []string{"webhook-test", "end-to-end"} {
		q := url.Values{"simulate_mode": {mode}, "simulate_type": {"all"}}
		body := renderInventoryPage(t, http.MethodGet, q)
		if !strings.Contains(body, "CSRF protection") {
			t.Errorf("%s over GET must be refused with the CSRF message", mode)
		}
	}
}
