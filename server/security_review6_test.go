package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/mattermost/mattermost-plugin-alertmanager/server/alertmanager"
)

// TestWebhookTestClientBlocksSSRF is the finding-#1 regression: the webhook-test
// POST used http.DefaultClient (proxy-honoring, redirect-following, no dial
// guard) against a team-admin-controllable --webhook-host, so a team admin could
// aim it at cloud metadata / internal services. The guarded webhookTestClient
// must refuse those destinations at dial time.
func TestWebhookTestClientBlocksSSRF(t *testing.T) {
	t.Cleanup(func() { alertmanager.SetAllowedNets(nil) })
	alertmanager.SetAllowedNets(nil) // no allowlist: metadata hard-blocked, private blocked

	// Cloud metadata — hard-blocked regardless of any allowlist.
	if err := postValidateTestMessage("http://169.254.169.254/hooks/abc", "abc", "r"); err == nil {
		t.Fatal("webhook-test to cloud metadata must be refused by the guarded client")
	}
	// A private IP with no allowlist is blocked by default.
	if err := postValidateTestMessage("http://10.0.0.5/hooks/abc", "abc", "r"); err == nil {
		t.Fatal("webhook-test to a private IP must be refused without an allowlist")
	}
}

// TestMetricsOmitsChannelNames is the finding-#5 regression: the bearer-protected
// /metrics endpoint must not emit raw Mattermost channel names as labels, since
// observability users scraping it aren't bound by MM channel ACLs.
func TestMetricsOmitsChannelNames(t *testing.T) {
	p := &Plugin{}
	p.API = &fakeLogAPI{}
	p.setConfiguration(newConfiguration([]alertConfig{
		{Name: "high-cpu--t-secret", Team: "t", Channel: "secret-incident-room", WebhookID: "h1"},
		{Name: "oom--t-secret", Team: "t", Channel: "secret-incident-room", WebhookID: "h2"},
	}, "", 0, "", "tok", 0))

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	p.handleMetrics(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "secret-incident-room") {
		t.Fatalf("metrics leaked a raw channel name:\n%s", body)
	}
	// Aggregate signal is still present.
	if !strings.Contains(body, "alertmanager_plugin_receivers_total 2") {
		t.Fatalf("expected aggregate receiver count, got:\n%s", body)
	}
}

// fakeRemoveAPI implements the scope lookups (GetChannel/GetTeam) AND the KV
// read/write that handleRemoveOne needs, so a full remove can be driven with a
// stale in-memory snapshot diverging from fresh KV.
type fakeRemoveAPI struct {
	plugin.API
	channels map[string]*model.Channel
	teams    map[string]*model.Team
	kv       map[string][]byte
}

func (f *fakeRemoveAPI) GetChannel(id string) (*model.Channel, *model.AppError) {
	if c, ok := f.channels[id]; ok {
		return c, nil
	}
	return nil, &model.AppError{StatusCode: 404}
}

func (f *fakeRemoveAPI) GetTeam(id string) (*model.Team, *model.AppError) {
	if t, ok := f.teams[id]; ok {
		return t, nil
	}
	return nil, &model.AppError{StatusCode: 404}
}

func (f *fakeRemoveAPI) KVGet(key string) ([]byte, *model.AppError) { return f.kv[key], nil }

func (f *fakeRemoveAPI) KVCompareAndSet(key string, oldValue, newValue []byte) (bool, *model.AppError) {
	if string(f.kv[key]) != string(oldValue) {
		return false, nil
	}
	f.kv[key] = newValue
	return true, nil
}

func (f *fakeRemoveAPI) LogWarn(string, ...any)  {}
func (f *fakeRemoveAPI) LogError(string, ...any) {}
func (f *fakeRemoveAPI) LogInfo(string, ...any)  {}

func (f *fakeRemoveAPI) PublishPluginClusterEvent(model.PluginClusterEvent, model.PluginClusterEventSendOptions) error {
	return nil
}

// TestRemoveOneRefusesIdentityMismatch is the finding-#3 regression: if the
// receiver named X was removed and re-created (new webhook ID) on another node
// after this node scoped its in-memory snapshot, `remove X` must NOT delete the
// new receiver — it matches on Name AND WebhookID, not name alone.
func TestRemoveOneRefusesIdentityMismatch(t *testing.T) {
	// Fresh KV holds X with the NEW webhook id.
	kvConfigs := []alertConfig{{Name: "high-cpu--ateam-alerts", Team: "ateam", Channel: "alerts", WebhookID: "h_new"}}
	kvBytes, _ := json.MarshalIndent(kvConfigs, "", "  ")

	api := &fakeRemoveAPI{
		channels: map[string]*model.Channel{"chanA": {Id: "chanA", Name: "alerts", TeamId: "teamA"}},
		teams:    map[string]*model.Team{"teamA": {Id: "teamA", Name: "ateam"}},
		kv:       map[string][]byte{kvKeyAlertConfigs: kvBytes},
	}
	p := &Plugin{}
	p.API = api
	// Stale in-memory snapshot: same name, OLD webhook id.
	p.setConfiguration(newConfiguration([]alertConfig{
		{Name: "high-cpu--ateam-alerts", Team: "ateam", Channel: "alerts", WebhookID: "h_old"},
	}, "", 0, "", "", 0))

	args := &model.CommandArgs{ChannelId: "chanA", UserId: "u1"}
	msg, err := p.handleRemoveOne(args, "high-cpu--ateam-alerts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "changed") {
		t.Fatalf("expected an identity-changed message, got: %q", msg)
	}
	// KV must still hold the new receiver — it was NOT deleted.
	var after []alertConfig
	if e := json.Unmarshal(api.kv[kvKeyAlertConfigs], &after); e != nil {
		t.Fatalf("bad KV after: %v", e)
	}
	if len(after) != 1 || after[0].WebhookID != "h_new" {
		t.Fatalf("re-created receiver was wrongly deleted; KV=%+v", after)
	}
}
