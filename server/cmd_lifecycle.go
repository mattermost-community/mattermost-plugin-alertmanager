package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"

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
func (p *Plugin) handleRemoveOne(args *model.CommandArgs, name string) (string, error) {
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

	if err := p.deleteIncomingWebhook(args.UserId, hookID); err != nil {
		// Webhook delete failures don't block removal of our entry —
		// admins can clean up the orphan webhook via System Console.
		p.API.LogWarn("could not delete incoming webhook on removal (continuing)", "webhookID", hookID, "err", err.Error())
	}
	if err := p.saveConfigs(filtered); err != nil {
		return fmt.Sprintf("Failed to persist config after webhook delete: %v", err), nil
	}

	return fmt.Sprintf(":wastebasket: Removed receiver `%s`. Don't forget to delete the corresponding `slack_configs` block from `alertmanager.yml`.", name), nil
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
	webhookFailures := make([]string, 0)
	for _, c := range current {
		if !namesToRemove[c.Name] {
			filtered = append(filtered, c)
			continue
		}
		if err := p.deleteIncomingWebhook(args.UserId, c.WebhookID); err != nil {
			p.API.LogWarn("remove-all: could not delete incoming webhook (config entry still pruned)",
				"receiver", c.Name, "webhookID", c.WebhookID, "err", err.Error())
			webhookFailures = append(webhookFailures, c.Name)
		}
		removed = append(removed, c.Name)
	}

	if err := p.saveConfigs(filtered); err != nil {
		return fmt.Sprintf("Failed to persist config after bulk delete: %v", err), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(":wastebasket: Removed %d receiver(s) from this channel:\n\n", len(removed)))
	for _, name := range removed {
		b.WriteString(fmt.Sprintf("- `%s`\n", name))
	}
	if len(webhookFailures) > 0 {
		b.WriteString(fmt.Sprintf("\n:warning: Couldn't delete the underlying webhook for %d receiver(s) (config entries are gone, but the webhooks may linger in System Console → Integrations): `%s`\n",
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
	webhookFailures := make([]string, 0)
	for _, c := range current {
		if !namesToRemove[c.Name] {
			filtered = append(filtered, c)
			continue
		}
		if err := p.deleteIncomingWebhook(args.UserId, c.WebhookID); err != nil {
			p.API.LogWarn("remove-set: could not delete incoming webhook (config entry still pruned)",
				"receiver", c.Name, "webhookID", c.WebhookID, "err", err.Error())
			webhookFailures = append(webhookFailures, c.Name)
		}
		removed = append(removed, c.Name)
	}

	if err := p.saveConfigs(filtered); err != nil {
		return fmt.Sprintf("Failed to persist config after set delete: %v", err), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(":wastebasket: Removed %d `%s`-set receiver(s) from this channel:\n\n", len(removed), setName))
	for _, name := range removed {
		b.WriteString(fmt.Sprintf("- `%s`\n", name))
	}
	if len(webhookFailures) > 0 {
		b.WriteString(fmt.Sprintf("\n:warning: Couldn't delete the underlying webhook for %d receiver(s) (config entries are gone, webhooks may linger in System Console → Integrations): `%s`\n",
			len(webhookFailures), strings.Join(webhookFailures, "`, `")))
	}
	b.WriteString("\nRemove the matching `slack_configs` and `routes:` entries from your `alertmanager.yml` and reload AM.")
	return b.String(), nil
}

// handleRotate: delete the old webhook, create a new one in the same
// channel, update the entry's WebhookID. Re-renders the YAML so the admin
// can paste the new URL into alertmanager.yml.
func (p *Plugin) handleRotate(args *model.CommandArgs) (string, error) {
	if err := p.requireChannelTeamAdmin(args.UserId, args.ChannelId); err != nil {
		return err.Error(), nil
	}

	fields := strings.Fields(args.Command)
	if len(fields) < 3 {
		return "Usage: `/alertmanager rotate <name>`", nil
	}
	name := fields[2]

	current := p.getConfiguration().AlertConfigs
	resolved := resolveReceiverName(current, name, args.ChannelId, p)
	idx := -1
	for i, c := range current {
		if c.Name == resolved {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Sprintf("Receiver %q not found.", name), nil
	}

	oldEntry := current[idx]
	channelID, err := p.resolveOrCreateChannel(oldEntry.Team, oldEntry.Channel)
	if err != nil {
		return fmt.Sprintf("Failed to resolve destination channel for rotation: %v", err), nil
	}

	newHookID, err := p.createIncomingWebhook(args.UserId, channelID, fmt.Sprintf("Alertmanager: %s", name))
	if err != nil {
		return fmt.Sprintf("Failed to create replacement webhook: %v", err), nil
	}

	if err := p.deleteIncomingWebhook(args.UserId, oldEntry.WebhookID); err != nil {
		p.API.LogWarn("could not delete old webhook during rotation (continuing)", "oldWebhookID", oldEntry.WebhookID, "err", err.Error())
	}

	updated := make([]alertConfig, len(current))
	copy(updated, current)
	updated[idx].WebhookID = newHookID

	if err := p.saveConfigs(updated); err != nil {
		_ = p.deleteIncomingWebhook(args.UserId, newHookID)
		return fmt.Sprintf("Failed to persist rotated config (new webhook rolled back): %v", err), nil
	}

	return p.renderRotateResponse(updated[idx]), nil
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
	// 2. Short-name match scoped to current channel
	if ch, appErr := p.API.GetChannel(channelID); appErr == nil {
		candidate := receiverNameForChannel(supplied, ch.Name)
		for _, c := range all {
			if c.Name == candidate {
				return c.Name
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

	var b strings.Builder
	b.WriteString(fmt.Sprintf("**%d receiver(s) bound to this channel:**\n\n", len(configs)))
	b.WriteString("| Name | Team | Channel | Alertmanager URL |\n")
	b.WriteString("|------|------|---------|------------------|\n")
	for _, c := range configs {
		amURL := c.AlertManagerURL
		if amURL == "" {
			amURL = "_(none)_"
		}
		b.WriteString(fmt.Sprintf("| `%s` | `%s` | `~%s` | %s |\n", c.Name, c.Team, c.Channel, amURL))
	}
	b.WriteString("\n_Full details (and the slack_configs YAML) for one receiver: `/alertmanager config <name>`_\n")
	return b.String(), nil
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

// dockerLocalhostWarning returns a markdown-formatted warning if the URL
// hostname is `localhost` or `127.0.0.1`. Returns empty if the URL is
// reachable-looking from inside a typical Docker-deployed Mattermost.
//
// The warning is informational, not blocking: an admin running Mattermost
// on bare metal with Alertmanager also on bare metal can legitimately use
// localhost. The vast majority of dev installs run both in Docker, where
// the URL needs to be host.docker.internal.
func dockerLocalhostWarning(rawURL, fieldLabel string) string {
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := parsed.Hostname()
	if host != "localhost" && host != "127.0.0.1" {
		return ""
	}
	suggested := strings.Replace(rawURL, "localhost", "host.docker.internal", 1)
	suggested = strings.Replace(suggested, "127.0.0.1", "host.docker.internal", 1)
	return fmt.Sprintf(
		":warning: **%s uses `%s`.** If Mattermost runs in Docker, this won't reach Alertmanager — `localhost` from inside the container points at the container itself, not the host or sibling containers. "+
			"Use `host.docker.internal` instead:\n\n"+
			"`%s`\n\n"+
			"Fix by re-running `/alertmanager add` with the corrected URL. Safe to ignore if both Mattermost and Alertmanager are running on the host directly (not in containers).\n\n---\n\n",
		fieldLabel, host, suggested,
	)
}

// saveConfigs marshals + validates + persists. Validation runs locally
// before SavePluginConfig so handlers can return clean errors without
// touching durable state.
func (p *Plugin) saveConfigs(entries []alertConfig) error {
	blob, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := parseAlertConfigs(string(blob)); err != nil {
		return fmt.Errorf("validation: %w", err)
	}
	// Lowercase the key because Mattermost's System Console webapp
	// lowercases setting.key when constructing the storage path — keeping
	// our save path aligned with its read path. Go's case-insensitive
	// JSON unmarshaling handles the read side regardless.
	return p.client.Configuration.SavePluginConfig(map[string]any{
		"alertconfigsjson": string(blob),
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
