package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Help works like a CLI. Type `help` at any level of the command tree and the
// detail grows as you go deeper:
//
//	/alertmanager help               every top-level command, one line each
//	/alertmanager config help        the subcommands of config
//	/alertmanager config add help    options, who can run it, and examples
//	/alertmanager help config add    same as `config add help`

type helpOption struct {
	Name string
	Desc string
}

type helpTopic struct {
	Path        string // space-separated command path, "" is not used
	Summary     string // one line, plain words
	Usage       string
	Description string
	Options     []helpOption
	Who         string
	Examples    []string
}

const helpTop = "/alertmanager"

var helpRegistry = []helpTopic{
	{
		Path:    "alerts",
		Summary: "See what is firing and whether Alertmanager is up",
		Usage:   "/alertmanager alerts [firing|status]",
		Description: "Read-only views of Alertmanager. A bare `alerts` is the same as `alerts firing`. " +
			"Only receivers bound to this channel are considered.",
		Examples: []string{"/alertmanager alerts", "/alertmanager alerts status"},
	},
	{
		Path:    "alerts firing",
		Summary: "Firing alerts for this channel",
		Usage:   "/alertmanager alerts [firing]",
		Description: "Lists firing alerts from Alertmanager whose `runbook=` label matches a receiver bound to this channel. " +
			"Alerts without a matching receiver here are not shown.",
		Who:      "Any user in the channel.",
		Examples: []string{"/alertmanager alerts", "/alertmanager alerts firing"},
	},
	{
		Path:        "alerts status",
		Summary:     "Alertmanager version and uptime",
		Usage:       "/alertmanager alerts status",
		Description: "Shows version and uptime for each Alertmanager backend this channel talks to, grouped by Alertmanager URL. Use it to tell \"nothing is firing\" apart from \"Alertmanager is unreachable\".",
		Who:         "Any user in the channel.",
		Examples:    []string{"/alertmanager alerts status"},
	},

	{
		Path:    "silence",
		Summary: "Mute alerts for a set time",
		Usage:   "/alertmanager silence <create|list|expire>",
		Description: "Silences live in Alertmanager, not in the plugin. Every channel that receives a silenced alert on the same Alertmanager goes quiet. " +
			"A TTL is always required, so nothing stays muted forever.",
		Examples: []string{
			"/alertmanager silence create 2h HighCPUUsage",
			"/alertmanager silence list",
		},
	},
	{
		Path:    "silence create",
		Summary: "Mute an alert, a group, or everything in this channel",
		Usage:   "/alertmanager silence create <TTL> <alert|group|all>",
		Description: "Creates a silence in Alertmanager that expires on its own. " +
			"`silence <TTL> <target>` without `create` works too. " +
			"A custom receiver has no `runbook` label, so a group or `all` does not match it. Silence that one by its alert name.",
		Options: []helpOption{
			{"<TTL>", "`30m`, or whole hours from `1h` through `72h`. `90m`, `1.5h`, and anything over `72h` are rejected."},
			{"<alert>", "An alert name such as `HighCPUUsage`. Matches `alertname` exactly. Up to `72h`."},
			{"<group>", "`application`, `compute`, `database`, `mattermost`, `networking`, `observability`, `security`, or `storage`. Matches that group's runbooks in this channel. Up to `72h`."},
			{"all", "Every runbook receiver in this channel. `30m` or `1h` only."},
		},
		Who: "Alert or group: team admin. `all`: system admin.",
		Examples: []string{
			"/alertmanager silence create 30m HighCPUUsage",
			"/alertmanager silence create 4h compute",
			"/alertmanager silence create 1h all",
		},
	},
	{
		Path:        "silence list",
		Summary:     "Show active silences",
		Usage:       "/alertmanager silence list",
		Description: "Lists active Alertmanager silences for this channel's backends, grouped by Alertmanager URL, with their IDs and end times.",
		Who:         "Any user in the channel.",
		Examples:    []string{"/alertmanager silence list"},
	},
	{
		Path:        "silence expire",
		Summary:     "End a silence early",
		Usage:       "/alertmanager silence expire <name> <silence-id>",
		Description: "Expires one silence now. Only silences created from this channel with `silence create` can be expired here. `silence create` prints the exact command to copy, and `silence list` shows the IDs.",
		Options: []helpOption{
			{"<name>", "A receiver bound to this channel on the Alertmanager that holds the silence."},
			{"<silence-id>", "The silence ID from `silence create` or `silence list`. Silences made elsewhere, or before this version, are ended in Alertmanager."},
		},
		Who:      "Team admin.",
		Examples: []string{"/alertmanager silence expire high-cpu-usage--ops-alerts 0c1b6c3e-5f0e-4c3a-9a55-2f9d1c7e8a10"},
	},

	{
		Path:    "config",
		Summary: "Manage this channel's alert receivers",
		Usage:   "/alertmanager config [list|<name>|add|add-custom|validate|rotate|remove]",
		Description: "A receiver connects one Alertmanager alert route to one Mattermost incoming webhook in this channel. " +
			"Everything here, including `add`, works on the channel you run it in. A bare `config` lists receivers.",
		Examples: []string{
			"/alertmanager config",
			"/alertmanager config add http://alertmanager.monitoring.svc.cluster.local:9093 compute",
			"/alertmanager config validate end-to-end",
		},
	},
	{
		Path:        "config list",
		Summary:     "List receivers bound to this channel",
		Usage:       "/alertmanager config [list]",
		Description: "Table of receivers in this channel, including when each webhook was last rotated.",
		Who:         "Any user in the channel.",
		Examples:    []string{"/alertmanager config", "/alertmanager config list"},
	},
	{
		Path:        "config <name>",
		Summary:     "Detail card for one receiver",
		Usage:       "/alertmanager config <name>",
		Description: "Shows the receiver's metadata and its `slack_configs` YAML, including the webhook URL. Names look like `high-cpu-usage--ops-alerts`: runbook slug, `--`, team, channel.",
		Who:         "Team admin or system admin. The webhook URL is a secret.",
		Examples:    []string{"/alertmanager config high-cpu-usage--ops-alerts"},
	},
	{
		Path:    "config add",
		Summary: "Create receivers for runbooks in this channel",
		Usage:   "/alertmanager config add <am-url> [target] [standard|crd] [on] [namespace=<ns>] [webhook-host=<url>]",
		Description: "Run it in the channel that should receive the alerts. It creates the Mattermost webhook and receiver entries for that channel, then DMs you the Alertmanager config to apply, with the Prometheus rules for the runbooks you added attached to the same message. " +
			"Create the channel first if it does not exist. Applying the DM'd config to Alertmanager is still your step.",
		Options: []helpOption{
			{"<am-url>", "Alertmanager base URL as reachable from the Mattermost server. No trailing slash. `localhost` inside a container is the container itself."},
			{"[target]", "A group or one runbook slug. Default `all`. Groups: `all`, `application`, `compute`, `database`, `mattermost`, `networking`, `observability`, `security`, `storage`. A group shares one webhook. A single slug gets its own."},
			{"standard", "Format of the DM'd config. The default. Plain `alertmanager.yml` receivers and routes for a file-based Alertmanager (config file, Docker, VM)."},
			{"crd", "Format of the DM'd config. Prometheus Operator objects, an AlertmanagerConfig (v1alpha1) plus a Secret, to `kubectl apply`. Use this with kube-prometheus-stack."},
			{"on", "Opt these receivers into rotation reminders (System Console, WebhookRotationDays)."},
			{"namespace=<ns>", "`metadata.namespace` for the `crd` objects. Default `monitoring`."},
			{"webhook-host=<url>", "Base URL Alertmanager should use to reach Mattermost, when it differs from the site URL."},
		},
		Who: "System admin, or a team admin of this channel's team.",
		Examples: []string{
			"/alertmanager config add http://kube-prometheus-stack-alertmanager.monitoring.svc.cluster.local:9093",
			"/alertmanager config add http://alertmanager.monitoring.svc.cluster.local:9093 compute on",
			"/alertmanager config add http://alertmanager.monitoring.svc.cluster.local:9093 high-cpu-usage",
			"/alertmanager config add http://alertmanager.monitoring.svc.cluster.local:9093 all crd namespace=monitoring",
		},
	},
	{
		Path:    "config add-custom",
		Summary: "Create one receiver for a non-runbook alert in this channel",
		Usage:   "/alertmanager config add-custom <am-url> <name> [standard|crd] [on] [namespace=<ns>] [webhook-host=<url>]",
		Description: "Run it in the channel that should receive the alerts. It creates one receiver named `<name>--<team>-<channel>` with its own webhook. " +
			"Unlike `add`, it writes no `runbook=` route. You wire the matcher yourself, and the DM'd config includes a commented stub.",
		Options: []helpOption{
			{"<am-url>", "Alertmanager base URL as reachable from the Mattermost server. No trailing slash."},
			{"<name>", "Lowercase `[a-z0-9_-]`, no `--`, not a runbook slug, group name, or one of `standard`, `crd`, `on`. The full name is capped at 190 characters."},
			{"standard", "Format of the DM'd config. The default. Plain `alertmanager.yml` receiver and route stub for a file-based Alertmanager (config file, Docker, VM)."},
			{"crd", "Format of the DM'd config. Prometheus Operator objects, an AlertmanagerConfig (v1alpha1) plus a Secret, to `kubectl apply`. Use this with kube-prometheus-stack."},
			{"on", "Opt this receiver into rotation reminders (System Console, WebhookRotationDays)."},
			{"namespace=<ns>", "`metadata.namespace` for the `crd` objects. Default `monitoring`."},
			{"webhook-host=<url>", "Base URL Alertmanager should use to reach Mattermost."},
		},
		Who: "System admin, or a team admin of this channel's team.",
		Examples: []string{
			"/alertmanager config add-custom http://alertmanager.monitoring.svc.cluster.local:9093 payments-latency",
			"/alertmanager config add-custom http://alertmanager.monitoring.svc.cluster.local:9093 payments-latency crd on",
		},
	},
	{
		Path:        "config validate",
		Summary:     "Check that alerts will reach this channel",
		Usage:       "/alertmanager config validate [all|group|name] [webhook-test|end-to-end|simulate <labels>] [severity=<level>]",
		Description: "Bare `validate` checks that Alertmanager is reachable and has each receiver loaded. The extra words add tests that have side effects, except `simulate`, which is read-only.",
		Options: []helpOption{
			{"[name|group]", "Limit the check to one receiver or one group. Default is every receiver in this channel."},
			{"webhook-test", "Also post a visible test message through each receiver's webhook."},
			{"end-to-end", "Also fire a synthetic alert through Alertmanager and watch this channel for delivery."},
			{"simulate <labels>", "Walk Alertmanager's loaded route tree with `key=value` labels and report which receivers would get the alert. Fires nothing."},
			{"severity=<level>", "Severity for `end-to-end`, typed by hand (not in typeahead): `warning` (default), `critical`, `info`, or `all`. `all` fires four alerts per receiver: warning, critical, info, and resolved."},
		},
		Who: "System admin, or a team admin of this channel's team.",
		Examples: []string{
			"/alertmanager config validate",
			"/alertmanager config validate compute webhook-test",
			"/alertmanager config validate high-cpu-usage--ops-alerts end-to-end severity=critical",
			"/alertmanager config validate simulate runbook=high-cpu-usage severity=critical",
		},
	},
	{
		Path:        "config rotate",
		Summary:     "Replace a receiver's webhook",
		Usage:       "/alertmanager config rotate <name|all overdue>",
		Description: "Deletes and recreates the webhook, which gives a new URL. Paste the new YAML into Alertmanager and reload it, or the old URL keeps failing.",
		Options: []helpOption{
			{"<name>", "One receiver to rotate."},
			{"all overdue", "Every receiver in this channel past the WebhookRotationDays threshold. That setting is 0 (off) by default."},
		},
		Who: "System admin, or a team admin of this channel's team.",
		Examples: []string{
			"/alertmanager config rotate high-cpu-usage--ops-alerts",
			"/alertmanager config rotate all overdue",
		},
	},
	{
		Path:    "config remove",
		Summary: "Delete a receiver, a group, or everything here",
		Usage:   "/alertmanager config remove <name|group|all> [force]",
		Description: "Deletes receivers and their webhooks in this channel. A single receiver is removed right away. " +
			"A group or `all` is a dry-run preview until you add `force`.",
		Options: []helpOption{
			{"<name>", "One receiver, for example `high-cpu-usage--ops-alerts`."},
			{"<group>", "Every receiver in that group in this channel. Needs `force`."},
			{"all", "Every receiver in this channel. Needs `force`."},
			{"force", "Confirm a group or `all` delete."},
		},
		Who: "System admin, or a team admin of this channel's team.",
		Examples: []string{
			"/alertmanager config remove high-cpu-usage--ops-alerts",
			"/alertmanager config remove compute",
			"/alertmanager config remove compute force",
		},
	},

	{
		Path:    "format",
		Summary: "Config and rules for this channel, as standard or crd",
		Usage:   "/alertmanager format <export|diff|rules>",
		Description: "`standard` is plain files for a file-based Alertmanager and Prometheus. `crd` is Prometheus Operator objects to `kubectl apply`. " +
			"`export` gives you everything for this channel, `diff` checks only the Alertmanager config, and `rules` gives only the sample alert rules.",
		Examples: []string{
			"/alertmanager format export crd",
			"/alertmanager format diff",
			"/alertmanager format rules crd",
		},
	},
	{
		Path:    "format export",
		Summary: "DM this channel's config and its rules",
		Usage:   "/alertmanager format export [standard|crd] [namespace=<ns>]",
		Description: "DMs every receiver and route in this channel, rendered from the current templates. The Prometheus rules for the runbooks those receivers cover are attached to the same DM. " +
			"Custom receivers have no sample rules.",
		Options: []helpOption{
			{"standard", "Plain `alertmanager.yml` receivers and routes, plus a plain Prometheus rules file to add to `rule_files:`. The default."},
			{"crd", "Prometheus Operator objects, an AlertmanagerConfig (v1alpha1) plus a Secret, plus a PrometheusRule named for this channel. All to `kubectl apply`."},
			{"namespace=<ns>", "`metadata.namespace` for the `crd` objects. Default `monitoring`."},
		},
		Who: "Team admin or system admin.",
		Examples: []string{
			"/alertmanager format export",
			"/alertmanager format export crd namespace=monitoring",
		},
	},
	{
		Path:        "format diff",
		Summary:     "Compare this channel with what Alertmanager loaded",
		Usage:       "/alertmanager format diff [standard|crd]",
		Description: "Alertmanager config only, no rules. Shows what Alertmanager has loaded for this channel against what the plugin would add, and validates the merged config with Alertmanager's own parser. With no format it detects standard or CRD from the loaded config.",
		Options: []helpOption{
			{"standard", "Match receivers by plain name. For a file-based Alertmanager."},
			{"crd", "Match receivers as `<namespace>/<config>/<name>`. For Prometheus Operator."},
		},
		Who:      "System admin.",
		Examples: []string{"/alertmanager format diff", "/alertmanager format diff crd"},
	},
	{
		Path:        "format rules",
		Summary:     "Get the sample Prometheus alert rules",
		Usage:       "/alertmanager format rules [standard|crd] [namespace=<ns>]",
		Description: "Rules only, all 37 of them, not limited to this channel. For this channel's rules together with its config, use `format export`.",
		Options: []helpOption{
			{"standard", "Links to browse the rules in your browser or download a plain Prometheus rules file. The default."},
			{"crd", "DM a PrometheusRule (`monitoring.coreos.com/v1`) with all 37 rules, ready to apply."},
			{"namespace=<ns>", "Namespace for the `crd` object. Default `monitoring`."},
		},
		Who: "Any user.",
		Examples: []string{
			"/alertmanager format rules",
			"/alertmanager format rules crd namespace=monitoring",
		},
	},

	{
		Path:        "admin",
		Summary:     "Sysadmin repair and settings tools",
		Usage:       "/alertmanager admin <repair|acknowledge|allowlist|metrics-token>",
		Description: "System admin only. Each command checks your role, so being in this group grants nothing extra.",
		Examples:    []string{"/alertmanager admin repair"},
	},
	{
		Path:    "admin repair",
		Summary: "Check receiver health and fix drift",
		Usage:   "/alertmanager admin repair [prune] [force]",
		Description: "A bare run changes nothing. It checks the stored receiver list and lists receivers whose Mattermost webhook was deleted out of band. " +
			"A background job prunes those every 5 minutes, and `prune` does it now.",
		Options: []helpOption{
			{"prune", "Remove receivers whose webhook no longer exists."},
			{"force", "Rewrite a corrupt stored receiver list from the plugin's in-memory copy. Use it only when a bare run reports the list as unreadable."},
		},
		Who: "System admin.",
		Examples: []string{
			"/alertmanager admin repair",
			"/alertmanager admin repair prune",
			"/alertmanager admin repair force",
		},
	},
	{
		Path:        "admin acknowledge",
		Summary:     "Hide an intentional AM-only receiver from warnings",
		Usage:       "/alertmanager admin acknowledge [add|remove <name>]",
		Description: "The admin inventory warns about receivers that exist in Alertmanager but not in the plugin. Acknowledge one you created on purpose, for example through GitOps. It does not mute alerts. Bare lists the hidden names.",
		Who:         "System admin.",
		Examples: []string{
			"/alertmanager admin acknowledge",
			"/alertmanager admin acknowledge add pagerduty-oncall",
			"/alertmanager admin acknowledge remove pagerduty-oncall",
		},
	},
	{
		Path:        "admin allowlist",
		Summary:     "Trusted image registries for the image rule",
		Usage:       "/alertmanager admin allowlist [add|remove <prefix>]",
		Description: "Extra registry prefixes for the UnexpectedContainerImage rule. They go into the next `format rules crd` file. Applying that file is still your step. Bare lists the shipped prefixes and yours.",
		Who:         "System admin.",
		Examples: []string{
			"/alertmanager admin allowlist",
			"/alertmanager admin allowlist add ghcr.io/myorg/",
			"/alertmanager admin allowlist remove ghcr.io/myorg/",
		},
	},
	{
		Path:        "admin metrics-token",
		Summary:     "Manage the Prometheus /metrics token",
		Usage:       "/alertmanager admin metrics-token [generate]",
		Description: "Bare shows whether a token is set and the endpoint. `generate` mints a new token, replaces the old one, shows it once, and prints a ready-to-paste `scrape_config`. System Console cannot show the token again, so regenerate if you lose it. Existing scrapers must be updated.",
		Who:         "System admin.",
		Examples:    []string{"/alertmanager admin metrics-token", "/alertmanager admin metrics-token generate"},
	},

	{
		Path:        "docs",
		Summary:     "Links to the documentation site",
		Usage:       "/alertmanager docs",
		Description: "Replies with links to the documentation site, the runbook library, and the sample Prometheus rules. Use the site sidebar to browse topics. `docs <topic>` is deprecated and only links to that page.",
		Who:         "Any user.",
		Examples:    []string{"/alertmanager docs"},
	},

	{
		Path:        "about",
		Summary:     "Plugin version, settings, and health",
		Usage:       "/alertmanager about",
		Description: "Build info, this channel's receiver count, and links. System admins also see org-wide counts, reconciler health, and which settings are configured.",
		Who:         "Any user. Extra detail for system admins.",
		Examples:    []string{"/alertmanager about"},
	},

	{
		Path:    "help",
		Summary: "Show help",
		Usage:   "/alertmanager help [command...]",
		Description: "Type `help` after any command. The top level is a short list. Each level down adds detail, and a single command shows its options, who can run it, and examples. " +
			"`help config add` is the same as `config add help`.",
		Examples: []string{
			"/alertmanager help",
			"/alertmanager config help",
			"/alertmanager config add help",
		},
	},
}

