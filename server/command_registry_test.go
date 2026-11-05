package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// accessAPI answers the permission lookups the access checks make.
type accessAPI struct {
	plugin.API
	sysadmin  bool
	teamAdmin bool
}

func (f *accessAPI) HasPermissionTo(string, *model.Permission) bool { return f.sysadmin }
func (f *accessAPI) LogWarn(string, ...any)                         {}

func (f *accessAPI) GetChannel(id string) (*model.Channel, *model.AppError) {
	return &model.Channel{Id: id, TeamId: "t1", Type: model.ChannelTypeOpen}, nil
}

func (f *accessAPI) GetTeamMember(string, string) (*model.TeamMember, *model.AppError) {
	roles := "team_user"
	if f.teamAdmin {
		roles += " team_admin"
	}
	return &model.TeamMember{Roles: roles}, nil
}

func accessPlugin(api *accessAPI) *Plugin {
	p := &Plugin{}
	p.API = api
	p.client = pluginapi.NewClient(api, nil)
	return p
}

func TestEveryVerbHasItsAccessLevel(t *testing.T) {
	cases := []struct {
		line   string
		name   string
		access accessLevel
	}{
		{"/alertmanager about", "about", accessAnyone},
		{"/alertmanager docs", "docs", accessAnyone},
		{"/alertmanager alerts", "alerts", accessAnyone},
		{"/alertmanager alerts firing", "alerts", accessAnyone},
		{"/alertmanager alerts status", "alerts status", accessAnyone},
		{"/alertmanager config", "config list", accessAnyone},
		{"/alertmanager config list", "config list", accessAnyone},
		{"/alertmanager config some-receiver", "config detail", accessChannelAdmin},
		{"/alertmanager config add http://am", "config add", accessChannelAdmin},
		{"/alertmanager config add-custom http://am x", "config add-custom", accessChannelAdmin},
		{"/alertmanager config remove x", "config remove", accessChannelAdmin},
		{"/alertmanager config rotate x", "config rotate", accessChannelAdmin},
		{"/alertmanager config validate", "config validate", accessChannelAdmin},
		{"/alertmanager format rules", "format rules", accessAnyone},
		{"/alertmanager format export", "format export", accessChannelAdmin},
		{"/alertmanager format diff", "format diff", accessSystemAdmin},
		{"/alertmanager silence list", "silence list", accessAnyone},
		{"/alertmanager silence create 1h compute", "silence create", accessChannelAdmin},
		{"/alertmanager silence 1h compute", "silence create", accessChannelAdmin},
		{"/alertmanager silence expire x y", "silence expire", accessChannelAdmin},
		{"/alertmanager admin repair", "admin repair", accessSystemAdmin},
		{"/alertmanager admin acknowledge", "admin acknowledge", accessSystemAdmin},
		{"/alertmanager admin allowlist", "admin allowlist", accessSystemAdmin},
		{"/alertmanager admin metrics-token", "admin metrics-token", accessSystemAdmin},
	}
	p := &Plugin{}
	for _, c := range cases {
		parsed := p.parseCommand(&model.CommandArgs{Command: c.line})
		if parsed.cmd == nil {
			t.Errorf("%q: no command, reply %q", c.line, parsed.reply)
			continue
		}
		got, ok := parsed.cmd.(verbCommand)
		if !ok || got.Name() != c.name || got.access != c.access {
			t.Errorf("%q: got %q access %d, want %q access %d", c.line, got.Name(), got.access, c.name, c.access)
		}
	}
}

func TestRegistryDeniesBeforeTheHandlerRuns(t *testing.T) {
	cases := []struct {
		line      string
		sysadmin  bool
		teamAdmin bool
		want      string
	}{
		{"/alertmanager config remove all", false, false, "team_admin"},
		{"/alertmanager config add http://am:9093", false, false, "team_admin"},
		{"/alertmanager silence create 1h compute", false, false, "team_admin"},
		{"/alertmanager silence expire x y", false, false, "team_admin"},
		{"/alertmanager format export", false, false, "team_admin"},
		{"/alertmanager format diff", false, true, "system_admin"},
		{"/alertmanager admin repair", false, true, "system_admin"},
		{"/alertmanager admin allowlist", false, true, "system_admin"},
		{"/alertmanager admin metrics-token", false, true, "system_admin"},
		{"/alertmanager admin acknowledge", false, true, "system_admin"},
	}
	for _, c := range cases {
		p := accessPlugin(&accessAPI{sysadmin: c.sysadmin, teamAdmin: c.teamAdmin})
		got := p.executeCommand(&model.CommandArgs{Command: c.line, ChannelId: "c1", UserId: "u1"})
		if !strings.Contains(got, c.want) {
			t.Errorf("%q = %q, want a denial mentioning %q", c.line, got, c.want)
		}
	}
}

func TestRegistryLetsTeamAdminsRunChannelCommands(t *testing.T) {
	p := accessPlugin(&accessAPI{teamAdmin: true})
	got := p.executeCommand(&model.CommandArgs{Command: "/alertmanager silence create", ChannelId: "c1", UserId: "u1"})
	if strings.Contains(got, "team_admin") || !strings.Contains(got, "Allowed times") {
		t.Fatalf("a team admin should reach the handler, got %q", got)
	}
}
