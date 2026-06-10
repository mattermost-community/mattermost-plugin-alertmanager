package alertmanager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/prometheus/alertmanager/types"
)

// ListSilences queries the Alertmanager /api/v2/silences endpoint and returns
// silences sorted by EndsAt descending (most recently-ending first).
func ListSilences(alertmanagerURL, user, password string) ([]types.Silence, error) {
	resp, err := httpRetry(http.MethodGet, alertmanagerURL+"/api/v2/silences", user, password)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var silences []types.Silence
	if errDec := json.NewDecoder(resp.Body).Decode(&silences); errDec != nil {
		return nil, errDec
	}

	sort.Slice(silences, func(i, j int) bool {
		return silences[i].EndsAt.After(silences[j].EndsAt)
	})
	return silences, nil
}

// ExpireSilence terminates the silence with the given ID. The Alertmanager
// API treats DELETE on /silence/{id} as "expire now" rather than literal
// deletion — the silence remains visible in history with state=expired.
func ExpireSilence(silenceID, alertmanagerURL, user, password string) error {
	if silenceID == "" {
		return fmt.Errorf("silence ID cannot be empty")
	}

	expireURL := fmt.Sprintf("%s/api/v2/silence/%s", alertmanagerURL, silenceID)
	resp, err := httpRetry(http.MethodDelete, expireURL, user, password)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return errors.New(string(body))
	}
	return nil
}

// Resolved reports whether a silence has already ended.
func Resolved(s types.Silence) bool {
	if s.EndsAt.IsZero() {
		return false
	}
	return !s.EndsAt.After(time.Now())
}
