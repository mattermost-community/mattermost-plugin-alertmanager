package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-alertmanager/server/alertmanager"
)

// amErrorText is the text of an Alertmanager error that is safe to show the
// caller. The raw transport text names the resolved IP and port, and a
// destination the SSRF guard blocked comes with the CIDR to allowlist, so only
// system admins see either. Everyone else gets the sanitized line. The raw text
// always goes to the plugin log.
func (p *Plugin) amErrorText(userID string, err error) string {
	p.logAMError(err)
	if p.requireSystemAdmin(userID) == nil {
		return amAdminErrorText(err)
	}
	return amMemberErrorText(err)
}

func amMemberErrorText(err error) string {
	var blocked *alertmanager.BlockedDestinationError
	if errors.As(err, &blocked) {
		return blocked.Error()
	}
	return err.Error()
}

// amAdminErrorText adds the raw detail, for callers already gated on system
// admin.
func amAdminErrorText(err error) string {
	var blocked *alertmanager.BlockedDestinationError
	var reqErr *alertmanager.RequestError
	switch {
	case errors.As(err, &blocked):
		return strings.Replace(err.Error(), blocked.Error(), blocked.Detail(), 1)
	case errors.As(err, &reqErr):
		return fmt.Sprintf("%s (%s)", err.Error(), reqErr.Detail())
	default:
		return err.Error()
	}
}

// logAMError writes the unsanitized text of an Alertmanager failure to the
// plugin log, which only system admins can read.
func (p *Plugin) logAMError(err error) {
	var blocked *alertmanager.BlockedDestinationError
	var reqErr *alertmanager.RequestError
	switch {
	case errors.As(err, &blocked):
		p.API.LogWarn("Alertmanager destination blocked by the SSRF guard", "detail", blocked.Detail())
	case errors.As(err, &reqErr):
		p.API.LogWarn("Alertmanager request failed", "error", reqErr.Error(), "detail", reqErr.Detail())
	}
}
