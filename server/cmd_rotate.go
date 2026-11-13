package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// handleRotate dispatches to single-receiver or bulk-overdue paths
// based on the args. Both share the underlying "delete old webhook,
// create a new one, persist, stamp LastRotatedAt" mechanism, just
// applied to one entry vs. many.
//
// Usage:
//
//	/alertmanager config rotate <name>           # one receiver by name
//	/alertmanager config rotate all overdue    # every receiver in this
//	                                       # channel past its rotation
//	                                       # threshold (sysadmin / team
//	                                       # admin only; reads
//	                                       # WebhookRotationDays from
//	                                       # System Console)
func (p *Plugin) handleRotate(args *model.CommandArgs) (string, error) {
	if err := p.requireChannelTeamAdmin(args.UserId, args.ChannelId); err != nil {
		return err.Error(), nil
	}

	fields := strings.Fields(args.Command)
	if len(fields) < 3 {
		return "Usage: `/alertmanager config rotate <name>` or `/alertmanager config rotate all overdue`", nil
	}
	target := fields[2]
	rest := fields[3:]

	if target == "all" && containsFlag(rest, "overdue") {
		return p.handleRotateOverdue(args)
	}

	summary, _, err := p.handleRotateSingle(args, target)
	return summary, err
}

// handleRotateSingle rotates the underlying Mattermost webhook for one
// receiver (or, in the group-webhook case, every receiver sharing that
// webhook). Stamps LastRotatedAt + clears LastReminderAt on every
// affected entry.
//
// Group-webhook behavior (v1.0.3+): rotating a grouped receiver rotates
// the SHARED webhook. Every receiver in that group gets the new URL —
// alertmanager.yml must be updated for all of them, not just the one
// the operator named. The response message lists the full affected set
// and (for groups) DMs the merged YAML bundle.
// The returned rotated bool is the authoritative success signal — true only when
// a webhook was actually rotated. handleRotateOverdue keys its accounting off it
// rather than string-matching the summary, so a copy-edit to the messages can't
// silently miscount a failure as a success.
func (p *Plugin) handleRotateSingle(args *model.CommandArgs, name string) (summary string, rotated bool, err error) {
	// Atomic read-modify-write. handleRotateOverdue calls this in a loop but
	// does not hold configWriteMu itself, so locking per-call is deadlock-free
	// and each rotation is an independent atomic update.
	p.configWriteMu.Lock()
	defer p.configWriteMu.Unlock()

	// CL-02: scope resolution to this team+channel and confirm membership before
	// touching anything — same guard as handleRemoveOne. Rotating another team's
	// receiver is blocked by the webhook API's 403 anyway, but scoping stops the
	// management-plane probe (and the cross-team name disclosure) before that.
	scoped := p.configsForCurrentChannel(args)
	resolved := resolveReceiverName(scoped, name, args.ChannelId, p)
	if !receiverNameInScope(scoped, resolved) {
		return fmt.Sprintf("Receiver %q not found.", name), false, nil
	}
	// Read the target's current shape from the in-memory snapshot for the webhook
	// side effects and display; the durable update below re-reads from KV and keys
	// off the webhook ID, so a concurrent write can't make us rotate the wrong set.
	var target alertConfig
	found := false
	for _, c := range scoped {
		if c.Name == resolved {
			target, found = c, true
			break
		}
	}
	if !found {
		return fmt.Sprintf("Receiver %q not found.", name), false, nil
	}
	oldHookID := target.WebhookID

	// target.Team/Channel are already canonical, so only the channel ID is needed.
	channelID, err := p.resolveChannelID(target.Team, target.Channel)
	if err != nil {
		return fmt.Sprintf("Failed to resolve destination channel for rotation: %v", err), false, nil
	}

	// Webhook display name for the replacement follows the same rule as
	// /alertmanager config add: the receiver-name format <base>--<team>-<channel>.
	// Group receivers use the category as the base; individual/legacy
	// receivers use their runbook slug, so the display name lines up with the
	// receiver name in System Console (and legacy entries pick up the
	// team+channel disambiguation on their next rotation).
	var newDisplayName string
	if target.GroupName != "" {
		newDisplayName = webhookDisplayNameFor(target.GroupName, target.Team, target.Channel)
	} else {
		newDisplayName = webhookDisplayNameFor(receiverBaseSlug(target.Name), target.Team, target.Channel)
	}
	newHookID, err := p.createIncomingWebhook(args.UserId, channelID, newDisplayName)
	if err != nil {
		return fmt.Sprintf("Failed to create replacement webhook: %v", err), false, nil
	}

	// Durable update: repoint every receiver sharing the old webhook ID to the new
	// one. Keyed off oldHookID (not indices from the stale snapshot) so it stays
	// correct against the freshly-read KV list; the create above already ran and is
	// NOT replayed on a CAS retry. The OLD webhook is deleted only AFTER this
	// commits (below), so a CAS failure leaves the old token live and the receiver
	// still resolvable — recoverable by re-running rotate — rather than pointing at
	// a webhook we already destroyed.
	now := time.Now().UTC()
	_, after, err := p.updateConfigsAtomic(func(current []alertConfig) ([]alertConfig, error) {
		out := make([]alertConfig, len(current))
		copy(out, current)
		for i := range out {
			if out[i].WebhookID == oldHookID {
				out[i].WebhookID = newHookID
				out[i].LastRotatedAt = now
				out[i].LastReminderAt = time.Time{}
			}
		}
		return out, nil
	})
	if err != nil {
		warn := ""
		if delErr := p.deleteIncomingWebhook(args.UserId, newHookID); delErr != nil {
			p.API.LogWarn("rotate rollback: could not delete the new webhook (orphan may remain)", "webhook", redactHookID(newHookID), "err", delErr.Error())
			warn = webhookRollbackWarning(newDisplayName)
		}
		return fmt.Sprintf("Failed to persist rotated config (new webhook rolled back): %v%s", err, warn), false, nil
	}

	// The affected set is every receiver now carrying the new webhook ID.
	affected := make([]alertConfig, 0, 1)
	for _, c := range after {
		if c.WebhookID == newHookID {
			affected = append(affected, c)
		}
	}
	if len(affected) == 0 {
		// A concurrent rotate/remove already repointed or dropped these receivers
		// between our snapshot and the durable write, so the webhook we just minted
		// serves nothing — clean it up rather than leave an orphan.
		_ = p.deleteIncomingWebhook(args.UserId, newHookID)
		return fmt.Sprintf("Receiver `%s` was modified concurrently; nothing was rotated. Re-run if still needed.", resolved), false, nil
	}

	// The repoint committed — now retire the old webhook. Doing it here (not before
	// the write) means every failure path above left the old token live and the
	// receiver intact. Track whether it actually deleted (B-003): rotation is often
	// triggered by a suspected leak, so the response must not claim "the old URL no
	// longer works" if the delete failed and the token is still live.
	oldDeleted := true
	if err := p.deleteIncomingWebhook(args.UserId, oldHookID); err != nil {
		oldDeleted = false
		p.API.LogWarn("could not delete old webhook after rotation (continuing)", "receiver", target.Name, "webhook", redactHookID(oldHookID), "err", err.Error())
	}

	p.auditLog("webhook.rotation.executed", args.UserId, target.Name, args.ChannelId,
		fmt.Sprintf("affected=%d group=%q", len(affected), target.GroupName))

	// Single-receiver case (legacy or true individual): inline YAML,
	// matches v1.0.2 behavior.
	if len(affected) == 1 {
		return p.renderRotateResponse(affected[0], oldDeleted, oldHookID), true, nil
	}

	// Group case: list affected receivers, DM the merged YAML bundle.
	return p.renderRotateGroupResponse(args.UserId, affected, target.GroupName, oldDeleted, oldHookID), true, nil
}

