package main

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// fakeConfigAPI drives OnConfigurationChange: it serves the receiver list from
// KV and counts GetTeamByName calls per team so the memoization can be asserted.
type fakeConfigAPI struct {
	plugin.API
	blob      []byte
	teamCalls map[string]int
}

func (f *fakeConfigAPI) LoadPluginConfiguration(any) error { return nil }

func (f *fakeConfigAPI) KVGet(string) ([]byte, *model.AppError) { return f.blob, nil }

func (f *fakeConfigAPI) GetTeamByName(name string) (*model.Team, *model.AppError) {
	f.teamCalls[name]++
	return &model.Team{Id: "id-" + name, Name: name}, nil
}

// TestOnConfigurationChangeMemoizesTeamLookup is the CL-25 regression: team
// existence is verified once per DISTINCT team, not once per entry, so a large
// list (many receivers sharing a handful of teams) doesn't issue an
// O(N)-plugin-RPC storm on every config change.
func TestOnConfigurationChangeMemoizesTeamLookup(t *testing.T) {
	entries := []alertConfig{
		{Name: "a--t1-c", Team: "t1", Channel: "c", WebhookID: "h1"},
		{Name: "b--t1-c", Team: "t1", Channel: "c", WebhookID: "h2"},
		{Name: "c--t2-c", Team: "t2", Channel: "c", WebhookID: "h3"},
		{Name: "d--t2-c", Team: "t2", Channel: "c", WebhookID: "h4"},
	}
	blob, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	api := &fakeConfigAPI{blob: blob, teamCalls: map[string]int{}}
	p := &Plugin{}
	p.API = api

	if err := p.OnConfigurationChange(); err != nil {
		t.Fatalf("OnConfigurationChange: %v", err)
	}

	// 4 entries across 2 distinct teams → each team verified exactly once.
	if api.teamCalls["t1"] != 1 || api.teamCalls["t2"] != 1 {
		t.Fatalf("expected one GetTeamByName per distinct team, got %v", api.teamCalls)
	}
	if got := len(p.getConfiguration().AlertConfigs); got != 4 {
		t.Fatalf("expected 4 receivers loaded, got %d", got)
	}
}
