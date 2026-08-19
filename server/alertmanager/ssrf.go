package alertmanager

import (
	"fmt"
	"net"
	"sync/atomic"
	"syscall"
)

// SSRF protection for outbound Alertmanager calls (finding F-001). A team admin
// supplies the Alertmanager base URL, and the Mattermost server then makes
// requests to it from its own network context (admin-inventory probes,
// /alertmanager validate, alerts/silences/status). Without a destination policy
// that is an SSRF primitive: http://169.254.169.254 (cloud metadata),
// http://127.0.0.1:8065 (Mattermost's own API), http://10.0.0.5 (internal svc).
//
// The check runs as a net.Dialer.Control hook — AFTER DNS resolution, BEFORE the
// connection — so it validates the ACTUAL target IP. That defeats DNS rebinding
// (a hostname that resolves to a safe IP at save time and to metadata at request
// time) which a save-time-only string check cannot.

// allowedNets is the admin-configured allowlist of CIDRs an Alertmanager may
// resolve to. Empty/nil => allow any address EXCEPT the always-blocked ranges.
// Held in an atomic.Pointer because it's set on the config-change goroutine and
// read on every dial from request goroutines.
var allowedNets atomic.Pointer[[]*net.IPNet]

// SetAllowedNets replaces the Alertmanager destination allowlist. Passing nil or
// an empty slice means "no allowlist" — only the always-blocked ranges are denied.
func SetAllowedNets(nets []*net.IPNet) { allowedNets.Store(&nets) }

// IsBlockedIP reports whether ip is in a range the plugin must never dial for an
// Alertmanager call, regardless of allowlist: loopback (127/8, ::1 — reaches
// Mattermost itself), link-local (169.254/16 incl. the 169.254.169.254 cloud
// metadata endpoint, and fe80::/10), unspecified (0.0.0.0, ::), and multicast.
// None is ever a legitimate Alertmanager. Exported for the save-time URL check.
func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4 // normalize IPv4-mapped IPv6 so mapped forms are caught too
	}
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// ipAllowed reports whether ip may be dialed.
//
// Precedence (matches "reject … unless allowlisted"): an explicit allowlist entry
// ALWAYS wins — if the admin configured AlertManagerAllowedCIDRs and ip is in it,
// permit it even if it's a normally-blocked range (this is how a legitimate
// same-host Alertmanager on 127.0.0.1 is re-enabled: allowlist 127.0.0.1/32). If
// an allowlist is set and ip is NOT in it, reject. With NO allowlist, block the
// always-dangerous ranges (loopback/link-local/metadata/multicast/unspecified)
// and allow everything else (so an in-cluster private-IP Alertmanager works
// out of the box while cloud-metadata / self-SSRF are closed by default).
func ipAllowed(ip net.IP) error {
	if allowed := allowedNets.Load(); allowed != nil && len(*allowed) > 0 {
		for _, n := range *allowed {
			if n.Contains(ip) {
				return nil
			}
		}
		return fmt.Errorf("refusing to connect to %s: not in the configured Alertmanager allowlist (AlertManagerAllowedCIDRs)", ip)
	}
	if IsBlockedIP(ip) {
		return fmt.Errorf("refusing to connect to %s: loopback/link-local/metadata/multicast/unspecified addresses are not valid Alertmanager targets (SSRF guard); allowlist it via AlertManagerAllowedCIDRs if this is intentional", ip)
	}
	return nil
}

// safeDialControl is the net.Dialer.Control hook installed on the Alertmanager
// transport. `address` is the post-DNS "IP:port"; we validate the IP before the
// connection is established.
func safeDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("SSRF guard: cannot parse dial address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("SSRF guard: resolved address %q is not an IP", host)
	}
	return ipAllowed(ip)
}
