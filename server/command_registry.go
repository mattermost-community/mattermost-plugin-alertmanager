package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// accessLevel is who may run a command. The registry checks it before the
// command runs, so a handler does not repeat the check. A rule that depends on
// the arguments, such as `silence create all` needing a system admin, stays in
// the handler.
type accessLevel int

const (
	// accessAnyone is any user who can run the slash command.
	accessAnyone accessLevel = iota
	// accessChannelAdmin is a system admin, or a team admin of the channel's team.
	accessChannelAdmin
	// accessSystemAdmin is a system admin.
	accessSystemAdmin
)

// Command is one runnable verb of /alertmanager.
type Command interface {
	Name() string
	// Authorize returns nil when the user may run the command in the channel,
	// otherwise a message for the user.
	Authorize(userID, channelID string) error
	// Run executes the command with args already rewritten for its handler.
	Run(args *model.CommandArgs) (string, error)
}

// verbCommand is the Command every verb uses: a name, an access level and a handler.
type verbCommand struct {
	p      *Plugin
	name   string
	access accessLevel
	run    func(*model.CommandArgs) (string, error)
}

func (c verbCommand) Name() string { return c.name }

func (c verbCommand) Authorize(userID, channelID string) error {
	switch c.access {
	case accessAnyone:
		return nil
	case accessChannelAdmin:
		return c.p.requireChannelTeamAdmin(userID, channelID)
	case accessSystemAdmin:
		return c.p.requireSystemAdmin(userID)
	default:
		return fmt.Errorf("command %q has an unknown access level %d", c.name, c.access)
	}
}

func (c verbCommand) Run(args *model.CommandArgs) (string, error) { return c.run(args) }

// parsedCommand is the one result of parsing a command line: the command to
// run and the arguments as its handler expects them. cmd is nil when the line
// needs no command, and reply is what to show instead.
type parsedCommand struct {
	cmd   Command
	args  *model.CommandArgs
	reply string
}

func (p *Plugin) newCommand(name string, access accessLevel, run func(*model.CommandArgs) (string, error)) verbCommand {
	return verbCommand{p: p, name: name, access: access, run: run}
}

func helpReply(path ...string) parsedCommand { return parsedCommand{reply: renderHelp(path)} }

// parseCommand turns `/alertmanager <group> [verb] ...` into a command. Group
// routing lives here only, so the access level of every verb is in this file.
func (p *Plugin) parseCommand(args *model.CommandArgs) parsedCommand {
	fields := strings.Fields(args.Command)
	group := fields[1]
	verb := ""
	if len(fields) > 2 {
		verb = strings.ToLower(fields[2])
	}

	switch group {
	case "about":
		return parsedCommand{cmd: p.newCommand("about", accessAnyone, func(a *model.CommandArgs) (string, error) { return p.handleAbout(a), nil }), args: args}
	case "admin":
		return p.parseAdmin(args, verb)
	case "alerts":
		return p.parseAlerts(args, verb, len(fields))
	case "config":
		return p.parseConfig(args, fields[2:])
	case "docs":
		return parsedCommand{cmd: p.newCommand("docs", accessAnyone, p.handleDocs), args: args}
	case "format":
		return p.parseFormat(args, verb)
	case "silence":
		return p.parseSilence(args, verb, len(fields))
	default:
		return parsedCommand{reply: fmt.Sprintf("Unknown subcommand `%s`. Run `/alertmanager help` for the list.", group)}
	}
}

func (p *Plugin) parseAdmin(args *model.CommandArgs, verb string) parsedCommand {
	verbs := map[string]func(*model.CommandArgs) (string, error){
		"repair":        p.handleRepair,
		"acknowledge":   p.handleAcknowledge,
		"allowlist":     p.handleAllowlist,
		"metrics-token": p.handleMetricsToken,
	}
	run, ok := verbs[verb]
	if !ok {
		return helpReply("admin")
	}
	return parsedCommand{cmd: p.newCommand("admin "+verb, accessSystemAdmin, run), args: withoutParent(args)}
}

