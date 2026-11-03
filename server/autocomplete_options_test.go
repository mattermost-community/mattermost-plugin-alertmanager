package main

import (
	"slices"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func itemNames(items []model.AutocompleteListItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Item)
	}
	return out
}

func TestAddOptionItems(t *testing.T) {
	const url = "http://am:9093"
	add := "alertmanager config add " + url + " "
	custom := "alertmanager config add-custom " + url + " slo-burn "
	cases := []struct {
		name, parsed, input string
		want                []string
	}{
		{"mattermost offers format first", add + "mattermost ", add + "mattermost ", []string{"crd", "standard"}},
		{"then the namespace flag alone", add + "mattermost crd ", add + "mattermost crd ", []string{"mm-namespace="}},
		{"then on alone", add + "mattermost crd mm-namespace=x ", add + "mattermost crd mm-namespace=x ", []string{"on"}},
		{"nothing left", add + "mattermost crd mm-namespace=x on ", add + "mattermost crd mm-namespace=x on ", []string{}},
		{"other targets have no namespace flag", add + "compute crd ", add + "compute crd ", []string{"on"}},
		{"format skipped, namespace typed", add + "mattermost ", add + "mattermost mm-namespace=ma", []string{"crd", "standard", "mm-namespace=ma", "on"}},
		{"namespace value finished", add + "mattermost ", add + "mattermost mm-namespace=ma on", []string{"crd", "standard", "mm-namespace=ma", "on"}},
		{"namespace not offered once typed", add + "mattermost mm-namespace=x ", add + "mattermost mm-namespace=x ", []string{"crd", "standard"}},
		{"case insensitive target", add + "Mattermost ", add + "Mattermost ", []string{"crd", "standard"}},
		{"free slug keeps the options going", add, add + "high-cpu-usage crd", []string{"crd", "standard", "on", "high-cpu-usage"}},
		{"known group is not echoed", add, add + "compute crd", []string{"crd", "standard", "on"}},
		{"finished flag is consumed", add + "compute ", add + "compute namespace=monitoring crd", []string{"crd", "standard", "on", "namespace=monitoring"}},
		{"custom: format then on", custom, custom, []string{"crd", "standard"}},
		{"custom: on after format", custom + "crd ", custom + "crd ", []string{"on"}},
		{"choosing a target, no options yet", add, add, []string{}},
		{"no url yet", "alertmanager config add ", "alertmanager config add ", []string{}},
		{"slash prefix is fine", "/" + add + "mattermost ", "/" + add + "mattermost ", []string{"crd", "standard"}},
		{"another command", "alertmanager config remove " + url + " ", "alertmanager config remove " + url + " ", []string{}},
		{"another plugin", "other config add " + url + " ", "other config add " + url + " ", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := itemNames(addOptionItems(c.parsed, c.input))
			if !slices.Equal(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestAddOptionItemsKeepHoverText(t *testing.T) {
	for _, it := range addOptionItems("alertmanager config add http://am:9093 mattermost ", "alertmanager config add http://am:9093 mattermost ") {
		if it.HelpText == "" {
			t.Errorf("%q needs a hover explanation", it.Item)
		}
	}
	got := addOptionItems("alertmanager config add http://am:9093 mattermost crd ", "alertmanager config add http://am:9093 mattermost crd ")
	if len(got) != 1 || got[0].Hint != "<namespace>" {
		t.Fatalf("namespace item = %+v", got)
	}
}

func TestHelpTopicItems(t *testing.T) {
	cases := []struct {
		name, parsed string
		want         []string
		wantNot      []string
	}{
		{"top level", "alertmanager help ", []string{"config", "format", "silence", "admin"}, nil},
		{"one level down", "alertmanager help config ", []string{"add", "add-custom", "list", "remove"}, []string{"<name>"}},
		{"leaf has no children", "alertmanager help config add ", []string{}, nil},
		{"unknown path", "alertmanager help nope ", []string{}, nil},
		{"not the help command", "alertmanager config ", []string{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := itemNames(helpTopicItems(c.parsed))
			for _, w := range c.want {
				if !slices.Contains(got, w) {
					t.Errorf("missing %q in %v", w, got)
				}
			}
			for _, w := range c.wantNot {
				if slices.Contains(got, w) {
					t.Errorf("unexpected %q in %v", w, got)
				}
			}
			if len(c.want) == 0 && len(got) != 0 {
				t.Errorf("got %v, want none", got)
			}
		})
	}
	for _, it := range helpTopicItems("alertmanager help config ") {
		if it.HelpText == "" {
			t.Errorf("%q needs a hover explanation", it.Item)
		}
	}
}

func TestHelpWordOnLeafCommands(t *testing.T) {
	root := getAutocompleteData()
	var walk func(n *model.AutocompleteData, path string)
	walk = func(n *model.AutocompleteData, path string) {
		for _, c := range n.SubCommands {
			walk(c, path+" "+c.Trigger)
		}
		if len(n.SubCommands) > 0 || n.Trigger == "help" || len(n.Arguments) == 0 {
			return
		}
		list, ok := n.Arguments[0].Data.(*model.AutocompleteStaticListArg)
		if !ok {
			return
		}
		items := itemNames(list.PossibleArguments)
		if path == " silence create" {
			if slices.Contains(items, "help") {
				t.Errorf("%s: the TTL list must stay sorted by duration", path)
			}
			return
		}
		if !slices.Contains(items, "help") {
			t.Errorf("%s: first list lacks help: %v", path, items)
		}
	}
	walk(root, "")

	var add *model.AutocompleteData
	for _, c := range root.SubCommands {
		if c.Trigger != "config" {
			continue
		}
		for _, sc := range c.SubCommands {
			if sc.Trigger == "add" {
				add = sc
			}
		}
	}
	urls := itemNames(add.Arguments[0].Data.(*model.AutocompleteStaticListArg).PossibleArguments)
	if !slices.Contains(urls, "help") {
		t.Fatalf("config add must offer help after the command, got %v", urls)
	}
}

func TestRulesOptionItems(t *testing.T) {
	const rules = "/alertmanager format rules "
	groups := itemNames(rulesGroupItems())
	first := slices.Concat(groups, []string{"help"})
	cases := []struct {
		name, parsed, input string
		want                []string
	}{
		{"nothing yet: groups only", rules, rules, first},
		{"group chosen: crd and standard", rules + "security ", rules + "security ", []string{"crd", "standard"}},
		{"format chosen: groups", rules + "crd ", rules + "crd ", groups},
		{"group and format: nothing left", rules + "security crd ", rules + "security crd ", []string{}},
		{"mattermost: namespace before the format", rules + "mattermost ", rules + "mattermost ", []string{"mm-namespace="}},
		{"mattermost, namespace given: format last", rules + "mattermost mm-namespace=x ", rules + "mattermost mm-namespace=x ", []string{"crd", "standard"}},
		{"mattermost and format: namespace", rules + "mattermost crd ", rules + "mattermost crd ", []string{"mm-namespace="}},
		{"format then mattermost: namespace", rules + "crd mattermost ", rules + "crd mattermost ", []string{"mm-namespace="}},
		{"namespace typed once", rules + "mattermost crd mm-namespace=x ", rules + "mattermost crd mm-namespace=x ", []string{}},
		{"partial namespace is echoed", rules + "mattermost crd ", rules + "mattermost crd mm-namespace=ma", []string{"mm-namespace=ma"}},
		{"partial namespace keeps the format option", rules + "mattermost ", rules + "mattermost mm-namespace=ma", []string{"mm-namespace=ma", "crd", "standard"}},
		{"runbook slug counts as a group", rules + "high-cpu-usage ", rules + "high-cpu-usage ", []string{"crd", "standard"}},
		{"free slug is consumed", rules, rules + "high-cpu-usage crd", slices.Concat(first, []string{"crd", "standard", "high-cpu-usage"})},
		{"finished flag is consumed", rules + "security ", rules + "security namespace=monitoring crd", []string{"crd", "standard", "namespace=monitoring"}},
		{"another command", "/alertmanager format export ", "/alertmanager format export ", []string{}},
		{"no parsed text", "", "", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := itemNames(rulesOptionItems(c.parsed, c.input))
			if !slices.Equal(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestFormatRulesAutocompleteUsesRulesOptions(t *testing.T) {
	var rules *model.AutocompleteData
	for _, format := range getAutocompleteData().SubCommands {
		if format.Trigger != "format" {
			continue
		}
		for _, sub := range format.SubCommands {
			if sub.Trigger == "rules" {
				rules = sub
			}
		}
	}
	if rules == nil || len(rules.Arguments) != rulesOptionSlots {
		t.Fatalf("format rules should have %d option slots, got %+v", rulesOptionSlots, rules)
	}
	for _, arg := range rules.Arguments {
		list, ok := arg.Data.(*model.AutocompleteDynamicListArg)
		if !ok || !arg.Required || list.FetchURL != rulesOptionsFetchURL() {
			t.Fatalf("slot = %+v", arg)
		}
	}
}
