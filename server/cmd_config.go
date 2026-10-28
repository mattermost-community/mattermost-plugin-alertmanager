package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// configKind is the read-only action /alertmanager config dispatches to when
// the first word is not one of the verbs in configVerbs.
type configKind int

const (
	configKindList configKind = iota
	configKindDetail
)

// parsedConfig is the positional part of /alertmanager config.
//
//	config                       → list this channel's receivers
//	config list                  → same
//	config <name>                → detail card for one receiver
//
// The channel is the scope. There is no group filter.
type parsedConfig struct {
	kind configKind
	name string
}

var errConfigUsage = errors.New("unrecognized config command")

// withoutParent drops the group word so a handler that predates the group sees
// the command it always parsed: `/alertmanager config add a b c` becomes
// `/alertmanager add a b c`.
func withoutParent(args *model.CommandArgs) *model.CommandArgs {
	fields := strings.Fields(args.Command)
	next := *args
	next.Command = strings.Join(append([]string{fields[0]}, fields[2:]...), " ")
	return &next
}

func (p *Plugin) handleConfigGroup(args *model.CommandArgs) (string, error) {
	fields := strings.Fields(args.Command)
	rest := fields[2:]

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
			return run(withoutParent(args))
		}
		if verb == "export" || verb == "diff" {
			return fmt.Sprintf(":warning: `config %s` moved. Run `/alertmanager format %s%s`.", verb, verb, formatTail(rest[1:])), nil
		}
	}

	parsed, err := parseConfigPositionals(rest)
	if errors.Is(err, errConfigUsage) {
		return renderHelp([]string{"config"}), nil
	}
	if err != nil {
		return err.Error(), nil
	}

	switch parsed.kind {
	case configKindList:
		return p.handleList(args)
	case configKindDetail:
		next := *args
		next.Command = "/alertmanager config " + parsed.name
		return p.handleConfig(&next)
	default:
		return "", fmt.Errorf("unhandled config kind %d", parsed.kind)
	}
}

// formatTail renders the old trailing words for a "moved" message.
func formatTail(words []string) string {
	if len(words) == 0 {
		return ""
	}
	return " " + strings.Join(words, " ")
}

func parseConfigPositionals(positional []string) (parsedConfig, error) {
	if len(positional) == 0 {
		return parsedConfig{kind: configKindList}, nil
	}
	switch first := strings.ToLower(positional[0]); first {
	case "list":
		return parsedConfig{kind: configKindList}, nil
	default:
		if len(positional) == 1 {
			return parsedConfig{kind: configKindDetail, name: positional[0]}, nil
		}
		return parsedConfig{}, errConfigUsage
	}
}