// parseHelpRequest reports whether the words after /alertmanager ask for help,
// and for which command path. A leading `help` always counts. A trailing `help`
// only counts when the words before it are a real command path, so a custom
// receiver that happens to be named "help" still works as an argument.
func parseHelpRequest(tokens []string) ([]string, bool) {
	if len(tokens) == 0 {
		return nil, false
	}
	if strings.EqualFold(tokens[0], "help") {
		return tokens[1:], true
	}
	last := len(tokens) - 1
	if strings.EqualFold(tokens[last], "help") {
		before := tokens[:last]
		if _, consumed := resolveHelpPath(before); consumed == len(before) {
			return before, true
		}
	}
	return nil, false
}

func findHelpTopic(path string) *helpTopic {
	for i := range helpRegistry {
		if helpRegistry[i].Path == path {
			return &helpRegistry[i]
		}
	}
	return nil
}

// childHelpTopics returns the topics one level below path, in registry order.
func childHelpTopics(path string) []helpTopic {
	var out []helpTopic
	for _, t := range helpRegistry {
		rest := t.Path
		if path != "" {
			var ok bool
			rest, ok = strings.CutPrefix(t.Path, path+" ")
			if !ok {
				continue
			}
		}
		if !strings.Contains(rest, " ") {
			out = append(out, t)
		}
	}
	return out
}

