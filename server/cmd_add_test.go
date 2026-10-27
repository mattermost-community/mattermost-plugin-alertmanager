package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
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

func TestAddRejectsBeforeAnySideEffect(t *testing.T) {
	p := &Plugin{}
	cases := map[string]string{
		"/alertmanager config add":                                       "Usage",
		"/alertmanager config add a b c d":                               "Usage",
		"/alertmanager config add ops alerts http://am:9093 compute":     "no longer arguments",
		"/alertmanager config add not-a-url":                             "Invalid Alertmanager URL",
		"/alertmanager config add http://am:9093 no-such-runbook":        "unknown target",
		"/alertmanager config add-custom":                                "Usage",
		"/alertmanager config add-custom ops alerts http://am:9093 slo":  "no longer arguments",
		"/alertmanager config add-custom not-a-url slo":                  "Invalid Alertmanager URL",
		"/alertmanager config add-custom http://am:9093 a b":             "Usage",
		"/alertmanager config add-custom http://am:9093 crd":             "Usage",
		"/alertmanager config add-custom http://am:9093 slo namespace=B": "Invalid `namespace`",
		"/alertmanager config add http://am:9093 compute namespace=B_d!": "Invalid `namespace`",
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
	dm := &channelLookupAPI{channels: map[string]*model.Channel{"dm1": {Id: "dm1", Type: model.ChannelTypeDirect}}}
	p := &Plugin{}
	p.API = dm
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
	dm := &channelLookupAPI{channels: map[string]*model.Channel{"dm1": {Id: "dm1", Type: model.ChannelTypeDirect}}}
	p := &Plugin{}
	p.API = dm
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
		"dm":   {Id: "dm", Type: model.ChannelTypeDirect},
		"gm":   {Id: "gm", Type: model.ChannelTypeGroup},
		"open": {Id: "open", Name: "alerts", TeamId: "t1", Type: model.ChannelTypeOpen},
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
		{"team channel reaches the permission check", "open", "plugin not fully initialized"},
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
// typeahead offers the URL first, then target, format, and rotation, and has no
// dynamic team or channel lists.
func TestAddAutocompleteHasNoTeamOrChannel(t *testing.T) {
	var config *model.AutocompleteData
	for _, c := range getAutocompleteData().SubCommands {
		if c.Trigger == "config" {
			config = c
		}
	}
	for _, trigger := range []string{"add", "add-custom"} {
		var node *model.AutocompleteData
		for _, c := range config.SubCommands {
			if c.Trigger == trigger {
				node = c
			}
		}
		if node == nil {
			t.Fatalf("config %s missing from typeahead", trigger)
		}
		for _, a := range node.Arguments {
			if a.Type == model.AutocompleteArgTypeDynamicList {
				t.Errorf("config %s still has a dynamic list argument: %q", trigger, a.HelpText)
			}
		}
	}

	// format is the argument after the URL (and target, for add).
	for trigger, wantArgs := range map[string]int{"add": 4, "add-custom": 4} {
		var node *model.AutocompleteData
		for _, c := range config.SubCommands {
			if c.Trigger == trigger {
				node = c
			}
		}
		if len(node.Arguments) != wantArgs {
			t.Fatalf("config %s has %d args, want %d (am-url, target/name, format, on)", trigger, len(node.Arguments), wantArgs)
		}
		format, ok := node.Arguments[2].Data.(*model.AutocompleteStaticListArg)
		if !ok || len(format.PossibleArguments) != 2 ||
			format.PossibleArguments[0].Item != "crd" || format.PossibleArguments[1].Item != "standard" {
			t.Fatalf("config %s third arg should list crd and standard, got %#v", trigger, node.Arguments[2].Data)
		}
		for _, it := range format.PossibleArguments {
			if it.HelpText == "" {
				t.Errorf("config %s format %q needs a hover explanation", trigger, it.Item)
			}
		}
	}
}
