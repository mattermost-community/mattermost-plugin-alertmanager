package main

import (
	"encoding/json"
	"strings"
	"time"
)

// yaml_janitor.go owns the lifecycle of DM'd YAML file attachments
// (alertmanager-receivers.yml, alertmanager-routes.yml). These files
// contain webhook URLs — channel-bound bearer tokens — so they get a
// finite retention period to limit long-term exposure if a user
// account is later compromised.
//
// Mattermost's plugin API doesn't expose direct file deletion. The
// approximation: delete the DM POST that has the files attached.
// Once the post is gone the files have no referencing post and become
// unreachable to the user (the actual file blob may linger in storage
// until MM's own GC runs, but it's not retrievable through chat).
//
// Storage: plugin KV under the prefix `yaml-delivery:`. Each entry
// records the post ID + creation timestamp. The reconciler ticks
// every 5 min and prunes entries past TTL via DeletePost.

// yamlDeliveryKeyPrefix isolates auto-delete-tracked post IDs in the
// plugin KV namespace. Don't change without coordinating a migration.
const yamlDeliveryKeyPrefix = "yaml-delivery:"

// yamlDeliveryRecord is the per-post state stored in plugin KV.
// Marshaled to JSON so future fields (e.g., per-post TTL override)
// don't break backward compat.
type yamlDeliveryRecord struct {
	PostID    string `json:"post_id"`
	CreatedAt int64  `json:"created_at"` // unix seconds
}

// trackYAMLForAutoDelete records a DM post ID for later janitor
// cleanup. Called immediately after a successful CreatePost. If TTL
// is disabled (configured to 0) this is a no-op.
func (p *Plugin) trackYAMLForAutoDelete(postID string) {
	if postID == "" {
		return
	}
	ttlHours := p.getConfiguration().AssembledYAMLTTLHours
	if ttlHours <= 0 {
		return // disabled — posts persist forever
	}

	rec := yamlDeliveryRecord{
		PostID:    postID,
		CreatedAt: time.Now().Unix(),
	}
	blob, err := json.Marshal(rec)
	if err != nil {
		p.API.LogWarn("yaml-janitor: failed to marshal record", "postID", postID, "err", err.Error())
		return
	}
	if appErr := p.API.KVSet(yamlDeliveryKeyPrefix+postID, blob); appErr != nil {
		p.API.LogWarn("yaml-janitor: failed to track post for auto-delete (post will persist beyond TTL)",
			"postID", postID, "err", appErr.Error())
	}
}

// sweepExpiredYAML walks the KV namespace and deletes any tracked
// post older than the configured TTL. Called from the reconciler
// cycle so we piggyback on existing scheduling.
//
// Errors on individual posts are logged and skipped — the sweep is
// best-effort, missed entries are picked up on the next cycle.
func (p *Plugin) sweepExpiredYAML() {
	ttlHours := p.getConfiguration().AssembledYAMLTTLHours
	if ttlHours <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(ttlHours) * time.Hour).Unix()

	const perPage = 200
	for page := 0; ; page++ {
		keys, appErr := p.API.KVList(page, perPage)
		if appErr != nil {
			p.API.LogWarn("yaml-janitor: KVList failed (skipping this cycle)", "err", appErr.Error())
			return
		}
		if len(keys) == 0 {
			return
		}
		pruned := 0
		for _, k := range keys {
			if !strings.HasPrefix(k, yamlDeliveryKeyPrefix) {
				continue
			}
			blob, appErr := p.API.KVGet(k)
			if appErr != nil || len(blob) == 0 {
				continue
			}
			var rec yamlDeliveryRecord
			if err := json.Unmarshal(blob, &rec); err != nil {
				_ = p.API.KVDelete(k)
				continue
			}
			if rec.CreatedAt > cutoff {
				continue
			}
			if appErr := p.API.DeletePost(rec.PostID); appErr != nil && appErr.StatusCode != 404 {
				p.API.LogWarn("yaml-janitor: DeletePost failed (will retry next cycle)",
					"postID", rec.PostID, "err", appErr.Error())
				continue
			}
			if appErr := p.API.KVDelete(k); appErr != nil {
				p.API.LogWarn("yaml-janitor: KVDelete failed", "key", k, "err", appErr.Error())
				continue
			}
			pruned++
		}
		if pruned > 0 {
			p.API.LogInfo("yaml-janitor: swept expired YAML deliveries", "pruned", pruned, "ttl_hours", ttlHours)
		}
		if len(keys) < perPage {
			return
		}
	}
}
