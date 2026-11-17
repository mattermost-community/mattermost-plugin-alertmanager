package main

import (
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// handleAlertsGroup serves the live view of Alertmanager. A bare `alerts` is
// the firing list, which is the command people type most.
func (p *Plugin) handleAlertsGroup(args *model.CommandArgs) (string, error) {
	fields := strings.Fields(args.Command)
	if len(fields) == 2 {
		return p.handleAlerts(args)
	}
	switch strings.ToLower(fields[2]) {
	case "firing":
		return p.handleAlerts(args)
	case "status":
		return p.handleStatus(args)
	default:
		return renderHelp([]string{"alerts"}), nil
	}
}

// handleSilenceGroup routes create, list, and expire. A TTL in the first
// position is accepted as `create`, so `silence 1h compute` still works.
func (p *Plugin) handleSilenceGroup(args *model.CommandArgs) (string, error) {
	fields := strings.Fields(args.Command)
	if len(fields) < 3 {
		return renderHelp([]string{"silence"}), nil
	}
	switch strings.ToLower(fields[2]) {
	case "list":
		return p.handleListSilences(args)
	case "expire":
		return p.handleExpireSilence(withoutParent(args))
	case "create":
		next := *args
		next.Command = strings.Join(append([]string{fields[0], fields[1]}, fields[3:]...), " ")
		return p.handleSilence(&next)
	default:
		return p.handleSilence(args)
	}
}

// handleAdmin groups the system-admin tools. Each handler still checks the
// caller's role, so the group adds no new access.
func (p *Plugin) handleAdmin(args *model.CommandArgs) (string, error) {
	fields := strings.Fields(args.Command)
	if len(fields) < 3 {
		return renderHelp([]string{"admin"}), nil
	}
	verbs := map[string]func(*model.CommandArgs) (string, error){
		"repair":        p.handleRepair,
		"acknowledge":   p.handleAcknowledge,
		"allowlist":     p.handleAllowlist,
		"metrics-token": p.handleMetricsToken,
	}
	run, ok := verbs[strings.ToLower(fields[2])]
	if !ok {
		return renderHelp([]string{"admin"}), nil
	}
	return run(withoutParent(args))
}
