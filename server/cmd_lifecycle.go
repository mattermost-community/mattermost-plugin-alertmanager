package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// All commands in this file mutate the plugin's persistent configuration
// (which lists registered receivers and their webhook IDs) and/or the
// Mattermost incoming webhook table. They gate on system_admin because
// every successful invocation either creates a webhook receiver (network
// surface) or removes one (alerts go silent without it).
//
// /alertmanager add (bulk creation from a runbook set) lives in
// cmd_scaffold.go — split out for size. This file owns the per-receiver
// lifecycle: remove, rotate, list, config.

// handleRemove dispatches to one of three modes by target arg:
//
//	<name>           → single receiver by name
//	<set>            → all receivers in this channel matching that runbook set
//	                   (compute, application, database, storage, networking,
//	                   observability) — requires --force
//	all              → every receiver in this channel — requires --force
//
// All paths are channel-scoped: a user in #web-alerts can't reach
// receivers bound to #db-alerts. Bulk paths (set, all) gate on --force
// to prevent accidental multi-receiver nukes.
//
// Set names take precedence over receiver names. Since plugin-managed
// receiver slugs (high-cpu-usage, etc.) don't collide with category
// names (compute, etc.), the precedence is safe.
func (p *Plugin) handleRemove(args *model.CommandArgs) (string, error) {
	if err := p.requireChannelTeamAdmin(args.UserId, args.ChannelId); err != nil {
		return err.Error(), nil
	}

	fields := strings.Fields(args.Command)
	if len(fields) < 3 {
		return "Usage: `/alertmanager remove <name>` | `/alertmanager remove <set> --force` | `/alertmanager remove all --force`\n\nSets: `compute`, `application`, `database`, `storage`, `networking`, `observability`", nil
	}
	target := strings.ToLower(fields[2])
	force := len(fields) >= 4 && fields[3] == "--force"

	if target == "all" {
		return p.handleRemoveAll(args, force)
	}
	// Check if it's a known category set name. scaffoldSets entries
	// with non-nil slug lists are the actual categories; nil entries
	// (standard, all) are aliases handled elsewhere.
	if slugs, ok := scaffoldSets[target]; ok && slugs != nil {
		return p.handleRemoveSet(args, target, slugs, force)
	}
	return p.handleRemoveOne(args, fields[2])
}

// handleRemoveOne removes a single receiver by name. Open to any
// system_admin because it's the bread-and-butter cleanup path; the name
// is supplied explicitly so there's no risk of fat-fingering into a
// bulk operation.
//
// Lookup accepts either the full suffixed name (e.g.
// high-cpu-usage--alert-slo-channel) or the short base slug
// (high-cpu-usage). For short-name lookups, the receiver must be bound
// to the current channel — disambiguates when the same slug exists in
// multiple channels.
//
// Webhook refcount (v1.0.3+): if the removed receiver shares its
// WebhookID with other receivers (group webhook), the webhook stays
// alive. Only when the last receiver pointing at a webhook is removed
// does the Mattermost incoming webhook get deleted. Order is
// save-then-delete so a failed delete leaves no stale receiver entry.
func (p *Plugin) handleRemoveOne(args *model.CommandArgs, name string) (string, error) {
	// Hold configWriteMu across the whole read-modify-write so the read below
	// and the save can't interleave with another mutator (lost-update safety).
	p.configWriteMu.Lock()
	defer p.configWriteMu.Unlock()

	// CL-02: resolve only within receivers bound to THIS team+channel so a
	// guessed short name can't reach another team's receiver, then confirm the
	// resolved name really is in scope — resolveReceiverName echoes its input on a
	// miss, which could coincide with another channel's full name and slip past
	// the global filter below (names are globally unique).
	scoped := p.configsForCurrentChannel(args)
	resolved := resolveReceiverName(scoped, name, args.ChannelId, p)
	if !receiverNameInScope(scoped, resolved) {
		return fmt.Sprintf("Receiver %q not found.", name), nil
	}

	var hookID string
	_, filtered, err := p.updateConfigsAtomic(func(current []alertConfig) ([]alertConfig, error) {
		hookID = "" // reset per attempt — the transform may run more than once
		out := make([]alertConfig, 0, len(current))
		for _, c := range current {
			if c.Name == resolved {
				hookID = c.WebhookID
				continue
			}
			out = append(out, c)
		}
		return out, nil
	})
	if err != nil {
		return fmt.Sprintf("Failed to persist config: %v", err), nil
	}
	if hookID == "" {
		// In scope per the in-memory snapshot but absent from the freshly-read KV
		// list — a concurrent writer already removed it.
		return fmt.Sprintf("Receiver %q not found.", name), nil
	}

	// Refcount: only delete the underlying webhook if no other receiver
	// still depends on it.
	if !webhookStillReferenced(filtered, hookID) {
		if err := p.deleteIncomingWebhook(args.UserId, hookID); err != nil {
			p.API.LogWarn("could not delete orphaned webhook on remove (continuing)", "receiver", resolved, "webhook", redactHookID(hookID), "err", err.Error())
		}
	}

	return fmt.Sprintf(":wastebasket: Removed receiver `%s`. Don't forget to delete the corresponding `slack_configs` block from `alertmanager.yml`.", resolved), nil
}

