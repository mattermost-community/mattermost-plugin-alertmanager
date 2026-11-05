package main

import (
	"errors"
	"strings"

	"github.com/mattermost/mattermost-plugin-alertmanager/server/alertmanager"
)

// amErrorText is the text of an Alertmanager error that is safe to show the
// caller. A destination blocked by the SSRF guard names an internal address and
// the CIDR to allowlist, so only system admins see that; everyone else gets the
// generic line. The detail always goes to the plugin log.
func (p *Plugin) amErrorText(userID string, err error) string {
	var blocked *alertmanager.BlockedDestinationError
	if !errors.As(err, &blocked) {
		return err.Error()
	}
	p.API.LogWarn("Alertmanager destination blocked by the SSRF guard", "detail", blocked.Detail())
	if p.requireSystemAdmin(userID) != nil {
		return err.Error()
	}
	return strings.Replace(err.Error(), blocked.Error(), blocked.Detail(), 1)
}
