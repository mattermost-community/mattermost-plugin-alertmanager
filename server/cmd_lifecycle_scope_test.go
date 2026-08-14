package main

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// fakeScopeAPI implements just the channel/team lookups configsForCurrentChannel
// and resolveReceiverName need. Everything else panics (embedded interface),
// which is fine for the cross-channel scoping tests below — they return before
// any save/webhook path runs.
type fakeScopeAPI struct {
	plugin.API
	channels map[string]*model.Channel
	teams    map[string]*model.Team
}

func (f *fakeScopeAPI) GetChannel(id string) (*model.Channel, *model.AppError) {
	if c, ok := f.channels[id]; ok {
		return c, nil
	}
	return nil, &model.AppError{StatusCode: 404}
}

func (f *fakeScopeAPI) GetTeam(id string) (*model.Team, *model.AppError) {
	if t, ok := f.teams[id]; ok {
		return t, nil
	}
	return nil, &model.AppError{StatusCode: 404}
}

// scopeTestPlugin wires a plugin whose only receivers are one bound to the
// caller's channel (ateam/alerts, channel id "chanA") and one bound to a
// different team+channel (bteam/secret). Used to prove remove/rotate can't reach
// the second from the first (CL-02).
func scopeTestPlugin() (*Plugin, *model.CommandArgs) {
	api := &fakeScopeAPI{
		channels: map[string]*model.Channel{
			"chanA": {Id: "chanA", Name: "alerts", TeamId: "teamA"},
		},
		teams: map[string]*model.Team{
			"teamA": {Id: "teamA", Name: "ateam"},
		},
	}
	p := &Plugin{}
	p.API = api
	p.setConfiguration(newConfiguration([]alertConfig{
		{Name: "high-cpu-usage--ateam-alerts", Team: "ateam", Channel: "alerts", WebhookID: "h1"},
		{Name: "high-cpu-usage--bteam-secret", Team: "bteam", Channel: "secret", WebhookID: "h2"},
	}, "", 0, "", "", 0))
	return p, &model.CommandArgs{ChannelId: "chanA", UserId: "u1"}
}

// TestRemoveOneRejectsCrossChannelName is the CL-02 regression for remove: a
// caller in ateam/alerts supplying another team's exact full receiver name must
// get "not found", and that receiver must remain in the config. Without the
// scope-membership guard the global exact-match filter would have deleted it.
func TestRemoveOneRejectsCrossChannelName(t *testing.T) {
	p, args := scopeTestPlugin()

	msg, err := p.handleRemoveOne(args, "high-cpu-usage--bteam-secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(msg, "not found") {
		t.Fatalf("expected not-found for a cross-channel name, got: %q", msg)
	}
	if !receiverNameInScope(p.getConfiguration().AlertConfigs, "high-cpu-usage--bteam-secret") {
		t.Fatalf("cross-channel receiver was removed — scoping failed")
	}
}

// TestRemoveOneShortNameStaysInChannel confirms a short base slug resolves to
// THIS channel's receiver, not the same-slug receiver in another team.
func TestRemoveOneShortNameResolvesInChannelOnly(t *testing.T) {
	p, args := scopeTestPlugin()

	// Both receivers share the base slug "high-cpu-usage". A short-name resolve
	// must pick the in-channel one; the cross-channel one is invisible here.
	scoped := p.configsForCurrentChannel(args)
	resolved := resolveReceiverName(scoped, "high-cpu-usage", args.ChannelId, p)
	if resolved != "high-cpu-usage--ateam-alerts" {
		t.Fatalf("short name resolved to %q, want the in-channel receiver", resolved)
	}
	if receiverNameInScope(scoped, "high-cpu-usage--bteam-secret") {
		t.Fatalf("cross-channel receiver leaked into the scoped set")
	}
}

// TestOldWebhookStatusLine is the B-003 regression: a successful delete says the
// old URL is dead; a failed delete must warn that it may still be live and name
// the webhook to remove by hand — never a false "no longer works".
func TestOldWebhookStatusLine(t *testing.T) {
	ok := oldWebhookStatusLine(true, "hook123")
	if !strings.Contains(ok, "no longer works") || strings.Contains(ok, "hook123") {
		t.Fatalf("deleted case should claim dead and not leak the ID: %q", ok)
	}
	warn := oldWebhookStatusLine(false, "hook123")
	if strings.Contains(warn, "no longer works") {
		t.Fatalf("failed-delete case must NOT claim the old URL is dead: %q", warn)
	}
	if !strings.Contains(warn, "hook123") || !strings.Contains(warn, "may still be live") {
		t.Fatalf("failed-delete case should warn and name the webhook: %q", warn)
	}
}
