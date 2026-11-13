package main

import (
	"errors"
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
	dial := fmt.Errorf("failed to query: %w", alertmanager.AsRequestError(&net.OpError{
		Op: "dial", Net: "tcp",
		Addr: &net.TCPAddr{IP: net.ParseIP("10.20.30.40"), Port: 9093},
		Err:  errors.New("connection refused"),
	}))

	p, api := newSilenceKVPlugin()

	api.sysadmin = true
	if got := p.amErrorText("admin", blocked); !strings.Contains(got, "10.20.0.0/16") {
		t.Errorf("system admin should see the suggested CIDR: %s", got)
	}
	if got := p.amErrorText("admin", dial); !strings.Contains(got, "10.20.30.40:9093") || !strings.Contains(got, "could not connect") {
		t.Errorf("system admin should see the sanitized line and the dial detail: %s", got)
	}

	blockedDial := &net.OpError{
		Op: "dial", Net: "tcp",
		Addr: &net.TCPAddr{IP: net.ParseIP("10.20.30.40"), Port: 9093},
		Err:  alertmanager.CheckDestinationIP(net.ParseIP("10.20.30.40")),
	}

	api.sysadmin = false
	for name, e := range map[string]error{"blocked": blocked, "dial": dial, "blocked dial": blockedDial} {
		got := p.amErrorText("member", e)
		if strings.Contains(got, "10.20.30.40") || strings.Contains(got, "/16") {
			t.Errorf("%s: member must not see the address or CIDR: %s", name, got)
		}
	}
	if got := p.amErrorText("member", blocked); !strings.Contains(got, "system admin") {
		t.Errorf("member should be told who to ask: %s", got)
	}

	plain := fmt.Errorf("connection refused")
	if got := p.amErrorText("member", plain); got != "connection refused" {
		t.Errorf("other errors pass through, got %q", got)
	}
}

func TestValidateFireDoesNotSurfaceTheRawDialError(t *testing.T) {
	_, loopback, _ := net.ParseCIDR("127.0.0.0/8")
	alertmanager.SetAllowedNets([]*net.IPNet{loopback})
	t.Cleanup(func() { alertmanager.SetAllowedNets(nil) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	_, err = postValidateSyntheticAlert("http://"+addr, "high-cpu-usage", "warning", nil, false)
	if err == nil || strings.Contains(err.Error(), addr) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("validate fire error names the address: %v", err)
	}
}
