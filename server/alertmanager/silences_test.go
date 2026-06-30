package alertmanager

import (
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"

	"github.com/prometheus/alertmanager/api/v2/models"
)

// ptrDateTime wraps a time.Time as *strfmt.DateTime — the swagger model
// uses pointers to strfmt.DateTime for required date fields, so test
// fixtures need two layers (convert + take address).
func ptrDateTime(t time.Time) *strfmt.DateTime {
	d := strfmt.DateTime(t)
	return &d
}

func TestResolved(t *testing.T) {
	// Zero value (EndsAt == nil) — Resolved should report false
	// because we don't know when (or whether) it ends.
	s := &models.GettableSilence{}
	assert.False(t, Resolved(s))

	// Ends one minute from now — not yet resolved.
	s.EndsAt = ptrDateTime(time.Now().Add(time.Minute))
	assert.False(t, Resolved(s))

	// Ended one minute ago — resolved.
	s.EndsAt = ptrDateTime(time.Now().Add(-1 * time.Minute))
	assert.True(t, Resolved(s))
}
