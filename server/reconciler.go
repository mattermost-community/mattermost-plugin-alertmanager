package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

// Make sure time is referenced from the package even if reconcileOrphans
// happens to not use it directly (it does, via time.NewTicker).
var _ = time.Now

// reconcilerInterval is the period at which the plugin re-checks every
// registered receiver's underlying Mattermost incoming webhook. Five
// minutes balances "tolerable lag between out-of-band webhook delete
// and plugin cleanup" against "noise in plugin logs / API call rate."
const reconcilerInterval = 5 * time.Minute

// reconcilerJobKey is the cluster-mutex key under which the reconcile
// job runs. Choice of name matters: the pluginapi cluster package uses
// this string to elect a single leader across all MM pod replicas, so
// don't change it without coordinating an upgrade.
const reconcilerJobKey = "alertmanager-orphan-reconciler"

// startReconciler registers the periodic reconcile job with the
// pluginapi cluster scheduler. Only ONE MM pod runs each cycle at a
// time — cluster.Schedule handles leader election via a KV mutex, so
// in HA Kubernetes deployments we avoid N pods racing on the same
// SavePluginConfig writes.
//
// Returns a stop function the caller (OnDeactivate) invokes to halt
// the scheduled job. If scheduling fails (KV mutex unavailable etc.),
// the plugin logs and returns a no-op stop — the plugin keeps working,
// just without automatic orphan pruning. Manual /alertmanager reconcile
// remains available.
//
// On each cycle the leader resolves an active system admin and mints
// a short-lived PAT for them to call Client4.GetIncomingWebhook — the
// plugin RPC API doesn't expose webhook CRUD, only Client4 does. The
// PAT is revoked immediately after the cycle. Audit log will show
// "ephemeral token minted by plugin for sysadmin X" entries every
// reconcilerInterval, from whichever pod won the leader election.
func (p *Plugin) startReconciler() func() {
	job, err := cluster.Schedule(
		p.API,
		reconcilerJobKey,
		cluster.MakeWaitForInterval(reconcilerInterval),
		p.runBackgroundReconcile,
	)
	if err != nil {
		p.API.LogWarn("reconciler: failed to schedule cluster job; automatic orphan pruning disabled (manual /alertmanager reconcile still works)",
			"err", err.Error())
		return func() {}
	}
	return func() {
		if err := job.Close(); err != nil {
			p.API.LogWarn("reconciler: failed to close scheduled job", "err", err.Error())
		}
	}
}

// runBackgroundReconcile resolves a sysadmin to act as and runs one
// reconcile pass. Errors are logged, not returned — the goroutine
// has no caller to return to.
//
// Also piggybacks the YAML auto-delete janitor onto this tick so we
// don't run a second goroutine with its own scheduling/leader-election.
//
// Always stamps the last-run timestamp ONCE at the end with the
// actual pruned count. Earlier version used a deferred
// recordReconcileRun(0) which overrode any in-body call with the
// real count — single explicit call here avoids the order-of-ops bug.
func (p *Plugin) runBackgroundReconcile() {
	pruneCount := 0
	// Always stamp at the end. Use a deferred call with a closure that
	// reads pruneCount at evaluation time (not at defer-registration
	// time) so the real count gets recorded.
	defer func() { p.recordReconcileRun(pruneCount) }()

	// Janitor runs first — independent of reconcileOrphans and doesn't
	// need a sysadmin context.
	p.sweepExpiredYAML()

	if len(p.getConfiguration().AlertConfigs) == 0 {
		return
	}
	sysadminID, err := p.findActiveSysadmin()
	if err != nil {
		p.API.LogWarn("reconciler: no active sysadmin available to act as; skipping cycle",
			"err", err.Error())
		return
	}
	pruned, err := p.reconcileOrphans(sysadminID)
	if err != nil {
		p.API.LogWarn("reconciler: cycle failed", "err", err.Error())
		return
	}
	pruneCount = len(pruned)

	// Rotation reminders run after orphan pruning so we don't waste
	// effort reminding for receivers about to be pruned this cycle.
	// Errors are non-fatal — keep the reconcile cycle's primary
	// purpose (orphan pruning) successful even if reminders fail.
	if err := p.checkRotationReminders(sysadminID); err != nil {
		p.API.LogWarn("reconciler: rotation reminder pass failed (continuing)",
			"err", err.Error())
	}
}

