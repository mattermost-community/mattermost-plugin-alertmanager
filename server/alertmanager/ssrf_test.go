package alertmanager

import (
	"net"
	"strings"
	"testing"
)

// mustCIDR parses a CIDR for tests, panicking on error. Shared across the
// package's tests (e.g. expire_silence_test allowlists loopback so its httptest
// servers are reachable through the SSRF-guarded client).
func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// TestIsBlockedIP pins the always-blocked ranges (F-001): loopback, link-local
// (incl. the cloud-metadata address), unspecified, and multicast are never valid
// Alertmanager targets; ordinary public and private-cluster addresses are not
// blocked (an in-cluster Alertmanager is typically on a private IP).
func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.0.0.53", "::1", // loopback
		"169.254.169.254", "169.254.1.1", "fe80::1", // link-local incl. metadata
		"0.0.0.0", "::", // unspecified
		"224.0.0.1", "ff02::1", // multicast
		"10.0.0.5", "192.168.1.9", "172.16.3.4", // RFC1918 private (blocked by default)
		"100.64.1.2",   // CGNAT / RFC6598
		"fd12:3456::1", // IPv6 ULA
	}
	for _, s := range blocked {
		if !IsBlockedIP(net.ParseIP(s)) {
			t.Errorf("expected %s to be blocked by default", s)
		}
	}
	allowed := []string{
		"8.8.8.8", "1.1.1.1", "203.0.113.9", "2606:4700::1", // public
	}
	for _, s := range allowed {
		if IsBlockedIP(net.ParseIP(s)) {
			t.Errorf("expected public %s NOT to be blocked", s)
		}
	}
	if !IsBlockedIP(nil) {
		t.Error("nil IP must be treated as blocked")
	}
}

// TestSafeDialControl exercises the dial-time guard across the three regimes:
// no allowlist (block dangerous, allow the rest), allowlist set (only listed
// permitted, overrides the always-block so a same-host AM can be re-enabled).
func TestSafeDialControl(t *testing.T) {
	t.Cleanup(func() { SetAllowedNets(nil) })

	// No allowlist: only PUBLIC destinations are dialable (block-by-default).
	SetAllowedNets(nil)
	if err := safeDialControl("tcp", "8.8.8.8:9093", nil); err != nil {
		t.Errorf("public IP should be allowed with no allowlist: %v", err)
	}
	if err := safeDialControl("tcp", "10.0.0.5:9093", nil); err == nil {
		t.Error("private RFC1918 must be blocked by default (no allowlist)")
	}
	if err := safeDialControl("tcp", "169.254.169.254:80", nil); err == nil {
		t.Error("cloud metadata must be blocked with no allowlist")
	}
	if err := safeDialControl("tcp", "127.0.0.1:8065", nil); err == nil {
		t.Error("loopback (Mattermost self) must be blocked with no allowlist")
	}

	// Allowlist set: only listed IPs permitted, and an allowlisted loopback is
	// re-enabled (reject … unless allowlisted).
	SetAllowedNets([]*net.IPNet{mustCIDR("10.0.0.0/8"), mustCIDR("127.0.0.0/8")})
	if err := safeDialControl("tcp", "10.1.2.3:9093", nil); err != nil {
		t.Errorf("allowlisted 10/8 should be permitted: %v", err)
	}
	if err := safeDialControl("tcp", "127.0.0.1:9093", nil); err != nil {
		t.Errorf("explicitly allowlisted loopback should be permitted: %v", err)
	}
	if err := safeDialControl("tcp", "192.168.1.5:9093", nil); err == nil {
		t.Error("192.168/16 not in allowlist must be rejected")
	}
	if err := safeDialControl("tcp", "169.254.169.254:80", nil); err == nil {
		t.Error("metadata not in allowlist must be rejected")
	}
}

// TestSetAllowedNetsParsesForDial is a light sanity check that a parsed CIDR
// flows through to the dial decision.
func TestSetAllowedNetsParsesForDial(t *testing.T) {
	t.Cleanup(func() { SetAllowedNets(nil) })
	SetAllowedNets([]*net.IPNet{mustCIDR("203.0.113.0/24")})
	if err := safeDialControl("tcp", "203.0.113.7:9093", nil); err != nil {
		t.Errorf("in-range public IP should be permitted: %v", err)
	}
	if err := safeDialControl("tcp", "203.0.114.7:9093", nil); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Errorf("out-of-range IP should be rejected with an allowlist message, got %v", err)
	}
}

// TestHardBlockedNotOverridable is the F-001 hardening: metadata/link-local/
// multicast/unspecified stay blocked even when an allowlist (including a
// catch-all /0) matches them, so no single broad setting can re-enable
// cloud-metadata SSRF.
func TestHardBlockedNotOverridable(t *testing.T) {
	t.Cleanup(func() { SetAllowedNets(nil) })
	SetAllowedNets([]*net.IPNet{mustCIDR("0.0.0.0/0"), mustCIDR("::/0")})

	for _, s := range []string{"169.254.169.254", "169.254.1.1", "fe80::1", "224.0.0.1", "0.0.0.0", "::"} {
		if err := CheckDestinationIP(net.ParseIP(s)); err == nil {
			t.Errorf("%s must stay blocked even under a /0 allowlist", s)
		}
	}
	// The soft tier (public/loopback/private) IS re-enabled by /0 at the dial
	// layer — which is exactly why the config parser rejects /0 (see the
	// updateAlertmanagerAllowlist test); non-overridability is only for the hard tier.
	if err := CheckDestinationIP(net.ParseIP("8.8.8.8")); err != nil {
		t.Errorf("public IP should be permitted under a /0 allowlist: %v", err)
	}
}