// renderRotateGroupResponse builds the in-channel summary AND fires the
// DM with the merged YAML bundle when the rotated webhook serves a
// multi-receiver group. Same DM shape as /alertmanager config rotate all
// overdue — operator pastes once into alertmanager.yml.
func (p *Plugin) renderRotateGroupResponse(userID string, affected []alertConfig, groupName string, oldDeleted bool, oldHookID string) string {
	primary := affected[0]
	var y strings.Builder
	y.WriteString(fmt.Sprintf("# Alertmanager receivers re-rotated by /alertmanager config rotate (group %q)\n", groupName))
	y.WriteString(fmt.Sprintf("# %d receiver(s) share the rotated webhook.\n", len(affected)))
	y.WriteString("# Paste under `receivers:` in your alertmanager.yml, then reload AM.\n")
	y.WriteString("# Old URLs deactivated immediately — alert delivery resumes after the AM reload.\n\n")
	for _, ac := range affected {
		y.WriteString(renderReceiverYAMLForKind(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL, ac.Custom))
		y.WriteString("\n")
	}
	routesYAML := assembleRoutesYAML(affected)
	if dmErr := p.dmYAMLBundle(userID, y.String(), routesYAML, len(affected), primary.AlertManagerURL, nil); dmErr != nil {
		p.API.LogWarn("rotation: couldn't DM YAML after group rotate", "err", dmErr.Error())
	}

	var b strings.Builder
	if oldDeleted {
		b.WriteString(fmt.Sprintf(":key: Rotated `%s` group webhook in `~%s`. **The old URL no longer works for any of the %d affected receiver(s).**\n\n", groupName, primary.Channel, len(affected)))
	} else {
		// B-003: the shared old webhook wasn't deleted — don't imply it's dead.
		b.WriteString(fmt.Sprintf(":key: Rotated `%s` group webhook in `~%s` for %d receiver(s). :warning: **The old shared webhook could NOT be deleted and may still be live** — remove the webhook named `%s` (fingerprint %s) in System Console → Integrations → Incoming Webhooks; if the rotation left two with that name, delete the older one.\n\n", groupName, primary.Channel, len(affected), webhookDisplayNameFor(groupName, primary.Team, primary.Channel), redactHookID(oldHookID)))
	}
	b.WriteString("**Affected:**\n")
	for _, ac := range affected {
		b.WriteString("- `" + ac.Name + "`\n")
	}
	b.WriteString(fmt.Sprintf("\nMerged YAML DM'd to you from `@%s`. Paste it into your `alertmanager.yml`, then reload AM (`curl -X POST %s/-/reload`).", webhookUsername, primary.AlertManagerURL))
	return b.String()
}

