package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	amconfig "github.com/prometheus/alertmanager/config"
)

// exportOptions are the parsed words of `/alertmanager format export`.
type exportOptions struct {
	format    string // "" when not given
	namespace string // CRD output only, validated
}

// diffOptions are the parsed words of `/alertmanager format diff`.
type diffOptions struct {
	format string // "" detects the deployment form from the loaded config
}

// handleExport renders fresh YAML for every receiver bound to the
// current channel and DMs the resulting receivers.yml to the calling
// sysadmin. Used to apply template/URL changes from a plugin upgrade
// to existing receivers without rotating their webhook IDs (which
// /alertmanager config remove all + add would do).
//
// Sysadmin-gated because the rendered YAML embeds webhook URLs
// (channel-bound bearer tokens). Channel-scoped: only receivers bound
// to the invocation channel get exported.
//
// Usage:
//
//	/alertmanager format export [standard|crd] [namespace=<ns>]
//	/alertmanager format diff [standard|crd]       # sysadmin-only
//
// diff shifts the output to a side-by-side comparison between the
// AM-loaded config (fetched live via /api/v2/status) and what the plugin
// would emit. Useful as the answer to "I have some receivers in AM YAML by
// hand, what would the plugin's additions look like merged in?" — without
// the operator having to guess the merge points themselves.
//
// Output: an in-channel ephemeral summary, plus DMs from @alertmanagerbot
// with the assembled receivers.yml (or alertmanager-diff.txt for diff) and
// the PrometheusRule for the runbooks those receivers cover.
func (p *Plugin) handleExport(args *model.CommandArgs, opts exportOptions) (string, error) {
	format := opts.format
	if format == "" {
		format = formatStandard
	}
	namespace := opts.namespace
	if namespace == "" {
		namespace = defaultCRDNamespace
	}

	scoped := p.configsForCurrentChannel(args)
	if len(scoped) == 0 {
		return ":information_source: No receivers bound to this channel — nothing to export.", nil
	}

	if format == formatCRD {
		return p.handleExportCRD(args, scoped, namespace)
	}

	// Assemble the YAML for every channel-scoped receiver. Same
	// concatenation pattern as the /alertmanager config add file, but iterates
	// over existing entries rather than newly-created ones.
	var y strings.Builder
	y.WriteString("# Alertmanager receivers exported by /alertmanager format export\n")
	y.WriteString("# All receivers currently registered in plugin config for this channel.\n")
	y.WriteString("# Hook IDs are preserved — replace the receivers: block in your\n")
	y.WriteString("# alertmanager.yml with this content, then reload AM.\n")
	y.WriteString(fmt.Sprintf("# Receivers: %d\n", len(scoped)))
	y.WriteString("\n")
	for _, ac := range scoped {
		y.WriteString(renderReceiverYAMLForKind(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL, ac.resolvedKind()))
		y.WriteString("\n")
	}

	// DM both the receivers AND the matching routes as separate files.
	// Same delivery path as /alertmanager config add — ephemeral file
	// attachments are unreliable in Mattermost, DMs persist normally.
	routesYAML := assembleRoutesYAML(scoped)
	rules, rulesReason := p.rulesFileForChannel(rulesContext{namespace: namespace, format: formatStandard, allowlist: true}, scoped)
	dmErr := p.dmYAMLBundle(args.UserId, y.String(), routesYAML, len(scoped), "", rules)
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
	b.WriteString("\n:information_source: _Template re-rendered at export time — may differ from what's currently loaded in Alertmanager if templates changed since these receivers were created. Run `/alertmanager config validate` to confirm the live AM state._\n")

	b.WriteString(rulesReply(rules, rulesReason, true))

	// Discoverability hint for diff. Sysadmin-only feature,
	// so the tip is conditional on the calling user actually being
	// a sysadmin — no point teasing a command the caller can't run.
	if p.requireSystemAdmin(args.UserId) == nil {
		b.WriteString("\n")
		b.WriteString(":bulb: **Sysadmin tip:** run `/alertmanager format diff` to compare Alertmanager's loaded config with what this channel would add.\n")
	}
	return b.String(), nil
}

// handleDiff is `format diff`, which the registry limits to system admins: the
// output is channel-scoped, but it prints this channel's webhook URLs (bearer
// tokens) taken from Alertmanager's loaded config.
func (p *Plugin) handleDiff(args *model.CommandArgs, opts diffOptions) (string, error) {
	scoped := p.configsForCurrentChannel(args)
	if len(scoped) == 0 {
		return ":information_source: No receivers bound to this channel — nothing to export.", nil
	}
	return p.handleExportDiff(args, scoped, opts.format)
}

// handleExportDiff is the format diff path. Fetches AM's
// currently-loaded config via /api/v2/status, identifies which of
// the channel's receivers are new vs already present, and renders a
// channel-scoped diff showing only this channel's section.
//
// With no format, auto-detects Prometheus Operator CRD mode: if AM's loaded
// config contains receivers with namespaced names (<ns>/mattermost-
// alertmanager-<hash>/<receiver-name>) matching any of this channel's
// receivers, CRD mode activates and the "already loaded" check matches on the
// third path segment instead of the full name. An explicit format ("crd" or
// "standard") skips detection and compares against that deployment form.
//
// Sysadmin-only: the diff includes webhook URLs (channel-bound bearer
// tokens) for this channel's receivers.
func (p *Plugin) handleExportDiff(args *model.CommandArgs, scoped []alertConfig, format string) (string, error) {
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
		switch format {
		case formatCRD:
			crdMode = true
			diffOutput.WriteString("Format: **crd** (requested). Matching receivers as `<namespace>/mattermost-alertmanager-.../<name>`.\n\n")
		case formatStandard:
			crdMode = false
			diffOutput.WriteString("Format: **standard** (requested). Matching receivers by their plain name.\n\n")
		default:
			if crdMode {
				diffOutput.WriteString("Detection: **CRD mode** (Prometheus Operator — channel receivers found as `<namespace>/mattermost-alertmanager-.../<name>`)\n\n")
			}
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
			newRecvs.WriteString(renderReceiverYAMLForKind(ac.Name, p.webhookURLForReceiver(ac), ac.Channel, p.runbookDefaultURL(receiverBaseSlug(ac.Name)), p.siteURL()+webhookIconURL, ac.resolvedKind()))
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
				diffOutput.WriteString("Validation: :white_check_mark: flat-equivalent config parses cleanly. For CRD deployments, apply via `kubectl apply` — use `/alertmanager format export crd` to get the AlertmanagerConfig YAML.\n\n")
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

// receiverNameLineRegex captures the receiver name on a `- name: foo`
// list entry. \s* (not \s+) allows matching at indent-0 for raw AM YAML
// as well as diff-prefixed lines (which always have at least 2 chars of
// prefix stripped before the body is matched). Used by detectCRDMode,
// receiverLoadedInAM and extractReceiverBlocks.
var receiverNameLineRegex = regexp.MustCompile(`^\s*-\s+name:\s+([^\s]+)`)

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
