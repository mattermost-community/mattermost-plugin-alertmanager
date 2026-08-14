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

	id, created, err := p.resolveOrCreateChannel("ateam", "existing", false, "")
	if err != nil || created || id != "chExisting" {
		t.Fatalf("pre-existing channel: id=%q created=%v err=%v (want chExisting, false, nil)", id, created, err)
	}

	id, created, err = p.resolveOrCreateChannel("ateam", "fresh", false, "")
	if err != nil || !created || id != "new-fresh" {
		t.Fatalf("missing channel: id=%q created=%v err=%v (want new-fresh, true, nil)", id, created, err)
	}
	if len(api.created) != 1 {
		t.Fatalf("expected exactly one channel created, got %d", len(api.created))
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
