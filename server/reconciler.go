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