// representativeOverdueNames dedups overdue receiver names by their shared
// webhook ID, returning one representative per distinct webhook in first-seen
// order. A group webhook is rotated as a unit (handleRotateSingle repoints every
// member), so rotating one member covers the whole group; without this, a shared
// webhook would be rotated once per overdue member — needless churn and duplicate
// DMs. Names with an unknown (missing-from-map) webhook are treated as their own
// group so they're never silently dropped.
// overdueReceiverNames returns the receivers eligible for `rotate all overdue`:
// only those the operator OPTED INTO rotation reminders (RotationRemindersEnabled,
// F-007) and whose last rotation is older than threshold. A zero LastRotatedAt is
// treated as "not yet due" (the reconciler stamps it on first sight). Matching
// handleList's overdue criteria keeps the bulk rotate from invalidating a webhook
// the operator never asked the plugin to manage on this schedule.
func overdueReceiverNames(scoped []alertConfig, now time.Time, threshold time.Duration) []string {
	var out []string
	for _, c := range scoped {
		if !c.RotationRemindersEnabled {
			continue
		}
		if c.LastRotatedAt.IsZero() {
			continue
		}
		if now.Sub(c.LastRotatedAt) > threshold {
			out = append(out, c.Name)
		}
	}
	return out
}

func representativeOverdueNames(overdueNames []string, webhookByName map[string]string) []string {
	seen := make(map[string]bool, len(overdueNames))
	reps := make([]string, 0, len(overdueNames))
	for _, name := range overdueNames {
		hook, ok := webhookByName[name]
		if ok && seen[hook] {
			continue
		}
		if ok {
			seen[hook] = true
		}
		reps = append(reps, name)
	}
	return reps
}

