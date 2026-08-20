package main

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-alertmanager/server/alertmanager"
)

// updateAlertmanagerAllowlist parses the AlertManagerAllowedCIDRs setting and
// installs it as the SSRF destination allowlist (F-001). Entries may be comma-,
// space-, or newline-separated CIDRs. Invalid entries are logged and skipped
// rather than failing the config load. An EMPTY setting means "no allowlist"
// (public allowed, soft tier blocked). A NON-EMPTY setting that yields zero
// usable CIDRs fails closed: keep a previous good allowlist if one is live, else
// deny all egress (cold start must not fall back to allow-public). The always-
// blocked ranges (loopback/link-local/metadata/multicast/unspecified) are denied
// by the dial guard regardless of this setting.
func (p *Plugin) updateAlertmanagerAllowlist(raw string) {
	split := func(r rune) bool { return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t' }
	fields := strings.FieldsFunc(raw, split)

	// Empty setting = no allowlist. Under the block-by-default posture that means
	// only public destinations are dialable (secure), so it's safe to install.
	if len(fields) == 0 {
		alertmanager.SetAllowedNets(nil)
		return
	}

	var nets []*net.IPNet
	for _, field := range fields {
		_, n, err := net.ParseCIDR(field)
		if err != nil {
			p.API.LogWarn("ignoring invalid AlertManagerAllowedCIDRs entry", "entry", field, "err", err.Error())
			continue
		}
		// Reject a catch-all /0 (0.0.0.0/0, ::/0): it would allow every address,
		// re-enabling loopback (Mattermost's own API) and every internal service —
		// turning "SSRF blocked by default" into "SSRF disabled by one setting"
		// (F-001). An allowlist must name actual networks; hard-blocked ranges
		// (metadata/link-local/multicast) stay blocked regardless via
		// CheckDestinationIP, but /0 is never a sensible allowlist entry.
		if ones, _ := n.Mask.Size(); ones == 0 {
			p.API.LogWarn("ignoring catch-all AlertManagerAllowedCIDRs entry (/0 would disable the SSRF guard); list the actual Alertmanager network instead", "entry", field)
			continue
		}
		nets = append(nets, n)
	}

	// Fail CLOSED on a non-empty-but-unusable allowlist (all invalid or catch-all
	// /0). The admin intended to RESTRICT egress and got it wrong, so we must not
	// fall back to the wider "no allowlist = public allowed" default.
	if len(nets) == 0 {
		if alertmanager.HasUsableAllowlist() {
			// A previous good allowlist is live — keep it rather than break a
			// running deployment on a transient fat-finger. This never widens.
			p.API.LogError("AlertManagerAllowedCIDRs has entries but none are usable (all invalid or catch-all /0); keeping the previous allowlist unchanged (fix the setting to change egress policy)")
			return
		}
		// Cold start / no previous allowlist: deny ALL Alertmanager egress until
		// the setting is fixed. Preserving the nil default here would fail OPEN —
		// public destinations would stay dialable despite an attempted allowlist.
		p.API.LogError("AlertManagerAllowedCIDRs has entries but none are usable (all invalid or catch-all /0) and no previous allowlist exists; denying ALL Alertmanager egress until the setting is fixed")
		alertmanager.SetDenyAll()
		return
	}
	alertmanager.SetAllowedNets(nets)
}

// outboundHTTPTimeout caps how long any single Alertmanager API call
// can hang before failing. Slash-command response budget is ~10s in
// Mattermost, so a 30s upper bound here keeps us under that even when
// the backoff retries kick in.
const outboundHTTPTimeout = 30 * time.Second

// updateAlertmanagerHTTPClient rebuilds the alertmanager package's
// HTTP client based on the current CA bundle setting. Called from
// OnConfigurationChange so cert rotation takes effect without
// requiring a plugin restart.
//
// When the bundle is empty/blank, we restore http.DefaultClient — that
// way disabling the setting reverts to system-root-CA behavior cleanly.
//
// When the bundle is set, we build a Transport with a cert pool
// containing system roots + the provided PEM. Malformed PEM is logged
// as a warning; the client still gets built (without the bad certs)
// rather than refusing to function entirely.
func (p *Plugin) updateAlertmanagerHTTPClient(caBundle string) {
	if strings.TrimSpace(caBundle) == "" {
		alertmanager.SetClient(&http.Client{
			Timeout:       outboundHTTPTimeout,
			Transport:     alertmanager.NewTransport(),
			CheckRedirect: alertmanager.RefuseRedirect,
		})
		return
	}

	// Start with system roots so the plugin doesn't lose the ability
	// to reach Alertmanagers behind public CAs. Some platforms return
	// nil from SystemCertPool — fall through to an empty pool in that
	// case.
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	if ok := pool.AppendCertsFromPEM([]byte(caBundle)); !ok {
		p.API.LogWarn("AlertManagerCABundle could not be parsed as PEM (no certs added); falling back to system roots only")
	}

	transport := alertmanager.NewTransport()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	alertmanager.SetClient(&http.Client{
		Timeout:       outboundHTTPTimeout,
		Transport:     transport,
		CheckRedirect: alertmanager.RefuseRedirect,
	})
}
