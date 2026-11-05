package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestParseHelpRequest(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		ok   bool
		path []string
	}{
		{"bare help", []string{"help"}, true, nil},
		{"help path", []string{"help", "config", "add"}, true, []string{"config", "add"}},
		{"trailing help on a group", []string{"config", "help"}, true, []string{"config"}},
		{"trailing help on a leaf", []string{"config", "add", "help"}, true, []string{"config", "add"}},
		{"case insensitive", []string{"Config", "HELP"}, true, []string{"Config"}},
		{"flags are not help", []string{"config", "add", "--help"}, false, nil},
		{"short flag is not help", []string{"silence", "-h"}, false, nil},
		{"not help", []string{"alerts", "status"}, false, nil},
		{
			"custom receiver named help is an argument",
			[]string{"config", "add-custom", "ops", "alerts", "http://am:9093", "help"},
			false, nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, ok := parseHelpRequest(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && !slices.Equal(path, tc.path) {
				t.Fatalf("path = %v, want %v", path, tc.path)
			}
		})
	}
}

func TestResolveHelpPath(t *testing.T) {
	cases := []struct {
		in       []string
		path     string
		consumed int
	}{
		{nil, "", 0},
		{[]string{"config", "add"}, "config add", 2},
		{[]string{"CONFIG", "Add"}, "config add", 2},
		{[]string{"config", "high-cpu-usage--ops-alerts"}, "config <name>", 2},
		{[]string{"alerts", "bogus"}, "alerts", 1},
		{[]string{"bogus"}, "", 0},
	}
	for _, tc := range cases {
		path, consumed := resolveHelpPath(tc.in)
		if path != tc.path || consumed != tc.consumed {
			t.Errorf("resolveHelpPath(%v) = %q,%d want %q,%d", tc.in, path, consumed, tc.path, tc.consumed)
		}
	}
}

func TestRenderHelpDetailGrowsWithDepth(t *testing.T) {
	top := renderHelp(nil)
	for _, name := range []string{"about", "admin", "alerts", "config", "docs", "format", "help", "silence"} {
		if !strings.Contains(top, "`"+name+"`") {
			t.Errorf("top-level help is missing %q", name)
		}
	}
	for _, deeper := range []string{"**Options**", "**Examples**", "**Who can run it:**"} {
		if strings.Contains(top, deeper) {
			t.Errorf("top-level help should stay simple, found %q", deeper)
		}
	}

	group := renderHelp([]string{"silence"})
	for _, want := range []string{"silence create", "silence list", "silence expire", "**Examples**"} {
		if !strings.Contains(group, want) {
			t.Errorf("group help is missing %q", want)
		}
	}
	if strings.Contains(group, "**Options**") {
		t.Error("group help should list subcommands, not options")
	}

	leaf := renderHelp([]string{"config", "add"})
	for _, want := range []string{"**Options**", "**Examples**", "**Who can run it:**", "crd"} {
		if !strings.Contains(leaf, want) {
			t.Errorf("leaf help is missing %q", want)
		}
	}

	if got := renderHelp([]string{"alerts", "bogus"}); !strings.Contains(got, "No help for") {
		t.Errorf("unknown topic should say so, got:\n%s", got)
	}
}

// TestHelpRegistryMatchesAutocomplete keeps help from drifting: every command
// in the typeahead tree has a help topic, and every topic is a real command.
func TestHelpRegistryMatchesAutocomplete(t *testing.T) {
	inTree := map[string]bool{}
	var walk func(n *model.AutocompleteData, path string)
	walk = func(n *model.AutocompleteData, path string) {
		for _, c := range n.SubCommands {
			p := strings.TrimSpace(path + " " + c.Trigger)
			if c.Trigger != "help" || path == "" {
				inTree[p] = true
			}
			walk(c, p)
		}
	}
	walk(getAutocompleteData(), "")

	for p := range inTree {
		if findHelpTopic(p) == nil {
			t.Errorf("command %q has no help topic", p)
		}
	}
	for _, topic := range helpRegistry {
		if strings.Contains(topic.Path, "<") {
			continue
		}
		if !inTree[topic.Path] {
			t.Errorf("help topic %q is not a command", topic.Path)
		}
		if topic.Summary == "" || topic.Usage == "" || len(topic.Examples) == 0 {
			t.Errorf("help topic %q needs a summary, usage, and example", topic.Path)
		}
	}
}

func TestExecuteCommandRoutesHelp(t *testing.T) {
	p := &Plugin{}
	cases := map[string]string{
		"/alertmanager":                           "**Usage:** `/alertmanager <command>",
		"/alertmanager help":                      "**Commands**",
		"/alertmanager config help":               "config add-custom",
		"/alertmanager config add help":           "**Examples**",
		"/alertmanager help config add":           "**Options**",
		"/alertmanager silence create help":       "silence create",
		"/alertmanager admin repair help":         "prune",
		"/alertmanager config <name> help":        "config <name>",
		"/alertmanager config some-receiver help": "config <name>",
	}
	for cmd, want := range cases {
		if got := p.executeCommand(&model.CommandArgs{Command: cmd}); !strings.Contains(got, want) {
			t.Errorf("%q = %q, want containing %q", cmd, got, want)
		}
	}
}

// TestTypeaheadIsAlphabetical pins the dropdown order: subcommands and the
// choices in every static list are alphabetical.
func TestTypeaheadIsAlphabetical(t *testing.T) {
	var check func(n *model.AutocompleteData, path string)
	check = func(n *model.AutocompleteData, path string) {
		var triggers []string
		for _, c := range n.SubCommands {
			triggers = append(triggers, strings.ToLower(c.Trigger))
		}
		if !slices.IsSorted(triggers) {
			t.Errorf("%q subcommands not alphabetical: %v", path, triggers)
		}
		for _, arg := range n.Arguments {
			list, ok := arg.Data.(*model.AutocompleteStaticListArg)
			if !ok {
				continue
			}
			var items []string
			for _, it := range list.PossibleArguments {
				items = append(items, strings.ToLower(it.Item))
			}
			if path == "silence create" && items[0] == "30m" {
				continue
			}
			if !slices.IsSorted(items) {
				t.Errorf("%q list %q not alphabetical: %v", path, arg.Name, items)
			}
		}
		for _, c := range n.SubCommands {
			check(c, strings.TrimSpace(path+" "+c.Trigger))
		}
	}
	check(getAutocompleteData(), "")
}

func TestSilenceTTLsGoByDuration(t *testing.T) {
	var got []string
	for _, c := range getAutocompleteData().SubCommands {
		if c.Trigger != "silence" {
			continue
		}
		for _, sc := range c.SubCommands {
			if sc.Trigger != "create" {
				continue
			}
			list := sc.Arguments[0].Data.(*model.AutocompleteStaticListArg)
			for _, it := range list.PossibleArguments {
				got = append(got, it.Item)
			}
		}
	}
	if want := []string{"30m", "1h", "4h", "24h", "72h"}; !slices.Equal(got, want) {
		t.Fatalf("TTL order = %v, want %v", got, want)
	}
}