// groupOverdueByWebhook groups overdue receiver names by their shared webhook ID
// (in first-seen order per hook), so a failed group rotation can report every
// member of the group, not just the representative that was actually attempted.
func groupOverdueByWebhook(overdueNames []string, webhookByName map[string]string) map[string][]string {
	byHook := make(map[string][]string, len(overdueNames))
	for _, name := range overdueNames {
		hook := webhookByName[name]
		byHook[hook] = append(byHook[hook], name)
	}
	return byHook
}

// handleRotateOverdue rotates every receiver bound to the calling
// channel whose LastRotatedAt is older than WebhookRotationDays.
// One DM at the end with the merged updated YAML — same format as
// /alertmanager format export — so the operator pastes once.
//
// Skipped silently when WebhookRotationDays is 0 (feature disabled);
// emits a hint pointing the sysadmin at the setting.
func (p *Plugin) handleRotateOverdue(args *model.CommandArgs) (string, error) {
	cfg := p.getConfiguration()
	if cfg.WebhookRotationDays <= 0 {
		return ":information_source: Webhook rotation reminders are disabled. Set `WebhookRotationDays` in System Console → Plugins → Alertmanager to a non-zero value to enable, then this command will identify receivers past the threshold.", nil
	}

	threshold := time.Duration(cfg.WebhookRotationDays) * 24 * time.Hour
	now := time.Now().UTC()

	scoped := p.configsForCurrentChannel(args)
	if len(scoped) == 0 {
		return ":information_source: No receivers bound to this channel — nothing to rotate.", nil
	}

	// Identify which receivers are overdue. Zero-value LastRotatedAt
	// counts as "rotated at plugin upgrade time" — the reconciler
	// stamps that on first sight so existing receivers don't trigger
	// reminders day-one. Here we trust that stamping has happened.
	overdueNames := overdueReceiverNames(scoped, now, threshold)

	if len(overdueNames) == 0 {
		return fmt.Sprintf(":white_check_mark: No receivers in this channel are past the %d-day rotation threshold.", cfg.WebhookRotationDays), nil
	}

	// Dedup by shared webhook before rotating: a group webhook serves multiple
	// receivers and handleRotateSingle rotates the WHOLE group at once, so
	// iterating every overdue member would rotate a shared webhook once per member
	// — redundant churn (extra create/delete) and a duplicate DM per member. Rotate
	// each distinct overdue webhook once via a representative.
	webhookByName := make(map[string]string, len(scoped))
	for _, c := range scoped {
		webhookByName[c.Name] = c.WebhookID
	}
	reps := representativeOverdueNames(overdueNames, webhookByName)
	// Reverse map so a FAILED group rotation still reports every overdue member,
	// not just the representative — otherwise the dedup would hide that the group's
	// other receivers are also still past threshold.
	overdueByWebhook := groupOverdueByWebhook(overdueNames, webhookByName)

	rotated := make([]alertConfig, 0, len(overdueNames))
	failed := make([]string, 0)
	for _, name := range reps {
		summary, ok, err := p.handleRotateSingle(args, name)
		if err != nil || !ok {
			// The whole group shares one webhook, so its rotation failed as a unit —
			// name every overdue member so the operator sees the full still-overdue set.
			failed = append(failed, strings.Join(overdueByWebhook[webhookByName[name]], ", ")+" — "+summary)
			continue
		}
		// Rotating the representative rotated its whole group. Collect every entry
		// now sharing the representative's NEW webhook so the merged DM/summary
		// still lists all affected receivers, not just the representatives.
		fresh := p.getConfiguration().AlertConfigs
		var newHook string
		for _, c := range fresh {
			if c.Name == name {
				newHook = c.WebhookID
				break
			}
		}
		for _, c := range fresh {
			if c.WebhookID == newHook {
				rotated = append(rotated, c)
			}
		}
	}

	// Build the merged YAML DM in the same shape /alertmanager format export
	// produces, but scoped to JUST the rotated set.
	var y strings.Builder
	y.WriteString("# Alertmanager receivers re-rotated by /alertmanager config rotate all overdue\n")
	y.WriteString(fmt.Sprintf("# %d receiver(s) past the %d-day rotation threshold.\n", len(rotated), cfg.WebhookRotationDays))
	y.WriteString("# Paste under `receivers:` in your alertmanager.yml, then reload AM.\n")
	y.WriteString("# Old URLs deactivated immediately — alert delivery resumes after the AM reload.\n\n")
	for _, ac := range rotated {
		y.WriteString(renderReceiverYAMLForKind(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL, ac.Custom))
		y.WriteString("\n")
	}
	routesYAML := assembleRoutesYAML(rotated)
	if dmErr := p.dmYAMLBundle(args.UserId, y.String(), routesYAML, len(rotated), "", nil); dmErr != nil {
		p.API.LogWarn("rotation: couldn't DM YAML after bulk overdue rotate", "err", dmErr.Error())
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(":arrows_counterclockwise: Rotated %d receiver(s) past the %d-day threshold.\n\n", len(rotated), cfg.WebhookRotationDays))
	if len(rotated) > 0 {
		b.WriteString("**Rotated:**\n")
		for _, ac := range rotated {
			b.WriteString("- `" + ac.Name + "`\n")
		}
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("Updated YAML DM'd to you from `@%s`. Paste it into your `alertmanager.yml`, then reload AM (`curl -X POST http://<am>/-/reload`). Old URLs deactivate immediately.\n", webhookUsername))
	}
	if len(failed) > 0 {
		b.WriteString("\n**Failed:**\n")
		for _, f := range failed {
			b.WriteString("- " + f + "\n")
		}
	}
	return b.String(), nil
}

