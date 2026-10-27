package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi/experimental/command"
)

const (
	commandTrigger = "alertmanager"
)

// getCommand returns the model.Command registration. Autocomplete data
// makes the typeahead helpful for sysadmins setting up new receivers.
// The icon shows up in the Mattermost typeahead list, making the
// command visually identifiable alongside other plugin commands.
func (p *Plugin) getCommand() (*model.Command, error) {
	iconData, err := command.GetIconData(p.API, "assets/alertmanager-logo.svg")
	if err != nil {
		// Non-fatal: a missing icon shouldn't block command registration.
		// Log and continue — the command works, just without the icon.
		p.API.LogWarn("failed to load command icon", "err", err.Error())
	}

	return &model.Command{
		Trigger:              commandTrigger,
		AutoComplete:         true,
		AutoCompleteDesc:     "Manage Alertmanager → Mattermost webhook bridges",
		AutoCompleteHint:     "[command]",
		AutocompleteData:     getAutocompleteData(),
		AutocompleteIconData: iconData,
	}, nil
}

func getAutocompleteData() *model.AutocompleteData {
	root := model.NewAutocompleteData(commandTrigger, "[command]", "Manage Alertmanager → Mattermost bridges")

	// A group node holds either subcommands or arguments, never both, so each
	// group below is all subcommands and the leaf nodes carry the arguments.
	// Mattermost lists children in registration order, so sortTypeahead
	// alphabetizes every level before return.
	admin := model.NewAutocompleteData("admin", "[repair|acknowledge|allowlist|metrics-token]", "Sysadmin tools: repair, acknowledge, allowlist, metrics-token")
	alerts := model.NewAutocompleteData("alerts", "[firing|status]", "Firing alerts for this channel, or Alertmanager status")
	config := model.NewAutocompleteData("config", "[list|add|add-custom|validate|rotate|remove]", "This channel's receivers: list, add, validate, rotate, remove")

	format := model.NewAutocompleteData("format", "[export|diff|rules]", "Config and rules for this channel in standard or crd format")

	for _, group := range []*model.AutocompleteData{admin, alerts, config, format} {
		group.AddCommand(model.NewAutocompleteData("help", "", "Show this command's subcommands and examples"))
	}

	root.AddCommand(model.NewAutocompleteData("about", "", "Plugin build info, configured settings, and links"))
	root.AddCommand(admin)
	root.AddCommand(alerts)
	root.AddCommand(config)
	root.AddCommand(format)

	alerts.AddCommand(model.NewAutocompleteData("firing", "", "Firing alerts whose runbook= label matches a receiver in this channel. The default."))
	alerts.AddCommand(model.NewAutocompleteData("status", "", "Alertmanager version and uptime per backend (grouped by Alertmanager URL)"))

	config.AddCommand(model.NewAutocompleteData("list", "", "List receivers bound to this channel. Type a receiver name after config for its detail card."))

	formatItems := []model.AutocompleteListItem{
		{Item: formatStandard, HelpText: "Plain alertmanager.yml receivers and routes. Paste into a file-based Alertmanager (config file, Docker, VM)."},
		{Item: formatCRD, HelpText: "Prometheus Operator objects (an AlertmanagerConfig and a Secret, plus a PrometheusRule for any runbooks) to kubectl apply. Use this with kube-prometheus-stack. namespace= sets where they go, default monitoring."},
	}

	formatExport := model.NewAutocompleteData("export", "[standard|crd]", "DM this channel's receivers and routes, plus the Prometheus rules for their runbooks")
	formatExport.AddStaticListArgument("Format (default standard).", false, formatItems)
	format.AddCommand(formatExport)

	formatDiff := model.NewAutocompleteData("diff", "[standard|crd]", "Diff this channel's Alertmanager config against what Alertmanager loaded (sysadmin)")
	formatDiff.AddStaticListArgument("Format (default: detected from the loaded config).", false, []model.AutocompleteListItem{
		{Item: formatStandard, HelpText: "Match receivers by plain name. For a file-based Alertmanager."},
		{Item: formatCRD, HelpText: "Match receivers as <namespace>/<config>/<name>. For Prometheus Operator."},
	})
	format.AddCommand(formatDiff)

	formatRules := model.NewAutocompleteData("rules", "[standard|crd]", "The sample Prometheus alerting rules only")
	formatRules.AddStaticListArgument("Format (default standard).", false, []model.AutocompleteListItem{
		{Item: formatStandard, HelpText: "Links to view the 37 sample rules in your browser or download them as a plain Prometheus rules file."},
		{Item: formatCRD, HelpText: "DM a ready-to-apply PrometheusRule (monitoring.coreos.com/v1) with all 37 rules, for Prometheus Operator. namespace=<ns> sets where it goes, default monitoring."},
	})
	format.AddCommand(formatRules)

	acknowledge := model.NewAutocompleteData("acknowledge", "[add|remove] [name]", "Hide a receiver from the AM-only inventory warning. Does not mute alerts. Sysadmin.")
	acknowledge.AddStaticListArgument("add or remove one receiver. Omit to list the names already hidden.", false, []model.AutocompleteListItem{
		{Item: "add", HelpText: "Stop listing this receiver as AM-only. Alerts it sends still fire."},
		{Item: "remove", HelpText: "Show the AM-only warning for this receiver again."},
	})
	acknowledge.AddTextArgument("Alertmanager receiver name. A namespace/config/name is stored as the last segment.", "[name]", "")
	admin.AddCommand(acknowledge)

	// add and add-custom bind to the channel the command runs in, so neither
	// takes a team or channel. The Alertmanager URL list is shared.
	amURLItems := []model.AutocompleteListItem{
		{Item: "http://host.docker.internal:9093", HelpText: "Docker Desktop / Compose — MM runs in a container and reaches Alertmanager via the host gateway. Use this, NOT localhost (localhost is the MM container itself)."},
		{Item: "http://alertmanager.monitoring.svc.cluster.local:9093", HelpText: "Kubernetes (self-managed service) — cluster-internal service DNS when you named the Service `alertmanager`. Change 'monitoring' to its namespace."},
		{Item: "http://kube-prometheus-stack-alertmanager.monitoring.svc.cluster.local:9093", HelpText: "kube-prometheus-stack default (most common). Replace `kube-prometheus-stack` with your Helm release name. Change `monitoring` to your namespace. Confirm: `kubectl get svc -A | grep alertmanager`"},
		{Item: "https://alertmanager.example.com", HelpText: "Custom / anything else — your Alertmanager base URL as reachable from the MM server. Replace the host; keep it scheme://host[:port], no trailing slash, no query string or fragment."},
	}
	const amURLPrompt = "Alertmanager base URL, reachable FROM the Mattermost server (no trailing slash). Pick a pattern or type your own."

	add := model.NewAutocompleteData("add", "[am-url] [target] [standard|crd] [on]", "Create receivers in THIS channel for a group set OR individual runbook slug. One shared webhook per group; individual slugs get their own. (sysadmin/team_admin)")
	add.AddStaticListArgument(amURLPrompt, false, amURLItems)
	add.AddStaticListArgument("Group set OR individual runbook slug (defaults to `all`). Type a slug freely; static list shows group sets only.", false, []model.AutocompleteListItem{
		{Item: "all", HelpText: "Every embedded runbook (default — 36 receivers in one shared webhook)"},
		{Item: "application", HelpText: "HTTP error, latency, endpoint, request-rate alerts (4) — one shared webhook"},
		{Item: "compute", HelpText: "CPU, memory, throttling, pod, deployment, node, image-pull, scheduling alerts (9) — one shared webhook"},
		{Item: "database", HelpText: "Connectivity, replication lag, query latency, connection saturation (4) — one shared webhook"},
		{Item: "mattermost", HelpText: "Mattermost-application alerts: API latency, login failures, WebSocket drop, DB slowness, error rate, license expiry (6) — one shared webhook. Requires Mattermost metrics enabled."},
		{Item: "networking", HelpText: "Ingress 5xx, cert expiry, DNS failure (3) — one shared webhook"},
		{Item: "observability", HelpText: "Prometheus scrape down, Alertmanager notify fail (2) — one shared webhook"},
		{Item: "security", HelpText: "Unexpected image, API auth spike, privileged container, shell-in-container, RBAC escalation, security tooling down (6) — one shared webhook"},
		{Item: "storage", HelpText: "PV full, disk fill rate (2) — one shared webhook"},
	})
	add.AddStaticListArgument("Optional: format of the config DM'd to you (default standard)", false, formatItems)
	add.AddStaticListArgument("Optional: enable webhook rotation reminders for these receivers", false, []model.AutocompleteListItem{
		{Item: "on", HelpText: "Remind sysadmins to rotate these webhooks once they are older than System Console → WebhookRotationDays. Without it, these receivers are never reminded. Applies to this channel only."},
	})
	config.AddCommand(add)

	// add-custom: one generic (non-runbook) receiver with a user-chosen name.
	// The final arg is a free-text custom name instead of a runbook target, and
	// no route is auto-generated.
	addCustom := model.NewAutocompleteData("add-custom", "[am-url] [name] [standard|crd] [on]", "Create ONE generic (non-runbook) receiver in THIS channel with a custom name. No auto route — you wire the matcher manually. (sysadmin/team_admin)")
	addCustom.AddStaticListArgument(amURLPrompt, false, amURLItems)
	addCustom.AddTextArgument("Custom receiver name: lowercase [a-z0-9_-], no `--`, not a runbook slug, category set, or standard/crd/on. Full name becomes `<name>--<team>-<channel>` (max 190 chars).", "[name]", "")
	addCustom.AddStaticListArgument("Optional: format of the config DM'd to you (default standard)", false, formatItems)
	addCustom.AddStaticListArgument("Optional: enable webhook rotation reminders for this receiver", false, []model.AutocompleteListItem{
		{Item: "on", HelpText: "Remind sysadmins to rotate this webhook once it is older than System Console → WebhookRotationDays. Without it, this receiver is never reminded."},
	})
	config.AddCommand(addCustom)

	allowlist := model.NewAutocompleteData("allowlist", "[add|remove] [prefix]", "Extra registry prefixes for UnexpectedContainerImage. Included in the next format rules crd file. Sysadmin.")
	allowlist.AddStaticListArgument("add or remove one prefix. Omit to list the shipped prefixes and yours.", false, []model.AutocompleteListItem{
		{Item: "add", HelpText: "Add a registry prefix, for example ghcr.io/myorg/. A missing trailing slash is added."},
		{Item: "remove", HelpText: "Remove a prefix previously added here. The shipped prefixes stay."},
	})
	allowlist.AddTextArgument("Registry prefix. Required after add or remove.", "[prefix]", "")
	admin.AddCommand(allowlist)

	root.AddCommand(model.NewAutocompleteData("docs", "", "Link to the documentation site, runbook library, and sample rules"))

	root.AddCommand(model.NewAutocompleteData("help", "[command]", "Show help. Type help after any command for more detail."))

	metricsToken := model.NewAutocompleteData("metrics-token", "[generate]", "Show status of the Prometheus /metrics bearer token, or `generate` to mint+reveal a new one with a ready-to-paste scrape_config (sysadmin)")
	metricsToken.AddStaticListArgument("Optional: `generate` rotates + reveals a new token; omit to just show status", false, []model.AutocompleteListItem{
		{Item: "generate", HelpText: "Mint a new token (rotates the current one) and reveal it once with a Prometheus scrape_config"},
	})
	admin.AddCommand(metricsToken)

	// First arg is a static list (set names + `all`) for discoverability,
	// but Mattermost autocomplete doesn't enforce — users can also type a
	// receiver name freely for single-receiver removal.
	remove := model.NewAutocompleteData("remove", "[name|set|all] [force]", "Delete one receiver, one set, or all receivers in this channel (sysadmin/team_admin)")
	remove.AddStaticListArgument("Pick a set or `all`, OR type a receiver name", false, []model.AutocompleteListItem{
		{Item: "all", HelpText: "Every receiver in this channel (requires force)"},
		{Item: "application", HelpText: "All application receivers in this channel (requires force)"},
		{Item: "compute", HelpText: "All compute receivers in this channel (requires force)"},
		{Item: "database", HelpText: "All database receivers in this channel (requires force)"},
		{Item: "mattermost", HelpText: "All Mattermost-application receivers in this channel (requires force)"},
		{Item: "networking", HelpText: "All networking receivers in this channel (requires force)"},
		{Item: "observability", HelpText: "All observability receivers in this channel (requires force)"},
		{Item: "security", HelpText: "All security receivers in this channel (requires force)"},
		{Item: "storage", HelpText: "All storage receivers in this channel (requires force)"},
	})
	remove.AddStaticListArgument("Pass `force` to confirm bulk delete", false, []model.AutocompleteListItem{
		{Item: "force", HelpText: "Confirms the bulk delete — without this, set/all targets do a dry-run preview"},
	})
	config.AddCommand(remove)

	repair := model.NewAutocompleteData("repair", "[prune] [force]", "Check the receiver list and preview orphaned receivers. prune removes them, force rewrites a corrupt list.")
	repair.AddStaticListArgument("Optional flag. Bare repair is read-only.", false, []model.AutocompleteListItem{
		{Item: "prune", HelpText: "Remove receivers whose Mattermost webhook was deleted out of band. Also runs every 5 minutes in the background."},
		{Item: "force", HelpText: "Rewrite a corrupt receiver-list KV value from the plugin's last-known in-memory list. Use only when repair reports it unreadable."},
	})
	admin.AddCommand(repair)

	rotate := model.NewAutocompleteData("rotate", "[name|all overdue]", "Recreate webhook with a new hook-id. `all overdue` rotates everything past the threshold set by System Console → WebhookRotationDays.")
	rotate.AddTextArgument("Receiver name to rotate, OR `all` followed by overdue to rotate everything past the rotation threshold in this channel", "[name|all]", "")
	rotate.AddStaticListArgument("Optional flag — only valid after `all`", false, []model.AutocompleteListItem{
		{Item: "overdue", HelpText: "Rotate only receivers past the WebhookRotationDays threshold (System Console setting; default 0 = feature off)"},
	})
	config.AddCommand(rotate)

	silenceCreate := model.NewAutocompleteData("create", "<30m|1h-72h> <alert|group|all>", "Mute alerts in Alertmanager for a TTL. 30m or whole hours. all is 1h max and sysadmin only. An alert or a group is team admin.")
	silenceCreate.AddStaticListArgument("TTL. Required. Other whole hours from 1h to 72h work too. all rejects anything over 1h.", false, []model.AutocompleteListItem{
		{Item: "30m", HelpText: "30 minutes. Valid for an alert, a group, or all."},
		{Item: "1h", HelpText: "1 hour. The longest TTL when the next argument is all."},
		{Item: "4h", HelpText: "4 hours. An alert or a group. Rejected for all."},
		{Item: "24h", HelpText: "24 hours. An alert or a group."},
		{Item: "72h", HelpText: "72 hours. The longest TTL for an alert or a group."},
	})
	silenceCreate.AddStaticListArgument("Group or all. Or type an alert name such as HighCPUUsage.", false, []model.AutocompleteListItem{
		{Item: "all", HelpText: "Every runbook receiver in this channel. System admin only. TTL cannot be longer than 1h."},
		{Item: "application", HelpText: "Application runbooks in this channel. Up to 72h."},
		{Item: "compute", HelpText: "Compute runbooks in this channel. Up to 72h."},
		{Item: "database", HelpText: "Database runbooks in this channel. Up to 72h."},
		{Item: "mattermost", HelpText: "Mattermost runbooks in this channel. Up to 72h."},
		{Item: "networking", HelpText: "Networking runbooks in this channel. Up to 72h."},
		{Item: "observability", HelpText: "Observability runbooks in this channel. Up to 72h."},
		{Item: "security", HelpText: "Security runbooks in this channel. Up to 72h."},
		{Item: "storage", HelpText: "Storage runbooks in this channel. Up to 72h."},
	})
	// create, list, and expire are subcommands because a node cannot mix
	// subcommands and arguments. The server also accepts `silence <ttl> <target>`
	// without `create`, but typeahead only offers the three verbs.
	silenceExpire := model.NewAutocompleteData("expire", "[name] [silence-id]", "Expire an active Alertmanager silence")
	silence := model.NewAutocompleteData("silence", "[create|list|expire]", "Mute alerts, list active silences, or expire one")
	silence.AddCommand(silenceCreate)
	silence.AddCommand(model.NewAutocompleteData("list", "", "List active Alertmanager silences (grouped by Alertmanager URL)"))
	silence.AddCommand(silenceExpire)
	silence.AddCommand(model.NewAutocompleteData("help", "", "Show this command's subcommands and examples"))
	root.AddCommand(silence)

	validate := model.NewAutocompleteData("validate", "[all|group] [webhook-test|end-to-end|simulate]", "Check that alerts will reach this channel: Alertmanager reach and receivers loaded. Sysadmin/team_admin.")
	// Dynamic so the hover lists only the groups this channel actually has.
	// Optional so a receiver name can still be typed instead.
	validate.AddDynamicListArgument("Limit the check to a group in this channel, or type a receiver name", validateTargetsFetchURL(), false)
	validate.AddStaticListArgument("Optional: add a test with side effects, or simulate routing", false, []model.AutocompleteListItem{
		{Item: modeWebhookTest, HelpText: "Also POST a visible test message to each receiver's webhook"},
		{Item: modeEndToEnd, HelpText: "Also fire a synthetic alert through Alertmanager and watch this channel for delivery"},
		{Item: modeSimulate, HelpText: "Walk Alertmanager's loaded route tree against labels you type after it, such as `simulate runbook=high-cpu-usage severity=critical`. Read-only, fires nothing."},
	})
	config.AddCommand(validate)

	sortTypeahead(root)
	return root
}

