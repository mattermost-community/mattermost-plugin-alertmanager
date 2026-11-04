package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

type channelLookupAPI struct {
	plugin.API
	channels map[string]*model.Channel
}

func (f *channelLookupAPI) GetChannel(id string) (*model.Channel, *model.AppError) {
	if c, ok := f.channels[id]; ok {
		return c, nil
	}
	return nil, &model.AppError{Message: "not found", StatusCode: 404}
}

func TestRemovedAddSyntaxMessage(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"old team and channel form", []string{"ops", "alerts", "http://am:9093", "compute"}, "no longer arguments"},
		{"old form with on", []string{"ops", "alerts", "http://am:9093", "on"}, "no longer arguments"},
		{"old custom form", []string{"ops", "alerts", "http://am:9093", "slo-burn"}, "no longer arguments"},
		{"new form", []string{"http://am:9093", "compute", "crd", "on"}, ""},
		{"new custom form", []string{"http://am:9093", "slo-burn", "webhook-host=http://mm:8065"}, ""},
		{"url only", []string{"http://am:9093"}, ""},
		{"nothing", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := removedAddSyntaxMessage(tc.args)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("new syntax rejected: %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want containing %q", got, tc.want)
			}
		})
	}
}

// sysadminPlugin is a plugin whose caller passes every access check, so a test
// can reach the handler it is about.
func sysadminPlugin(api plugin.API) *Plugin {
	p := &Plugin{}
	p.API = &sysadminAPI{API: api}
	p.client = pluginapi.NewClient(p.API, nil)
	return p
}

type sysadminAPI struct{ plugin.API }

func (f *sysadminAPI) HasPermissionTo(string, *model.Permission) bool { return true }

func TestAddRejectsBeforeAnySideEffect(t *testing.T) {
	p := sysadminPlugin(&channelLookupAPI{channels: map[string]*model.Channel{"c1": {Id: "c1", Type: model.ChannelTypeOpen, TeamId: "t1"}}})
	cases := map[string]string{
		"/alertmanager config add":                                               "Usage",
		"/alertmanager config add a b c d":                                       "Usage",
		"/alertmanager config add ops alerts http://am:9093 compute":             "no longer arguments",
		"/alertmanager config add not-a-url":                                     "Invalid Alertmanager URL",
		"/alertmanager config add http://am:9093 no-such-runbook":                "unknown target",
		"/alertmanager config add-custom":                                        "Usage",
		"/alertmanager config add-custom ops alerts http://am:9093 slo":          "no longer arguments",
		"/alertmanager config add-custom not-a-url slo":                          "Invalid Alertmanager URL",
		"/alertmanager config add-custom http://am:9093 a b":                     "Usage",
		"/alertmanager config add-custom http://am:9093 crd":                     "Usage",
		"/alertmanager config add-custom http://am:9093 slo namespace=B":         "Invalid `namespace`",
		"/alertmanager config add http://am:9093 compute namespace=B_d!":         "Invalid `namespace`",
		"/alertmanager config add http://am:9093 compute mm-namespace=chat":      "only apply to the `mattermost` target",
		"/alertmanager config add http://am:9093 mattermost mm-namespace=Bad_NS": "mm-namespace",
		"/alertmanager config add http://am:9093 mattermost-operator":            "unknown target",
	}
	for cmd, want := range cases {
		got := p.executeCommand(&model.CommandArgs{Command: cmd, ChannelId: "c1", UserId: "u1"})
		if !strings.Contains(got, want) {
			t.Errorf("%q = %q, want containing %q", cmd, got, want)
		}
	}
}

// TestAddFormatAndOnAreWordsInAnyOrder checks that `standard`, `crd` and `on`
// are consumed as words rather than mistaken for the target: each command gets
// past argument parsing and stops at the destination check.
func TestAddFormatAndOnAreWordsInAnyOrder(t *testing.T) {
	p := sysadminPlugin(&channelLookupAPI{channels: map[string]*model.Channel{"dm1": {Id: "dm1", Type: model.ChannelTypeDirect}}})
	for _, cmd := range []string{
		"/alertmanager config add http://am:9093",
		"/alertmanager config add http://am:9093 crd",
		"/alertmanager config add http://am:9093 compute crd on",
		"/alertmanager config add http://am:9093 on crd compute",
		"/alertmanager config add http://am:9093 STANDARD",
	} {
		got := p.executeCommand(&model.CommandArgs{Command: cmd, ChannelId: "dm1", UserId: "u1"})
		if !strings.Contains(got, "direct or group message") {
			t.Errorf("%q = %q, want to reach the destination check", cmd, got)
		}
	}
}

