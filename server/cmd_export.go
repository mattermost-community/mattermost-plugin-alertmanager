package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	amconfig "github.com/prometheus/alertmanager/config"
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

	// --format=standard (default, alertmanager.yml) | crd (AlertmanagerConfig).
	format, rest := extractFlagValue(rest, "--format=")
	if format == "" {
		format = formatStandard
	}
	// --namespace= applies to CRD output only; defaults to monitoring.
	namespace, rest := extractFlagValue(rest, "--namespace=")
	if namespace == "" {
		namespace = defaultCRDNamespace
	} else if err := validateCRDNamespace(namespace); err != nil {
		return fmt.Sprintf(":warning: Invalid `--namespace`: %v", err), nil
	}

	diffMode := containsFlag(rest, "--diff-against-loaded")

	// --diff-against-loaded stays sysadmin-only. Output is channel-scoped,
	// but it still prints this channel's webhook URLs (bearer tokens)
	// taken from Alertmanager's loaded config. Plain export stays
	// team_admin: it only emits the channel-scoped receivers the caller
	// already manages.
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

	switch format {
	case formatStandard:
		// fall through to the file-format path below
	case formatCRD:
		return p.handleExportCRD(args, scoped, namespace)
	default:
		return fmt.Sprintf(":warning: Unknown `--format=%s`. Use `standard` (alertmanager.yml, default) or `crd` (Prometheus Operator AlertmanagerConfig).", format), nil
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
		y.WriteString(renderReceiverYAMLForKind(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL, ac.Custom))
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
	b.WriteString("\n:information_source: _Template re-rendered at export time — may differ from what's currently loaded in Alertmanager if templates changed since these receivers were created. Run `/alertmanager validate` to confirm the live AM state._\n")

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
// channel-scoped diff showing only this channel's section.
//
// Auto-detects Prometheus Operator CRD mode: if AM's loaded config
// contains receivers with namespaced names (<ns>/mattermost-alertmanager-
// <hash>/<receiver-name>) matching any of this channel's receivers, CRD
// mode activates and the "already loaded" check matches on the third
// path segment instead of the full name.
//
// Sysadmin-only: the diff includes webhook URLs (channel-bound bearer
// tokens) for this channel's receivers.
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

		// Build the set of this channel's receiver names for CRD
		// detection, filtering, and route extraction.
		ownNames := make(map[string]bool, len(g.Receivers))
		for _, ac := range g.Receivers {
			ownNames[ac.Name] = true
		}

		// Auto-detect Prometheus Operator CRD mode: the Operator namespaces
		// receiver names as <ns>/<alertmanagerconfig-name>/<receiver-name>.
		// If any of this channel's receivers appear in that namespaced form,
		// switch to CRD-aware matching for toAdd detection and route extraction.
		crdMode := detectCRDMode(entry.ConfigBody, ownNames)
		if crdMode {
			diffOutput.WriteString("Detection: **CRD mode** (Prometheus Operator — channel receivers found as `<namespace>/mattermost-alertmanager-.../<name>`)\n\n")
		}

		// Identify additions using CRD-aware name matching.
		var toAdd []alertConfig
		var alreadyLoaded []string
		for _, ac := range g.Receivers {
			if receiverLoadedInAM(entry.ConfigBody, ac.Name, crdMode) {
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
			// Show what AM currently has for this channel (channel-scoped).
			diffOutput.WriteString(buildChannelScopedDiff(entry.ConfigBody, ownNames, crdMode, "", ""))
			diffOutput.WriteString("No additions needed for this backend.\n\n")
			continue
		}

		// Render the receivers + routes that would be added.
		var newRecvs, newRoutes strings.Builder
		for _, ac := range toAdd {
			newRecvs.WriteString(renderReceiverYAMLForKind(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL, ac.Custom))
			newRecvs.WriteString("\n")
		}
		newRoutes.WriteString(assembleRoutesYAML(toAdd))

		// Validation: run on the flat-merged config. In CRD mode this is
		// advisory — the actual apply path is kubectl, not paste — but a
		// schema error still signals a real problem with the receiver/route
		// structure regardless of deployment method.
		_, mergedYAML := buildDiffAgainstLoaded(entry.ConfigBody, newRecvs.String(), newRoutes.String())
		if err := validateMergedConfig(mergedYAML); err != nil {
			fmt.Fprintf(&diffOutput, "Validation: :x: merged config would NOT load.\n  Error: %s\n", err.Error())
			if crdMode {
				diffOutput.WriteString("  (CRD mode: this validates the flat equivalent — apply the AlertmanagerConfig via kubectl for your actual deployment.)\n")
			}
			diffOutput.WriteString("  Do NOT paste this into alertmanager.yml as-is — fix the underlying issue first.\n\n")
		} else {
			if crdMode {
				diffOutput.WriteString("Validation: :white_check_mark: flat-equivalent config parses cleanly. For CRD deployments, apply via `kubectl apply` — use `/alertmanager show export --format=crd` to get the AlertmanagerConfig YAML.\n\n")
			} else {
				diffOutput.WriteString("Validation: :white_check_mark: merged config parses cleanly via alertmanager/config. Safe to paste.\n\n")
			}
		}

		// Channel-scoped diff: only this channel's section. Context lines
		// show what AM currently has for this channel; + lines are additions.
		// Own-channel webhook URLs are shown in the clear — the sysadmin
		// needs them to verify correctness. No other channels' data appears.
		channelDiff := buildChannelScopedDiff(entry.ConfigBody, ownNames, crdMode, newRecvs.String(), newRoutes.String())
		diffOutput.WriteString(channelDiff)
		diffOutput.WriteString("\n")
	}

	// DM the channel-scoped diff as a file.
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
		Message:   fmt.Sprintf("Channel-scoped diff against loaded Alertmanager config (channel: %s).\n\nOnly this channel's receivers and routes are shown — no other channels' data.", args.ChannelId),
		FileIds:   []string{info.Id},
	}
	if _, postErr := p.API.CreatePost(post); postErr != nil {
		return fmt.Sprintf(":warning: Couldn't post DM with diff attachment: %v", postErr), nil
	}

	summary.WriteString(fmt.Sprintf("Total additions across all backends: **%d**\n\n", totalAdds))
	summary.WriteString(fmt.Sprintf("Channel-scoped diff DM'd as `alertmanager-diff.txt` from `@%s`. Open the DM to download.\n\n", webhookUsername))
	summary.WriteString(":information_source: Output is scoped to this channel only — context lines show what AM currently has for this channel; `+` lines show what would be added. Schema validation runs against the flat-merge in-memory regardless of deployment mode.")
	return summary.String(), nil
}

