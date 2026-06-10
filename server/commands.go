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
		"- `/alertmanager add <team> <channel> <am-url> [set]` — bulk-create receivers for a named set: `all` (default, every runbook), `application`, `compute`, `database`, `networking`, `observability`, `storage`\n" +
		"- `/alertmanager alerts` — list currently firing alerts (grouped by Alertmanager URL — one section per backend, not per receiver)\n" +
		"- `/alertmanager config <name>` — show full detail card + slack_configs YAML for one receiver\n" +
		"- `/alertmanager docs [topic]` — embedded documentation (tab through topics: architecture, configuration, development, kubernetes, slash_commands)\n" +
		"- `/alertmanager expire_silence <name> <silence-id>` — expire a silence\n" +
		"- `/alertmanager export` — DM the assembled receivers.yml + routes.yml for this channel\n" +
		"- `/alertmanager help` — this message\n" +
		"- `/alertmanager list` — list receivers bound to this channel\n" +
		"- `/alertmanager reconcile` — prune receivers whose Mattermost webhook has been deleted out-of-band (sysadmin; runs automatically every 5 min)\n" +
		"- `/alertmanager remove <name|set|all> [--force]` — delete a receiver, a runbook set, or everything in this channel\n" +
		"- `/alertmanager rotate <name>` — delete + recreate the webhook (new hook-id, new URL)\n" +
		"- `/alertmanager silences` — list active silences (grouped by Alertmanager URL)\n" +
		"- `/alertmanager status` — Alertmanager version + uptime per backend (grouped by Alertmanager URL)\n" +
		"- `/alertmanager validate [name|set] [--webhook-test] [--end-to-end]` — validate pipeline configuration: AM reach, receiver loaded in AM, optional synthetic delivery"
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

	add := model.NewAutocompleteData("add", "[team] [channel] [am-url] [set] [--webhook-host=<url>]", "Create receivers for a named runbook set (sysadmin/team_admin)")
	add.AddDynamicListArgument("Mattermost team URL slug — tab through your teams", teamFetchURL, true)
	add.AddDynamicListArgument("Mattermost channel URL slug — public channels in the chosen team (or type a new name to auto-create)", channelFetchURL, true)
	add.AddTextArgument("Alertmanager API base URL (no trailing slash)", "[am-url]", "")
	add.AddStaticListArgument("Set of runbooks to create (defaults to `all` = every embedded runbook)", false, []model.AutocompleteListItem{
		{Item: "all", HelpText: "Every embedded runbook (default — 20 receivers)"},
		{Item: "application", HelpText: "HTTP error, latency, endpoint, request-rate alerts (4)"},
		{Item: "compute", HelpText: "CPU, memory, pod, deployment, node alerts (6)"},
		{Item: "database", HelpText: "Connectivity, replication lag, query latency (3)"},
		{Item: "networking", HelpText: "Ingress 5xx, cert expiry, DNS failure (3)"},
		{Item: "observability", HelpText: "Prometheus scrape down, Alertmanager notify fail (2)"},
		{Item: "storage", HelpText: "PV full, disk fill rate (2)"},
	})
	root.AddCommand(add)

	root.AddCommand(model.NewAutocompleteData("alerts", "", "List currently firing alerts (grouped by Alertmanager URL)"))

	cfg := model.NewAutocompleteData("config", "[name]", "Show full detail card + slack_configs YAML for one receiver (sysadmin/team_admin)")
	cfg.AddTextArgument("Receiver name (run `/alertmanager list` first to see what's here)", "[name]", "")
	root.AddCommand(cfg)

	docs := model.NewAutocompleteData("docs", "[topic]", "Show embedded documentation in chat")
	docs.AddStaticListArgument("Pick a topic", false, []model.AutocompleteListItem{
		{Item: "architecture", HelpText: "Plugin design: why slack_configs over webhook_configs"},
		{Item: "configuration", HelpText: "Plugin settings + alertmanager.yml structure"},
		{Item: "development", HelpText: "Local build, test, deploy workflow"},
		{Item: "kubernetes", HelpText: "K8s deployment notes (WebhookHost, HA reconciler, NetworkPolicy)"},
		{Item: "slash_commands", HelpText: "Full reference of all /alertmanager subcommands"},
	})
	root.AddCommand(docs)

	root.AddCommand(model.NewAutocompleteData("expire_silence", "[name] [silence-id]", "Expire an active Alertmanager silence"))

	root.AddCommand(model.NewAutocompleteData("export", "", "DM the assembled receivers.yml + routes.yml for this channel (sysadmin/team_admin)"))

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
		{Item: "storage", HelpText: "All storage receivers in this channel (requires --force)"},
	})
	remove.AddStaticListArgument("Pass `--force` to confirm bulk delete", false, []model.AutocompleteListItem{
		{Item: "--force", HelpText: "Confirms the bulk delete — without this, set/all targets do a dry-run preview"},
	})
	root.AddCommand(remove)

	rotate := model.NewAutocompleteData("rotate", "[name]", "Recreate the webhook with a new hook-id (sysadmin/team_admin)")
	rotate.AddTextArgument("Name of the receiver to rotate", "[name]", "")
	root.AddCommand(rotate)

	root.AddCommand(model.NewAutocompleteData("silences", "", "List active Alertmanager silences (grouped by Alertmanager URL)"))

	root.AddCommand(model.NewAutocompleteData("status", "", "Alertmanager version + uptime per backend (grouped by Alertmanager URL)"))

	validate := model.NewAutocompleteData("validate", "[name|set] [--webhook-test] [--end-to-end]", "Validate pipeline configuration — checks AM reach + receiver loaded in AM (sysadmin/team_admin)")
	validate.AddStaticListArgument("Pick a set to limit the check, OR type a receiver name", false, []model.AutocompleteListItem{
		{Item: "all", HelpText: "Every receiver bound to this channel (default)"},
		{Item: "application", HelpText: "Application receivers only"},
		{Item: "compute", HelpText: "Compute receivers only"},
		{Item: "database", HelpText: "Database receivers only"},
		{Item: "networking", HelpText: "Networking receivers only"},
		{Item: "observability", HelpText: "Observability receivers only"},
		{Item: "storage", HelpText: "Storage receivers only"},
	})
	validate.AddStaticListArgument("Optional flags to add side-effect tests", false, []model.AutocompleteListItem{
		{Item: "--webhook-test", HelpText: "Also POST a visible test message to each receiver's webhook"},
		{Item: "--end-to-end", HelpText: "Also fire a synthetic alert through Alertmanager; watch the channel for delivery"},
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
