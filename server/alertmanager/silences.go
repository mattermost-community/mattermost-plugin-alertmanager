package alertmanager

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/prometheus/alertmanager/api/v2/models"
)

// silenceIDRegex matches an Alertmanager silence ID, which is a UUID — the
// pinned AM versions mint v4 UUIDs for silences. Validating the ID against this
// grammar before it is interpolated into the request path blocks path traversal
// (`../../../-/reload`) and query injection (`?`) through an attacker-controlled
// ID: Go's HTTP client does not normalize dot segments, so a raw traversal would
// otherwise reach the wire with the basic-auth header attached (CL-08).
var silenceIDRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

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
	if errDec := DecodeJSONLimited(resp.Body, &silences); errDec != nil {
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
	// Reject anything that isn't a UUID before it touches the request path (CL-08).
	if !silenceIDRegex.MatchString(silenceID) {
		return fmt.Errorf("invalid silence ID: must be a UUID")
	}

	expireURL := fmt.Sprintf("%s/api/v2/silence/%s", alertmanagerURL, silenceID)
	resp, err := httpRetry(http.MethodDelete, expireURL, user, password)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// Do NOT reflect the response body: on a path that reaches a proxied backend,
	// echoing the body would turn a non-200 into a content-disclosure oracle.
	// Return a generic, status-only error instead (CL-08).
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Alertmanager returned status %d when expiring the silence", resp.StatusCode)
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
