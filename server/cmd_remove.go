package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// handleRemove dispatches to one of three modes by target arg:
//
//	<name>           → single receiver by name
//	<set>            → all receivers in this channel matching that runbook set
//	                   (compute, application, database, storage, networking,
//	                   observability) — requires force
//	all              → every receiver in this channel — requires force
//
// All paths are channel-scoped: a user in #web-alerts can't reach
// receivers bound to #db-alerts. Bulk paths (set, all) gate on force
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
		return "Usage: `/alertmanager config remove <name>` | `/alertmanager config remove <set> force` | `/alertmanager config remove all force`\n\nSets: `compute`, `application`, `database`, `storage`, `networking`, `observability`", nil
	}
	target := strings.ToLower(fields[2])
	force := len(fields) >= 4 && fields[3] == "force"

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
	// Capture the WebhookID we previewed so the CAS transform deletes only THIS
	// receiver, not a same-name one that another node removed+re-created between
	// our scoped snapshot and the fresh-KV write (identity, not just name).
	expectedHookID := webhookIDForName(scoped, resolved)

	var hookID string
	var identityChanged bool
	_, filtered, err := p.updateConfigsAtomic(func(current []alertConfig) ([]alertConfig, error) {
		hookID = "" // reset per attempt — the transform may run more than once
		identityChanged = false
		out := make([]alertConfig, 0, len(current))
		for _, c := range current {
			if c.Name == resolved {
				if c.WebhookID != expectedHookID {
					// Same name, different webhook — a different receiver now. Leave
					// it in place rather than delete something we never previewed.
					identityChanged = true
					out = append(out, c)
					continue
				}
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
	if identityChanged {
		return fmt.Sprintf("Receiver %q changed since you last listed it (it was re-created elsewhere with a new webhook). Nothing removed — re-run `/alertmanager config list` then remove again.", resolved), nil
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

// webhookIDForName returns the WebhookID of the named receiver in the snapshot,
// or "" if absent. Used to pin a remove to the exact receiver identity that was
// previewed, so a same-name re-create on another node isn't deleted by mistake.
func webhookIDForName(entries []alertConfig, name string) string {
	for _, c := range entries {
		if c.Name == name {
			return c.WebhookID
		}
	}
	return ""
}

// expectedIdentities maps receiver Name -> previewed WebhookID across a set of
// entries. Bulk/set remove transforms delete an entry only when BOTH its name
// and webhook ID match this map, so a same-name receiver re-created on another
// node between preview and the fresh-KV write is left intact.
func expectedIdentities(entries []alertConfig) map[string]string {
	m := make(map[string]string, len(entries))
	for _, c := range entries {
		m[c.Name] = c.WebhookID
	}
	return m
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
//	/alertmanager config remove all           → dry-run preview (lists targets)
//	/alertmanager config remove all force   → actually removes
//
// Webhook delete failures don't abort — the plugin config entry is still
// pruned so /alertmanager config list reflects the truth. Orphan webhooks (if
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
		// Dry-run: list what would be deleted, refuse to execute without force.
		var b strings.Builder
		b.WriteString(fmt.Sprintf(":warning: **About to remove %d receiver(s) bound to this channel:**\n\n", len(scoped)))
		for _, c := range scoped {
			b.WriteString(fmt.Sprintf("- `%s` (webhook `%s`)\n", c.Name, redactHookID(c.WebhookID)))
		}
		b.WriteString("\nThis deletes the plugin config entries AND the underlying Mattermost incoming webhooks. **The corresponding `slack_configs` blocks in your `alertmanager.yml` will start failing immediately** — clean them up after.\n\n")
		b.WriteString("To proceed, re-run with `force`:\n\n```\n/alertmanager config remove all force\n```\n")
		return b.String(), nil
	}

	// Build the set of names to prune (channel-scoped) and walk the full
	// config so we keep entries from other channels intact.
	expected := expectedIdentities(scoped)

	removed := make([]string, 0, len(scoped))
	skippedChanged := make([]string, 0)
	current, filtered, err := p.updateConfigsAtomic(func(current []alertConfig) ([]alertConfig, error) {
		removed = removed[:0] // reset per attempt
		skippedChanged = skippedChanged[:0]
		out := make([]alertConfig, 0, len(current))
		for _, c := range current {
			want, targeted := expected[c.Name]
			if !targeted {
				out = append(out, c)
				continue
			}
			if c.WebhookID != want {
				// Same name, different webhook — re-created elsewhere since we
				// scoped. Keep it and report rather than delete a receiver we
				// never previewed.
				skippedChanged = append(skippedChanged, c.Name)
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
			webhookFailures = append(webhookFailures, redactHookID(hookID)) // F-002: fingerprint, not the raw bearer ID
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
		b.WriteString(fmt.Sprintf("\n:warning: Couldn't delete %d underlying webhook(s) — config entries are gone, but the webhooks may still be live. Find them by name in System Console → Integrations → Incoming Webhooks and remove them. Fingerprints: `%s`\n",
			len(webhookFailures), strings.Join(webhookFailures, "`, `")))
	}
	if len(skippedChanged) > 0 {
		b.WriteString(fmt.Sprintf("\n:information_source: Left %d receiver(s) untouched — they were re-created (new webhook) since you last listed this channel: `%s`. Re-run `/alertmanager config list` then remove again if you still want them gone.\n",
			len(skippedChanged), strings.Join(skippedChanged, "`, `")))
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
//	/alertmanager config remove compute            → dry-run preview
//	/alertmanager config remove compute force    → actually removes
//
// Webhook delete failures don't abort — config entries are still pruned
// so /alertmanager config list reflects the new truth. Orphan webhooks (if
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
			b.WriteString(fmt.Sprintf("- `%s` (webhook `%s`)\n", c.Name, redactHookID(c.WebhookID)))
		}
		b.WriteString(fmt.Sprintf("\nReceivers in this channel NOT in the `%s` set will be left alone. To proceed:\n\n```\n/alertmanager config remove %s force\n```\n", setName, setName))
		return b.String(), nil
	}

	expected := expectedIdentities(matched)

	removed := make([]string, 0, len(matched))
	skippedChanged := make([]string, 0)
	current, filtered, err := p.updateConfigsAtomic(func(current []alertConfig) ([]alertConfig, error) {
		removed = removed[:0] // reset per attempt
		skippedChanged = skippedChanged[:0]
		out := make([]alertConfig, 0, len(current))
		for _, c := range current {
			want, targeted := expected[c.Name]
			if !targeted {
				out = append(out, c)
				continue
			}
			if c.WebhookID != want {
				// Re-created elsewhere since we scoped — keep it, don't delete a
				// receiver we never previewed.
				skippedChanged = append(skippedChanged, c.Name)
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
			webhookFailures = append(webhookFailures, redactHookID(hookID)) // F-002: fingerprint, not the raw bearer ID
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
		b.WriteString(fmt.Sprintf("\n:warning: Couldn't delete %d underlying webhook(s) — config entries gone, but the webhooks may still be live. Find them by name in System Console → Integrations → Incoming Webhooks and remove them. Fingerprints: `%s`\n",
			len(webhookFailures), strings.Join(webhookFailures, "`, `")))
	}
	if len(skippedChanged) > 0 {
		b.WriteString(fmt.Sprintf("\n:information_source: Left %d receiver(s) untouched — re-created (new webhook) since you last listed this channel: `%s`. Re-run `/alertmanager config list` then remove again if you still want them gone.\n",
			len(skippedChanged), strings.Join(skippedChanged, "`, `")))
	}
	b.WriteString("\nRemove the matching `slack_configs` and `routes:` entries from your `alertmanager.yml` and reload AM.")
	return b.String(), nil
}