// ExecuteCommand is the Mattermost hook for /alertmanager <subcommand>.
// We dispatch to per-subcommand handlers and post the response as an
// ephemeral message — multiline command output stays out of channel
// history unless explicitly desired.
func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	msg := p.executeCommand(args)
	if msg != "" {
		p.postEphemeralResponse(args, msg)
	}
	return &model.CommandResponse{}, nil
}

func (p *Plugin) executeCommand(args *model.CommandArgs) string {
	fields := strings.Fields(args.Command)
	if len(fields) == 0 || fields[0] != "/"+commandTrigger {
		return ""
	}
	if len(fields) < 2 {
		return renderHelp(nil)
	}
	if path, ok := parseHelpRequest(fields[1:]); ok {
		return renderHelp(path)
	}

	if fields[1] == "rules" {
		return ":warning: `rules` moved. Run `/alertmanager format rules`, or `/alertmanager format rules crd` for the PrometheusRule CRD."
	}
	if msg := removedDashFlagMessage(fields[1:]); msg != "" {
		return msg
	}

	// Same order as the autocomplete registration. Group handlers drop their
	// own word and hand the rest to the leaf handlers (see withoutParent).
	switch fields[1] {
	case "about":
		return p.handleAbout(args)
	case "admin":
		msg, err := p.handleAdmin(args)
		return joinErr(msg, err)
	case "alerts":
		msg, err := p.handleAlertsGroup(args)
		return joinErr(msg, err)
	case "config":
		msg, err := p.handleConfigGroup(args)
		return joinErr(msg, err)
	case "docs":
		msg, err := p.handleDocs(args)
		return joinErr(msg, err)
	case "format":
		msg, err := p.handleFormatGroup(args)
		return joinErr(msg, err)
	case "silence":
		msg, err := p.handleSilenceGroup(args)
		return joinErr(msg, err)
	default:
		return fmt.Sprintf("Unknown subcommand `%s`. Run `/alertmanager help` for the list.", fields[1])
	}
}

