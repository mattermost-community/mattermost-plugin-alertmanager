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

// TestUpdateAlertmanagerAllowlist is the F-001 config-parser guard: a catch-all
// /0 is rejected (must not disable the guard), a non-empty-but-unusable setting
// on COLD START denies all egress (fail closed, not fall back to allow-public),
// the same misconfig with a live good allowlist preserves it, and a valid CIDR
// installs.
func TestUpdateAlertmanagerAllowlist(t *testing.T) {
	p := &Plugin{}
	p.API = fakeLogAPI{}
	t.Cleanup(func() { alertmanager.SetAllowedNets(nil) })

	priv := net.ParseIP("10.0.0.5")
	pub := net.ParseIP("8.8.8.8")

	// COLD START, all-invalid non-empty setting: no previous allowlist to keep, so
	// the guard must fail CLOSED — deny ALL egress, not fall back to allow-public.
	// This is the regression for "all-invalid allowlist fails open on cold start".
	alertmanager.SetAllowedNets(nil)
	p.updateAlertmanagerAllowlist("0.0.0.0/0, ::/0")
	if err := alertmanager.CheckDestinationIP(priv); err == nil {
		t.Fatal("cold-start all-invalid setting must deny private")
	}
	if err := alertmanager.CheckDestinationIP(pub); err == nil {
		t.Fatal("cold-start all-invalid setting must deny even PUBLIC (fail closed), not fall back to allow-public")
	}

	// Valid CIDR installs (clears the deny-all state).
	p.updateAlertmanagerAllowlist("10.0.0.0/8")
	if err := alertmanager.CheckDestinationIP(priv); err != nil {
		t.Fatalf("10.0.0.5 should be allowed once 10/8 is allowlisted: %v", err)
	}
	if err := alertmanager.CheckDestinationIP(pub); err == nil {
		t.Fatal("8.8.8.8 should be blocked once a 10/8-only allowlist is installed")
	}

	// All-invalid update WITH a live good allowlist: preserve [10/8], don't nuke a
	// running deployment on a fat-finger.
	p.updateAlertmanagerAllowlist("not-a-cidr, 0.0.0.0/0")
	if err := alertmanager.CheckDestinationIP(priv); err != nil {
		t.Fatalf("all-invalid update must preserve the previous allowlist: %v", err)
	}

	// Explicitly clearing (empty) removes the allowlist → block-private returns,
	// public reachable again (no allowlist = allow-public default).
	p.updateAlertmanagerAllowlist("")
	if err := alertmanager.CheckDestinationIP(priv); err == nil {
		t.Fatal("empty setting must clear the allowlist so private is blocked again")
	}
	if err := alertmanager.CheckDestinationIP(pub); err != nil {
		t.Fatalf("empty setting = no allowlist, public must be reachable: %v", err)
	}
}
