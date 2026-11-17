package main

import (
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
)

// addDestination is where add and add-custom bind receivers: the channel the
// command ran in.
type addDestination struct {
	channelID string
	team      string
	channel   string
}

// currentChannelDestination resolves the channel the command ran in and checks
// the caller may manage it. The message is user-facing and non-empty on failure.
// Direct and group messages are rejected because a receiver needs a team
// channel to live in.
func (p *Plugin) currentChannelDestination(args *model.CommandArgs) (addDestination, string) {
	if args.ChannelId == "" {
		return addDestination{}, "Run this command in the channel that should receive the alerts."
	}
	channel, appErr := p.API.GetChannel(args.ChannelId)
	if appErr != nil {
		return addDestination{}, fmt.Sprintf("Could not look up this channel: %v", appErr)
	}
	if channel.Type != model.ChannelTypeOpen && channel.Type != model.ChannelTypePrivate {
		return addDestination{}, "Receivers cannot be added in a direct or group message. Run this in a team channel."
	}
	if err := p.requireChannelTeamAdmin(args.UserId, args.ChannelId); err != nil {
		return addDestination{}, err.Error()
	}
	team, appErr := p.API.GetTeam(channel.TeamId)
	if appErr != nil {
		return addDestination{}, fmt.Sprintf("Could not look up this channel's team: %v", appErr)
	}
	return addDestination{channelID: channel.Id, team: team.Name, channel: channel.Name}, ""
}