// resolveHelpPath walks tokens down the registry. A `<name>` topic matches any
// word that is not a named subcommand, which is how `config <receiver>` gets
// help. consumed is how many tokens matched.
func resolveHelpPath(tokens []string) (path string, consumed int) {
	for i, tok := range tokens {
		next := strings.ToLower(tok)
		if path != "" {
			next = path + " " + next
		}
		if findHelpTopic(next) != nil {
			path = next
			continue
		}
		placeholder := ""
		for _, c := range childHelpTopics(path) {
			if idx := strings.LastIndex(c.Path, " <"); idx >= 0 && strings.HasSuffix(c.Path, ">") {
				placeholder = c.Path
			}
		}
		if placeholder == "" {
			return path, i
		}
		path = placeholder
	}
	return path, len(tokens)
}

func renderHelp(tokens []string) string {
	path, consumed := resolveHelpPath(tokens)
	var b strings.Builder
	if consumed < len(tokens) {
		fmt.Fprintf(&b, ":warning: No help for `%s %s`.", helpTop, strings.Join(tokens, " "))
		if path == "" {
			b.WriteString(" Showing the top level.\n\n")
		} else {
			fmt.Fprintf(&b, " Showing `%s`.\n\n", path)
		}
	}
	if path == "" {
		b.WriteString(renderTopHelp())
		return b.String()
	}
	b.WriteString(renderTopicHelp(*findHelpTopic(path)))
	return b.String()
}

