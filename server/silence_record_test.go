package main

import (
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/prometheus/alertmanager/api/v2/models"
)

const testSilenceID = "0c1b6c3e-5f0e-4c3a-9a55-2f9d1c7e8a10"

type silenceKVAPI struct {
	plugin.API
	kv       map[string][]byte
	expiry   map[string]int64
	sysadmin bool
}

func (f *silenceKVAPI) KVGet(key string) ([]byte, *model.AppError) { return f.kv[key], nil }

func (f *silenceKVAPI) KVSetWithExpiry(key string, value []byte, exp int64) *model.AppError {
	f.kv[key] = value
	f.expiry[key] = exp
	return nil
}

func (f *silenceKVAPI) HasPermissionTo(string, *model.Permission) bool { return f.sysadmin }
func (f *silenceKVAPI) LogInfo(string, ...any)                         {}
func (f *silenceKVAPI) LogWarn(string, ...any)                         {}
func (f *silenceKVAPI) LogAuditRec(*model.AuditRecord)                 {}

func (f *silenceKVAPI) GetChannel(id string) (*model.Channel, *model.AppError) {
	return &model.Channel{Id: id, Name: "alerts", TeamId: "teamA"}, nil
}

func (f *silenceKVAPI) GetTeam(string) (*model.Team, *model.AppError) {
	return &model.Team{Id: "teamA", Name: "ateam"}, nil
}

func newSilenceKVPlugin() (*Plugin, *silenceKVAPI) {
	api := &silenceKVAPI{kv: map[string][]byte{}, expiry: map[string]int64{}, sysadmin: true}
	p := &Plugin{}
	p.API = api
	p.client = pluginapi.NewClient(api, nil)
	p.setConfiguration(newConfiguration([]alertConfig{
		{Name: "high-cpu-usage--ateam-alerts", Team: "ateam", Channel: "alerts", WebhookID: "h1", AlertManagerURL: "http://am.example:9093"},
	}, "", 0, "", "", 0))
	return p, api
}

func TestRecordedSilenceIsExpirableOnlyFromItsChannelAndAlertmanager(t *testing.T) {
	p, _ := newSilenceKVPlugin()
	rec := silenceRecord{ChannelID: "chanA", AMURL: "http://am.example:9093", CreatedBy: "u1"}
	if err := p.recordSilence(testSilenceID, rec, 2*time.Hour); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, id, channel, am string
		want                  bool
	}{
		{"same channel and AM", testSilenceID, "chanA", "http://am.example:9093", true},
		{"other channel", testSilenceID, "chanB", "http://am.example:9093", false},
		{"other Alertmanager", testSilenceID, "chanA", "http://other:9093", false},
		{"unrecorded silence", "11111111-1111-1111-1111-111111111111", "chanA", "http://am.example:9093", false},
	}
	for _, c := range cases {
		got, err := p.silenceCreatedFor(c.id, c.channel, c.am)
		if err != nil || got != c.want {
			t.Errorf("%s: got %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}

func TestRecordSilenceOutlivesTheSilence(t *testing.T) {
	p, api := newSilenceKVPlugin()
	if err := p.recordSilence(testSilenceID, silenceRecord{ChannelID: "chanA"}, 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, want := api.expiry[silenceRecordKey(testSilenceID)], int64((3 * time.Hour).Seconds()); got != want {
		t.Fatalf("expiry = %d, want %d", got, want)
	}
}

func TestSilenceExpirableRejectsMalformedIDs(t *testing.T) {
	p, api := newSilenceKVPlugin()
	hostile := strings.Repeat("a", 500)
	api.kv[kvKeySilencePrefix+hostile] = []byte(`{"channel_id":"chanA","am_url":"u"}`)
	if p.silenceExpirable(hostile, "chanA", "u") {
		t.Fatal("non-UUID IDs must never be offered for expire")
	}
}

func TestExpireSilenceRefusesSilenceNotCreatedFromChannel(t *testing.T) {
	p, _ := newSilenceKVPlugin()
	args := &model.CommandArgs{
		ChannelId: "chanA", UserId: "u1",
		Command: "/alertmanager silence expire high-cpu-usage--ateam-alerts " + testSilenceID,
	}

	msg, err := p.handleExpireSilence(withoutParent(args))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "cannot be expired here") {
		t.Fatalf("expected refusal for an unrecorded silence, got: %s", msg)
	}
}

func TestExpireSilenceRefusesSilenceFromAnotherChannel(t *testing.T) {
	p, _ := newSilenceKVPlugin()
	if err := p.recordSilence(testSilenceID, silenceRecord{ChannelID: "chanB", AMURL: "http://am.example:9093"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	args := &model.CommandArgs{
		ChannelId: "chanA", UserId: "u1",
		Command: "/alertmanager silence expire high-cpu-usage--ateam-alerts " + strings.ToUpper(testSilenceID),
	}

	msg, _ := p.handleExpireSilence(withoutParent(args))
	if !strings.Contains(msg, "cannot be expired here") {
		t.Fatalf("expected refusal for another channel's silence, got: %s", msg)
	}
}

func TestExpireSilenceRejectsNonUUID(t *testing.T) {
	p, _ := newSilenceKVPlugin()
	args := &model.CommandArgs{
		ChannelId: "chanA", UserId: "u1",
		Command: "/alertmanager silence expire high-cpu-usage--ateam-alerts ../../-/reload",
	}
	msg, _ := p.handleExpireSilence(withoutParent(args))
	if !strings.Contains(msg, "Invalid silence ID") {
		t.Fatalf("got: %s", msg)
	}
}

func TestFormatSilenceLineOffersExpireOnlyWhenExpirable(t *testing.T) {
	id := testSilenceID
	s := &models.GettableSilence{}
	s.ID = &id

	if out := formatSilenceLine("r", s, true); !strings.Contains(out, "/alertmanager silence expire r "+id) {
		t.Errorf("expirable silence should show the command:\n%s", out)
	}
	out := formatSilenceLine("r", s, false)
	if strings.Contains(out, "silence expire") || !strings.Contains(out, "Not created from this channel") {
		t.Errorf("foreign silence must not show an expire command:\n%s", out)
	}
}
