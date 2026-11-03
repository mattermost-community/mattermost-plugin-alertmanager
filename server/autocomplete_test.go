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

func packItemNames(parsed, userInput string) []string {
	var out []string
	for _, it := range mattermostPackItems(parsed, userInput) {
		out = append(out, it.Item)
	}
	return out
}

func TestMattermostPackOffersNamespaceFlagOnlyForMattermost(t *testing.T) {
	const add = "/alertmanager config add http://am:9093 "
	cases := []struct {
		name, parsed, input string
		want                []string
	}{
		{"mattermost target", add + "mattermost ", add + "mattermost ", []string{"mm-namespace="}},
		{"mattermost then format", add + "mattermost crd ", add + "mattermost crd ", []string{"mm-namespace="}},
		{"format rules", "/alertmanager format rules mattermost ", "/alertmanager format rules mattermost ", []string{"mm-namespace="}},
		{"case insensitive", add + "Mattermost ", add + "Mattermost ", []string{"mm-namespace="}},
		{"other group", add + "compute ", add + "compute ", []string{}},
		{"format rules other word", "/alertmanager format rules crd ", "/alertmanager format rules crd ", []string{}},
		{"all", add + "all ", add + "all ", []string{}},
		{"already typed", add + "mattermost mm-namespace=x ", add + "mattermost mm-namespace=x ", []string{}},
		{"echoes the partial value", add + "mattermost crd ", add + "mattermost crd mm-namespace=matt", []string{"mm-namespace=matt"}},
		{"unrelated partial word", add + "mattermost ", add + "mattermost cr", []string{"mm-namespace="}},
		{"no parsed text", "", "", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := packItemNames(c.parsed, c.input)
			if got == nil {
				got = []string{}
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestMattermostPackHandlerReturnsJSONList(t *testing.T) {
	p := &Plugin{}
	req := httptest.NewRequest(http.MethodGet, mattermostPackPath+"?parsed=/alertmanager+config+add+u+mattermost+&user_input=/alertmanager+config+add+u+mattermost+", http.NoBody)
	rec := httptest.NewRecorder()
	p.handleAutocompleteMattermostPack(rec, req)

	var items []model.AutocompleteListItem
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if len(items) != 1 || items[0].Item != mmNamespaceFlag || items[0].HelpText == "" {
		t.Fatalf("got %+v", items)
	}

	rec = httptest.NewRecorder()
	p.handleAutocompleteMattermostPack(rec, httptest.NewRequest(http.MethodGet, mattermostPackPath+"?parsed=/alertmanager+config+add+u+all+", http.NoBody))
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("non-pack should be an empty JSON list, got %q", rec.Body.String())
	}
}

func TestMattermostPackFlagIsRegisteredOnAddAndRules(t *testing.T) {
	for _, path := range [][]string{{"config", "add"}, {"format", "rules"}} {
		node := getAutocompleteData()
		for _, trigger := range path {
			var next *model.AutocompleteData
			for _, sc := range node.SubCommands {
				if sc.Trigger == trigger {
					next = sc
				}
			}
			if next == nil {
				t.Fatalf("missing %v", path)
			}
			node = next
		}
		found := false
		for _, arg := range node.Arguments {
			if arg.Type == model.AutocompleteArgTypeDynamicList {
				found = true
			}
		}
		if !found {
			t.Errorf("%v has no dynamic list for %s", path, mmNamespaceFlag)
		}
	}
}
