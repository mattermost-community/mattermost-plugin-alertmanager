package alertmanager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/prometheus/alertmanager/api/v2/models"
)

// silenceIDPattern restricts a caller-supplied silence ID to the shape
// Alertmanager actually issues (a UUID or a ULID), before it is
// interpolated into the request URL. ExpireSilence builds its request
// URL by string concatenation (alertmanagerURL + "/api/v2/silence/" +
// silenceID), so an unvalidated silenceID is as much a path-control
// vector as the base URL half of security finding F-002 — a caller who
// controls both halves could smuggle a query string, a fragment, or
// dot-segment path traversal into the request. 1-64 chars of
// [A-Za-z0-9-] comfortably fits a 36-char UUID and a 26-char ULID.
// Compiled once at package init rather than per call.
var silenceIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

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
	if !silenceIDPattern.MatchString(silenceID) {
		return fmt.Errorf("invalid silence ID %q: must be 1-64 characters of [A-Za-z0-9-]", silenceID)
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
