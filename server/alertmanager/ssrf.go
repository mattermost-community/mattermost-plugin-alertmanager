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

// allowlistState is the installed SSRF destination policy. Three states so a
// misconfigured (non-empty but unusable) allowlist can fail CLOSED instead of
// falling back to the wider "no allowlist" default on a cold start:
//   - Load() == nil OR (denyAll=false, nets empty): NO allowlist — public
//     destinations are allowed, the soft tier (loopback/private) is blocked.
//   - denyAll == true: block EVERY destination. Installed when the admin set a
//     non-empty AlertManagerAllowedCIDRs that parsed to zero usable CIDRs and no
//     previous good allowlist exists (see updateAlertmanagerAllowlist).
//   - nets non-empty: permit only IPs inside these CIDRs (re-enables loopback/
//     private/in-cluster targets); everything else is blocked.
type allowlistState struct {
	denyAll bool
	nets    []*net.IPNet
}

// allowlist holds the current destination policy in an atomic.Pointer because
// it's set on the config-change goroutine and read on every dial from request
// goroutines.
var allowlist atomic.Pointer[allowlistState]

// SetAllowedNets replaces the Alertmanager destination allowlist. Passing nil or
// an empty slice means "no allowlist" — only the always-blocked ranges are denied.
func SetAllowedNets(nets []*net.IPNet) { allowlist.Store(&allowlistState{nets: nets}) }

// SetDenyAll installs the fail-closed state: every Alertmanager destination is
// refused. Used when a non-empty allowlist setting yields zero usable CIDRs and
// there is no previous good allowlist to preserve — the admin tried to RESTRICT
// egress, so falling back to "allow public" would fail open (finding: all-invalid
// allowlist fails open on cold start).
func SetDenyAll() { allowlist.Store(&allowlistState{denyAll: true}) }

// HasUsableAllowlist reports whether a non-empty CIDR allowlist is currently
// installed. Lets the config parser distinguish "keep the live allowlist on a
// transient bad edit" from "cold start with no allowlist → deny all".
func HasUsableAllowlist() bool {
	st := allowlist.Load()
	return st != nil && !st.denyAll && len(st.nets) > 0
}

// cgnatNet is the RFC 6598 shared address space (carrier-grade NAT), which
// net.IP.IsPrivate does NOT cover but which is still internal/non-public.
var cgnatNet = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n
}()

// normalizeIP maps an IPv4-mapped IPv6 address to its 4-byte form so mapped
// variants (e.g. ::ffff:169.254.169.254) are classified the same as the bare v4.
func normalizeIP(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}

// isHardBlockedIP reports whether ip is in a range that is NEVER a legitimate
// Alertmanager and must be blocked even when an allowlist matches it (F-001
// hardening): link-local (incl. the 169.254.169.254 cloud-metadata endpoint and
// fe80::/10), multicast, and unspecified. This is what stops a broad allowlist
// entry (e.g. a mistaken 10.0.0.0/8-that-somehow-widens, or the /0 we reject
// outright) from re-enabling metadata SSRF. Loopback is deliberately NOT here —
// a same-host Alertmanager on 127.0.0.1 is a real (if narrow) case, re-enabled
// only by an explicit allowlist entry.
func isHardBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	ip = normalizeIP(ip)
	return ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// IsBlockedIP reports whether ip is blocked BY DEFAULT (no allowlist): the
// hard-blocked ranges above PLUS the soft tier that an allowlist CAN re-enable —
// loopback (reaches Mattermost itself) and internal/non-public ranges (RFC1918 +
// IPv6 ULA via net.IP.IsPrivate, and CGNAT 100.64/10). The soft tier can be a
// legitimate in-cluster/same-host Alertmanager, so the admin re-enables it by
// allowlisting its specific CIDR.
func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if isHardBlockedIP(ip) {
		return true
	}
	n := normalizeIP(ip)
	return n.IsLoopback() || n.IsPrivate() || cgnatNet.Contains(n)
}

// CheckDestinationIP reports whether ip may be dialed for an Alertmanager call,
// returning a descriptive error when it may not. Single source of truth for both
// the dial-time guard and the save-time URL validation, so the two never disagree
// (F-006).
//
// Precedence:
//  1. Hard-blocked ranges (metadata/link-local/multicast/unspecified) are ALWAYS
//     rejected — no allowlist entry, however broad, can re-enable them (F-001:
//     an "allow all" CIDR must not turn the guard off for cloud metadata).
//  2. Deny-all state (a non-empty setting that yielded zero usable CIDRs, cold
//     start): reject everything until the setting is fixed — fail closed.
//  3. Otherwise, if an allowlist is configured, permit iff ip is in it (this
//     re-enables loopback/private/in-cluster targets), else reject.
//  4. With no allowlist, block the soft tier (loopback + private) and allow only
//     public addresses.
func CheckDestinationIP(ip net.IP) error {
	if isHardBlockedIP(ip) {
		return fmt.Errorf("refusing to connect to %s: link-local/cloud-metadata/multicast/unspecified addresses are never valid Alertmanager targets (not overridable by AlertManagerAllowedCIDRs)", ip)
	}
	if st := allowlist.Load(); st != nil {
		if st.denyAll {
			return fmt.Errorf("refusing to connect to %s: AlertManagerAllowedCIDRs is set but has no usable CIDRs; all Alertmanager egress is denied until the setting is fixed", ip)
		}
		if len(st.nets) > 0 {
			for _, n := range st.nets {
				if n.Contains(ip) {
					return nil
				}
			}
			return fmt.Errorf("refusing to connect to %s: not in the configured Alertmanager allowlist (AlertManagerAllowedCIDRs)", ip)
		}
	}
	if IsBlockedIP(ip) {
		return fmt.Errorf("refusing to connect to %s: loopback/private/internal addresses are blocked by default (SSRF guard); add its CIDR to AlertManagerAllowedCIDRs to permit an in-cluster Alertmanager", ip)
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
