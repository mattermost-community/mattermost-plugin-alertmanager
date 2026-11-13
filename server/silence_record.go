package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-alertmanager/server/alertmanager"
)

const (
	kvKeySilencePrefix = "silence:v1:"
	// The record outlives the silence a little so a late expire still finds it.
	silenceRecordGrace = time.Hour
)

// silenceRecord ties a silence created by this plugin to the channel and
// Alertmanager it was created for. Expire only acts on silences with a record
// for the calling channel, so a team admin cannot end another team's silence
// on a shared Alertmanager.
type silenceRecord struct {
	ChannelID string `json:"channel_id"`
	AMURL     string `json:"am_url"`
	CreatedBy string `json:"created_by"`
}

func silenceRecordKey(silenceID string) string {
	return kvKeySilencePrefix + silenceID
}

// One key per silence, so concurrent creates never overwrite each other.
func (p *Plugin) recordSilence(silenceID string, rec silenceRecord, ttl time.Duration) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	expiry := int64((ttl + silenceRecordGrace).Seconds())
	if appErr := p.API.KVSetWithExpiry(silenceRecordKey(silenceID), raw, expiry); appErr != nil {
		return fmt.Errorf("record silence %s: %w", silenceID, appErr)
	}
	return nil
}

// silenceCreatedFor reports whether this plugin created the silence from the
// given channel against the given Alertmanager.
func (p *Plugin) silenceCreatedFor(silenceID, channelID, amURL string) (bool, error) {
	raw, appErr := p.API.KVGet(silenceRecordKey(silenceID))
	if appErr != nil {
		return false, fmt.Errorf("read silence record %s: %w", silenceID, appErr)
	}
	if len(raw) == 0 {
		return false, nil
	}
	var rec silenceRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return false, nil
	}
	return rec.ChannelID == channelID && rec.AMURL == amURL, nil
}

// silenceExpirable is the list-view check. Any lookup problem counts as not
// expirable so the list never offers a command that would be refused.
func (p *Plugin) silenceExpirable(silenceID, channelID, amURL string) bool {
	id := strings.ToLower(silenceID)
	if !alertmanager.ValidSilenceID(id) {
		return false
	}
	ok, err := p.silenceCreatedFor(id, channelID, amURL)
	return err == nil && ok
}
