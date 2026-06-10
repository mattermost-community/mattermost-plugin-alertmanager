package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// handleExport renders fresh YAML for every receiver bound to the
// current channel and DMs the resulting receivers.yml to the calling
// sysadmin. Used to apply template/URL changes from a plugin upgrade
// to existing receivers without rotating their webhook IDs (which
// /alertmanager remove all + add would do).
//
// Sysadmin-gated because the rendered YAML embeds webhook URLs
// (channel-bound bearer tokens). Channel-scoped: only receivers bound
// to the invocation channel get exported.
//
// Usage:
//
//	/alertmanager export
//
// Output: an in-channel ephemeral summary (count + receiver names),
// plus a DM from @alertmanagerbot with the assembled receivers.yml
// attached.
func (p *Plugin) handleExport(args *model.CommandArgs) (string, error) {
	if err := p.requireChannelTeamAdmin(args.UserId, args.ChannelId); err != nil {
		return err.Error(), nil
	}

	scoped := p.configsForCurrentChannel(args)
	if len(scoped) == 0 {
		return ":information_source: No receivers bound to this channel — nothing to export.", nil
	}

	// Assemble the YAML for every channel-scoped receiver. Same
	// concatenation pattern as the /alertmanager add file, but iterates
	// over existing entries rather than newly-created ones.
	var y strings.Builder
	y.WriteString("# Alertmanager receivers exported by /alertmanager export\n")
	y.WriteString("# All receivers currently registered in plugin config for this channel.\n")
	y.WriteString("# Hook IDs are preserved — replace the receivers: block in your\n")
	y.WriteString("# alertmanager.yml with this content, then reload AM.\n")
	y.WriteString(fmt.Sprintf("# Receivers: %d\n", len(scoped)))
	y.WriteString("\n")
	for _, ac := range scoped {
		y.WriteString(renderReceiverYAML(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL))
		y.WriteString("\n")
	}

	// DM both the receivers AND the matching routes as separate files.
	// Same delivery path as /alertmanager add — ephemeral file
	// attachments are unreliable in Mattermost, DMs persist normally.
	routesYAML := assembleRoutesYAML(scoped)
	dmErr := p.dmYAMLBundle(args.UserId, y.String(), routesYAML, len(scoped), "")
	if dmErr != nil {
		return fmt.Sprintf(
			":warning: Couldn't DM the assembled YAML (%v). Inline copy below — paste under `receivers:` in your `alertmanager.yml`:\n\n```yaml\n%s```\n",
			dmErr, y.String(),
		), nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(":page_facing_up: Exported %d receiver(s) to your DM with `@%s`.\n\n", len(scoped), webhookUsername))
	b.WriteString("**Receivers exported:**\n\n```\n")
	for _, ac := range scoped {
		b.WriteString(ac.Name + "\n")
	}
	b.WriteString("```\n\n")
	b.WriteString("Open your DM with the bot to download `alertmanager-receivers.yml`. Replace the `receivers:` block in your `alertmanager.yml` with the file contents, then reload Alertmanager. Hook IDs are preserved — no webhook rotation involved.\n")
	return b.String(), nil
}