// removedDashFlagMessage rejects any `--word` argument. Flags are plain words
// now (`force`, `prune`) or `key=value` (`namespace=monitoring`), so the reply
// shows the exact rewrite instead of leaving the user to guess.
func removedDashFlagMessage(words []string) string {
	for _, w := range words {
		after, ok := strings.CutPrefix(w, "--")
		if !ok || after == "" {
			continue
		}
		switch {
		case w == "--private":
			return ":warning: `--private` was removed. Create the channel first (public or private), then run this command inside it."
		case strings.HasPrefix(w, "--format="):
			return ":warning: `--format=` was removed. Write the format as a plain word: `standard` or `crd`."
		default:
			return fmt.Sprintf(":warning: `%s` was removed. Commands no longer use `--`. Write `%s` instead.", w, after)
		}
	}
	return ""
}

// joinErr flattens (string, error) into a single user-facing string. If err
// is non-nil it takes priority — error messages from handlers are intended
// for the user, not the logs.
func joinErr(msg string, err error) string {
	if err != nil {
		return err.Error()
	}
	return msg
}

// postEphemeralResponse sends the rendered message to the calling user
// only, not the whole channel. Most slash-command outputs (YAML snippets,
// token-containing URLs) are sysadmin-only by nature.
func (p *Plugin) postEphemeralResponse(args *model.CommandArgs, msg string) {
	post := &model.Post{
		UserId:    p.BotUserID,
		ChannelId: args.ChannelId,
		RootId:    args.RootId,
		Message:   msg,
	}
	_ = p.API.SendEphemeralPost(args.UserId, post)
}