// detectCRDMode returns true if AM's loaded config contains any receiver
// whose name follows the Prometheus Operator CRD naming pattern
// <namespace>/<alertmanagerconfig-name>/<receiver-name> where the third
// path segment matches a receiver name owned by this channel.
func detectCRDMode(loadedYAML string, ownNames map[string]bool) bool {
	for line := range strings.SplitSeq(loadedYAML, "\n") {
		m := receiverNameLineRegex.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := strings.Trim(m[1], `"'`)
		parts := strings.SplitN(name, "/", 3)
		if len(parts) == 3 && ownNames[parts[2]] {
			return true
		}
	}
	return false
}

// receiverLoadedInAM checks whether a receiver name is present in AM's loaded
// config. Match the full name on each `- name:` line. A substring check treats
// a shorter channel as already loaded when a longer one exists
// (x--sre-alerts vs x--sre-alerts-prod), and the diff then drops the addition.
// In CRD mode it also matches the namespaced form by comparing only the third
// slash-delimited segment (<ns>/<config-name>/<receiver-name>).
func receiverLoadedInAM(loadedYAML, name string, crdMode bool) bool {
	for line := range strings.SplitSeq(loadedYAML, "\n") {
		m := receiverNameLineRegex.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		loadedName := strings.Trim(m[1], `"'`)
		if loadedName == name {
			return true
		}
		if !crdMode {
			continue
		}
		parts := strings.SplitN(loadedName, "/", 3)
		if len(parts) == 3 && parts[2] == name {
			return true
		}
	}
	return false
}