func renderTopHelp() string {
	var b strings.Builder
	b.WriteString("**Alertmanager bridge** for Mattermost. Commands are channel-scoped, so you only see receivers bound to this channel.\n\n")
	fmt.Fprintf(&b, "**Usage:** `%s <command> [subcommand] [options]`\n\n**Commands**\n", helpTop)
	for _, t := range childHelpTopics("") {
		fmt.Fprintf(&b, "- `%s` — %s\n", t.Path, t.Summary)
	}
	fmt.Fprintf(&b, "\nType `help` after any command for more detail, for example `%s config help`, then `%s config add help`.\n", helpTop, helpTop)
	return b.String()
}

func renderTopicHelp(t helpTopic) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**`%s %s`** — %s\n\n", helpTop, t.Path, t.Summary)
	fmt.Fprintf(&b, "**Usage:** `%s`\n", t.Usage)
	if t.Description != "" {
		b.WriteString("\n" + t.Description + "\n")
	}

	kids := childHelpTopics(t.Path)
	if len(kids) > 0 {
		b.WriteString("\n**Commands**\n")
		for _, k := range kids {
			fmt.Fprintf(&b, "- `%s` — %s\n", k.Usage, k.Summary)
		}
	}
	if len(t.Options) > 0 {
		b.WriteString("\n**Options**\n")
		for _, o := range t.Options {
			fmt.Fprintf(&b, "- `%s` — %s\n", o.Name, o.Desc)
		}
	}
	if t.Who != "" {
		fmt.Fprintf(&b, "\n**Who can run it:** %s\n", t.Who)
	}
	if len(t.Examples) > 0 {
		b.WriteString("\n**Examples**\n```\n" + strings.Join(t.Examples, "\n") + "\n```\n")
	}
	if len(kids) > 0 {
		fmt.Fprintf(&b, "\nType `%s %s <command> help` for one command.\n", helpTop, t.Path)
	}
	return b.String()
}

