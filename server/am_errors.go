package main

import (
	"errors"
	"fmt"
	"net"
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

// webhookTestError is a webhook test POST that never got a reply. Error is the
// line safe for team admins. detail is the transport text with the hook ID
// scrubbed, which still names the webhook host and its resolved address.
type webhookTestError struct {
	msg    string
	detail string
}

func (e *webhookTestError) Error() string { return e.msg }

func newWebhookTestError(err error, scrubbed string) *webhookTestError {
	var blocked *alertmanager.BlockedDestinationError
	var netErr net.Error
	msg := "POST failed: the webhook URL did not respond"
	switch {
	case errors.As(err, &blocked):
		msg = "POST failed: " + blocked.Error()
	case errors.As(err, &netErr) && netErr.Timeout():
		msg = "POST failed: no reply within 5s"
	}
	return &webhookTestError{msg: msg, detail: "POST failed: " + scrubbed}
}

// webhookTestDetail is the full text for callers already gated on system admin.
func webhookTestDetail(err error) string {
	var te *webhookTestError
	if errors.As(err, &te) {
		return te.detail
	}
	return err.Error()
}

// webhookTestErrorText logs a webhook test failure and returns the detail to
// system admins and the short line to everyone else.
func (p *Plugin) webhookTestErrorText(userID string, err error) string {
	var te *webhookTestError
	if !errors.As(err, &te) {
		return err.Error()
	}
	p.API.LogWarn("webhook test POST failed", "detail", te.detail)
	if p.requireSystemAdmin(userID) == nil {
		return webhookTestDetail(err)
	}
	return te.msg
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
