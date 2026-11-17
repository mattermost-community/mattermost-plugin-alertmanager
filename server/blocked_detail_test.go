package main

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-alertmanager/server/alertmanager"
)

// TestURLValidationKeepsTheSSRFDetailBehindTheErrorType pins who sees the CIDR
// to allowlist: slash commands get the generic text and a typed error to gate
// on, and only the System Console path spells the detail out.
func TestURLValidationKeepsTheSSRFDetailBehindTheErrorType(t *testing.T) {
	_, allowed, _ := net.ParseCIDR("203.0.113.0/24")
	alertmanager.SetAllowedNets([]*net.IPNet{allowed})
	t.Cleanup(func() { alertmanager.SetAllowedNets(nil) })

	for name, err := range map[string]error{
		"AlertManagerURL": validateAlertManagerURL("http://10.20.30.40:9093"),
		"WebhookHost":     validateWebhookHost("http://10.20.30.40:8065"),
	} {
		var blocked *alertmanager.BlockedDestinationError
		if !errors.As(err, &blocked) {
			t.Fatalf("%s: want a BlockedDestinationError in the chain, got %v", name, err)
		}
		if strings.Contains(err.Error(), "10.20.0.0/16") {
			t.Errorf("%s: slash-command text names the CIDR to allowlist: %s", name, err)
		}
		if got := withBlockedDetail(err).Error(); !strings.Contains(got, "10.20.0.0/16") || !strings.Contains(got, name) {
			t.Errorf("%s: System Console text should name the setting and the CIDR: %s", name, got)
		}
	}
}