// rotationOverdueEntry is one receiver flagged as overdue. Hoisted
// to package scope so checkRotationReminders and sendRotationReminderDM
// share the same type (Go's inferred local-struct types don't match
// across function boundaries).
type rotationOverdueEntry struct {
	Name          string
	DaysOverdue   int
	LastRotatedAt time.Time
}

// reminderRepeatInterval throttles how often a single receiver can
// be reminded about overdue rotation. Once an admin gets a DM, they
// have this long to act before the plugin nags again. One week
// matches "I'll get to it in the next maintenance window" without
// drifting into "I forgot about that DM" territory.
const reminderRepeatInterval = 7 * 24 * time.Hour

// checkRotationReminders scans every receiver for overdue rotation
// and DMs sysadmins when found. Grouped by channel so admins get
// one DM per channel listing all overdue receivers there, instead
// of N DMs per receiver.
//
// Skipped silently when WebhookRotationDays is 0 (feature disabled).
//
// Stamps LastRotatedAt = now() for receivers with zero-value
// LastRotatedAt — those were created before this feature shipped,
// and we want them to start a fresh clock rather than fire reminders
// day-one. This is a one-time migration per receiver.
func (p *Plugin) checkRotationReminders(sysadminID string) error {
	cfg := p.getConfiguration()
	if cfg.WebhookRotationDays <= 0 {
		return nil
	}
	threshold := time.Duration(cfg.WebhookRotationDays) * 24 * time.Hour
	now := time.Now().UTC()

	current := cfg.AlertConfigs
	if len(current) == 0 {
		return nil
	}

	// First pass: identify zero-value LastRotatedAt entries that need
	// the migration stamp. Mutate a working copy; persist at the end.
	updated := make([]alertConfig, len(current))
	copy(updated, current)
	mutated := false
	for i := range updated {
		if updated[i].LastRotatedAt.IsZero() {
			updated[i].LastRotatedAt = now
			mutated = true
		}
	}

	// Second pass: find overdue receivers, group by channel, and DM.
	byChannel := make(map[string][]rotationOverdueEntry)
	channelToTeam := make(map[string]string)
	indexByName := make(map[string]int, len(updated))
	for i, c := range updated {
		indexByName[c.Name] = i
		// Per-receiver opt-in: skip receivers that weren't created
		// with the `on` flag at /alertmanager add time. The global
		// WebhookRotationDays threshold only fires reminders for
		// receivers that explicitly opted in.
		if !c.RotationRemindersEnabled {
			continue
		}
		if c.LastRotatedAt.IsZero() {
			continue // just stamped above; will fire on the next cycle
		}
		age := now.Sub(c.LastRotatedAt)
		if age < threshold {
			continue
		}
		// Throttle: skip if we already reminded recently.
		if !c.LastReminderAt.IsZero() && now.Sub(c.LastReminderAt) < reminderRepeatInterval {
			continue
		}
		daysOverdue := int((age - threshold) / (24 * time.Hour))
		byChannel[c.Channel] = append(byChannel[c.Channel], rotationOverdueEntry{
			Name:          c.Name,
			DaysOverdue:   daysOverdue,
			LastRotatedAt: c.LastRotatedAt,
		})
		channelToTeam[c.Channel] = c.Team
	}

	if len(byChannel) > 0 {
		// Resolve a recipient — for v1, DM the calling sysadmin we
		// already minted a PAT for. Future: enumerate channel team
		// admins via GetChannelMembers + filter by role.
		dm, appErr := p.API.GetDirectChannel(p.BotUserID, sysadminID)
		if appErr != nil {
			return fmt.Errorf("open DM channel with sysadmin %s: %w", sysadminID, appErr)
		}

		for channel, entries := range byChannel {
			p.sendRotationReminderDM(dm.Id, channel, entries, cfg.WebhookRotationDays)
			// Stamp LastReminderAt on each entry we just notified.
			for _, e := range entries {
				if idx, ok := indexByName[e.Name]; ok {
					updated[idx].LastReminderAt = now
					mutated = true
				}
			}
			p.auditLog("webhook.rotation.reminder", sysadminID, channel,
				"", fmt.Sprintf("count=%d", len(entries)))
		}
	}

	if mutated {
		if err := p.saveConfigs(updated); err != nil {
			return fmt.Errorf("persist rotation timestamps: %w", err)
		}
	}
	return nil
}