func TestAddCustomFormatAndOnAreWords(t *testing.T) {
	p := sysadminPlugin(&channelLookupAPI{channels: map[string]*model.Channel{"dm1": {Id: "dm1", Type: model.ChannelTypeDirect}}})
	for _, cmd := range []string{
		"/alertmanager config add-custom http://am:9093 slo",
		"/alertmanager config add-custom http://am:9093 slo crd",
		"/alertmanager config add-custom http://am:9093 slo crd on namespace=monitoring",
		"/alertmanager config add-custom http://am:9093 on slo STANDARD",
	} {
		got := p.executeCommand(&model.CommandArgs{Command: cmd, ChannelId: "dm1", UserId: "u1"})
		if !strings.Contains(got, "direct or group message") {
			t.Errorf("%q = %q, want to reach the destination check", cmd, got)
		}
	}
}

func TestSplitAddWords(t *testing.T) {
	format, on, pos := splitAddWords([]string{"http://am:9093", "CRD", "compute", "On"})
	if format != formatCRD || !on || len(pos) != 2 || pos[0] != "http://am:9093" || pos[1] != "compute" {
		t.Fatalf("got %q %v %v", format, on, pos)
	}
	format, on, pos = splitAddWords([]string{"http://am:9093"})
	if format != formatStandard || on || len(pos) != 1 {
		t.Fatalf("defaults wrong: %q %v %v", format, on, pos)
	}
}

func TestCurrentChannelDestination(t *testing.T) {
	api := &channelLookupAPI{channels: map[string]*model.Channel{
		"dm": {Id: "dm", Type: model.ChannelTypeDirect},
		"gm": {Id: "gm", Type: model.ChannelTypeGroup},
	}}
	p := &Plugin{}
	p.API = api

	cases := []struct {
		name      string
		channelID string
		want      string
	}{
		{"no channel", "", "Run this command in the channel"},
		{"unknown channel", "missing", "Could not look up this channel"},
		{"direct message", "dm", "direct or group message"},
		{"group message", "gm", "direct or group message"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, msg := p.currentChannelDestination(&model.CommandArgs{ChannelId: tc.channelID, UserId: "u1"})
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("msg = %q, want containing %q", msg, tc.want)
			}
		})
	}
}

// TestAddAutocompleteHasNoTeamOrChannel pins the channel-implied design: the
// typeahead offers the URL first, then the target (or custom name), then the
// trailing options, and has no dynamic team or channel lists.
func TestAddAutocompleteHasNoTeamOrChannel(t *testing.T) {
	var config *model.AutocompleteData
	for _, c := range getAutocompleteData().SubCommands {
		if c.Trigger == "config" {
			config = c
		}
	}
	for trigger, slots := range map[string]int{"add": addOptionSlots, "add-custom": addCustomOptionSlots} {
		var node *model.AutocompleteData
		for _, c := range config.SubCommands {
			if c.Trigger == trigger {
				node = c
			}
		}
		if node == nil {
			t.Fatalf("config %s missing from typeahead", trigger)
		}
		if len(node.Arguments) != 2+slots {
			t.Fatalf("config %s has %d args, want am-url, target/name and %d option slots", trigger, len(node.Arguments), slots)
		}
		if node.Arguments[0].Type != model.AutocompleteArgTypeStaticList {
			t.Errorf("config %s first arg must be the Alertmanager URL list", trigger)
		}
		for _, a := range node.Arguments[2:] {
			dyn, ok := a.Data.(*model.AutocompleteDynamicListArg)
			if !ok || dyn.FetchURL != addOptionsFetchURL() || !a.Required {
				t.Errorf("config %s option slot must be a required dynamic list on the add-options path: %+v", trigger, a)
			}
		}
	}
}