// extractReceiverBlocks parses the receivers: section of loadedYAML and
// returns the raw YAML lines (bare list items, no receivers: header) for
// every receiver whose name passes the keep function. Multi-line receiver
// blocks (with nested slack_configs, etc.) are extracted intact.
func extractReceiverBlocks(loadedYAML string, keep func(name string) bool) string {
	lines := strings.Split(loadedYAML, "\n")
	inReceivers := false
	var currentBlock []string
	currentName := ""
	dashIndent := -1
	var result []string

	flush := func() {
		if currentName != "" && keep(currentName) && len(currentBlock) > 0 {
			result = append(result, currentBlock...)
		}
		currentBlock = nil
		currentName = ""
	}

	for _, line := range lines {
		body := strings.TrimLeft(line, " \t")
		indent := len(line) - len(body)
		trimmed := strings.TrimRight(line, " \t")

		if !inReceivers {
			if strings.HasPrefix(trimmed, "receivers:") {
				inReceivers = true
			}
			continue
		}

		// Exit on any non-indented, non-sequence, non-comment line other
		// than "receivers:" itself. In standard AM configs the receivers:
		// block runs to the next top-level key (route:, global:, etc.).
		if indent == 0 && body != "" && !strings.HasPrefix(body, "- ") && !strings.HasPrefix(body, "#") && !strings.HasPrefix(body, "receivers:") {
			flush()
			inReceivers = false
			continue
		}

		// New receiver list item detected by the "- name:" pattern.
		// Only treat it as a new item if it's at the same indent as prior
		// receiver items — guards against nested "- name:" in sub-configs.
		if m := receiverNameLineRegex.FindStringSubmatch(line); m != nil {
			if dashIndent == -1 {
				dashIndent = indent
			}
			if indent == dashIndent {
				flush()
				currentName = strings.Trim(m[1], `"'`)
				currentBlock = []string{line}
				continue
			}
		}

		if currentName != "" {
			currentBlock = append(currentBlock, line)
		}
	}
	flush()

	if len(result) == 0 {
		return ""
	}
	return strings.Join(result, "\n")
}

// extractRouteItems returns the raw YAML lines for every top-level route item
// in loadedYAML whose receiver: field matches a receiver in ownNames.
// In CRD mode, matches on the third slash-segment of namespaced receiver names.
// Each returned item includes its full multi-line block (matchers, receiver,
// continue, etc.) but not nested sub-route blocks from other channels.
func extractRouteItems(loadedYAML string, ownNames map[string]bool, crdMode bool) []string {
	lines := strings.Split(loadedYAML, "\n")
	inRoute := false
	inRoutes := false
	routesIndent := -1
	itemIndent := -1
	var currentItem []string
	var result []string

	routeOwned := func(item []string) bool {
		for _, l := range item {
			body := strings.TrimSpace(l)
			if !strings.HasPrefix(body, "receiver:") {
				continue
			}
			rName := strings.TrimSpace(strings.TrimPrefix(body, "receiver:"))
			rName = strings.Trim(rName, `"'`)
			if ownNames[rName] {
				return true
			}
			if crdMode {
				parts := strings.SplitN(rName, "/", 3)
				if len(parts) == 3 && ownNames[parts[2]] {
					return true
				}
			}
		}
		return false
	}

	flush := func() {
		if len(currentItem) > 0 && routeOwned(currentItem) {
			result = append(result, currentItem...)
		}
		currentItem = nil
	}

	for _, line := range lines {
		body := strings.TrimLeft(line, " \t")
		indent := len(line) - len(body)
		trimmed := strings.TrimRight(line, " \t")

		if !inRoute {
			if strings.HasPrefix(trimmed, "route:") {
				inRoute = true
			}
			continue
		}
		if !inRoutes {
			// Only capture the FIRST routes: under route: (same guard as
			// buildDiffAgainstLoaded — prevents nested sub-route blocks from
			// overwriting routesIndent).
			if inRoute && strings.HasPrefix(body, "routes:") {
				inRoutes = true
				routesIndent = indent
			}
			continue
		}

		// Exit routes block at a non-sequence, non-comment sibling of routes:.
		if len(trimmed) > 0 && indent <= routesIndent && !strings.HasPrefix(body, "- ") && !strings.HasPrefix(body, "#") {
			flush()
			break
		}

		// New route item: "- " at the item indent level.
		if strings.HasPrefix(body, "- ") {
			if itemIndent == -1 {
				itemIndent = indent
			}
			if indent == itemIndent {
				flush()
				currentItem = []string{line}
				continue
			}
		}

		if len(currentItem) > 0 {
			currentItem = append(currentItem, line)
		}
	}
	flush()

	return result
}