// sendRotationReminderDM posts the per-channel reminder summary as
// a bot message in the sysadmin's DM. Best-effort — logged on
// failure but not returned, because reminder send for one channel
// shouldn't block other channels' reminders in the same cycle.
func (p *Plugin) sendRotationReminderDM(dmChannelID, channel string, entries []rotationOverdueEntry, thresholdDays int) {
	var b strings.Builder
	b.WriteString(":warning: **Webhook rotation due**\n\n")
	b.WriteString(fmt.Sprintf("The following receiver(s) in **#%s** haven't been rotated in over %d days:\n\n", channel, thresholdDays))
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("- `%s` — last rotated %s ago (%d days overdue)\n",
			e.Name,
			humanDuration(time.Since(e.LastRotatedAt)),
			e.DaysOverdue,
		))
	}
	b.WriteString(fmt.Sprintf("\nRotate them when convenient. In #%s, run:\n\n```\n/alertmanager rotate all --overdue\n```\n\n", channel))
	b.WriteString("You'll receive a DM with the updated YAML. Paste it into your `alertmanager.yml` and reload AM (`curl -X POST http://<am>/-/reload`). Old URLs deactivate immediately on rotation.\n\n")
	b.WriteString(fmt.Sprintf("See the [rotation playbook](%s/plugins/%s/public/help/rotation.html) for the full procedure.\n",
		p.siteURL(), Manifest.Id))

	post := &model.Post{
		UserId:    p.BotUserID,
		ChannelId: dmChannelID,
		Message:   b.String(),
	}
	if _, appErr := p.API.CreatePost(post); appErr != nil {
		p.API.LogWarn("rotation reminder DM failed", "channel", channel, "err", appErr.Error())
	}
}

// reconcileOrphans walks the current AlertConfigs and prunes any whose
// underlying Mattermost incoming webhook returns 404 from the API.
// `actingUserID` is the user whose identity the plugin temporarily
// assumes (via short-lived PAT) to query the webhook API. Must be a
// sysadmin or it'll fail with 403.
//
// Transient/permission errors (non-404 API failures) are logged and
// the receiver is NOT pruned — we'd rather leave a working entry alone
// than yank it on a flaky GetIncomingWebhook call. The next reconcile
// cycle will revisit.
//
// Race against handleAdd / handleRotate: if either runs concurrently,
// the reconciler's filtered save could overwrite the new entry. The
// window is small (between getConfiguration here and saveConfigs) and
// the next cycle picks up the eventual-consistent truth. Acceptable
// for v1 — alternative is a global write mutex, which complicates
// every config-mutating path for a rare edge case.
func (p *Plugin) reconcileOrphans(actingUserID string) ([]string, error) {
	current := p.getConfiguration().AlertConfigs
	if len(current) == 0 {
		return nil, nil
	}

	c, cleanup, err := p.ephemeralClient4(actingUserID)
	if err != nil {
		return nil, fmt.Errorf("mint reconciler PAT: %w", err)
	}
	defer cleanup()

	orphanSet := make(map[string]bool)
	for _, ac := range current {
		_, resp, err := c.GetIncomingWebhook(context.Background(), ac.WebhookID, "")
		if err == nil {
			continue
		}
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			orphanSet[ac.Name] = true
			continue
		}
		// Transient or permission error — don't prune. Log and move on.
		p.API.LogWarn("reconciler: error checking webhook (will retry next cycle)",
			"receiver", ac.Name, "webhookID", ac.WebhookID, "err", err.Error())
	}

	if len(orphanSet) == 0 {
		return nil, nil
	}

	// Re-read inside the write-modify path to minimize the race window
	// with concurrent add/rotate operations.
	fresh := p.getConfiguration().AlertConfigs
	filtered := make([]alertConfig, 0, len(fresh))
	pruned := make([]string, 0, len(orphanSet))
	for _, c := range fresh {
		if orphanSet[c.Name] {
			pruned = append(pruned, c.Name)
			continue
		}
		filtered = append(filtered, c)
	}

	if len(pruned) == 0 {
		return nil, nil
	}

	if err := p.saveConfigs(filtered); err != nil {
		p.API.LogWarn("reconciler: failed to persist after pruning orphans",
			"pruned", strings.Join(pruned, ","), "err", err.Error())
		return nil, fmt.Errorf("persist filtered config: %w", err)
	}

	p.API.LogInfo("reconciler: pruned orphan receivers (webhooks deleted out-of-band)",
		"count", len(pruned), "names", strings.Join(pruned, ","))
	return pruned, nil
}

