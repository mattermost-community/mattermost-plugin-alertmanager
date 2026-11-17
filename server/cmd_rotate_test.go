package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// rotateAPI drives a full overdue rotation. Webhook create/delete go over
// Client4 to localBaseURL, so the fake Mattermost REST API is an httptest
// server whose port becomes ServiceSettings.ListenAddress.
type rotateAPI struct {
	*fakeRemoveAPI
	listenAddr string
	dmFails    bool
	uploads    map[string]string
}

func (f *rotateAPI) GetConfig() *model.Config {
	cfg := &model.Config{}
	cfg.ServiceSettings.ListenAddress = model.NewPointer(f.listenAddr)
	cfg.ServiceSettings.SiteURL = model.NewPointer("https://mm.example.com")
	return cfg
}

func (f *rotateAPI) GetTeamByName(name string) (*model.Team, *model.AppError) {
	for _, t := range f.teams {
		if t.Name == name {
			return t, nil
		}
	}
	return nil, &model.AppError{StatusCode: 404}
}

func (f *rotateAPI) GetChannelByName(teamID, name string, _ bool) (*model.Channel, *model.AppError) {
	for _, c := range f.channels {
		if c.TeamId == teamID && c.Name == name {
			return c, nil
		}
	}
	return nil, &model.AppError{StatusCode: 404}
}

func (f *rotateAPI) CreateUserAccessToken(t *model.UserAccessToken) (*model.UserAccessToken, *model.AppError) {
	return &model.UserAccessToken{Id: "tok", Token: "secret", UserId: t.UserId}, nil
}

func (f *rotateAPI) RevokeUserAccessToken(string) *model.AppError { return nil }

func (f *rotateAPI) LogAuditRec(*model.AuditRecord) {}

func (f *rotateAPI) GetDirectChannel(string, string) (*model.Channel, *model.AppError) {
	if f.dmFails {
		return nil, &model.AppError{Message: "dm unavailable"}
	}
	return &model.Channel{Id: "dm"}, nil
}

func (f *rotateAPI) UploadFile(data []byte, _, filename string) (*model.FileInfo, *model.AppError) {
	f.uploads[filename] = string(data)
	return &model.FileInfo{Id: filename}, nil
}

func (f *rotateAPI) CreatePost(*model.Post) (*model.Post, *model.AppError) {
	return &model.Post{Id: "p1"}, nil
}

func newRotateTestPlugin(t *testing.T, deleteFails, dmFails bool) (*Plugin, *rotateAPI) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v4/hooks/incoming":
			_ = json.NewEncoder(w).Encode(&model.IncomingWebhook{Id: "hooknew"})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v4/hooks/incoming/"):
			if deleteFails {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"delete failed"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status":"OK"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	stale := time.Now().UTC().Add(-60 * 24 * time.Hour)
	configs := []alertConfig{{
		Name: "high-cpu--ops-alerts", Team: "ops", Channel: "alerts", WebhookID: "hookold",
		AlertManagerURL: "http://am:9093", RotationRemindersEnabled: true, LastRotatedAt: stale,
	}}
	kvBytes, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	api := &rotateAPI{
		fakeRemoveAPI: &fakeRemoveAPI{
			channels: map[string]*model.Channel{"chanA": {Id: "chanA", Name: "alerts", TeamId: "teamA"}},
			teams:    map[string]*model.Team{"teamA": {Id: "teamA", Name: "ops"}},
			kv:       map[string][]byte{kvKeyAlertConfigs: kvBytes},
		},
		listenAddr: ":" + u.Port(),
		dmFails:    dmFails,
		uploads:    map[string]string{},
	}
	p := &Plugin{}
	p.API = api
	p.setConfiguration(newConfiguration(configs, "", 0, "", "", 30))
	return p, api
}

func TestRotateOverdueReportsAnOldWebhookThatIsStillLive(t *testing.T) {
	p, api := newRotateTestPlugin(t, true, false)
	got, err := p.handleRotateOverdue(&model.CommandArgs{ChannelId: "chanA", UserId: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Rotated 1 receiver") {
		t.Fatalf("rotation should still count as done:\n%s", got)
	}
	if strings.Contains(got, "deactivate immediately") {
		t.Fatalf("reply must not say the old URLs are dead:\n%s", got)
	}
	if !strings.Contains(got, "Old webhook still live") || !strings.Contains(got, "high-cpu--ops-alerts") {
		t.Fatalf("reply should name the receiver whose old webhook is live:\n%s", got)
	}
	if yml := api.uploads["alertmanager-receivers.yml"]; strings.Contains(yml, "deactivated immediately") {
		t.Fatalf("DM'd YAML must not say the old URLs are dead:\n%s", yml)
	}
}

func TestRotateOverdueSaysWhenTheYAMLWasNotDelivered(t *testing.T) {
	p, _ := newRotateTestPlugin(t, false, true)
	got, err := p.handleRotateOverdue(&model.CommandArgs{ChannelId: "chanA", UserId: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "DM'd to you from") {
		t.Fatalf("reply claims the YAML was delivered:\n%s", got)
	}
	if !strings.Contains(got, "could not be DM'd") || !strings.Contains(got, "/alertmanager format export") {
		t.Fatalf("reply should give a way to recover the new URLs:\n%s", got)
	}
}

func TestRotateGroupReplySaysWhenTheYAMLWasNotDelivered(t *testing.T) {
	p, _ := newRotateTestPlugin(t, false, true)
	affected := []alertConfig{
		{Name: "compute--ops-alerts", Team: "ops", Channel: "alerts", WebhookID: "hooknew", GroupName: "compute"},
		{Name: "oom--ops-alerts", Team: "ops", Channel: "alerts", WebhookID: "hooknew", GroupName: "compute"},
	}
	got := p.renderRotateGroupResponse("u1", affected, "compute", true, "hookold")
	if strings.Contains(got, "DM'd to you from") {
		t.Fatalf("reply claims the YAML was delivered:\n%s", got)
	}
	if !strings.Contains(got, "/alertmanager format export") {
		t.Fatalf("reply should give a way to recover the new URLs:\n%s", got)
	}
}

func TestRotateOverdueCleanRunStillSaysOldURLsAreDead(t *testing.T) {
	p, api := newRotateTestPlugin(t, false, false)
	got, err := p.handleRotateOverdue(&model.CommandArgs{ChannelId: "chanA", UserId: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "DM'd to you from") || !strings.Contains(got, "deactivate immediately") {
		t.Fatalf("clean rotation reply changed:\n%s", got)
	}
	if strings.Contains(got, "still live") {
		t.Fatalf("clean rotation must not warn:\n%s", got)
	}
	if !strings.Contains(api.uploads["alertmanager-receivers.yml"], "hooks/hooknew") {
		t.Fatalf("DM'd YAML should carry the new webhook URL:\n%s", api.uploads["alertmanager-receivers.yml"])
	}
}
