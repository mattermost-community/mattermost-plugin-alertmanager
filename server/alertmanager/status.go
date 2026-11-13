package alertmanager

import (
	"bytes"
	"context"
	"net/http"
	"time"
)

// StatusResponse is the data returned by Alertmanager about its current status.
type StatusResponse struct {
	Uptime      time.Time `json:"uptime"`
	VersionInfo struct {
		Branch    string `json:"branch"`
		BuildDate string `json:"buildDate"`
		BuildUser string `json:"buildUser"`
		GoVersion string `json:"goVersion"`
		Revision  string `json:"revision"`
		Version   string `json:"version"`
	} `json:"versionInfo"`
}

// Status queries the Alertmanager /api/v2/status endpoint.
func Status(ctx context.Context, alertmanagerURL string) (StatusResponse, error) {
	var statusResponse StatusResponse

	body, _, err := httpRetry(ctx, http.MethodGet, alertmanagerURL+"/api/v2/status")
	if err != nil {
		return statusResponse, err
	}

	if err := DecodeJSONLimited(bytes.NewReader(body), &statusResponse); err != nil {
		return statusResponse, err
	}
	return statusResponse, nil
}