// sortTypeahead puts every hover in alphabetical order: subcommands at each
// level and the choices inside each static list. Lists made only of durations
// (the silence TTLs) go by length of time instead, so 30m comes before 1h and
// 4h before 24h. Argument positions are left alone because they are the order
// the words are typed.
func sortTypeahead(n *model.AutocompleteData) {
	byName := func(a, b string) int { return cmp.Compare(strings.ToLower(a), strings.ToLower(b)) }
	slices.SortStableFunc(n.SubCommands, func(a, b *model.AutocompleteData) int {
		return byName(a.Trigger, b.Trigger)
	})
	for _, arg := range n.Arguments {
		if list, ok := arg.Data.(*model.AutocompleteStaticListArg); ok {
			sortStaticList(list.PossibleArguments, byName)
		}
	}
	for _, c := range n.SubCommands {
		sortTypeahead(c)
	}
}

func sortStaticList(items []model.AutocompleteListItem, byName func(a, b string) int) {
	durations := make(map[string]time.Duration, len(items))
	for _, it := range items {
		d, err := time.ParseDuration(it.Item)
		if err != nil {
			slices.SortStableFunc(items, func(a, b model.AutocompleteListItem) int {
				return byName(a.Item, b.Item)
			})
			return
		}
		durations[it.Item] = d
	}
	slices.SortStableFunc(items, func(a, b model.AutocompleteListItem) int {
		return cmp.Compare(durations[a.Item], durations[b.Item])
	})
}
