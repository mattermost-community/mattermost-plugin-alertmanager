package main

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"strings"
	"time"

	"github.com/christopherfickess/mattermost-plugin-alertmanager/server/alertmanager"
)

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
		alertmanager.Client = http.DefaultClient
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

	alertmanager.Client = &http.Client{
		Timeout: outboundHTTPTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
}
