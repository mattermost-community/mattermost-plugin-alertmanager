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
//	/alertmanager export                          # plain export
//	/alertmanager export --diff-against-loaded    # sysadmin-only diff
//
// `--diff-against-loaded` shifts the output to a side-by-side
// diff between the AM-loaded config (fetched live via /api/v2/status)
// and what the plugin would emit. Useful as the answer to "I have
// some receivers in AM YAML by hand, what would the plugin's
// additions look like merged in?" — without the operator having to
// guess the merge points themselves.
//
// Output: an in-channel ephemeral summary, plus a DM from
// @alertmanagerbot with the assembled receivers.yml (or
// alertmanager-diff.txt when --diff-against-loaded is set).
func (p *Plugin) handleExport(args *model.CommandArgs) (string, error) {
	fields := strings.Fields(args.Command)
	rest := fields[2:]
	diffMode := containsFlag(rest, "--diff-against-loaded")

	// --diff-against-loaded is sysadmin-only because it surfaces the
	// AM-loaded YAML verbatim, which includes every receiver's
	// webhook URL and any basic-auth creds across the org. Plain
	// export stays channel-team-admin since it only emits the
	// channel-scoped receivers we already manage.
	if diffMode {
		if err := p.requireSystemAdmin(args.UserId); err != nil {
			return err.Error(), nil
		}
	} else {
		if err := p.requireChannelTeamAdmin(args.UserId, args.ChannelId); err != nil {
			return err.Error(), nil
		}
	}

	scoped := p.configsForCurrentChannel(args)
	if len(scoped) == 0 {
		return ":information_source: No receivers bound to this channel — nothing to export.", nil
	}

	if diffMode {
		return p.handleExportDiff(args, scoped)
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

	// Discoverability hint for the diff flag. Sysadmin-only feature,
	// so the tip is conditional on the calling user actually being
	// a sysadmin — no point teasing a command the caller can't run.
	if p.requireSystemAdmin(args.UserId) == nil {
		b.WriteString("\n")
		b.WriteString(":bulb: **Sysadmin tip:** run `/alertmanager export --diff-against-loaded` to see a side-by-side diff between Alertmanager's currently-loaded config and what this export would add. Useful before pasting into a hand-maintained `alertmanager.yml`.\n")
	}
	return b.String(), nil
}

// handleExportDiff is the --diff-against-loaded path. Fetches AM's
// currently-loaded config via /api/v2/status, identifies which of
// the channel's receivers are new vs already present, and renders a
// unified-diff-style output showing what the plugin would add.
//
// Sysadmin-only: the loaded AM config includes every receiver in
// the org (not just the calling channel's), including their webhook
// URLs and any basic-auth creds. This is intentional for v1 — the
// reviewer specifically asked for "raw" output to evaluate. A
// future iteration will scope the displayed output to the calling
// channel only and redact other-channel secrets.
func (p *Plugin) handleExportDiff(args *model.CommandArgs, scoped []alertConfig) (string, error) {
	groups := groupByAMURL(scoped)
	var diffOutput strings.Builder
	var summary strings.Builder
	summary.WriteString(":mag: **Export diff vs loaded Alertmanager config**\n\n")
	summary.WriteString(fmt.Sprintf("Scoped to %d receiver(s) in this channel across %d Alertmanager backend(s).\n\n", len(scoped), len(groups)))

	totalAdds := 0
	for _, g := range groups {
		entry := p.probeAMReachability(g.URL)
		fmt.Fprintf(&diffOutput, "=== Alertmanager backend: %s ===\n\n", g.URL)
		if !entry.Reachable {
			fmt.Fprintf(&diffOutput, "BACKEND UNREACHABLE (%s) — cannot diff against unknown config.\n\n", entry.Status)
			continue
		}
		if entry.ConfigBody == "" {
			diffOutput.WriteString("AM responded but didn't return its config body in /api/v2/status — older AM versions did this. Cannot diff.\n\n")
			continue
		}

		// Identify additions: receivers in our channel's scope whose
		// `name:` doesn't appear in AM's loaded YAML.
		var toAdd []alertConfig
		var alreadyLoaded []string
		for _, ac := range g.Receivers {
			if strings.Contains(entry.ConfigBody, "name: "+ac.Name) {
				alreadyLoaded = append(alreadyLoaded, ac.Name)
				continue
			}
			toAdd = append(toAdd, ac)
		}
		totalAdds += len(toAdd)

		fmt.Fprintf(&diffOutput, "Receivers in channel: %d\n", len(g.Receivers))
		fmt.Fprintf(&diffOutput, "  Already loaded in AM: %d\n", len(alreadyLoaded))
		fmt.Fprintf(&diffOutput, "  Would be added:       %d\n\n", len(toAdd))

		if len(toAdd) == 0 {
			diffOutput.WriteString("No additions needed for this backend.\n\n")
			continue
		}

		// Render the receivers + routes we'd add, then build the diff.
		var newRecvs, newRoutes strings.Builder
		for _, ac := range toAdd {
			newRecvs.WriteString(renderReceiverYAML(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL))
			newRecvs.WriteString("\n")
		}
		newRoutes.WriteString(assembleRoutesYAML(toAdd))

		diffText := buildDiffAgainstLoaded(entry.ConfigBody, newRecvs.String(), newRoutes.String())
		diffOutput.WriteString(diffText)
		diffOutput.WriteString("\n")
	}

	// DM the raw diff as a file. Keep the in-channel response light:
	// a count + pointer to the DM.
	dm, appErr := p.API.GetDirectChannel(p.BotUserID, args.UserId)
	if appErr != nil {
		return fmt.Sprintf(":warning: Couldn't open DM channel: %v", appErr), nil
	}
	info, appErr := p.API.UploadFile([]byte(diffOutput.String()), dm.Id, "alertmanager-diff.txt")
	if appErr != nil {
		return fmt.Sprintf(":warning: Couldn't upload diff file: %v\n\nInline copy:\n\n```\n%s\n```", appErr, diffOutput.String()), nil
	}
	post := &model.Post{
		UserId:    p.BotUserID,
		ChannelId: dm.Id,
		Message:   fmt.Sprintf("Diff against loaded Alertmanager config (channel: %s).\n\n_Raw output, no redaction — review before sharing._", args.ChannelId),
		FileIds:   []string{info.Id},
	}
	if _, postErr := p.API.CreatePost(post); postErr != nil {
		return fmt.Sprintf(":warning: Couldn't post DM with diff attachment: %v", postErr), nil
	}

	summary.WriteString(fmt.Sprintf("Total additions across all backends: **%d**\n\n", totalAdds))
	summary.WriteString(fmt.Sprintf("Diff DM'd as `alertmanager-diff.txt` from `@%s`. Open the DM to download.\n", webhookUsername))
	summary.WriteString("\n:warning: This output is RAW — it contains every receiver loaded in AM (across all channels and teams), including their webhook URLs and any basic-auth credentials. Don't paste it into a public channel. Redacted-output mode is planned for a follow-up release.")
	return summary.String(), nil
}

// buildDiffAgainstLoaded produces unified-diff-style output showing
// what would be inserted into the AM-loaded YAML. Not a strict diff
// algorithm — assumes pure-addition semantics (which the plugin's
// additions always are) and identifies the insertion points by
// finding top-level `receivers:` and `route.routes:` keys.
//
// Lines that exist in the loaded YAML get prefixed with two spaces
// (unified-diff "context" convention). Inserted lines get prefixed
// with `+ ` (unified-diff "addition" convention). The result is
// readable with `git diff`-style rendering or in any text editor.
//
// Behavior when the insertion points aren't found (malformed YAML,
// unusual structure): append at end with a comment explaining the
// fallback. The operator can still see what would be added and apply
// the merge manually.
func buildDiffAgainstLoaded(loadedYAML, newReceivers, newRoutes string) string {
	var b strings.Builder

	loadedLines := strings.Split(loadedYAML, "\n")

	// First pass: emit loaded lines, marking where we'll insert.
	// We find the END of the receivers: block (first top-level key
	// after `receivers:` OR EOF) and the END of the route.routes:
	// block (first sibling-or-shallower key after `routes:`).
	receiversEndIdx := -1
	routesEndIdx := -1
	inReceivers := false
	inRoute := false
	inRoutes := false

	for i, line := range loadedLines {
		trim := strings.TrimRight(line, " \t")
		// Detect transitions out of receivers: when we see a top-level
		// key after `receivers:`.
		if inReceivers && len(trim) > 0 && trim[0] != ' ' && trim[0] != '\t' && trim[0] != '#' && !strings.HasPrefix(trim, "receivers:") {
			receiversEndIdx = i
			inReceivers = false
		}
		// Detect transitions out of routes: when we dedent back to
		// route:'s level or shallower.
		if inRoutes && len(trim) > 0 && !strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "\t\t") {
			routesEndIdx = i
			inRoutes = false
			inRoute = false
		}

		if strings.HasPrefix(trim, "receivers:") {
			inReceivers = true
		}
		if strings.HasPrefix(trim, "route:") {
			inRoute = true
		}
		if inRoute && strings.HasPrefix(strings.TrimLeft(line, " \t"), "routes:") {
			inRoutes = true
		}
	}
	// Boundary case: section ran to EOF.
	if inReceivers && receiversEndIdx == -1 {
		receiversEndIdx = len(loadedLines)
	}
	if inRoutes && routesEndIdx == -1 {
		routesEndIdx = len(loadedLines)
	}

	// Helper closures avoid repeating the marker+addition emit
	// pattern at three different call sites (mid-file, end-of-file,
	// and no-block-found fallback).
	emitReceivers := func() {
		b.WriteString("+ # ---- plugin additions: receivers ----\n")
		for _, addLine := range strings.Split(strings.TrimRight(newReceivers, "\n"), "\n") {
			b.WriteString("+ ")
			b.WriteString(addLine)
			b.WriteString("\n")
		}
	}
	emitRoutes := func() {
		b.WriteString("+ # ---- plugin additions: routes ----\n")
		for _, addLine := range strings.Split(strings.TrimRight(newRoutes, "\n"), "\n") {
			b.WriteString("+ ")
			b.WriteString(addLine)
			b.WriteString("\n")
		}
	}

	// Second pass: emit with diff markers. The loop bound is
	// `<= len` rather than `< len` so insertions whose index lands
	// exactly at end-of-file (block ran to EOF) still fire — without
	// this we'd silently drop additions on YAML configs whose
	// receivers: or route.routes: block is the very last section.
	for i := 0; i <= len(loadedLines); i++ {
		if i == receiversEndIdx && newReceivers != "" {
			emitReceivers()
		}
		if i == routesEndIdx && newRoutes != "" {
			emitRoutes()
		}
		if i < len(loadedLines) {
			b.WriteString("  ")
			b.WriteString(loadedLines[i])
			b.WriteString("\n")
		}
	}

	// Fallback: if insertion points weren't found at all (malformed
	// YAML, unusual layout, partial config), append at the end of
	// the output with a NOTE so the operator knows it wasn't a
	// clean splice.
	if receiversEndIdx == -1 && newReceivers != "" {
		b.WriteString("\n+ # ---- plugin additions: receivers ----\n")
		b.WriteString("+ # NOTE: couldn't find `receivers:` block — merge these manually under it.\n")
		for _, addLine := range strings.Split(strings.TrimRight(newReceivers, "\n"), "\n") {
			b.WriteString("+ ")
			b.WriteString(addLine)
			b.WriteString("\n")
		}
	}
	if routesEndIdx == -1 && newRoutes != "" {
		b.WriteString("\n+ # ---- plugin additions: routes ----\n")
		b.WriteString("+ # NOTE: couldn't find `route.routes:` block — merge these manually under it.\n")
		for _, addLine := range strings.Split(strings.TrimRight(newRoutes, "\n"), "\n") {
			b.WriteString("+ ")
			b.WriteString(addLine)
			b.WriteString("\n")
		}
	}

	return b.String()
}
