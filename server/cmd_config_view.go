package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// handleList: read-only summary of receivers bound to the current
// channel — always scoped, no org-wide escape hatch. A user running
// /alertmanager config list in #web-alerts should never see DB or compute
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
	fields := strings.Fields(args.Command)
	if len(fields) < 3 {
		return "Usage: `/alertmanager config <name>`\n\nList available receiver names with `/alertmanager config list`.", nil
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
			"Receiver `%s` is not bound to this channel. Run `/alertmanager config list` here to see what is.",
			name,
		), nil
	}

	yaml := renderReceiverYAMLForKind(match.Name, p.webhookURLForReceiver(*match), match.Channel, p.runbookDefaultURL(receiverBaseSlug(match.Name)), p.siteURL()+webhookIconURL, match.resolvedKind())

	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Receiver `%s`**\n\n", match.Name))
	b.WriteString(fmt.Sprintf("- **Team:** `%s`\n", match.Team))
	b.WriteString(fmt.Sprintf("- **Channel:** `~%s`\n", match.Channel))
	b.WriteString(fmt.Sprintf("- **Alertmanager URL:** `%s`\n", match.AlertManagerURL))
	b.WriteString(fmt.Sprintf("- **Webhook ID:** `%s`\n", redactHookID(match.WebhookID)))
	runbookURL := p.runbookDefaultURL(receiverBaseSlug(match.Name))
	b.WriteString(fmt.Sprintf("- **Runbook (default):** [%s](%s)\n", receiverBaseSlug(match.Name), runbookURL))
	if match.User != "" {
		// Username is shown but password is never echoed — even masked,
		// echoing it teaches admins the password length, which weakens it.
		b.WriteString(fmt.Sprintf("- **AM basic-auth user:** `%s` _(password set; not shown)_\n", match.User))
	}
	b.WriteString("\n**slack_configs YAML** (paste under `receivers:` in alertmanager.yml):\n\n```yaml\n")
	b.WriteString(yaml)
	b.WriteString("```\n\n")
	b.WriteString(":information_source: _Template re-rendered at export time — may differ from what's currently loaded in Alertmanager if templates changed since this receiver was created. Run `/alertmanager config validate` to confirm the live AM state._\n\n")
	b.WriteString(fmt.Sprintf("**Reload Alertmanager:**\n```\ncurl -X POST %s/-/reload\n```\n\n", match.AlertManagerURL))
	b.WriteString("**Actions:**\n")
	b.WriteString(fmt.Sprintf("- `/alertmanager config rotate %s` — rotate webhook URL (URL changes, paste the new YAML)\n", match.Name))
	b.WriteString(fmt.Sprintf("- `/alertmanager config remove %s` — delete the receiver and its webhook\n", match.Name))
	return b.String(), nil
}
