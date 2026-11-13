package main

import (
	"errors"
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
