package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mattermost/mattermost/server/public/model"
)

const validateTargetsPath = "/autocomplete/validate-targets"

// setHelpText is the hover text for a group offered by validate.
var setHelpText = map[string]string{
	"application":   "Application receivers in this channel only",
	"compute":       "Compute receivers in this channel only",
	"database":      "Database receivers in this channel only",
	"mattermost":    "Mattermost-application receivers in this channel only",
	"networking":    "Networking receivers in this channel only",
	"observability": "Observability receivers in this channel only",
	"security":      "Security receivers in this channel only",
	"storage":       "Storage receivers in this channel only",
}

// handleAutocompleteValidateTargets lists `all` plus the groups that have at
// least one receiver in the channel the caller is typing in. Mattermost passes
// channel_id on every dynamic-list call. Non-members only get `all`, so the
// hover never reveals what is configured in a channel they cannot see.
func (p *Plugin) handleAutocompleteValidateTargets(w http.ResponseWriter, r *http.Request, userID string) {
	items := []model.AutocompleteListItem{{Item: "all", HelpText: "Every receiver bound to this channel (default)"}}

	channelID := r.URL.Query().Get("channel_id")
	if channelID != "" {
		if _, appErr := p.API.GetChannelMember(channelID, userID); appErr == nil {
			items = append(items, p.channelGroupItems(channelID)...)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(items); err != nil {
		p.API.LogWarn("autocomplete: could not write validate targets", "err", err.Error())
	}
}

// channelGroupItems returns one item per group that has a receiver in the
// channel, in the stable order of scaffoldSetNames.
func (p *Plugin) channelGroupItems(channelID string) []model.AutocompleteListItem {
	present := make(map[string]bool)
	for _, c := range p.configsForCurrentChannel(&model.CommandArgs{ChannelId: channelID}) {
		present[receiverBaseSlug(c.Name)] = true
	}

	var items []model.AutocompleteListItem
	for _, name := range scaffoldSetNames() {
		if name == "all" || !isKnownSet(name) {
			continue
		}
		for _, slug := range scaffoldSets[name] {
			if present[slug] {
				items = append(items, model.AutocompleteListItem{Item: name, HelpText: setHelpText[name]})
				break
			}
		}
	}
	return items
}

func validateTargetsFetchURL() string {
	return fmt.Sprintf("/plugins/%s%s", Manifest.Id, validateTargetsPath)
}
