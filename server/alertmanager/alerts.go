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
	defer resp.Body.Close()

	var alertResponse []*types.Alert
	if errDec := json.NewDecoder(resp.Body).Decode(&alertResponse); errDec != nil {
		return nil, errDec
	}
	return alertResponse, nil
}
