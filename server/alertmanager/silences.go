package alertmanager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/prometheus/alertmanager/api/v2/models"
)

// silenceIDRegex matches an Alertmanager silence ID, which is a UUID — the
// pinned AM versions mint v4 UUIDs for silences. Validating the ID against this
// grammar before it is interpolated into the request path blocks path traversal
// (`../../../-/reload`) and query injection (`?`) through an attacker-controlled
// ID: Go's HTTP client does not normalize dot segments, so a raw traversal would
// otherwise reach the wire with the basic-auth header attached (CL-08).
var silenceIDRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidSilenceID reports whether id has the UUID grammar of a silence ID.
func ValidSilenceID(id string) bool {
	return silenceIDRegex.MatchString(id)
}

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
	body, _, err := httpRetry(http.MethodGet, alertmanagerURL+"/api/v2/silences", user, password)
	if err != nil {
		return nil, err
	}

	var silences []*models.GettableSilence
	if errDec := DecodeJSONLimited(bytes.NewReader(body), &silences); errDec != nil {
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
	_, statusCode, err := httpRetry(http.MethodDelete, expireURL, user, password)
	if err != nil {
		return err
	}

	// Do NOT reflect the response body: on a path that reaches a proxied backend,
	// echoing the body would turn a non-200 into a content-disclosure oracle.
	// Return a generic, status-only error instead (CL-08).
	if statusCode != http.StatusOK {
		return fmt.Errorf("expiring silence: Alertmanager returned status %d", statusCode)
	}
	return nil
}

// SilenceMatcher is one label match on a silence. Regex true is Alertmanager's
// `=~` operator. The name and value are sent as JSON fields, not as a URL.
type SilenceMatcher struct {
	Name  string
	Value string
	Regex bool
}

type silenceIDResponse struct {
	SilenceID string `json:"silenceID"`
}

// CreateSilence posts a new silence. The caller chooses the end time. This
// function does not invent a duration. The returned id is a UUID. A body that
// is not that shape is rejected so a hostile Alertmanager cannot put markdown
// into the command reply.
func CreateSilence(alertmanagerURL, user, password, createdBy, comment string, endsAt time.Time, matchers []SilenceMatcher) (string, error) {
	if len(matchers) == 0 {
		return "", fmt.Errorf("silence requires at least one matcher")
	}
	if !endsAt.After(time.Now()) {
		return "", fmt.Errorf("silence end time is not in the future")
	}

	now := strfmt.DateTime(time.Now().UTC())
	end := strfmt.DateTime(endsAt.UTC())
	amMatchers := make(models.Matchers, 0, len(matchers))
	for _, m := range matchers {
		isRegex := m.Regex
		isEqual := true
		name := m.Name
		value := m.Value
		amMatchers = append(amMatchers, &models.Matcher{
			IsRegex: &isRegex,
			IsEqual: &isEqual,
			Name:    &name,
			Value:   &value,
		})
	}
	payload := models.PostableSilence{
		Silence: models.Silence{
			Comment:   &comment,
			CreatedBy: &createdBy,
			StartsAt:  &now,
			EndsAt:    &end,
			Matchers:  amMatchers,
		},
	}
	raw, err := payload.MarshalJSON()
	if err != nil {
		return "", fmt.Errorf("encode silence: %w", err)
	}

	body, _, err := httpPostJSON(alertmanagerURL+"/api/v2/silences", user, password, raw)
	if errors.Is(err, ErrOutcomeUnknown) {
		if id, ok := findCreatedSilence(alertmanagerURL, user, password, &payload.Silence); ok {
			return id, nil
		}
		return "", &RequestError{
			msg: "Alertmanager did not confirm the silence, so it may exist anyway. Check `/alertmanager silence list` before trying again",
			err: err,
		}
	}
	if err != nil {
		return "", err
	}
	var decoded silenceIDResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("alertmanager did not return a silence id")
	}
	if !silenceIDRegex.MatchString(decoded.SilenceID) {
		return "", fmt.Errorf("alertmanager returned a silence id that is not a UUID")
	}
	return decoded.SilenceID, nil
}

// findCreatedSilence looks for the silence a POST with a lost reply may have
// created, so the caller records it instead of leaving an untracked silence.
// It matches creator, comment, matchers and end time, which together identify
// the request; StartsAt is not compared because Alertmanager moves a past
// start to the time it stored the silence.
func findCreatedSilence(alertmanagerURL, user, password string, want *models.Silence) (string, bool) {
	silences, err := ListSilences(alertmanagerURL, user, password)
	if err != nil {
		return "", false
	}
	wantEnd := time.Time(*want.EndsAt)
	for _, s := range silences {
		if s.ID == nil || !ValidSilenceID(*s.ID) {
			continue
		}
		if s.Status != nil && s.Status.State != nil && *s.Status.State == models.SilenceStatusStateExpired {
			continue
		}
		if deref(s.CreatedBy) != *want.CreatedBy || deref(s.Comment) != *want.Comment {
			continue
		}
		if d := silenceEndsAt(s).Sub(wantEnd); d < -time.Second || d > time.Second {
			continue
		}
		if sameMatchers(s.Matchers, want.Matchers) {
			return *s.ID, true
		}
	}
	return "", false
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func matcherKey(m *models.Matcher) string {
	if m == nil {
		return ""
	}
	isEqual := m.IsEqual == nil || *m.IsEqual
	isRegex := m.IsRegex != nil && *m.IsRegex
	return fmt.Sprintf("%q %q %t %t", deref(m.Name), deref(m.Value), isEqual, isRegex)
}

func sameMatchers(a, b models.Matchers) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, m := range a {
		counts[matcherKey(m)]++
	}
	for _, m := range b {
		k := matcherKey(m)
		if counts[k] == 0 {
			return false
		}
		counts[k]--
	}
	return true
}

// Resolved reports whether a silence has already ended.
func Resolved(s *models.GettableSilence) bool {
	endsAt := silenceEndsAt(s)
	if endsAt.IsZero() {
		return false
	}
	return !endsAt.After(time.Now())
}