// webhookStillReferenced returns true when at least one entry in the
// supplied slice still references the given webhookID. The post-remove
// caller uses this to decide whether to clean up the Mattermost webhook.
func webhookStillReferenced(entries []alertConfig, webhookID string) bool {
	for _, c := range entries {
		if c.WebhookID == webhookID {
			return true
		}
	}
	return false
}

// orphanedWebhookIDs returns webhookIDs referenced by `before` but not
// by `after`. Used after bulk-remove operations to identify webhooks
// whose last receiver was just removed. Stable order — preserves the
// order of first appearance in `before` so log output is deterministic.
func orphanedWebhookIDs(before, after []alertConfig) []string {
	afterRefs := make(map[string]bool, len(after))
	for _, c := range after {
		afterRefs[c.WebhookID] = true
	}
	seen := make(map[string]bool)
	orphans := make([]string, 0)
	for _, c := range before {
		if seen[c.WebhookID] {
			continue
		}
		seen[c.WebhookID] = true
		if !afterRefs[c.WebhookID] {
			orphans = append(orphans, c.WebhookID)
		}
	}
	return orphans
}

// handleRemoveAll removes every receiver bound to the current channel.
// Two-step UX:
//
//	/alertmanager remove all           → dry-run preview (lists targets)
//	/alertmanager remove all --force   → actually removes
//
// Webhook delete failures don't abort — the plugin config entry is still
// pruned so /alertmanager list reflects the truth. Orphan webhooks (if
// any survive) can be cleaned up via System Console.
func (p *Plugin) handleRemoveAll(args *model.CommandArgs, force bool) (string, error) {
	// Atomic read-modify-write: hold configWriteMu from the read below
	// through the save so concurrent mutators can't cause a lost update.
	p.configWriteMu.Lock()
	defer p.configWriteMu.Unlock()

	scoped := p.configsForCurrentChannel(args)
	if len(scoped) == 0 {
		return ":information_source: No receivers bound to this channel — nothing to remove.", nil
	}

	if !force {
		// Dry-run: list what would be deleted, refuse to execute without --force.
		var b strings.Builder
		b.WriteString(fmt.Sprintf(":warning: **About to remove %d receiver(s) bound to this channel:**\n\n", len(scoped)))
		for _, c := range scoped {
			b.WriteString(fmt.Sprintf("- `%s` (webhook `%s`)\n", c.Name, c.WebhookID))
		}
		b.WriteString("\nThis deletes the plugin config entries AND the underlying Mattermost incoming webhooks. **The corresponding `slack_configs` blocks in your `alertmanager.yml` will start failing immediately** — clean them up after.\n\n")
		b.WriteString("To proceed, re-run with `--force`:\n\n```\n/alertmanager remove all --force\n```\n")
		return b.String(), nil
	}

	// Build the set of names to prune (channel-scoped) and walk the full
	// config so we keep entries from other channels intact.
	namesToRemove := make(map[string]bool, len(scoped))
	for _, c := range scoped {
		namesToRemove[c.Name] = true
	}

	removed := make([]string, 0, len(scoped))
	current, filtered, err := p.updateConfigsAtomic(func(current []alertConfig) ([]alertConfig, error) {
		removed = removed[:0] // reset per attempt
		out := make([]alertConfig, 0, len(current))
		for _, c := range current {
			if !namesToRemove[c.Name] {
				out = append(out, c)
				continue
			}
			removed = append(removed, c.Name)
		}
		return out, nil
	})
	if err != nil {
		return fmt.Sprintf("Failed to persist config after bulk delete: %v", err), nil
	}

	// Refcount-aware webhook cleanup: only webhooks with zero remaining
	// references get deleted. Shared group webhooks survive partial
	// removes from other channels.
	orphans := orphanedWebhookIDs(current, filtered)
	webhookFailures := make([]string, 0)
	for _, hookID := range orphans {
		if err := p.deleteIncomingWebhook(args.UserId, hookID); err != nil {
			p.API.LogWarn("remove-all: could not delete orphaned webhook (config entries pruned)",
				"webhook", redactHookID(hookID), "err", err.Error())
			webhookFailures = append(webhookFailures, hookID)
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(":wastebasket: Removed %d receiver(s) from this channel:\n\n", len(removed)))
	for _, name := range removed {
		b.WriteString(fmt.Sprintf("- `%s`\n", name))
	}
	if len(orphans) > 0 {
		b.WriteString(fmt.Sprintf("\nDeleted %d Mattermost webhook(s) whose last receiver was just removed.\n", len(orphans)-len(webhookFailures)))
	}
	if len(webhookFailures) > 0 {
		b.WriteString(fmt.Sprintf("\n:warning: Couldn't delete %d underlying webhook(s) (config entries are gone, but webhook IDs may linger in System Console → Integrations): `%s`\n",
			len(webhookFailures), strings.Join(webhookFailures, "`, `")))
	}
	b.WriteString("\nClean up the corresponding `slack_configs` blocks in `alertmanager.yml` and reload AM.")
	return b.String(), nil
}

// handleRemoveSet removes every receiver in the current channel whose
// base slug is in the named runbook set. Channel-scoped; receivers in
// other channels (even ones matching the set) are not touched.
//
// Two-step UX matching handleRemoveAll:
//
//	/alertmanager remove compute            → dry-run preview
//	/alertmanager remove compute --force    → actually removes
//
// Webhook delete failures don't abort — config entries are still pruned
// so /alertmanager list reflects the new truth. Orphan webhooks (if
// any survive the delete attempt) can be cleaned up via System Console.
func (p *Plugin) handleRemoveSet(args *model.CommandArgs, setName string, setSlugs []string, force bool) (string, error) {
	// Atomic read-modify-write: hold configWriteMu across the read + save.
	p.configWriteMu.Lock()
	defer p.configWriteMu.Unlock()

	// Build a set of base slugs for matching. receiverBaseSlug handles
	// both legacy unsuffixed names (`high-cpu-usage`) and channel-
	// suffixed ones (`high-cpu-usage--alert-sre-channel`) — both
	// resolve to the same base.
	baseSlugSet := make(map[string]bool, len(setSlugs))
	for _, s := range setSlugs {
		baseSlugSet[s] = true
	}

	scoped := p.configsForCurrentChannel(args)
	matched := make([]alertConfig, 0, len(scoped))
	for _, c := range scoped {
		if baseSlugSet[receiverBaseSlug(c.Name)] {
			matched = append(matched, c)
		}
	}

	if len(matched) == 0 {
		return fmt.Sprintf(":information_source: No `%s`-set receivers bound to this channel — nothing to remove.", setName), nil
	}

	if !force {
		var b strings.Builder
		b.WriteString(fmt.Sprintf(":warning: **About to remove %d `%s`-set receiver(s) bound to this channel:**\n\n", len(matched), setName))
		for _, c := range matched {
			b.WriteString(fmt.Sprintf("- `%s` (webhook `%s`)\n", c.Name, c.WebhookID))
		}
		b.WriteString(fmt.Sprintf("\nReceivers in this channel NOT in the `%s` set will be left alone. To proceed:\n\n```\n/alertmanager remove %s --force\n```\n", setName, setName))
		return b.String(), nil
	}

	namesToRemove := make(map[string]bool, len(matched))
	for _, c := range matched {
		namesToRemove[c.Name] = true
	}

	removed := make([]string, 0, len(matched))
	current, filtered, err := p.updateConfigsAtomic(func(current []alertConfig) ([]alertConfig, error) {
		removed = removed[:0] // reset per attempt
		out := make([]alertConfig, 0, len(current))
		for _, c := range current {
			if !namesToRemove[c.Name] {
				out = append(out, c)
				continue
			}
			removed = append(removed, c.Name)
		}
		return out, nil
	})
	if err != nil {
		return fmt.Sprintf("Failed to persist config after set delete: %v", err), nil
	}

	// Refcount-aware webhook cleanup: only fully-orphaned webhooks
	// get deleted. A group webhook that still serves receivers in
	// another channel (fan-out) survives.
	orphans := orphanedWebhookIDs(current, filtered)
	webhookFailures := make([]string, 0)
	for _, hookID := range orphans {
		if err := p.deleteIncomingWebhook(args.UserId, hookID); err != nil {
			p.API.LogWarn("remove-set: could not delete orphaned webhook (config entries pruned)",
				"webhook", redactHookID(hookID), "err", err.Error())
			webhookFailures = append(webhookFailures, hookID)
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(":wastebasket: Removed %d `%s`-set receiver(s) from this channel:\n\n", len(removed), setName))
	for _, name := range removed {
		b.WriteString(fmt.Sprintf("- `%s`\n", name))
	}
	if len(orphans) > 0 {
		b.WriteString(fmt.Sprintf("\nDeleted %d Mattermost webhook(s) whose last receiver was just removed.\n", len(orphans)-len(webhookFailures)))
	}
	if len(webhookFailures) > 0 {
		b.WriteString(fmt.Sprintf("\n:warning: Couldn't delete %d underlying webhook(s) (config entries gone; webhook IDs may linger in System Console → Integrations): `%s`\n",
			len(webhookFailures), strings.Join(webhookFailures, "`, `")))
	}
	b.WriteString("\nRemove the matching `slack_configs` and `routes:` entries from your `alertmanager.yml` and reload AM.")
	return b.String(), nil
}

// handleRotate dispatches to single-receiver or bulk-overdue paths
// based on the args. Both share the underlying "delete old webhook,
// create a new one, persist, stamp LastRotatedAt" mechanism, just
// applied to one entry vs. many.
//
// Usage:
//
//	/alertmanager rotate <name>           # one receiver by name
//	/alertmanager rotate all --overdue    # every receiver in this
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
		return "Usage: `/alertmanager rotate <name>` or `/alertmanager rotate all --overdue`", nil
	}
	target := fields[2]
	rest := fields[3:]

	if target == "all" && containsFlag(rest, "--overdue") {
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

	// Rotation resolves the receiver's existing channel; target.Team/Channel are
	// already canonical, and a rotate never scaffolds a new channel, so only the
	// channel ID is needed here.
	rc, err := p.resolveOrCreateChannel(target.Team, target.Channel, false, "")
	if err != nil {
		return fmt.Sprintf("Failed to resolve destination channel for rotation: %v", err), false, nil
	}
	channelID := rc.channelID

	// Webhook display name for the replacement follows the same rule as
	// /alertmanager add: the receiver-name format <base>--<team>-<channel>.
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
// multi-receiver group. Same DM shape as /alertmanager rotate all
// --overdue — operator pastes once into alertmanager.yml.
func (p *Plugin) renderRotateGroupResponse(userID string, affected []alertConfig, groupName string, oldDeleted bool, oldHookID string) string {
	primary := affected[0]
	var y strings.Builder
	y.WriteString(fmt.Sprintf("# Alertmanager receivers re-rotated by /alertmanager rotate (group %q)\n", groupName))
	y.WriteString(fmt.Sprintf("# %d receiver(s) share the rotated webhook.\n", len(affected)))
	y.WriteString("# Paste under `receivers:` in your alertmanager.yml, then reload AM.\n")
	y.WriteString("# Old URLs deactivated immediately — alert delivery resumes after the AM reload.\n\n")
	for _, ac := range affected {
		y.WriteString(renderReceiverYAMLForKind(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL, ac.Custom))
		y.WriteString("\n")
	}
	routesYAML := assembleRoutesYAML(affected)
	if dmErr := p.dmYAMLBundle(userID, y.String(), routesYAML, len(affected), primary.AlertManagerURL); dmErr != nil {
		p.API.LogWarn("rotation: couldn't DM YAML after group rotate", "err", dmErr.Error())
	}

	var b strings.Builder
	if oldDeleted {
		b.WriteString(fmt.Sprintf(":key: Rotated `%s` group webhook in `~%s`. **The old URL no longer works for any of the %d affected receiver(s).**\n\n", groupName, primary.Channel, len(affected)))
	} else {
		// B-003: the shared old webhook wasn't deleted — don't imply it's dead.
		b.WriteString(fmt.Sprintf(":key: Rotated `%s` group webhook in `~%s` for %d receiver(s). :warning: **The old shared webhook could NOT be deleted and may still be live** — delete webhook `%s` in System Console → Integrations → Incoming Webhooks.\n\n", groupName, primary.Channel, len(affected), oldHookID))
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
// /alertmanager export — so the operator pastes once.
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
	var overdueNames []string
	for _, c := range scoped {
		if c.LastRotatedAt.IsZero() {
			continue
		}
		if now.Sub(c.LastRotatedAt) > threshold {
			overdueNames = append(overdueNames, c.Name)
		}
	}

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

	// Build the merged YAML DM in the same shape /alertmanager export
	// produces, but scoped to JUST the rotated set.
	var y strings.Builder
	y.WriteString("# Alertmanager receivers re-rotated by /alertmanager rotate all --overdue\n")
	y.WriteString(fmt.Sprintf("# %d receiver(s) past the %d-day rotation threshold.\n", len(rotated), cfg.WebhookRotationDays))
	y.WriteString("# Paste under `receivers:` in your alertmanager.yml, then reload AM.\n")
	y.WriteString("# Old URLs deactivated immediately — alert delivery resumes after the AM reload.\n\n")
	for _, ac := range rotated {
		y.WriteString(renderReceiverYAMLForKind(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL, ac.Custom))
		y.WriteString("\n")
	}
	routesYAML := assembleRoutesYAML(rotated)
	if dmErr := p.dmYAMLBundle(args.UserId, y.String(), routesYAML, len(rotated), ""); dmErr != nil {
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

// resolveReceiverName takes a user-supplied receiver name and returns
// the actual stored name, accepting either the full suffixed form
// (high-cpu-usage--alert-slo-channel) or the short base slug
// (high-cpu-usage). For short-name lookups, prefers a match in the
// current channel; falls back to the unsuffixed legacy form anywhere.
// Returns the original input unchanged if no match found — the caller
// then surfaces a "not found" error.
//
// The plugin pointer is needed to resolve the current channel's slug
// from its ID, which is what the receiver name is suffixed with.
// receiverNameInScope reports whether name is among the channel-scoped
// receivers. remove/rotate use it to confirm a resolved name really belongs to
// the invocation channel before mutating it: resolveReceiverName returns its raw
// input unchanged on a miss, which could coincide with another channel's full
// receiver name, so an exact-match against the global list downstream would
// otherwise act cross-channel (CL-02).
func receiverNameInScope(scoped []alertConfig, name string) bool {
	for _, c := range scoped {
		if c.Name == name {
			return true
		}
	}
	return false
}

func resolveReceiverName(all []alertConfig, supplied, channelID string, p *Plugin) string {
	// 1. Exact match — covers full suffixed names AND legacy unsuffixed names
	for _, c := range all {
		if c.Name == supplied {
			return c.Name
		}
	}
	// 2. Short-name match scoped to current team + channel. Team is part of
	// the receiver name now, so the candidate needs the team slug too.
	if ch, appErr := p.API.GetChannel(channelID); appErr == nil {
		if team, teamErr := p.API.GetTeam(ch.TeamId); teamErr == nil {
			candidate := receiverNameForChannel(supplied, team.Name, ch.Name)
			for _, c := range all {
				if c.Name == candidate {
					return c.Name
				}
			}
		}
	}
	// 3. Short-name fallback against base slug across all entries
	for _, c := range all {
		if receiverBaseSlug(c.Name) == supplied {
			return c.Name
		}
	}
	return supplied
}

// handleList: read-only summary of receivers bound to the current
// channel — always scoped, no org-wide escape hatch. A user running
// /alertmanager list in #web-alerts should never see DB or compute
// receivers from other channels, even with admin privileges.
// Cross-channel inventory is a System-Console-only concern.
//
// Open to all users in the channel — no sysadmin gate. The output only
// reveals receiver names + AM URLs (no webhook URLs or auth), so it's
// safe for general visibility.
func (p *Plugin) handleList(args *model.CommandArgs) (string, error) {
	configs := p.configsForCurrentChannel(args)
	if len(configs) == 0 {
		return emptyScopeMessage("list"), nil
	}

	cfg := p.getConfiguration()
	threshold := time.Duration(cfg.WebhookRotationDays) * 24 * time.Hour
	now := time.Now()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("**%d receiver(s) bound to this channel:**\n\n", len(configs)))
	b.WriteString("| Name | Team | Channel | Alertmanager URL | Rotated |\n")
	b.WriteString("|------|------|---------|------------------|---------|\n")
	for _, c := range configs {
		amURL := c.AlertManagerURL
		if amURL == "" {
			amURL = "_(none)_"
		}
		// Overdue marker only when the per-receiver opt-in is on AND
		// the global threshold > 0 AND the age exceeds the threshold.
		// All three are required — otherwise the reminder system itself
		// wouldn't fire for this receiver, so flagging it as overdue in
		// list output would mislead the operator.
		overdue := c.RotationRemindersEnabled && cfg.WebhookRotationDays > 0 &&
			!c.LastRotatedAt.IsZero() && now.Sub(c.LastRotatedAt) > threshold
		b.WriteString(fmt.Sprintf("| `%s` | `%s` | `~%s` | %s | %s |\n",
			c.Name, c.Team, c.Channel, amURL,
			formatRotationAge(c.LastRotatedAt, now, overdue)))
	}
	b.WriteString("\n_Full details (and the slack_configs YAML) for one receiver: `/alertmanager config <name>`_\n")
	if cfg.WebhookRotationDays > 0 {
		b.WriteString(fmt.Sprintf("_Rotation threshold: %d days (System Console → WebhookRotationDays). ⚠️ = past threshold + opted in to reminders._\n", cfg.WebhookRotationDays))
	}
	return b.String(), nil
}

// formatRotationAge turns a LastRotatedAt timestamp into a short
// human label for the list view. Zero value → "never" (pre-rotation
// or pre-feature receiver). < 24h → "today". < 48h → "yesterday".
// Older → "N days ago". The ⚠️ prefix lands only when the caller has
// already determined this receiver is past its rotation threshold —
// formatting doesn't re-derive that decision.
func formatRotationAge(t, now time.Time, overdue bool) string {
	prefix := ""
	if overdue {
		prefix = "⚠️ "
	}
	if t.IsZero() {
		return prefix + "never"
	}
	age := now.Sub(t)
	switch {
	case age < 24*time.Hour:
		return prefix + "today"
	case age < 48*time.Hour:
		return prefix + "yesterday"
	default:
		return fmt.Sprintf("%s%d days ago", prefix, int(age/(24*time.Hour)))
	}
}

// handleConfig renders the full detail card for one receiver bound to
// the current channel: metadata, the slack_configs YAML block ready to
// paste, the AM reload command, and a quick-action menu.
//
// Sysadmin-gated because the YAML embeds the webhook URL, which is a
// channel-bound bearer token. Channel-scoped: the named receiver must
// be bound to this channel; cross-channel lookup is refused without
// disambiguating "doesn't exist anywhere" vs "exists elsewhere", to
// prevent receiver-name enumeration across channels.
func (p *Plugin) handleConfig(args *model.CommandArgs) (string, error) {
	if err := p.requireChannelTeamAdmin(args.UserId, args.ChannelId); err != nil {
		return err.Error(), nil
	}

	fields := strings.Fields(args.Command)
	if len(fields) < 3 {
		return "Usage: `/alertmanager config <name>`\n\nList available receiver names with `/alertmanager list`.", nil
	}
	name := fields[2]

	scoped := p.configsForCurrentChannel(args)
	var match *alertConfig
	for i := range scoped {
		// Accept either the full suffixed name or the short base slug.
		if scoped[i].Name == name || receiverBaseSlug(scoped[i].Name) == name {
			match = &scoped[i]
			break
		}
	}
	if match == nil {
		return fmt.Sprintf(
			"Receiver `%s` is not bound to this channel. Run `/alertmanager list` here to see what is.",
			name,
		), nil
	}

	yaml := renderReceiverYAMLForKind(match.Name, p.webhookURLForReceiver(*match), match.Channel, p.runbookDefaultURL(receiverBaseSlug(match.Name)), p.siteURL()+webhookIconURL, match.Custom)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Receiver `%s`**\n\n", match.Name))
	b.WriteString(fmt.Sprintf("- **Team:** `%s`\n", match.Team))
	b.WriteString(fmt.Sprintf("- **Channel:** `~%s`\n", match.Channel))
	b.WriteString(fmt.Sprintf("- **Alertmanager URL:** `%s`\n", match.AlertManagerURL))
	b.WriteString(fmt.Sprintf("- **Webhook ID:** `%s`\n", match.WebhookID))
	b.WriteString(fmt.Sprintf("- **Runbook (default):** %s\n", p.runbookDefaultURL(receiverBaseSlug(match.Name))))
	if match.User != "" {
		// Username is shown but password is never echoed — even masked,
		// echoing it teaches admins the password length, which weakens it.
		b.WriteString(fmt.Sprintf("- **AM basic-auth user:** `%s` _(password set; not shown)_\n", match.User))
	}
	b.WriteString("\n**slack_configs YAML** (paste under `receivers:` in alertmanager.yml):\n\n```yaml\n")
	b.WriteString(yaml)
	b.WriteString("```\n\n")
	b.WriteString(fmt.Sprintf("**Reload Alertmanager:**\n```\ncurl -X POST %s/-/reload\n```\n\n", match.AlertManagerURL))
	b.WriteString("**Actions:**\n")
	b.WriteString(fmt.Sprintf("- `/alertmanager rotate %s` — rotate webhook URL (URL changes, paste the new YAML)\n", match.Name))
	b.WriteString(fmt.Sprintf("- `/alertmanager remove %s` — delete the receiver and its webhook\n", match.Name))
	return b.String(), nil
}

// resolvedChannel is the outcome of resolveOrCreateChannel. teamName/channelName
// are the CANONICAL names read back from the resolved Mattermost objects, not the
// caller's raw arguments — callers persist these (CL-39) so a stored receiver
// never carries a name that Mattermost itself wouldn't accept on lookup.
type resolvedChannel struct {
	channelID   string
	teamName    string
	channelName string
	created     bool // this call brought the channel into existence
}

// resolveOrCreateChannel maps team-slug + channel-slug → resolvedChannel,
// creating the channel if missing. When private is true a private channel is
// created and the caller is added as a member (a private channel is invisible to
// the caller otherwise, and the webhook is created with the caller's token, which
// requires channel access). callerUserID may be empty for paths that only ever
// resolve an existing channel (e.g. rotate). Used by add / add-custom / rotate.
//
// created reports whether THIS call brought the channel into existence (vs.
// resolving a pre-existing one). Callers use it to roll back — delete a channel
// they just created — when the rest of the add fails, without ever touching a
// channel that was already there (CL-01).
//
// teamName/channelName are returned so callers persist the authoritative names
// (CL-39). Storing the raw args instead was only safe because alertConfigNameRegex
// happens to reject anything that could differ — a load-bearing guard in another
// file that a future regex relaxation would silently defeat.
func (p *Plugin) resolveOrCreateChannel(teamSlug, channelSlug string, private bool, callerUserID string) (resolvedChannel, error) {
	team, appErr := p.API.GetTeamByName(teamSlug)
	if appErr != nil {
		return resolvedChannel{}, fmt.Errorf("get team %q: %w", teamSlug, appErr)
	}

	channel, appErr := p.API.GetChannelByName(team.Id, channelSlug, false)
	if appErr == nil {
		return resolvedChannel{channelID: channel.Id, teamName: team.Name, channelName: channel.Name, created: false}, nil
	}
	if appErr.StatusCode != http.StatusNotFound {
		return resolvedChannel{}, fmt.Errorf("get channel %q: %w", channelSlug, appErr)
	}

	chanType := model.ChannelTypeOpen
	if private {
		chanType = model.ChannelTypePrivate
	}
	newChannel, appErr := p.API.CreateChannel(&model.Channel{
		Name:        channelSlug,
		DisplayName: channelSlug,
		Type:        chanType,
		TeamId:      team.Id,
		CreatorId:   p.BotUserID,
	})
	if appErr != nil {
		return resolvedChannel{}, fmt.Errorf("create channel %q: %w", channelSlug, appErr)
	}

	// Private channels are invisible to non-members and the webhook is created
	// with the caller's token, so add the caller. Best-effort: a membership
	// failure shouldn't lose the created channel — surface it via the webhook
	// step's own error if it then fails.
	if private && callerUserID != "" {
		if _, mErr := p.API.AddChannelMember(newChannel.Id, callerUserID); mErr != nil {
			p.API.LogWarn("could not add caller to new private channel", "channel", channelSlug, "err", mErr.Error())
		}
	}
	return resolvedChannel{channelID: newChannel.Id, teamName: team.Name, channelName: newChannel.Name, created: true}, nil
}

// rollbackCreatedChannel archives a channel this add call just created after the
// rest of the add failed, so a failed add can't leave an empty squatted channel
// behind (CL-01). No-op when the channel pre-existed (created=false) — the plugin
// must never remove a channel it didn't make. Best-effort: a failed archive is
// logged, not surfaced, since the caller is already returning an add error.
func (p *Plugin) rollbackCreatedChannel(created bool, channelID, teamSlug, channelSlug string) {
	if !created || channelID == "" {
		return
	}
	// HA safety: between this pod creating the channel and its add failing,
	// another pod can resolve the same channel as existing and successfully
	// attach a receiver to it. Archiving here would delete that live
	// destination. Re-read the receiver list from KV (cluster-consistent, unlike
	// this pod's in-memory snapshot) and skip the archive if anything is now
	// bound to this team+channel — leave the empty channel for explicit cleanup
	// rather than risk removing another pod's in-use channel.
	fresh, err := p.loadAlertConfigsFromKV()
	if err != nil {
		// Fail CLOSED: if we can't read the list, we can't prove the channel is
		// unused, so don't archive it — a transient KV/parse error during the
		// concurrent-add window could otherwise delete another pod's live
		// destination. Leave the (possibly empty) channel for explicit cleanup.
		p.API.LogWarn("skipped channel rollback: could not read receiver list to confirm non-use (failing closed)", "channelID", channelID, "err", err.Error())
		return
	}
	for _, c := range fresh {
		if c.Team == teamSlug && c.Channel == channelSlug {
			p.API.LogWarn("skipped channel rollback: a receiver is now bound to it (HA concurrent add)", "channelID", channelID)
			return
		}
	}
	if appErr := p.API.DeleteChannel(channelID); appErr != nil {
		p.API.LogWarn("could not roll back channel created for a failed add", "channelID", channelID, "err", appErr.Error())
	}
}

// maxConfigWriteAttempts caps the compare-and-set retry loop in
// updateConfigsAtomic. Contention on the receiver list is admin-initiated and
// rare; a handful of attempts absorbs a genuine cross-pod race without spinning.
const maxConfigWriteAttempts = 5

// updateConfigsAtomic performs a cluster-safe read-modify-write of the receiver
// list (CL-24) and returns the list as it was BEFORE the change and AFTER it.
//
// configWriteMu only serializes writers within ONE pod. In HA, slash commands
// run on whichever pod serves the request, a KV write does NOT fire
// OnConfigurationChange on the other pods, and SavePluginConfig/KVSet is a blind
// overwrite — so two pods each computing from their own stale in-memory snapshot
// would lose an update. This reads the list straight from KV, applies transform
// to that fresh state, and commits with KVCompareAndSet (the write lands only if
// KV still holds exactly what we read); on a lost race it reloads and retries.
//
// transform must be a PURE function of the list it is handed — it may run several
// times, so it must not perform side effects (create/delete webhooks etc.). Do
// those before (incorporating the result into the transform) or after, keyed off
// the returned before/after lists.
//
// The caller MUST hold configWriteMu across its whole RMW; the guard panics
// otherwise. Holding it keeps intra-pod writers from needlessly losing CAS races
// to each other — CAS then only ever retries on a genuine cross-pod conflict.
func (p *Plugin) updateConfigsAtomic(transform func(current []alertConfig) ([]alertConfig, error)) (before, after []alertConfig, err error) {
	// Guard: TryLock succeeds only when the mutex is unlocked, so a success here
	// means nobody holds it — a caller forgot to lock. Fail loud rather than
	// silently reopen the race.
	if p.configWriteMu.TryLock() {
		p.configWriteMu.Unlock()
		panic("updateConfigsAtomic called without configWriteMu held — lock configWriteMu across the full read-modify-write")
	}

	for range maxConfigWriteAttempts {
		oldBytes, appErr := p.API.KVGet(kvKeyAlertConfigs)
		if appErr != nil {
			return nil, nil, fmt.Errorf("read receiver list from KV: %w", appErr)
		}
		current, perr := parseAlertConfigs(string(oldBytes))
		if perr != nil {
			return nil, nil, fmt.Errorf("parse current receiver list: %w", perr)
		}

		next, terr := transform(current)
		if terr != nil {
			return nil, nil, terr
		}

		newBytes, merr := json.MarshalIndent(next, "", "  ")
		if merr != nil {
			return nil, nil, fmt.Errorf("marshal: %w", merr)
		}
		// Validate before persisting so a bad write can't corrupt durable state.
		parsed, verr := parseAlertConfigs(string(newBytes))
		if verr != nil {
			return nil, nil, fmt.Errorf("validation: %w", verr)
		}

		// Commit only if KV still holds exactly what we read. A nil oldBytes (fresh
		// install — KVGet returns nil, never an empty slice, for an absent key)
		// makes this an insert-if-absent, which is what we want.
		set, appErr := p.API.KVCompareAndSet(kvKeyAlertConfigs, oldBytes, newBytes)
		if appErr != nil {
			return nil, nil, fmt.Errorf("persist receiver list to KV: %w", appErr)
		}
		if !set {
			continue // another pod wrote between our read and write — reload + retry
		}

		// Refresh THIS node's in-memory config from the committed write, then tell
		// peers to reload from KV — a KV write does not fire OnConfigurationChange
		// on other nodes, so without the broadcast they'd serve a stale list. The
		// config-map-backed settings are untouched by this write.
		p.applyAlertConfigsToMemory(parsed)
		p.broadcastConfigReload()
		return current, parsed, nil
	}
	return nil, nil, fmt.Errorf("receiver list is being modified concurrently; please retry")
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
		ac.Name, oldWebhookStatusLine(oldDeleted, oldHookID), yaml, ac.AlertManagerURL,
	)
}

// oldWebhookStatusLine returns the sentence describing the fate of the old
// webhook after a rotation: a clean "no longer works" when the delete succeeded,
// or an explicit warning (with the webhook ID to find in System Console) when it
// did not — so an admin rotating because of a suspected leak isn't told the token
// is dead when it may still be live (B-003).
func oldWebhookStatusLine(oldDeleted bool, oldHookID string) string {
	if oldDeleted {
		return "**The old webhook URL no longer works.**"
	}
	return fmt.Sprintf(":warning: **The old webhook could NOT be deleted and may still be live** — delete webhook `%s` in System Console → Integrations → Incoming Webhooks.", oldHookID)
}
