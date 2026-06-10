// Package alertmanager wraps the small subset of the Alertmanager
// REST API the plugin actually uses: list alerts, list/expire silences,
// fetch status. The HTTP client this package shares (Client) honors the
// plugin's AlertManagerCABundle setting so self-signed Alertmanager
// installs work without an OS-level trust store change. Callers pass
// the AM URL + optional basic-auth credentials per call — connection
// state isn't kept here.
package alertmanager

import (
	"encoding/json"
	"net/http"

	"github.com/prometheus/alertmanager/types"
)

// ListAlerts queries the Alertmanager /api/v2/alerts endpoint. user and
// password are optional HTTP Basic Auth credentials; pass empty strings when
// the Alertmanager API is not protected.
func ListAlerts(alertmanagerURL, user, password string) ([]*types.Alert, error) {
	resp, err := httpRetry(http.MethodGet, alertmanagerURL+"/api/v2/alerts", user, password)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var alertResponse []*types.Alert
	if errDec := json.NewDecoder(resp.Body).Decode(&alertResponse); errDec != nil {
		return nil, errDec
	}
	return alertResponse, nil
}
