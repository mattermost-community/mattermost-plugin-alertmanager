package main

import (
	"net"
	"testing"

	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/mattermost/mattermost-plugin-alertmanager/server/alertmanager"
)

type fakeLogAPI struct{ plugin.API }

func (fakeLogAPI) LogWarn(string, ...any)  {}
func (fakeLogAPI) LogError(string, ...any) {}

// TestUpdateAlertmanagerAllowlist is the F-001/F-002 config-parser guard: a
// catch-all /0 is rejected (must not disable the guard), an all-invalid update
// fails closed (keeps the previous allowlist), and a valid CIDR installs.
func TestUpdateAlertmanagerAllowlist(t *testing.T) {
	p := &Plugin{}
	p.API = fakeLogAPI{}
	t.Cleanup(func() { alertmanager.SetAllowedNets(nil) })

	priv := net.ParseIP("10.0.0.5")
	pub := net.ParseIP("8.8.8.8")

	// /0 rejected → no allowlist installed → block-private default holds.
	alertmanager.SetAllowedNets(nil)
	p.updateAlertmanagerAllowlist("0.0.0.0/0, ::/0")
	if err := alertmanager.CheckDestinationIP(priv); err == nil {
		t.Fatal("a /0-only setting must be rejected — private must stay blocked")
	}
	if err := alertmanager.CheckDestinationIP(pub); err != nil {
		t.Fatalf("public should be reachable with no installed allowlist: %v", err)
	}

	// Valid CIDR installs.
	p.updateAlertmanagerAllowlist("10.0.0.0/8")
	if err := alertmanager.CheckDestinationIP(priv); err != nil {
		t.Fatalf("10.0.0.5 should be allowed once 10/8 is allowlisted: %v", err)
	}

	// All-invalid update fails closed: the previous [10/8] is preserved.
	p.updateAlertmanagerAllowlist("not-a-cidr, 0.0.0.0/0")
	if err := alertmanager.CheckDestinationIP(priv); err != nil {
		t.Fatalf("all-invalid update must preserve the previous allowlist: %v", err)
	}

	// Explicitly clearing (empty) removes the allowlist → block-private returns.
	p.updateAlertmanagerAllowlist("")
	if err := alertmanager.CheckDestinationIP(priv); err == nil {
		t.Fatal("empty setting must clear the allowlist so private is blocked again")
	}
}