// parseAlerts serves the live view of Alertmanager. A bare `alerts` is the
// firing list, which is the command people type most.
func (p *Plugin) parseAlerts(args *model.CommandArgs, verb string, words int) parsedCommand {
	if words == 2 || verb == "firing" {
		return parsedCommand{cmd: p.newCommand("alerts", accessAnyone, p.handleAlerts), args: args}
	}
	if verb == "status" {
		return parsedCommand{cmd: p.newCommand("alerts status", accessAnyone, p.handleStatus), args: args}
	}
	return helpReply("alerts")
}

// parseSilence routes create, list, and expire. A TTL in the first position is
// accepted as `create`, so `silence 1h compute` still works.
func (p *Plugin) parseSilence(args *model.CommandArgs, verb string, words int) parsedCommand {
	if words < 3 {
		return helpReply("silence")
	}
	switch verb {
	case "list":
		return parsedCommand{cmd: p.newCommand("silence list", accessAnyone, p.handleListSilences), args: args}
	case "expire":
		return parsedCommand{cmd: p.newCommand("silence expire", accessSystemAdmin, p.handleExpireSilence), args: withoutParent(args)}
	case "create":
		fields := strings.Fields(args.Command)
		next := *args
		next.Command = strings.Join(append([]string{fields[0], fields[1]}, fields[3:]...), " ")
		return parsedCommand{cmd: p.newCommand("silence create", accessSystemAdmin, p.handleSilence), args: &next}
	default:
		return parsedCommand{cmd: p.newCommand("silence create", accessSystemAdmin, p.handleSilence), args: args}
	}
}

// parseFormat gives each format verb its own access level. They share one
// handler because it parses the words they have in common.
func (p *Plugin) parseFormat(args *model.CommandArgs, verb string) parsedCommand {
	access := map[string]accessLevel{
		"export": accessChannelAdmin,
		"diff":   accessSystemAdmin,
		"rules":  accessChannelAdmin,
	}
	level, ok := access[verb]
	if !ok {
		return helpReply("format")
	}
	return parsedCommand{cmd: p.newCommand("format "+verb, level, p.handleFormatGroup), args: args}
}

func (p *Plugin) parseConfig(args *model.CommandArgs, rest []string) parsedCommand {
	verbs := map[string]func(*model.CommandArgs) (string, error){
		"add":        p.handleAdd,
		"add-custom": p.handleAddCustom,
		"remove":     p.handleRemove,
		"rotate":     p.handleRotate,
		"validate":   p.handleValidate,
	}
	if len(rest) > 0 {
		verb := strings.ToLower(rest[0])
		if run, ok := verbs[verb]; ok {
			return parsedCommand{cmd: p.newCommand("config "+verb, accessChannelAdmin, run), args: withoutParent(args)}
		}
		if verb == "export" || verb == "diff" {
			return parsedCommand{reply: fmt.Sprintf(":warning: `config %s` moved. Run `/alertmanager format %s%s`.", verb, verb, formatTail(rest[1:]))}
		}
	}

	parsed, err := parseConfigPositionals(rest)
	if errors.Is(err, errConfigUsage) {
		return helpReply("config")
	}
	if err != nil {
		return parsedCommand{reply: err.Error()}
	}

	switch parsed.kind {
	case configKindList:
		return parsedCommand{cmd: p.newCommand("config list", accessAnyone, p.handleList), args: args}
	case configKindDetail:
		next := *args
		next.Command = "/alertmanager config " + parsed.name
		return parsedCommand{cmd: p.newCommand("config detail", accessChannelAdmin, p.handleConfig), args: &next}
	default:
		return parsedCommand{reply: fmt.Sprintf("unhandled config kind %d", parsed.kind)}
	}
}