// recordReconcileRun stamps the last-run timestamp + pruned count on
// the Plugin struct. Read by the admin inventory page's health banner.
// Memory-only — resets on plugin restart, which is fine for a
// "is the janitor running?" indicator.
func (p *Plugin) recordReconcileRun(pruned int) {
	p.reconcilerStatusLock.Lock()
	defer p.reconcilerStatusLock.Unlock()
	p.reconcilerLastRun = time.Now()
	p.reconcilerLastPruned = pruned
}

// reconcileStatus returns the last reconcile timestamp + pruned count
// for the admin inventory health banner.
func (p *Plugin) reconcileStatus() (lastRun time.Time, pruned int) {
	p.reconcilerStatusLock.Lock()
	defer p.reconcilerStatusLock.Unlock()
	return p.reconcilerLastRun, p.reconcilerLastPruned
}

// findActiveSysadmin returns the user ID of an active (non-deleted,
// non-bot) system admin. Used by the background reconciler to mint a
// PAT — the goroutine has no calling user, so it has to borrow some
// admin's identity to call Client4. First match wins; this isn't a
// load-balancing decision, just "find someone authorized."
func (p *Plugin) findActiveSysadmin() (string, error) {
	users, appErr := p.API.GetUsers(&model.UserGetOptions{
		Role:    model.SystemAdminRoleId,
		Page:    0,
		PerPage: 100,
	})
	if appErr != nil {
		return "", fmt.Errorf("list users: %w", appErr)
	}
	for _, u := range users {
		if u.DeleteAt != 0 {
			continue
		}
		if u.IsBot {
			continue
		}
		if !strings.Contains(u.Roles, model.SystemAdminRoleId) {
			continue
		}
		return u.Id, nil
	}
	return "", errors.New("no active human system admin found")
}

// handleReconcile is the manual trigger for the orphan-prune logic
// (`/alertmanager reconcile`). Sysadmin-only — the operation mutates
// plugin config, and only sysadmins can read webhook state anyway.
//
// The slash command exists alongside the background goroutine for two
// reasons: (1) impatient cleanup after a known System Console webhook
// delete, and (2) verifying the reconciler works during development.
func (p *Plugin) handleReconcile(args *model.CommandArgs) (string, error) {
	if err := p.requireSystemAdmin(args.UserId); err != nil {
		return err.Error(), nil
	}

	pruned, err := p.reconcileOrphans(args.UserId)
	if err != nil {
		return fmt.Sprintf(":warning: Reconcile failed: %v", err), nil
	}
	if len(pruned) == 0 {
		return ":white_check_mark: Reconcile complete — no orphan receivers found. All registered webhooks are still alive in Mattermost.", nil
	}
	return fmt.Sprintf(
		":wastebasket: Reconcile complete — pruned %d orphan receiver(s) whose webhooks were deleted out-of-band:\n\n- `%s`\n\n"+
			"_Don't forget to delete the corresponding `slack_configs` blocks from your `alertmanager.yml` if you haven't already, then reload Alertmanager._",
		len(pruned),
		strings.Join(pruned, "`\n- `"),
	), nil
}
