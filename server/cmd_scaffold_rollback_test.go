package main

import (
	"net/http"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// fakeChannelAPI implements the team/channel lifecycle methods
// resolveOrCreateChannel and rollbackCreatedChannel use, recording created and
// deleted channels so the CL-01 rollback behavior can be asserted.
type fakeChannelAPI struct {
	plugin.API
	teams            map[string]*model.Team    // keyed by team name
	existingChannels map[string]*model.Channel // keyed by "<teamID>/<channelName>"
	created          []*model.Channel
	deleted          []string
	addedMembers     []string // "<channelID>/<userID>" recorded by AddChannelMember
}

func (f *fakeChannelAPI) AddChannelMember(channelID, userID string) (*model.ChannelMember, *model.AppError) {
	f.addedMembers = append(f.addedMembers, channelID+"/"+userID)
	return &model.ChannelMember{ChannelId: channelID, UserId: userID}, nil
}

func (f *fakeChannelAPI) GetTeamByName(name string) (*model.Team, *model.AppError) {
	if t, ok := f.teams[name]; ok {
		return t, nil
	}
	return nil, &model.AppError{StatusCode: http.StatusNotFound}
}

func (f *fakeChannelAPI) GetChannelByName(teamID, name string, _ bool) (*model.Channel, *model.AppError) {
	if c, ok := f.existingChannels[teamID+"/"+name]; ok {
		return c, nil
	}
	return nil, &model.AppError{StatusCode: http.StatusNotFound}
}

func (f *fakeChannelAPI) CreateChannel(ch *model.Channel) (*model.Channel, *model.AppError) {
	ch.Id = "new-" + ch.Name
	f.created = append(f.created, ch)
	return ch, nil
}

func (f *fakeChannelAPI) DeleteChannel(id string) *model.AppError {
	f.deleted = append(f.deleted, id)
	return nil
}

// TestResolveOrCreateChannelReportsCreated pins the created flag that drives
// CL-01 rollback: a pre-existing channel reports created=false (never a rollback
// candidate), a missing one is created and reports created=true.
func TestResolveOrCreateChannelReportsCreated(t *testing.T) {
	api := &fakeChannelAPI{
		teams: map[string]*model.Team{"ateam": {Id: "teamA", Name: "ateam"}},
		existingChannels: map[string]*model.Channel{
			"teamA/existing": {Id: "chExisting", Name: "existing", TeamId: "teamA"},
		},
	}
	p := &Plugin{}
	p.API = api

	rc, err := p.resolveOrCreateChannel("ateam", "existing", false, "")
	if err != nil || rc.created || rc.channelID != "chExisting" {
		t.Fatalf("pre-existing channel: %#v err=%v (want chExisting, created=false)", rc, err)
	}
	if rc.teamName != "ateam" || rc.channelName != "existing" {
		t.Fatalf("pre-existing channel returned non-canonical names: %#v", rc)
	}

	rc, err = p.resolveOrCreateChannel("ateam", "fresh", false, "")
	if err != nil || !rc.created || rc.channelID != "new-fresh" {
		t.Fatalf("missing channel: %#v err=%v (want new-fresh, created=true)", rc, err)
	}
	if rc.teamName != "ateam" || rc.channelName != "fresh" {
		t.Fatalf("created channel returned non-canonical names: %#v", rc)
	}
	if len(api.created) != 1 {
		t.Fatalf("expected exactly one channel created, got %d", len(api.created))
	}
}

// TestResolveOrCreateChannelPrivate covers the --private feature: a newly-created
// channel is PRIVATE and the caller is added as a member (a private channel is
// invisible to non-members and the webhook is minted with the caller's token). A
// pre-existing channel is returned as-is with no membership change.
func TestResolveOrCreateChannelPrivate(t *testing.T) {
	api := &fakeChannelAPI{
		teams: map[string]*model.Team{"ateam": {Id: "teamA", Name: "ateam"}},
		existingChannels: map[string]*model.Channel{
			"teamA/existing": {Id: "chExisting", Name: "existing", TeamId: "teamA"},
		},
	}
	p := &Plugin{}
	p.API = api

	// New private channel: created private, caller added.
	rc, err := p.resolveOrCreateChannel("ateam", "secret", true, "u1")
	if err != nil || !rc.created {
		t.Fatalf("private create: %#v err=%v (want created=true)", rc, err)
	}
	if len(api.created) != 1 || api.created[0].Type != model.ChannelTypePrivate {
		t.Fatalf("expected one PRIVATE channel, got %#v", api.created)
	}
	if want := "new-secret/u1"; len(api.addedMembers) != 1 || api.addedMembers[0] != want {
		t.Fatalf("caller not added to new private channel: %#v (want %q)", api.addedMembers, want)
	}

	// Pre-existing channel: no create, no membership change (even with private=true).
	rc, err = p.resolveOrCreateChannel("ateam", "existing", true, "u1")
	if err != nil || rc.created || rc.channelID != "chExisting" {
		t.Fatalf("pre-existing with --private: %#v err=%v (want chExisting, created=false)", rc, err)
	}
	if len(api.created) != 1 || len(api.addedMembers) != 1 {
		t.Fatalf("pre-existing channel should not create or add members: created=%d added=%d", len(api.created), len(api.addedMembers))
	}
}

// TestRollbackCreatedChannel is the CL-01 regression: a self-created channel is
// archived after a failed add, a pre-existing one is never touched, and an empty
// ID is a no-op.
func TestRollbackCreatedChannel(t *testing.T) {
	api := &fakeChannelAPI{}
	p := &Plugin{}
	p.API = api

	p.rollbackCreatedChannel(false, "chExisting")
	if len(api.deleted) != 0 {
		t.Fatalf("must never archive a pre-existing channel, deleted=%v", api.deleted)
	}

	p.rollbackCreatedChannel(true, "new-fresh")
	if len(api.deleted) != 1 || api.deleted[0] != "new-fresh" {
		t.Fatalf("expected the self-created channel archived, deleted=%v", api.deleted)
	}

	p.rollbackCreatedChannel(true, "")
	if len(api.deleted) != 1 {
		t.Fatalf("empty channel ID should be a no-op, deleted=%v", api.deleted)
	}
}
