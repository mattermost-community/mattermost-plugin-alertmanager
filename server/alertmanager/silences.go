package alertmanager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/prometheus/alertmanager/api/v2/models"
)

// silenceEndsAt extracts EndsAt as a time.Time, with zero value if nil.
// v2 API uses *strfmt.DateTime (pointer to a time.Time alias); the
// upstream "Required: true" annotation means non-malicious servers
// always send it, but a nil check guards against bad responses.
func silenceEndsAt(s *models.GettableSilence) time.Time {
	if s == nil || s.EndsAt == nil {
		return time.Time{}
	}
	return time.Time(*s.EndsAt)
}

// ListSilences queries the Alertmanager /api/v2/silences endpoint and returns
// silences sorted by EndsAt descending (most recently-ending first).
//
// Returns []*models.GettableSilence — that's the swagger-generated type for
// the GET response in prometheus/alertmanager >= v0.31, which replaced the
// removed types.Silence struct.
func ListSilences(alertmanagerURL, user, password string) ([]*models.GettableSilence, error) {
	resp, err := httpRetry(http.MethodGet, alertmanagerURL+"/api/v2/silences", user, password)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var silences []*models.GettableSilence
	if errDec := json.NewDecoder(resp.Body).Decode(&silences); errDec != nil {
		return nil, errDec
	}

	sort.Slice(silences, func(i, j int) bool {
		return silenceEndsAt(silences[i]).After(silenceEndsAt(silences[j]))
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
func Resolved(s *models.GettableSilence) bool {
	endsAt := silenceEndsAt(s)
	if endsAt.IsZero() {
		return false
	}
	return !endsAt.After(time.Now())
}
