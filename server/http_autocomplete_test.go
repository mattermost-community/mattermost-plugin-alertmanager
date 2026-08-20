package main

import "testing"

// TestExtractTeamSlugFromParsed pins the team-argument extraction that drives
// the dynamic channel autocomplete. The key regression it guards: `add-custom`
// must be recognized alongside `add` — both take <team> <channel> at the same
// positions, and both wire the channel dropdown to this handler. Missing
// add-custom left its channel autocomplete stuck on "_fill-team-first_" forever.
func TestExtractTeamSlugFromParsed(t *testing.T) {
	cases := []struct {
		name   string
		parsed string
		want   string
	}{
		{"add with team", "/alertmanager add starlight-alerting", "starlight-alerting"},
		{"add-custom with team", "/alertmanager add-custom starlight-alerting", "starlight-alerting"},
		{"add with team and channel typed", "/alertmanager add sre town-square", "sre"},
		{"add-custom with team and channel typed", "/alertmanager add-custom sre town-square", "sre"},
		{"no team yet", "/alertmanager add", ""},
		{"add-custom no team yet", "/alertmanager add-custom", ""},
		{"unrelated subcommand", "/alertmanager list town-square", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractTeamSlugFromParsed(tc.parsed); got != tc.want {
				t.Fatalf("extractTeamSlugFromParsed(%q) = %q, want %q", tc.parsed, got, tc.want)
			}
		})
	}
}
