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

	current := p.getConfiguration().AlertConfigs
	resolved := resolveReceiverName(current, name, args.ChannelId, p)
	var hookID string
	filtered := make([]alertConfig, 0, len(current))
	for _, c := range current {
		if c.Name == resolved {
			hookID = c.WebhookID
			continue
		}
		filtered = append(filtered, c)
	}
	if hookID == "" {
		return fmt.Sprintf("Receiver %q not found.", name), nil
	}

	if err := p.saveConfigsLocked(filtered); err != nil {
		return fmt.Sprintf("Failed to persist config: %v", err), nil
	}

	// Refcount: only delete the underlying webhook if no other receiver
	// still depends on it.
	if !webhookStillReferenced(filtered, hookID) {
		if err := p.deleteIncomingWebhook(args.UserId, hookID); err != nil {
			p.API.LogWarn("could not delete orphaned webhook on remove (continuing)", "webhookID", hookID, "err", err.Error())
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

	current := p.getConfiguration().AlertConfigs
	filtered := make([]alertConfig, 0, len(current))
	removed := make([]string, 0, len(scoped))
	for _, c := range current {
		if !namesToRemove[c.Name] {
			filtered = append(filtered, c)
			continue
		}
		removed = append(removed, c.Name)
	}

	if err := p.saveConfigsLocked(filtered); err != nil {
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
				"webhookID", hookID, "err", err.Error())
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

	current := p.getConfiguration().AlertConfigs
	filtered := make([]alertConfig, 0, len(current))
	removed := make([]string, 0, len(matched))
	for _, c := range current {
		if !namesToRemove[c.Name] {
			filtered = append(filtered, c)
			continue
		}
		removed = append(removed, c.Name)
	}

	if err := p.saveConfigsLocked(filtered); err != nil {
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
				"webhookID", hookID, "err", err.Error())
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

	return p.handleRotateSingle(args, target)
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
func (p *Plugin) handleRotateSingle(args *model.CommandArgs, name string) (string, error) {
	// Atomic read-modify-write. handleRotateOverdue calls this in a loop but
	// does not hold configWriteMu itself, so locking per-call is deadlock-free
	// and each rotation is an independent atomic update.
	p.configWriteMu.Lock()
	defer p.configWriteMu.Unlock()

	current := p.getConfiguration().AlertConfigs
	resolved := resolveReceiverName(current, name, args.ChannelId, p)
	targetIdx := -1
	for i, c := range current {
		if c.Name == resolved {
			targetIdx = i
			break
		}
	}
	if targetIdx == -1 {
		return fmt.Sprintf("Receiver %q not found.", name), nil
	}

	target := current[targetIdx]
	oldHookID := target.WebhookID

	// Find every receiver sharing this webhookID. For legacy receivers
	// (empty GroupName, individual webhook) this is just the named one.
	// For grouped receivers it's the whole group, including any in other
	// channels — though with the current group-create logic, all members
	// share team+channel so cross-channel sharing shouldn't arise. Still,
	// the parser tolerates it via the same-team+channel+group invariant,
	// so the rotation handler tolerates it too.
	affectedIdx := make([]int, 0)
	for i, c := range current {
		if c.WebhookID == oldHookID {
			affectedIdx = append(affectedIdx, i)
		}
	}

	channelID, err := p.resolveOrCreateChannel(target.Team, target.Channel)
	if err != nil {
		return fmt.Sprintf("Failed to resolve destination channel for rotation: %v", err), nil
	}

	// Webhook display name for the replacement follows the same rule as
	// /alertmanager add: <group-or-slug>--<channel>. Legacy receivers
	// (empty GroupName) keep the per-receiver naming form so the System
	// Console webhook list stays self-explanatory for those entries.
	var newDisplayName string
	if target.GroupName != "" {
		newDisplayName = fmt.Sprintf("Alertmanager: %s--%s", target.GroupName, target.Channel)
	} else {
		newDisplayName = fmt.Sprintf("Alertmanager: %s", target.Name)
	}
	newHookID, err := p.createIncomingWebhook(args.UserId, channelID, newDisplayName)
	if err != nil {
		return fmt.Sprintf("Failed to create replacement webhook: %v", err), nil
	}

	if err := p.deleteIncomingWebhook(args.UserId, oldHookID); err != nil {
		p.API.LogWarn("could not delete old webhook during rotation (continuing)", "oldWebhookID", oldHookID, "err", err.Error())
	}

	updated := make([]alertConfig, len(current))
	copy(updated, current)
	now := time.Now().UTC()
	for _, idx := range affectedIdx {
		updated[idx].WebhookID = newHookID
		updated[idx].LastRotatedAt = now
		updated[idx].LastReminderAt = time.Time{}
	}

	if err := p.saveConfigsLocked(updated); err != nil {
		_ = p.deleteIncomingWebhook(args.UserId, newHookID)
		return fmt.Sprintf("Failed to persist rotated config (new webhook rolled back): %v", err), nil
	}

	p.auditLog("webhook.rotation.executed", args.UserId, target.Name, args.ChannelId,
		fmt.Sprintf("affected=%d group=%q", len(affectedIdx), target.GroupName))

	// Single-receiver case (legacy or true individual): inline YAML,
	// matches v1.0.2 behavior.
	if len(affectedIdx) == 1 {
		ac := updated[affectedIdx[0]]
		return p.renderRotateResponse(ac), nil
	}

	// Group case: list affected receivers, DM the merged YAML bundle.
	affected := make([]alertConfig, 0, len(affectedIdx))
	for _, idx := range affectedIdx {
		affected = append(affected, updated[idx])
	}
	return p.renderRotateGroupResponse(args.UserId, affected, target.GroupName), nil
}

// renderRotateGroupResponse builds the in-channel summary AND fires the
// DM with the merged YAML bundle when the rotated webhook serves a
// multi-receiver group. Same DM shape as /alertmanager rotate all
// --overdue — operator pastes once into alertmanager.yml.
func (p *Plugin) renderRotateGroupResponse(userID string, affected []alertConfig, groupName string) string {
	primary := affected[0]
	var y strings.Builder
	y.WriteString(fmt.Sprintf("# Alertmanager receivers re-rotated by /alertmanager rotate (group %q)\n", groupName))
	y.WriteString(fmt.Sprintf("# %d receiver(s) share the rotated webhook.\n", len(affected)))
	y.WriteString("# Paste under `receivers:` in your alertmanager.yml, then reload AM.\n")
	y.WriteString("# Old URLs deactivated immediately — alert delivery resumes after the AM reload.\n\n")
	for _, ac := range affected {
		y.WriteString(renderReceiverYAML(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL))
		y.WriteString("\n")
	}
	routesYAML := assembleRoutesYAML(affected)
	if dmErr := p.dmYAMLBundle(userID, y.String(), routesYAML, len(affected), primary.AlertManagerURL); dmErr != nil {
		p.API.LogWarn("rotation: couldn't DM YAML after group rotate", "err", dmErr.Error())
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(":key: Rotated `%s` group webhook in `~%s`. **The old URL no longer works for any of the %d affected receiver(s).**\n\n", groupName, primary.Channel, len(affected)))
	b.WriteString("**Affected:**\n")
	for _, ac := range affected {
		b.WriteString("- `" + ac.Name + "`\n")
	}
	b.WriteString(fmt.Sprintf("\nMerged YAML DM'd to you from `@%s`. Paste it into your `alertmanager.yml`, then reload AM (`curl -X POST %s/-/reload`).", webhookUsername, primary.AlertManagerURL))
	return b.String()
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

	rotated := make([]alertConfig, 0, len(overdueNames))
	failed := make([]string, 0)
	for _, name := range overdueNames {
		summary, err := p.handleRotateSingle(args, name)
		if err != nil || strings.HasPrefix(summary, "Failed") || strings.HasPrefix(summary, "Receiver") {
			failed = append(failed, name+" — "+summary)
			continue
		}
		// Pull the updated entry from the current config so the
		// summary DM has the new WebhookID baked in.
		for _, c := range p.getConfiguration().AlertConfigs {
			if c.Name == name {
				rotated = append(rotated, c)
				break
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
		y.WriteString(renderReceiverYAML(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL))
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

	yaml := renderReceiverYAML(match.Name, p.webhookURLForReceiver(*match), match.Channel, p.runbookDefaultURL(receiverBaseSlug(match.Name)), p.siteURL()+webhookIconURL)

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

// resolveOrCreateChannel maps team-slug + channel-slug → channel ID,
// creating the channel as an open channel if missing. Used by add and
// rotate when we need to bind a webhook to a channel.
func (p *Plugin) resolveOrCreateChannel(teamSlug, channelSlug string) (string, error) {
	team, appErr := p.API.GetTeamByName(teamSlug)
	if appErr != nil {
		return "", fmt.Errorf("get team %q: %w", teamSlug, appErr)
	}

	channel, appErr := p.API.GetChannelByName(team.Id, channelSlug, false)
	if appErr == nil {
		return channel.Id, nil
	}
	if appErr.StatusCode != http.StatusNotFound {
		return "", fmt.Errorf("get channel %q: %w", channelSlug, appErr)
	}

	created, appErr := p.API.CreateChannel(&model.Channel{
		Name:        channelSlug,
		DisplayName: channelSlug,
		Type:        model.ChannelTypeOpen,
		TeamId:      team.Id,
		CreatorId:   p.BotUserID,
	})
	if appErr != nil {
		return "", fmt.Errorf("create channel %q: %w", channelSlug, appErr)
	}
	return created.Id, nil
}

// saveConfigs marshals + validates + persists. Validation runs locally
// before SavePluginConfig so handlers can return clean errors without
// touching durable state.
//
// Mattermost's SavePluginConfig REPLACES the entire plugin config map
// rather than merging. If we pass just `alertconfigsjson`, every
// other setting (WebhookHost, MetricsToken, CA bundle, YAML TTL) gets
// wiped on every save — which happens on /alertmanager add, remove,
// rotate, and the background reconciler. The bug is silent: the
// settings still appear in System Console (defaults from
// plugin.json's settings_schema kick in), but custom values an
// admin set are erased on the next mutate operation.
//
// Fix: always pass the full set of keys we own. Read the live
// configuration first, splice in the new alertconfigsjson, write
// the merged map back. Lowercased keys because MM's webapp
// lowercases setting.key when constructing the storage path —
// keeping our save path aligned with its read path. Go's
// case-insensitive JSON unmarshaling handles the read side
// regardless.
// saveConfigsLocked persists the receiver list. The caller MUST hold
// configWriteMu across its entire read-modify-write (from the initial
// getConfiguration read through this save) — that's what makes the RMW
// atomic and prevents lost updates (two callers computing from the same
// stale snapshot, the second clobbering the first). Locking only inside
// the save serialized writes but did not close that race.
func (p *Plugin) saveConfigsLocked(entries []alertConfig) error {
	// Guard: TryLock succeeds only when the mutex is unlocked, so a success
	// here means nobody holds it — a caller forgot to lock. Fail loud rather
	// than silently reopen the race. Never false-positives: if this goroutine
	// (or any other) holds the lock, TryLock returns false and we proceed.
	if p.configWriteMu.TryLock() {
		p.configWriteMu.Unlock()
		panic("saveConfigsLocked called without configWriteMu held — lock configWriteMu across the full read-modify-write")
	}

	blob, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := parseAlertConfigs(string(blob)); err != nil {
		return fmt.Errorf("validation: %w", err)
	}
	cur := p.getConfiguration()
	return p.client.Configuration.SavePluginConfig(map[string]any{
		"alertconfigsjson":      string(blob),
		"webhookhost":           cur.WebhookHost,
		"assembledyamlttlhours": cur.AssembledYAMLTTLHours,
		"alertmanagercabundle":  cur.AlertManagerCABundle,
		"metricstoken":          cur.MetricsToken,
		"webhookrotationdays":   cur.WebhookRotationDays,
	})
}

// renderRotateResponse builds the success message for /alertmanager
// rotate. The receiver's slack_configs YAML embeds the new webhook URL,
// so the admin re-pastes the whole block to update alertmanager.yml.
func (p *Plugin) renderRotateResponse(ac alertConfig) string {
	yaml := renderReceiverYAML(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL)
	return fmt.Sprintf(
		":key: Rotated webhook for `%s`. **The old webhook URL no longer works.**\n\n"+
			"**Update your `alertmanager.yml`:**\n\n```yaml\n%s```\n\n"+
			"**Then reload Alertmanager:**\n```\ncurl -X POST %s/-/reload\n```",
		ac.Name, yaml, ac.AlertManagerURL,
	)
}