// renderRotateResponse builds the success message for /alertmanager
// rotate. The receiver's slack_configs YAML embeds the new webhook URL,
// so the admin re-pastes the whole block to update alertmanager.yml.
//
// oldDeleted reports whether the previous webhook was actually deleted; when it
// wasn't, the message must NOT claim the old URL is dead (B-003) and instead
// tells the admin to remove the lingering webhook by hand.
func (p *Plugin) renderRotateResponse(ac alertConfig, oldDeleted bool, oldHookID string) string {
	yaml := renderReceiverYAMLForKind(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL, ac.Custom)
	return fmt.Sprintf(
		":key: Rotated webhook for `%s`. %s\n\n"+
			"**Update your `alertmanager.yml`:**\n\n```yaml\n%s```\n\n"+
			"**Then reload Alertmanager:**\n```\ncurl -X POST %s/-/reload\n```",
		ac.Name, oldWebhookStatusLine(oldDeleted, webhookDisplayNameFor(receiverBaseSlug(ac.Name), ac.Team, ac.Channel), oldHookID), yaml, ac.AlertManagerURL,
	)
}

// oldWebhookStatusLine returns the sentence describing the fate of the old
// webhook after a rotation: a clean "no longer works" when the delete succeeded,
// or an explicit warning (with the webhook ID to find in System Console) when it
// did not — so an admin rotating because of a suspected leak isn't told the token
// is dead when it may still be live (B-003).
func oldWebhookStatusLine(oldDeleted bool, displayName, oldHookID string) string {
	if oldDeleted {
		return "**The old webhook URL no longer works.**"
	}
	// Identify the lingering webhook by DISPLAY NAME (how System Console lists
	// them), not the raw hook ID — that ID is a live bearer token and must not
	// reach a chat response (F-002). The redacted fingerprint is for log
	// correlation only.
	return fmt.Sprintf(":warning: **The old webhook could NOT be deleted and may still be live** — remove the webhook named `%s` (fingerprint %s) in System Console → Integrations → Incoming Webhooks. If the rotation left two with that name, delete the older one.", displayName, redactHookID(oldHookID))
}
