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

	helpMsg = "**Alertmanager bridge slash commands** _(channel-scoped — you only see receivers bound to this channel)_\n\n" +
		"_All commands listed in alphabetical order to match the autocomplete dropdown._\n\n" +
		"- `/alertmanager about` — plugin build info, configured settings, reconciler health, jump-off links\n" +
		"- `/alertmanager add <team> <channel> <am-url> [target] [on]` — create receivers for a group set OR an individual runbook slug. Group sets: `all` (default), `application`, `compute`, `database`, `networking`, `observability`, `security`, `storage`. Each group share ONE Mattermost webhook; individual-slug adds get their own webhook. Trailing `on` opts these receivers INTO rotation reminders (configured via System Console → WebhookRotationDays).\n" +
		"- `/alertmanager alerts` — list currently firing alerts (grouped by Alertmanager URL — one section per backend, not per receiver)\n" +
		"- `/alertmanager config <name>` — show full detail card + slack_configs YAML for one receiver\n" +
		"- `/alertmanager docs [topic]` — embedded documentation (tab through topics: alerts, requirements, architecture, configuration, development, kubernetes, slash_commands)\n" +
		"- `/alertmanager expire_silence <name> <silence-id>` — expire a silence\n" +
		"- `/alertmanager export [--diff-against-loaded]` — DM the assembled receivers.yml + routes.yml for this channel; with `--diff-against-loaded` (sysadmin) diff against AM's currently-loaded config\n" +
		"- `/alertmanager help` — this message\n" +
		"- `/alertmanager list` — list receivers bound to this channel\n" +
		"- `/alertmanager reconcile` — prune receivers whose Mattermost webhook has been deleted out-of-band (sysadmin; runs automatically every 5 min)\n" +
		"- `/alertmanager remove <name|set|all> [--force]` — delete a receiver, a runbook set, or everything in this channel\n" +
		"- `/alertmanager rotate <name>` — delete + recreate the webhook (new hook-id, new URL)\n" +
		"- `/alertmanager rules` — links to the shipped sample Prometheus alerting rules (browsable HTML + downloadable YAML) so you can wire up the Prometheus side without cloning the repo\n" +
		"- `/alertmanager silences` — list active silences (grouped by Alertmanager URL)\n" +
		"- `/alertmanager status` — Alertmanager version + uptime per backend (grouped by Alertmanager URL)\n" +
		"- `/alertmanager validate [name|set] [--webhook-test|--end-to-end|--simulate <labels>] [--severity=<value>]` — validate pipeline configuration. Without flags: AM reach + receiver-loaded-in-AM. `--webhook-test` / `--end-to-end` fire side-effect tests. `--simulate runbook=<slug> severity=<level>` walks AM's loaded route tree against the labels and reports which receiver(s) an alert with those labels would dispatch to (read-only, no synthetic alert). `--severity=<warning|critical|info|all>` controls which severities `--end-to-end` fires; `all` fires four alerts per receiver (warning + critical + info + resolved) so you can visually verify every render path in one shot."
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

	// Dynamic-list arguments for team and channel are backed by ServeHTTP
	// endpoints in http.go. The team list shows teams the calling user
	// belongs to; the channel list reads the team-slug the user has
	// already typed and returns that team's public channels. Both args
	// accept free text — sysadmins can type a channel name that doesn't
	// exist yet (it gets auto-created on /alertmanager add).
	teamFetchURL := fmt.Sprintf("/plugins/%s/autocomplete/teams", Manifest.Id)
	channelFetchURL := fmt.Sprintf("/plugins/%s/autocomplete/channels", Manifest.Id)

	// Subcommands registered in alphabetical order — Mattermost renders
	// them in registration order in the autocomplete dropdown, so this
	// is the order users see. about → add → alerts → ... → validate.

	root.AddCommand(model.NewAutocompleteData("about", "", "Plugin build info, configured settings, and links"))

	add := model.NewAutocompleteData("add", "[team] [channel] [am-url] [target] [on]", "Create receivers for a group set OR individual runbook slug. One shared webhook per group; individual slugs get their own. Trailing `on` opts in to rotation reminders. (sysadmin/team_admin)")
	add.AddDynamicListArgument("Mattermost team URL slug — tab through your teams", teamFetchURL, true)
	add.AddDynamicListArgument("Mattermost channel URL slug — public channels in the chosen team (or type a new name to auto-create)", channelFetchURL, true)
	add.AddTextArgument("Alertmanager API base URL (no trailing slash)", "[am-url]", "")
	add.AddStaticListArgument("Group set OR individual runbook slug (defaults to `all`). Type a slug freely; static list shows group sets only.", false, []model.AutocompleteListItem{
		{Item: "all", HelpText: "Every embedded runbook (default — 30 receivers in one shared webhook)"},
		{Item: "application", HelpText: "HTTP error, latency, endpoint, request-rate alerts (4) — one shared webhook"},
		{Item: "compute", HelpText: "CPU, memory, throttling, pod, deployment, node, image-pull, scheduling alerts (9) — one shared webhook"},
		{Item: "database", HelpText: "Connectivity, replication lag, query latency, connection saturation (4) — one shared webhook"},
		{Item: "networking", HelpText: "Ingress 5xx, cert expiry, DNS failure (3) — one shared webhook"},
		{Item: "observability", HelpText: "Prometheus scrape down, Alertmanager notify fail (2) — one shared webhook"},
		{Item: "security", HelpText: "Unexpected image, API auth spike, privileged container, shell-in-container, RBAC escalation, security tooling down (6) — one shared webhook"},
		{Item: "storage", HelpText: "PV full, disk fill rate (2) — one shared webhook"},
	})
	add.AddStaticListArgument("Optional: enable webhook rotation reminders for these receivers", false, []model.AutocompleteListItem{
		{Item: "on", HelpText: "Opt receivers in this channel INTO rotation reminders. Sysadmins get DM'd when these webhooks haven't been rotated for the threshold set in System Console → WebhookRotationDays. Without this flag, these receivers are never reminded — even if the global threshold is set. Per-channel scope: opting in here does not affect receivers in other channels."},
	})
	root.AddCommand(add)

	root.AddCommand(model.NewAutocompleteData("alerts", "", "List currently firing alerts (grouped by Alertmanager URL)"))

	cfg := model.NewAutocompleteData("config", "[name]", "Show full detail card + slack_configs YAML for one receiver (sysadmin/team_admin)")
	cfg.AddTextArgument("Receiver name (run `/alertmanager list` first to see what's here)", "[name]", "")
	root.AddCommand(cfg)

	docs := model.NewAutocompleteData("docs", "[topic]", "Show embedded documentation in chat")
	docs.AddStaticListArgument("Pick a topic", false, []model.AutocompleteListItem{
		{Item: "alerts", HelpText: "Catalog of every alert type + severity + what fires it, by group"},
		{Item: "requirements", HelpText: "Per-alert requirements: which metric/exporter/tooling each alert needs to fire"},
		{Item: "architecture", HelpText: "Plugin design: why slack_configs over webhook_configs"},
		{Item: "configuration", HelpText: "Plugin settings + alertmanager.yml structure"},
		{Item: "development", HelpText: "Local build, test, deploy workflow"},
		{Item: "kubernetes", HelpText: "K8s deployment notes (WebhookHost, HA reconciler, NetworkPolicy)"},
		{Item: "rotation", HelpText: "Webhook rotation reminder playbook (WebhookRotationDays, `on` opt-in, `rotate all --overdue`)"},
		{Item: "slash_commands", HelpText: "Full reference of all /alertmanager subcommands"},
	})
	root.AddCommand(docs)

	root.AddCommand(model.NewAutocompleteData("expire_silence", "[name] [silence-id]", "Expire an active Alertmanager silence"))

	exportCmd := model.NewAutocompleteData("export", "[--diff-against-loaded]", "DM the assembled receivers.yml + routes.yml for this channel (sysadmin/team_admin)")
	exportCmd.AddStaticListArgument("Optional flag: diff against AM's currently-loaded config (sysadmin only)", false, []model.AutocompleteListItem{
		{Item: "--diff-against-loaded", HelpText: "Output a side-by-side diff between AM's loaded YAML and what this export would add (sysadmin only)"},
	})
	root.AddCommand(exportCmd)

	root.AddCommand(model.NewAutocompleteData("help", "", "Show slash-command help"))

	list := model.NewAutocompleteData("list", "", "List receivers bound to this channel")
	root.AddCommand(list)

	root.AddCommand(model.NewAutocompleteData("reconcile", "", "Prune receivers whose Mattermost webhook was deleted out-of-band (sysadmin; also runs every 5 min)"))

	// First arg is a static list (set names + `all`) for discoverability,
	// but Mattermost autocomplete doesn't enforce — users can also type a
	// receiver name freely for single-receiver removal.
	remove := model.NewAutocompleteData("remove", "[name|set|all] [--force]", "Delete one receiver, one set, or all receivers in this channel (sysadmin/team_admin)")
	remove.AddStaticListArgument("Pick a set or `all`, OR type a receiver name", false, []model.AutocompleteListItem{
		{Item: "all", HelpText: "Every receiver in this channel (requires --force)"},
		{Item: "application", HelpText: "All application receivers in this channel (requires --force)"},
		{Item: "compute", HelpText: "All compute receivers in this channel (requires --force)"},
		{Item: "database", HelpText: "All database receivers in this channel (requires --force)"},
		{Item: "networking", HelpText: "All networking receivers in this channel (requires --force)"},
		{Item: "observability", HelpText: "All observability receivers in this channel (requires --force)"},
		{Item: "security", HelpText: "All security receivers in this channel (requires --force)"},
		{Item: "storage", HelpText: "All storage receivers in this channel (requires --force)"},
	})
	remove.AddStaticListArgument("Pass `--force` to confirm bulk delete", false, []model.AutocompleteListItem{
		{Item: "--force", HelpText: "Confirms the bulk delete — without this, set/all targets do a dry-run preview"},
	})
	root.AddCommand(remove)

	rotate := model.NewAutocompleteData("rotate", "[name|all --overdue]", "Recreate webhook with a new hook-id. `all --overdue` rotates everything past the threshold set by System Console → WebhookRotationDays.")
	rotate.AddTextArgument("Receiver name to rotate, OR `all` followed by --overdue to rotate everything past the rotation threshold in this channel", "[name|all]", "")
	rotate.AddStaticListArgument("Optional flag — only valid after `all`", false, []model.AutocompleteListItem{
		{Item: "--overdue", HelpText: "Rotate only receivers past the WebhookRotationDays threshold (System Console setting; default 0 = feature off)"},
	})
	root.AddCommand(rotate)

	root.AddCommand(model.NewAutocompleteData("rules", "", "Links to the shipped sample Prometheus alerting rules (view or download)"))

	root.AddCommand(model.NewAutocompleteData("silences", "", "List active Alertmanager silences (grouped by Alertmanager URL)"))

	root.AddCommand(model.NewAutocompleteData("status", "", "Alertmanager version + uptime per backend (grouped by Alertmanager URL)"))

	validate := model.NewAutocompleteData("validate", "[name|set] [--webhook-test|--end-to-end|--simulate <labels>]", "Validate pipeline configuration — checks AM reach + receiver loaded in AM, or simulates route resolution with --simulate (sysadmin/team_admin)")
	validate.AddStaticListArgument("Pick a set to limit the check, OR type a receiver name", false, []model.AutocompleteListItem{
		{Item: "all", HelpText: "Every receiver bound to this channel (default)"},
		{Item: "application", HelpText: "Application receivers only"},
		{Item: "compute", HelpText: "Compute receivers only"},
		{Item: "database", HelpText: "Database receivers only"},
		{Item: "networking", HelpText: "Networking receivers only"},
		{Item: "observability", HelpText: "Observability receivers only"},
		{Item: "security", HelpText: "Security receivers only"},
		{Item: "storage", HelpText: "Storage receivers only"},
	})
	validate.AddStaticListArgument("Optional mode flag — pick how validate runs", false, []model.AutocompleteListItem{
		{Item: "--webhook-test", HelpText: "Also POST a visible test message to each receiver's webhook"},
		{Item: "--end-to-end", HelpText: "Also fire a synthetic alert through Alertmanager; watch the channel for delivery (default severity=warning)"},
		{Item: "--simulate", HelpText: "Walk AM's loaded route tree against a label set you supply. Type `key=value` pairs after the flag (e.g., `--simulate runbook=high-cpu-usage severity=critical`). Read-only — no synthetic alert is fired, safe to run repeatedly."},
	})
	// Severity is a modifier for --end-to-end. Mattermost autocomplete
	// is positional, so this gets its own position immediately after
	// the mode flag — typing `--end-to-end<space>` brings up this
	// dropdown next, making the values discoverable without leaving
	// chat.
	validate.AddStaticListArgument("Optional severity for --end-to-end (no effect on other modes)", false, []model.AutocompleteListItem{
		{Item: "--severity=warning", HelpText: "Fire --end-to-end synthetic at warning severity (yellow sidebar). This is the default if --severity is omitted."},
		{Item: "--severity=critical", HelpText: "Fire --end-to-end synthetic at critical severity (red sidebar)."},
		{Item: "--severity=info", HelpText: "Fire --end-to-end synthetic at info severity (blue sidebar)."},
		{Item: "--severity=all", HelpText: "Fire FOUR synthetic alerts per receiver: warning + critical + info + resolved. Use to visually verify every render path (color mapping, title format, resolved variant) in one shot. Multiplies the alert count — scope to one or two receivers for visual smoke tests."},
	})
	root.AddCommand(validate)

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
		return "Missing subcommand. Run `/alertmanager help` for the list."
	}

	// Dispatch cases in alphabetical order matching the autocomplete
	// registration so contributor mental model stays consistent.
	switch fields[1] {
	case "about":
		return p.handleAbout(args)
	case "add":
		msg, err := p.handleAdd(args)
		return joinErr(msg, err)
	case "alerts":
		msg, err := p.handleAlerts(args)
		return joinErr(msg, err)
	case "config":
		msg, err := p.handleConfig(args)
		return joinErr(msg, err)
	case "docs":
		msg, err := p.handleDocs(args)
		return joinErr(msg, err)
	case "expire_silence":
		msg, err := p.handleExpireSilence(args)
		return joinErr(msg, err)
	case "export":
		msg, err := p.handleExport(args)
		return joinErr(msg, err)
	case "help":
		return helpMsg
	case "list":
		msg, err := p.handleList(args)
		return joinErr(msg, err)
	case "reconcile":
		msg, err := p.handleReconcile(args)
		return joinErr(msg, err)
	case "remove":
		msg, err := p.handleRemove(args)
		return joinErr(msg, err)
	case "rotate":
		msg, err := p.handleRotate(args)
		return joinErr(msg, err)
	case "rules":
		msg, err := p.handleRules(args)
		return joinErr(msg, err)
	case "silences":
		msg, err := p.handleListSilences(args)
		return joinErr(msg, err)
	case "status":
		msg, err := p.handleStatus(args)
		return joinErr(msg, err)
	case "validate":
		msg, err := p.handleValidate(args)
		return joinErr(msg, err)
	default:
		return fmt.Sprintf("Unknown subcommand `%s`. Run `/alertmanager help` for the list.", fields[1])
	}
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
