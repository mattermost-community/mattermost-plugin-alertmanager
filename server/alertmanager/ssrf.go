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

// cgnatNet is the RFC 6598 shared address space (carrier-grade NAT), which
// net.IP.IsPrivate does NOT cover but which is still internal/non-public.
var cgnatNet = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n
}()

// IsBlockedIP reports whether ip is in a range the plugin blocks BY DEFAULT for
// an Alertmanager call (i.e. when no allowlist is configured). Two tiers, both
// blocked by default; an explicit AlertManagerAllowedCIDRs entry overrides either
// (see CheckDestinationIP):
//   - Never-legit: loopback (reaches Mattermost itself), link-local (incl. the
//     169.254.169.254 cloud-metadata endpoint), multicast, unspecified.
//   - Internal/non-public: RFC1918 + IPv6 ULA (net.IP.IsPrivate) and CGNAT
//     (100.64/10). These CAN be a legitimate in-cluster Alertmanager, so the
//     admin re-enables them by allowlisting the specific CIDR — that is the
//     deliberate "block internal SSRF by default" posture (F-001).
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
		ip.IsUnspecified() ||
		ip.IsPrivate() ||
		cgnatNet.Contains(ip)
}

// CheckDestinationIP reports whether ip may be dialed for an Alertmanager call,
// returning a descriptive error when it may not. Single source of truth for both
// the dial-time guard and the save-time URL validation, so the two never disagree
// (F-006: a literal loopback/private URL the admin allowlisted must SAVE, not just
// dial).
//
// Precedence — an explicit allowlist entry ALWAYS wins (matches "reject … unless
// allowlisted"): if AlertManagerAllowedCIDRs contains ip, permit it even if it's a
// normally-blocked range (this re-enables a same-host or in-cluster Alertmanager).
// If an allowlist is set and ip is NOT in it, reject. With NO allowlist, block the
// default-blocked ranges (see IsBlockedIP) and allow only public addresses.
func CheckDestinationIP(ip net.IP) error {
	if allowed := allowedNets.Load(); allowed != nil && len(*allowed) > 0 {
		for _, n := range *allowed {
			if n.Contains(ip) {
				return nil
			}
		}
		return fmt.Errorf("refusing to connect to %s: not in the configured Alertmanager allowlist (AlertManagerAllowedCIDRs)", ip)
	}
	if IsBlockedIP(ip) {
		return fmt.Errorf("refusing to connect to %s: loopback/link-local/metadata/multicast/private/internal addresses are blocked by default (SSRF guard); add its CIDR to AlertManagerAllowedCIDRs to permit an in-cluster Alertmanager", ip)
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
	return CheckDestinationIP(ip)
}
