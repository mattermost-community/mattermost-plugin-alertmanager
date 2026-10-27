package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

func TestParseConfigPositionals(t *testing.T) {
	cases := []struct {
		name     string
		in       []string
		wantKind configKind
		wantName string
		wantErr  string
	}{
		{name: "bare lists", in: nil, wantKind: configKindList},
		{name: "list", in: []string{"list"}, wantKind: configKindList},
		{name: "receiver name", in: []string{"compute--team-chan"}, wantKind: configKindDetail, wantName: "compute--team-chan"},
		{name: "name with extra args", in: []string{"some-name", "extra"}, wantErr: "unrecognized"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseConfigPositionals(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.kind != tc.wantKind || got.name != tc.wantName {
				t.Fatalf("got %+v, want kind=%d name=%q", got, tc.wantKind, tc.wantName)
			}
		})
	}
}

func TestWithoutParentDropsGroupWord(t *testing.T) {
	args := &model.CommandArgs{Command: "/alertmanager config add team chan http://am:9093 compute", UserId: "u1"}
	got := withoutParent(args)
	if want := "/alertmanager add team chan http://am:9093 compute"; got.Command != want {
		t.Fatalf("Command = %q, want %q", got.Command, want)
	}
	if got.UserId != "u1" {
		t.Fatalf("other fields must carry over, UserId = %q", got.UserId)
	}
	if args.Command != "/alertmanager config add team chan http://am:9093 compute" {
		t.Fatal("withoutParent must not mutate the caller's args")
	}
}

func TestParseRepairFlags(t *testing.T) {
	cases := []struct {
		in          []string
		force, prun bool
		ok          bool
	}{
		{nil, false, false, true},
		{[]string{"prune"}, false, true, true},
		{[]string{"force"}, true, false, true},
		{[]string{"force", "prune"}, true, true, true},
		{[]string{"prun"}, false, false, false},
		{[]string{"--force"}, false, false, false},
	}
	for _, tc := range cases {
		force, prune, ok := parseRepairFlags(tc.in)
		if force != tc.force || prune != tc.prun || ok != tc.ok {
			t.Errorf("parseRepairFlags(%v) = %v,%v,%v want %v,%v,%v", tc.in, force, prune, ok, tc.force, tc.prun, tc.ok)
		}
	}
}

// TestRemovedTopLevelCommandsAreUnknown pins the decision to drop the old
// names outright rather than alias them.
func TestRemovedTopLevelCommandsAreUnknown(t *testing.T) {
	p := &Plugin{}
	for _, old := range []string{
		"show", "list", "status", "silences", "expire_silence", "reconcile", "repair",
		"add", "add-custom", "remove", "rotate", "validate", "export",
		"acknowledge", "allowlist", "metrics-token",
	} {
		got := p.executeCommand(&model.CommandArgs{Command: "/alertmanager " + old})
		if !strings.Contains(got, "Unknown subcommand") {
			t.Errorf("/alertmanager %s = %q, want Unknown subcommand", old, got)
		}
	}
}

// TestDashFlagsAreRejectedWithTheNewSpelling pins that `--word` is gone
// everywhere and the reply shows the plain-word rewrite.
func TestDashFlagsAreRejectedWithTheNewSpelling(t *testing.T) {
	p := &Plugin{}
	cases := map[string]string{
		"/alertmanager admin repair --prune":                               "Write `prune`",
		"/alertmanager config remove all --force":                          "Write `force`",
		"/alertmanager config rotate all --overdue":                        "Write `overdue`",
		"/alertmanager config add http://am:9093 --namespace=x":            "Write `namespace=x`",
		"/alertmanager config add http://am:9093 --webhook-host=http://mm": "Write `webhook-host=http://mm`",
		"/alertmanager config validate --end-to-end":                       "Write `end-to-end`",
		"/alertmanager config add http://am:9093 --private":                "`--private` was removed",
		"/alertmanager config add http://am:9093 --format=crd":             "plain word",
		"/alertmanager rules --format=operator":                            "format rules crd",
	}
	for cmd, want := range cases {
		if got := p.executeCommand(&model.CommandArgs{Command: cmd}); !strings.Contains(got, want) {
			t.Errorf("%q = %q, want containing %q", cmd, got, want)
		}
	}
}

func TestGroupUsageReplies(t *testing.T) {
	p := &Plugin{}
	cases := map[string]string{
		"/alertmanager alerts bogus":               "alerts status",
		"/alertmanager admin":                      "admin repair",
		"/alertmanager admin bogus":                "admin repair",
		"/alertmanager config export yaml":         "moved",
		"/alertmanager config diff crd":            "format diff crd",
		"/alertmanager rules":                      "format rules",
		"/alertmanager format":                     "format export",
		"/alertmanager format bogus":               "format export",
		"/alertmanager format export yaml":         "Unknown format",
		"/alertmanager format rules a b":           "Too many words",
		"/alertmanager format diff namespace=B_d!": "Invalid `namespace`",
		"/alertmanager config a b":                 "config add-custom",
	}
	for cmd, want := range cases {
		if got := p.executeCommand(&model.CommandArgs{Command: cmd}); !strings.Contains(got, want) {
			t.Errorf("%q = %q, want containing %q", cmd, got, want)
		}
	}
}

// TestAutocompleteTreeShape guards two Mattermost typeahead rules: a node with
// arguments never searches its subcommands, so a node must hold one or the
// other, and top-level order is alphabetical.
func TestAutocompleteTreeShape(t *testing.T) {
	root := getAutocompleteData()

	var walk func(n *model.AutocompleteData, path string)
	walk = func(n *model.AutocompleteData, path string) {
		if len(n.SubCommands) > 0 && len(n.Arguments) > 0 {
			t.Errorf("%s has both subcommands and arguments, so typeahead would ignore the subcommands", path)
		}
		for _, c := range n.SubCommands {
			walk(c, path+" "+c.Trigger)
		}
	}
	walk(root, "/"+commandTrigger)

	names := func(n *model.AutocompleteData) []string {
		out := make([]string, 0, len(n.SubCommands))
		for _, c := range n.SubCommands {
			out = append(out, c.Trigger)
		}
		return out
	}
	child := func(parent *model.AutocompleteData, trigger string) *model.AutocompleteData {
		for _, c := range parent.SubCommands {
			if c.Trigger == trigger {
				return c
			}
		}
		t.Fatalf("%s has no %q subcommand", parent.Trigger, trigger)
		return nil
	}

	wantTop := []string{"about", "admin", "alerts", "config", "docs", "format", "help", "silence"}
	if got := names(root); !slices.Equal(got, wantTop) {
		t.Fatalf("top-level = %v, want %v", got, wantTop)
	}
	wantGroups := map[string][]string{
		"admin":   {"acknowledge", "allowlist", "metrics-token", "repair", "help"},
		"alerts":  {"firing", "status", "help"},
		"config":  {"list", "add", "add-custom", "remove", "rotate", "validate", "help"},
		"format":  {"export", "diff", "rules", "help"},
		"silence": {"create", "list", "expire", "help"},
	}
	for group, want := range wantGroups {
		got := names(child(root, group))
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s children = %v, want %v", group, got, want)
		}
	}

	for _, verb := range []string{"export", "diff", "rules"} {
		if got := len(child(child(root, "format"), verb).Arguments); got != 1 {
			t.Errorf("format %s has %d args, want 1 format list", verb, got)
		}
	}
}
