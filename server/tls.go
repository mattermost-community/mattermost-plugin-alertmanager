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
// rather than failing the config load; an empty/all-invalid result means "no
// allowlist" — the always-blocked ranges (loopback/link-local/metadata/multicast/
// unspecified) are still denied by the dial guard regardless.
func (p *Plugin) updateAlertmanagerAllowlist(raw string) {
	split := func(r rune) bool { return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t' }
	var nets []*net.IPNet
	for _, field := range strings.FieldsFunc(raw, split) {
		_, n, err := net.ParseCIDR(field)
		if err != nil {
			p.API.LogWarn("ignoring invalid AlertManagerAllowedCIDRs entry", "entry", field, "err", err.Error())
			continue
		}
		nets = append(nets, n)
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