// buildChannelScopedDiff produces a diff showing only this channel's section:
// currently-loaded receivers/routes as context lines (  prefix), new
// additions as (+ prefix). Unlike buildDiffAgainstLoaded which shows the full
// AM config as context, this is scoped to the calling channel only.
func buildChannelScopedDiff(loadedYAML string, ownNames map[string]bool, crdMode bool, newReceivers, newRoutes string) string {
	keep := func(name string) bool {
		if ownNames[name] {
			return true
		}
		if crdMode {
			parts := strings.SplitN(name, "/", 3)
			return len(parts) == 3 && ownNames[parts[2]]
		}
		return false
	}

	loadedRecv := extractReceiverBlocks(loadedYAML, keep)
	loadedRouteLines := extractRouteItems(loadedYAML, ownNames, crdMode)

	var b strings.Builder

	b.WriteString("--- receivers (this channel) ---\n")
	if loadedRecv != "" {
		for line := range strings.SplitSeq(strings.TrimRight(loadedRecv, "\n"), "\n") {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if newReceivers != "" {
		b.WriteString("+ # ---- plugin additions: receivers ----\n")
		for addLine := range strings.SplitSeq(strings.TrimRight(newReceivers, "\n"), "\n") {
			b.WriteString("+ ")
			b.WriteString(addLine)
			b.WriteString("\n")
		}
	}
	if loadedRecv == "" && newReceivers == "" {
		b.WriteString("  (none)\n")
	}

	b.WriteString("\n--- routes (this channel) ---\n")
	if len(loadedRouteLines) > 0 {
		for _, line := range loadedRouteLines {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	newRoutesBody := routesBodyOnly(newRoutes)
	if newRoutesBody != "" {
		b.WriteString("+ # ---- plugin additions: routes ----\n")
		for addLine := range strings.SplitSeq(strings.TrimRight(newRoutesBody, "\n"), "\n") {
			b.WriteString("+ ")
			b.WriteString(addLine)
			b.WriteString("\n")
		}
	}
	if len(loadedRouteLines) == 0 && newRoutesBody == "" {
		b.WriteString("  (none)\n")
	}

	return b.String()
}

// buildDiffAgainstLoaded produces unified-diff-style output showing
// what would be inserted into the AM-loaded YAML AND emits the
// merged YAML as a plain (un-prefixed) string for downstream
// validation. Not a strict diff algorithm — assumes pure-addition
// semantics (which the plugin's additions always are) and
// identifies the insertion points by finding top-level `receivers:`
// and `route.routes:` keys.
//
// diffDisplay: each line of the loaded YAML prefixed with `  `
// (two-space context convention), each inserted line prefixed with
// `+ ` (unified-diff addition convention). Readable with git-diff
// style or any text editor.
//
// mergedYAML: same content as diffDisplay minus the prefixes — the
// actual YAML that would result from pasting the diff's additions
// into the AM config. Used by validateMergedConfig downstream so we
// can tell the operator whether the paste would parse cleanly.
//
// Behavior when the insertion points aren't found (malformed YAML,
// unusual structure): append at end with a comment explaining the
// fallback. The operator can still see what would be added and
// apply the merge manually; validation may still pass if the
// surrounding YAML is well-formed.
func buildDiffAgainstLoaded(loadedYAML, newReceivers, newRoutes string) (diffDisplay, mergedYAML string) {
	var b, m strings.Builder

	// Strip the "routes:" header key and comment preamble that
	// assembleRoutesYAML emits. In the diff context we insert items
	// INTO an existing route.routes: block, not start a new parallel
	// one — including the key would create a duplicate routes: mapping
	// entry inside route:, which the YAML parser rejects.
	newRoutes = routesBodyOnly(newRoutes)

	loadedLines := strings.Split(loadedYAML, "\n")

	// First pass: emit loaded lines, marking where we'll insert.
	// We find the END of the receivers: block (first top-level key
	// after `receivers:` OR EOF) and the END of the route.routes:
	// block (first sibling key at routesIndent OR shallower, after
	// all sequence items have been consumed).
	receiversEndIdx := -1
	routesEndIdx := -1
	inReceivers := false
	inRoute := false
	inRoutes := false
	var routesIndent int

	for i, line := range loadedLines {
		trim := strings.TrimRight(line, " \t")
		// Detect transitions out of receivers: when we see a top-level
		// key after `receivers:`.
		if inReceivers && len(trim) > 0 && trim[0] != ' ' && trim[0] != '\t' && trim[0] != '#' && !strings.HasPrefix(trim, "receivers:") {
			receiversEndIdx = i
			inReceivers = false
		}
		// Detect transitions out of routes: when we see a line at or
		// shallower than routesIndent that isn't a sequence item ("- ")
		// or comment. This finds the END of the list, not the start.
		if inRoutes && len(trim) > 0 {
			lineTrimmed := strings.TrimLeft(line, " \t")
			lineIndent := len(line) - len(lineTrimmed)
			if lineIndent <= routesIndent && !strings.HasPrefix(lineTrimmed, "- ") && !strings.HasPrefix(lineTrimmed, "#") {
				routesEndIdx = i
				inRoutes = false
				inRoute = false
			}
		}

		if strings.HasPrefix(trim, "receivers:") {
			inReceivers = true
		}
		if strings.HasPrefix(trim, "route:") {
			inRoute = true
		}
		// Only capture the FIRST routes: key seen under route: — nested
		// route items can also have their own routes: sub-blocks, and
		// re-entering here would overwrite routesIndent with the wrong
		// (deeper) indent level, causing insertion inside a sub-route
		// instead of at the top-level route.routes: list.
		if inRoute && !inRoutes && strings.HasPrefix(strings.TrimLeft(line, " \t"), "routes:") {
			inRoutes = true
			routesIndent = len(line) - len(strings.TrimLeft(line, " \t"))
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
	// and no-block-found fallback). Each emit writes to BOTH the
	// diff display (with `+ ` markers + comment headers) and the
	// merged YAML (plain text, ready for validation/paste).
	emitReceivers := func() {
		b.WriteString("+ # ---- plugin additions: receivers ----\n")
		for addLine := range strings.SplitSeq(strings.TrimRight(newReceivers, "\n"), "\n") {
			b.WriteString("+ ")
			b.WriteString(addLine)
			b.WriteString("\n")
			m.WriteString(addLine)
			m.WriteString("\n")
		}
	}
	emitRoutes := func() {
		b.WriteString("+ # ---- plugin additions: routes ----\n")
		for addLine := range strings.SplitSeq(strings.TrimRight(newRoutes, "\n"), "\n") {
			b.WriteString("+ ")
			b.WriteString(addLine)
			b.WriteString("\n")
			m.WriteString(addLine)
			m.WriteString("\n")
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
			m.WriteString(loadedLines[i])
			m.WriteString("\n")
		}
	}

	// Fallback: if insertion points weren't found at all (malformed
	// YAML, unusual layout, partial config), append at the end of
	// the output with a NOTE so the operator knows it wasn't a
	// clean splice. We still append to the merged YAML so validation
	// runs over the same content the operator would see.
	if receiversEndIdx == -1 && newReceivers != "" {
		b.WriteString("\n+ # ---- plugin additions: receivers ----\n")
		b.WriteString("+ # NOTE: couldn't find `receivers:` block — merge these manually under it.\n")
		for addLine := range strings.SplitSeq(strings.TrimRight(newReceivers, "\n"), "\n") {
			b.WriteString("+ ")
			b.WriteString(addLine)
			b.WriteString("\n")
			m.WriteString(addLine)
			m.WriteString("\n")
		}
	}
	if routesEndIdx == -1 && newRoutes != "" {
		b.WriteString("\n+ # ---- plugin additions: routes ----\n")
		b.WriteString("+ # NOTE: couldn't find `route.routes:` block — merge these manually under it.\n")
		for addLine := range strings.SplitSeq(strings.TrimRight(newRoutes, "\n"), "\n") {
			b.WriteString("+ ")
			b.WriteString(addLine)
			b.WriteString("\n")
			m.WriteString(addLine)
			m.WriteString("\n")
		}
	}

	return b.String(), m.String()
}

// routesBodyOnly strips the comment preamble and "routes:" header key from
// assembleRoutesYAML output, returning only the bare YAML sequence items.
// buildDiffAgainstLoaded uses this because it inserts items INTO an existing
// route.routes: block — not alongside it — so the "routes:" key must not
// appear in the inserted content.
func routesBodyOnly(routesYAML string) string {
	if routesYAML == "" {
		return ""
	}
	lines := strings.Split(routesYAML, "\n")
	var out []string
	inBody := false
	for _, line := range lines {
		if !inBody {
			if strings.HasPrefix(strings.TrimLeft(line, " \t"), "- ") {
				inBody = true
				out = append(out, line)
			}
			// Skip comments, blank lines, and the "routes:" header.
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// redactSensitiveLineRegex matches a YAML line setting one of the
// sensitive AM-config keys to a quoted-or-unquoted scalar. Captures
// the leading whitespace + key + ":" so we can substitute just the
// value while preserving indentation.
//
// Limited to keys that actually carry secret material in alertmanager
// configs — api_url (webhook bearer tokens), password (basic auth),
// service_key + routing_key + integration_url (PagerDuty / Opsgenie
// / generic webhook). Adding more is cheap; the conservative default
// avoids redacting keys that look secret but aren't.
// redactSensitiveLineRegex matches a YAML scalar line whose KEY looks
// secret-bearing, case-insensitively and by substring so it catches the many
// Alertmanager variants (F-004): password / auth_password / smtp_auth_password,
// secret / client_secret, *_token / bot_token / bearer_token, api_key,
// credentials (incl. authorization.credentials), service_key, routing_key — plus
// ANY key ending in `url` (url, api_url, slack_api_url, webhook_url, integration_url,
// and proxy_url, which can embed proxyuser:proxypass@). Over-matching a non-secret
// URL in the diff is acceptable; leaking a real secret is not.
var redactSensitiveLineRegex = regexp.MustCompile(`(?i)^(\s+(?:-\s+)?(?:[a-z0-9_]*(?:password|passwd|secret|token|api[_-]?key|credential|service_key|routing_key)[a-z0-9_]*|[a-z0-9_]*url):\s+).+$`)

// redactFlowStyleValueRegex catches a sensitive key/value pair embedded in
// FLOW-style YAML — inside { } or [ ] — which the line-anchored block-style regex
// above cannot see because the key isn't at the start of the line. Alertmanager
// accepts flow-style, so a user's loaded alertmanager.yml can carry secrets this
// way, e.g. `webhook_configs: [{url: "https://…/hooks/secret"}]` or
// `http_config: {proxy_url: "http://user:pass@proxy:8080"}`. Same key set as the
// block regex; ReplaceAll masks EVERY pair on the line. Group 1 is the retained
// `key:` prefix, group 2 is the value (double-quoted, single-quoted, or an
// unquoted run up to the next `,`/`}`/`]`/space) that gets masked.
var redactFlowStyleValueRegex = regexp.MustCompile(`(?i)((?:[a-z0-9_]*(?:password|passwd|secret|token|api[_-]?key|credential|service_key|routing_key)[a-z0-9_]*|[a-z0-9_]*url)\s*:\s*)("(?:[^"\\]|\\.)*"|'[^']*'|[^,}\]\s]+)`)

// receiverNameLineRegex captures the receiver name on a `- name: foo`
// list entry. \s* (not \s+) allows matching at indent-0 for raw AM YAML
// as well as diff-prefixed lines (which always have at least 2 chars of
// prefix stripped before the body is matched). Used by detectCRDMode,
// extractReceiverBlocks, and redactOtherChannelsInDiff.
var receiverNameLineRegex = regexp.MustCompile(`^\s*-\s+name:\s+([^\s]+)`)

// redactOtherChannelsInDiff masks sensitive values (api_url, password,
// vendor-specific keys) in the diff DISPLAY for receivers that don't
// belong to the calling channel. The validation step has already run
// on the un-redacted in-memory merge, so the security promise here is
// purely about what lands in the operator's DM: only their channel's
// receivers' secrets are exposed; everyone else's get `<REDACTED>`.
//
// `+ ` lines (additions from THIS channel) never get redacted — those
// are the URLs the operator needs to paste into alertmanager.yml.
//
// `  ` lines (context, copied from AM's loaded YAML) get redacted
// when they sit inside a receiver block whose name isn't in the
// caller's set. Lines outside receiver blocks (global config, route
// tree, etc.) pass through unchanged because they don't typically
// carry secrets.
// keyColumn returns the column at which the mapping key begins on a YAML line
// body: leading spaces plus an optional "- " sequence indicator. A block
// scalar's content is indented deeper than this, so it bounds the redaction of
// a `key: |` block's continuation lines.
func keyColumn(body string) int {
	lead := len(body) - len(strings.TrimLeft(body, " "))
	rest := body[lead:]
	if strings.HasPrefix(rest, "-") {
		afterDash := strings.TrimLeft(rest[1:], " ")
		lead += 1 + (len(rest[1:]) - len(afterDash))
	}
	return lead
}

func redactOtherChannelsInDiff(diffDisplay string, ownReceiverNames map[string]bool) string {
	lines := strings.Split(diffDisplay, "\n")
	out := make([]string, 0, len(lines))

	inReceiversBlock := false
	currentReceiver := ""
	// blockScalarKeyCol >= 0 means we're inside a redacted block scalar (`key: |`
	// or `key: >`) whose key sat at that column; more-indented following lines are
	// the block's (secret) content and must be redacted too (F-003).
	blockScalarKeyCol := -1

	for _, line := range lines {
		if len(line) < 2 {
			out = append(out, line) // blank/short: stays part of any open block scalar
			continue
		}
		prefix := line[:2]
		body := line[2:]
		bodyIndent := len(body) - len(strings.TrimLeft(body, " "))
		bodyTrim := strings.TrimSpace(body)

		// If we're inside a redacted block scalar, redact its content lines (those
		// indented deeper than the key) and only exit when indentation dedents back
		// to the key column or shallower.
		if blockScalarKeyCol >= 0 {
			if bodyTrim == "" {
				out = append(out, line) // blank line inside the block — no secret
				continue
			}
			if bodyIndent > blockScalarKeyCol {
				out = append(out, prefix+strings.Repeat(" ", bodyIndent)+"<REDACTED>")
				continue
			}
			blockScalarKeyCol = -1 // dedented out of the block; fall through
		}

		// Track entry into / exit from the `receivers:` block. We
		// only consider TOP-LEVEL `receivers:` — nested mentions in
		// comments or doc strings don't shift state.
		if bodyTrim == "receivers:" {
			inReceiversBlock = true
			currentReceiver = ""
		} else if len(bodyTrim) > 0 && len(body) > 0 && body[0] != ' ' && body[0] != '#' && !strings.HasPrefix(bodyTrim, "receivers:") {
			inReceiversBlock = false
			currentReceiver = ""
		}

		// Inside the receivers block, watch for `- name: <X>` markers
		// to know which receiver we're currently rendering.
		if inReceiversBlock {
			if m := receiverNameLineRegex.FindStringSubmatch(body); m != nil {
				currentReceiver = strings.Trim(m[1], `"'`)
			}
		}

		// Redact a sensitive line EVERYWHERE except:
		//   - additions (`+ `): the caller's own proposed config — they need the
		//     api_url to paste, so it's shown intentionally.
		//   - context inside the caller's OWN receiver block: their own secrets.
		// So global/top-level secrets (e.g. global.smtp_auth_password,
		// global.slack_api_url) and other channels' receiver secrets are all
		// redacted from the DM'd diff, not just non-owned receiver blocks (F-004).
		isAddition := prefix == "+ "
		inOwnReceiver := inReceiversBlock && currentReceiver != "" && ownReceiverNames[currentReceiver]
		if !isAddition && !inOwnReceiver {
			if m := redactSensitiveLineRegex.FindStringSubmatch(body); m != nil {
				value := strings.TrimSpace(body[len(m[1]):])
				line = prefix + m[1] + "<REDACTED>"
				// If the value is a YAML block scalar (| or >, with optional
				// chomping/indent indicators), redact the following indented
				// content lines too (F-003) — otherwise the secret sits on the
				// continuation lines, unmasked.
				if strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">") {
					blockScalarKeyCol = keyColumn(body)
				}
			} else if redactFlowStyleValueRegex.MatchString(body) {
				// No block-style match, but the line carries a sensitive key/value
				// pair in flow-style YAML (inside { } / [ ]). Mask every such value
				// on the line so flow-style configs don't leak secrets the anchored
				// block regex can't see.
				line = prefix + redactFlowStyleValueRegex.ReplaceAllString(body, "${1}<REDACTED>")
			}
		}
		out = append(out, line)
	}

	return strings.Join(out, "\n")
}

// validateMergedConfig runs the merged YAML through the official
// Alertmanager config parser to confirm the paste would actually
// load. Catches schema errors (undefined receiver references,
// malformed matchers, route tree issues) that a textual splice
// could happily produce but AM would reject at reload time.
//
// Returns nil on success. On failure, returns a user-facing error
// the operator can act on (typically the exact error AM would print
// during config reload). Empty input is treated as no-op: we can't
// validate "no additions" against AM, so skip rather than mislead.
func validateMergedConfig(mergedYAML string) error {
	if strings.TrimSpace(mergedYAML) == "" {
		return nil
	}
	_, err := amconfig.Load(mergedYAML)
	return err
}
