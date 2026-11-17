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

func TestOverrideSetupHintNamesWhatIsStillOff(t *testing.T) {
	cases := []struct {
		username, icon bool
		want           []string
		notWant        []string
	}{
		{false, false, []string{"your name and profile picture", "override usernames", "override profile picture icons"}, nil},
		{true, false, []string{"your profile picture", "override profile picture icons"}, []string{"your name", "override usernames"}},
		{false, true, []string{"your name instead", "override usernames"}, []string{"profile picture"}},
	}
	for _, c := range cases {
		p := &Plugin{}
		p.API = overrideSettingsAPI{username: c.username, icon: c.icon}
		hint := p.overrideSetupHint()
		for _, w := range append(c.want, "Turn on Mattermost's webhook username and icon overrides") {
			if !strings.Contains(hint, w) {
				t.Errorf("username=%v icon=%v: hint lacks %q: %q", c.username, c.icon, w, hint)
			}
		}
		for _, w := range c.notWant {
			if strings.Contains(hint, w) {
				t.Errorf("username=%v icon=%v: hint wrongly says %q: %q", c.username, c.icon, w, hint)
			}
		}
	}

	p := &Plugin{}
	p.API = overrideSettingsAPI{username: true, icon: true}
	if hint := p.overrideSetupHint(); hint != "" {
		t.Errorf("both settings on: want no hint, got %q", hint)
	}
}
