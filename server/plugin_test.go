package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

type overrideSettingsAPI struct {
	plugin.API
	username, icon bool
}

func (a overrideSettingsAPI) GetConfig() *model.Config {
	cfg := &model.Config{}
	cfg.ServiceSettings.EnablePostUsernameOverride = &a.username
	cfg.ServiceSettings.EnablePostIconOverride = &a.icon
	return cfg
}

func TestOverrideSetupHintShowsUntilBothSettingsAreOn(t *testing.T) {
	cases := []struct {
		username, icon bool
		wantHint       bool
	}{
		{false, false, true},
		{true, false, true},
		{false, true, true},
		{true, true, false},
	}
	for _, c := range cases {
		p := &Plugin{}
		p.API = overrideSettingsAPI{username: c.username, icon: c.icon}
		hint := p.overrideSetupHint()
		if got := strings.Contains(hint, "Posts will show as you"); got != c.wantHint {
			t.Errorf("username=%v icon=%v: hint shown=%v, want %v (%q)", c.username, c.icon, got, c.wantHint, hint)
		}
		if c.wantHint && !strings.Contains(hint, "EnableIntegrationOverrides") {
			t.Errorf("hint does not name the plugin setting: %q", hint)
		}
	}
}
