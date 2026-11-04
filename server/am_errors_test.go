package main

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/mattermost/mattermost-plugin-alertmanager/server/alertmanager"
)

func TestAMErrorTextShowsNetworkDetailToSystemAdminsOnly(t *testing.T) {
	_, ipNet, err := net.ParseCIDR("203.0.113.0/24")
	if err != nil {
		t.Fatal(err)
	}
	alertmanager.SetAllowedNets([]*net.IPNet{ipNet})
	t.Cleanup(func() { alertmanager.SetAllowedNets(nil) })

	blocked := fmt.Errorf("failed to query: %w", alertmanager.CheckDestinationIP(net.ParseIP("10.20.30.40")))

	p, api := newSilenceKVPlugin()

	api.sysadmin = true
	if got := p.amErrorText("admin", blocked); !strings.Contains(got, "10.20.0.0/16") {
		t.Errorf("system admin should see the suggested CIDR: %s", got)
	}

	api.sysadmin = false
	got := p.amErrorText("member", blocked)
	if strings.Contains(got, "10.20.30.40") || strings.Contains(got, "/16") {
		t.Errorf("member must not see the address or CIDR: %s", got)
	}
	if !strings.Contains(got, "system admin") {
		t.Errorf("member should be told who to ask: %s", got)
	}

	plain := fmt.Errorf("connection refused")
	if got := p.amErrorText("member", plain); got != "connection refused" {
		t.Errorf("other errors pass through, got %q", got)
	}
}
