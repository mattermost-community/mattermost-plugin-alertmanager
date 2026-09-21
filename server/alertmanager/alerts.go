// Package alertmanager wraps the small subset of the Alertmanager
// REST API the plugin actually uses: list alerts, list/expire silences,
// fetch status. The HTTP client this package shares (Client) honors the
// plugin's AlertManagerCABundle setting so self-signed Alertmanager
// installs work without an OS-level trust store change. Callers pass
// the AM URL + optional basic-auth credentials per call — connection
// state isn't kept here.
package alertmanager

import (
	"bytes"
	"net/http"

	"github.com/prometheus/alertmanager/alert"
)

// ListAlerts queries the Alertmanager /api/v2/alerts endpoint. user and
// password are optional HTTP Basic Auth credentials; pass empty strings when
// the Alertmanager API is not protected.
//
// Returns []*alert.Alert. Prior versions used types.Alert; that type
// remains in v0.33 as a deprecated alias for alert.Alert — using the
// canonical path here silences the SA1019 lint and stays current as
// the alias eventually gets removed upstream.
func ListAlerts(alertmanagerURL, user, password string) ([]*alert.Alert, error) {
	body, _, err := httpRetry(http.MethodGet, alertmanagerURL+"/api/v2/alerts", user, password)
	if err != nil {
		return nil, err
	}

	var alertResponse []*alert.Alert
	if errDec := DecodeJSONLimited(bytes.NewReader(body), &alertResponse); errDec != nil {
		return nil, errDec
	}
	return alertResponse, nil
}
