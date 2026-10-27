package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type memberAPI struct {
	plugin.API
	members map[string]bool
}

func (f *memberAPI) GetChannelMember(channelID, userID string) (*model.ChannelMember, *model.AppError) {
	if f.members[channelID+"/"+userID] {
		return &model.ChannelMember{ChannelId: channelID, UserId: userID}, nil
	}
	return nil, &model.AppError{Message: "not a member", StatusCode: 404}
}

func (f *memberAPI) GetChannel(id string) (*model.Channel, *model.AppError) {
	return &model.Channel{Id: id, Name: "alerts", TeamId: "t1"}, nil
}

func (f *memberAPI) GetTeam(id string) (*model.Team, *model.AppError) {
	return &model.Team{Id: id, Name: "ops"}, nil
}

func (f *memberAPI) LogWarn(string, ...any) {}

func validateTargetItems(t *testing.T, p *Plugin, channelID, userID string) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, validateTargetsPath+"?channel_id="+channelID, http.NoBody)
	rec := httptest.NewRecorder()
	p.handleAutocompleteValidateTargets(rec, req, userID)

	var items []model.AutocompleteListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Item)
	}
	return out
}

func TestValidateTargetsOnlyListsGroupsInThisChannel(t *testing.T) {
	compute := scaffoldSets["compute"][0]
	storage := scaffoldSets["storage"][0]
	p := &Plugin{}
	p.API = &memberAPI{members: map[string]bool{"c1/u1": true}}
	p.setConfiguration(newConfiguration([]alertConfig{
		{Name: compute + "--ops-alerts", Team: "ops", Channel: "alerts", WebhookID: "h1"},
		{Name: "slo-burn--ops-alerts", Team: "ops", Channel: "alerts", Custom: true, WebhookID: "h2"},
		{Name: storage + "--ops-other", Team: "ops", Channel: "other", WebhookID: "h3"},
	}, "", 0, "", "", 0))

	got := validateTargetItems(t, p, "c1", "u1")
	if want := []string{"all", "compute"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestValidateTargetsHidesGroupsFromNonMembers(t *testing.T) {
	p := &Plugin{}
	p.API = &memberAPI{members: map[string]bool{}}
	p.setConfiguration(newConfiguration([]alertConfig{
		{Name: scaffoldSets["compute"][0] + "--ops-alerts", Team: "ops", Channel: "alerts", WebhookID: "h1"},
	}, "", 0, "", "", 0))

	if got := validateTargetItems(t, p, "c1", "u1"); !slices.Equal(got, []string{"all"}) {
		t.Fatalf("non-member got %v, want only all", got)
	}
	if got := validateTargetItems(t, p, "", "u1"); !slices.Equal(got, []string{"all"}) {
		t.Fatalf("no channel got %v, want only all", got)
	}
}

func TestValidateModesArePlainWords(t *testing.T) {
	var validate *model.AutocompleteData
	for _, c := range getAutocompleteData().SubCommands {
		if c.Trigger != "config" {
			continue
		}
		for _, sc := range c.SubCommands {
			if sc.Trigger == "validate" {
				validate = sc
			}
		}
	}
	if validate == nil || len(validate.Arguments) != 2 {
		t.Fatalf("validate should offer exactly group then mode, got %+v", validate)
	}
	if validate.Arguments[0].Type != model.AutocompleteArgTypeDynamicList {
		t.Errorf("group hover must be a per-channel dynamic list")
	}
	modes, ok := validate.Arguments[1].Data.(*model.AutocompleteStaticListArg)
	if !ok {
		t.Fatalf("mode arg is not a static list")
	}
	var got []string
	for _, it := range modes.PossibleArguments {
		got = append(got, it.Item)
	}
	if want := []string{"end-to-end", "simulate", "webhook-test"}; !slices.Equal(got, want) {
		t.Fatalf("modes = %v, want %v", got, want)
	}
}

func TestModeWords(t *testing.T) {
	args := []string{"compute", "End-To-End", "webhook-test", "simulate", "runbook=x"}
	if i := indexOfMode(args, modeSimulate); i != 3 {
		t.Fatalf("simulate index = %d", i)
	}
	if indexOfMode(args, modeEndToEnd) != 1 || indexOfMode(args, modeWebhookTest) != 2 {
		t.Fatalf("mode words match case-insensitively")
	}
	rest := stripModes(args, modeWebhookTest, modeEndToEnd)
	if !slices.Equal(rest, []string{"compute", "simulate", "runbook=x"}) {
		t.Fatalf("rest = %v", rest)
	}
	if !strings.EqualFold(args[1], "end-to-end") {
		t.Fatal("stripModes must not modify its input")
	}
}
