package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hako/durafmt"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/prometheus/alertmanager/alert"
	"github.com/prometheus/alertmanager/api/v2/models"

	"github.com/mattermost/mattermost-plugin-alertmanager/server/alertmanager"
)

// derefStr returns *p, or "" if p is nil. Used throughout the silence
// formatting code because the swagger-generated GettableSilence model
// uses pointer fields for all required string properties (id, comment,
// createdBy, matcher.name/value, status.state).
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Channel-scoping is the load-bearing UX decision in this file. Every
// query command (status, alerts, silences, expire_silence) filters the
// registered configs to those bound to the channel the slash command was
// invoked from. Rationale: a user running /alertmanager status in
// #db-alerts probably wants the DB-team Alertmanager's status, not every
// AM the org has ever wired up.
//
// Sysadmin scoping vs channel scoping are independent:
//   - Mutating commands (add/remove/rotate) require sysadmin and can
//     reference any config by name.
//   - View commands here use channel context for filtering, are open to
//     all users (anyone in the channel sees the same Alertmanager state
//     anyone else does).

// configsForCurrentChannel returns the registered configs whose Channel
// field matches the slash command's invocation channel. Empty slice
// means "this channel has no associated configs."
func (p *Plugin) configsForCurrentChannel(args *model.CommandArgs) []alertConfig {
	channel, appErr := p.API.GetChannel(args.ChannelId)
	if appErr != nil {
		// Resolving the channel failed — fall back to "no matches" so the
		// command doesn't accidentally leak global state.
		p.API.LogWarn("could not resolve current channel for scoping",
			"channelID", args.ChannelId, "err", appErr.Error())
		return nil
	}

	// Team-scope as well as channel-scope: channel names are unique only per
	// team (`town-square` exists in every team), so matching on channel name
	// alone would leak — and let commands like `remove all` act on — another
	// team's receivers that happen to share this channel's name.
	team, appErr := p.API.GetTeam(channel.TeamId)
	if appErr != nil {
		p.API.LogWarn("could not resolve current team for scoping",
			"teamID", channel.TeamId, "err", appErr.Error())
		return nil
	}

	all := p.getConfiguration().AlertConfigs
	matched := make([]alertConfig, 0, len(all))
	for _, c := range all {
		if c.Team == team.Name && c.Channel == channel.Name {
			matched = append(matched, c)
		}
	}
	return matched
}

// emptyScopeMessage is the user-visible explanation when a channel has
// no configs. Suggests the next action. Intentionally does NOT advertise
// any cross-channel listing — channel scoping is absolute for the slash
// commands, cross-channel inventory is System Console only.
func emptyScopeMessage(verb string) string {
	return fmt.Sprintf(
		"No Alertmanager receivers are configured for this channel — `%s` has nothing to operate on.\n\n"+
			"Create the canonical set of receivers here: `/alertmanager add <team> <channel> <am-url> [set]` (sysadmin).",
		verb,
	)
}

// groupByAMURL deduplicates receivers by their Alertmanager URL. Each
// entry in the returned slice holds one AM URL and the list of
// receivers bound to it. Order is stable based on first appearance of
// each URL in the input so output is deterministic across calls.
//
// Why this matters: in the common case where one channel hosts N
// receivers all pointing at the same Alertmanager (e.g. all 30 canonical
// runbook receivers in #alerts), the older per-receiver loop hit AM N
// times and printed N copies of the same "firing alerts" or "loaded
// silences" block. Querying once per distinct AM URL collapses that to
// a single section per backend with the receiver list rolled up.
func groupByAMURL(configs []alertConfig) []amGroup {
	idx := make(map[string]int, len(configs))
	out := make([]amGroup, 0, len(configs))
	for _, ac := range configs {
		if i, ok := idx[ac.AlertManagerURL]; ok {
			out[i].Receivers = append(out[i].Receivers, ac)
			continue
		}
		idx[ac.AlertManagerURL] = len(out)
		out = append(out, amGroup{
			URL:       ac.AlertManagerURL,
			User:      ac.User,
			Password:  ac.Password,
			Receivers: []alertConfig{ac},
		})
	}
	return out
}

// amGroup is one Alertmanager URL plus the receivers bound to it. User
// and Password are taken from the first receiver in the group — basic
// auth credentials are an AM-level setting so all receivers sharing a
// URL share creds. If they don't, that's a config bug; first-wins is
// the predictable behavior.
type amGroup struct {
	URL       string
	User      string
	Password  string
	Receivers []alertConfig
}

// receiverNames returns the receivers' Name fields formatted as inline
// code, joined by commas — used in section headers showing which
// receivers are bound to a given Alertmanager.
func (g amGroup) receiverNames() string {
	names := make([]string, 0, len(g.Receivers))
	for _, r := range g.Receivers {
		names = append(names, "`"+r.Name+"`")
	}
	return strings.Join(names, ", ")
}

// handleStatus reports each unique Alertmanager backend's version and
// uptime for receivers bound to the current channel. Queries each AM
// once even when multiple receivers point at it.
func (p *Plugin) handleStatus(args *model.CommandArgs) (string, error) {
	scoped := p.configsForCurrentChannel(args)
	if len(scoped) == 0 {
		return emptyScopeMessage("status"), nil
	}

	groups := groupByAMURL(scoped)
	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"**Alertmanager status — %d receiver(s) across %d backend(s):**\n\n",
		len(scoped), len(groups),
	))
	for _, g := range groups {
		status, err := alertmanager.Status(g.URL, g.User, g.Password)
		if err != nil {
			b.WriteString(fmt.Sprintf(
				"- %s → :x: failed to reach: `%v`\n  Bound receivers: %s\n\n",
				g.URL, err, g.receiverNames(),
			))
			continue
		}
		uptime := durafmt.Parse(time.Since(status.Uptime)).LimitFirstN(2).String()
		b.WriteString(fmt.Sprintf(
			"- %s → :white_check_mark: version `%s`, uptime %s\n  Bound receivers (%d): %s\n\n",
			g.URL, status.VersionInfo.Version, uptime, len(g.Receivers), g.receiverNames(),
		))
	}
	return b.String(), nil
}

// handleAlerts lists currently-firing alerts grouped by AM URL. Queries
// each distinct AM once — without the dedup, 20 receivers pointing at
// the same AM would render 20 copies of the same alert list.
func (p *Plugin) handleAlerts(args *model.CommandArgs) (string, error) {
	scoped := p.configsForCurrentChannel(args)
	if len(scoped) == 0 {
		return emptyScopeMessage("alerts"), nil
	}

	groups := groupByAMURL(scoped)
	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"**Firing alerts — %d receiver(s) across %d backend(s):**\n\n",
		len(scoped), len(groups),
	))
	totalCount := 0
	for _, g := range groups {
		alerts, err := alertmanager.ListAlerts(g.URL, g.User, g.Password)
		if err != nil {
			b.WriteString(fmt.Sprintf(
				"**Alertmanager %s** — :x: failed to query: `%v`\n  Bound receivers: %s\n\n",
				g.URL, err, g.receiverNames(),
			))
			continue
		}
		if len(alerts) == 0 {
			b.WriteString(fmt.Sprintf(
				"**Alertmanager %s** — :tada: no active alerts\n  Bound receivers (%d): %s\n\n",
				g.URL, len(g.Receivers), g.receiverNames(),
			))
			continue
		}
		totalCount += len(alerts)
		b.WriteString(fmt.Sprintf(
			"**Alertmanager %s — %d alert(s)**\n  Bound receivers (%d): %s\n\n",
			g.URL, len(alerts), len(g.Receivers), g.receiverNames(),
		))
		for _, a := range alerts {
			b.WriteString(formatAlertLine(a))
		}
		b.WriteString("\n")
	}

	if totalCount == 0 {
		return b.String() + "_No active alerts across this channel's Alertmanagers._", nil
	}
	return b.String(), nil
}

// formatAlertLine renders one alert as a one-liner — keeps output dense
// for channels that might have a lot of alerts firing at once.
func formatAlertLine(a *alert.Alert) string {
	status := string(a.Status())
	severity := ""
	if v, ok := a.Labels["severity"]; ok {
		severity = string(v)
	}
	summary := ""
	if v, ok := a.Annotations["summary"]; ok {
		summary = string(v)
	}
	resolved := strconv.FormatBool(a.Resolved())
	return fmt.Sprintf("- **%s** [%s] severity=`%s` resolved=`%s` — %s\n",
		a.Name(), status, severity, resolved, summary)
}

// handleListSilences lists active silences for receivers in the current
// channel. Per-silence Expire actions aren't wired in v1.0 — those need
// the legacy interactive-post-action path which we no longer have since
// the plugin doesn't post directly anymore. v1.1: re-evaluate whether
// we can attach interactive elements to ephemeral responses.
func (p *Plugin) handleListSilences(args *model.CommandArgs) (string, error) {
	scoped := p.configsForCurrentChannel(args)
	if len(scoped) == 0 {
		return emptyScopeMessage("silences"), nil
	}

	groups := groupByAMURL(scoped)
	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"**Active silences — %d receiver(s) across %d backend(s):**\n\n",
		len(scoped), len(groups),
	))
	totalActive := 0
	for _, g := range groups {
		silences, err := alertmanager.ListSilences(g.URL, g.User, g.Password)
		if err != nil {
			b.WriteString(fmt.Sprintf(
				"**Alertmanager %s** — :x: failed to query: `%v`\n  Bound receivers: %s\n\n",
				g.URL, err, g.receiverNames(),
			))
			continue
		}
		active := make([]*models.GettableSilence, 0, len(silences))
		for _, s := range silences {
			// Status + State are pointer fields in the v2 model.
			// Treat a missing Status as "not expired" so a malformed
			// response doesn't silently drop entries.
			state := ""
			if s != nil && s.Status != nil {
				state = derefStr(s.Status.State)
			}
			if state != "expired" {
				active = append(active, s)
			}
		}
		if len(active) == 0 {
			b.WriteString(fmt.Sprintf(
				"**Alertmanager %s** — no active silences\n  Bound receivers (%d): %s\n\n",
				g.URL, len(g.Receivers), g.receiverNames(),
			))
			continue
		}
		totalActive += len(active)
		b.WriteString(fmt.Sprintf(
			"**Alertmanager %s — %d active silence(s)**\n  Bound receivers (%d): %s\n\n",
			g.URL, len(active), len(g.Receivers), g.receiverNames(),
		))
		// expire_silence still requires a receiver name (channel-scope
		// check), so pick the first receiver in the group as the handle
		// for any expire commands shown in this section.
		expireHandle := g.Receivers[0].Name
		for _, s := range active {
			b.WriteString(formatSilenceLine(expireHandle, s))
		}
		b.WriteString("\n")
	}

	if totalActive == 0 {
		return b.String() + "_No active silences across this channel's Alertmanagers._", nil
	}
	return b.String(), nil
}

// formatSilenceLine renders one silence as a few lines, including the
// `/alertmanager expire_silence <name> <silence-id>` invocation users
// would run to expire it.
//
// Every relevant field in models.GettableSilence is a pointer (swagger's
// "required" convention). derefStr + time conversions guard against
// malformed responses by treating nil as zero value.
func formatSilenceLine(configName string, s *models.GettableSilence) string {
	if s == nil {
		return ""
	}
	var matchers []string
	for _, m := range s.Matchers {
		if m == nil {
			continue
		}
		matchers = append(matchers, fmt.Sprintf("`%s=%q`", derefStr(m.Name), derefStr(m.Value)))
	}
	var endsAt, startsAt time.Time
	if s.EndsAt != nil {
		endsAt = time.Time(*s.EndsAt)
	}
	if s.StartsAt != nil {
		startsAt = time.Time(*s.StartsAt)
	}
	endsIn := durafmt.Parse(time.Until(endsAt)).LimitFirstN(2).String()
	id := derefStr(s.ID)
	return fmt.Sprintf(
		"- **ID:** `%s`\n  **By:** %s • **Created:** %s ago • **Ends in:** %s\n  **Matchers:** %s\n  **Comment:** %s\n  **Expire:** `/alertmanager expire_silence %s %s`\n\n",
		id, derefStr(s.CreatedBy),
		durafmt.Parse(time.Since(startsAt)).LimitFirstN(2).String(),
		endsIn,
		strings.Join(matchers, " "),
		derefStr(s.Comment),
		configName, id,
	)
}

// handleExpireSilence expires a silence on the named receiver's
// Alertmanager. Channel-scoped — the receiver must be associated with
// the current channel (a user in #web shouldn't be able to expire a
// silence on the DB team's Alertmanager just by knowing the name).
//
// Sysadmins can bypass channel scoping by running this from a channel
// associated with the receiver, or by using the unscoped form once we
// add it (future v1.x).
func (p *Plugin) handleExpireSilence(args *model.CommandArgs) (string, error) {
	fields := strings.Fields(args.Command)
	if len(fields) != 4 {
		return "Usage: `/alertmanager expire_silence <name> <silence-id>`", nil
	}
	name, silenceID := fields[2], fields[3]

	scoped := p.configsForCurrentChannel(args)
	var match *alertConfig
	for i := range scoped {
		if scoped[i].Name == name {
			match = &scoped[i]
			break
		}
	}
	if match == nil {
		return fmt.Sprintf(
			"Receiver `%s` is not associated with this channel. Run this command from the channel where the receiver was registered, or use `/alertmanager configs` here to see what's available.",
			name,
		), nil
	}

	if err := alertmanager.ExpireSilence(silenceID, match.AlertManagerURL, match.User, match.Password); err != nil {
		return fmt.Sprintf("Failed to expire silence `%s` on `%s`: %v", silenceID, name, err), nil
	}
	return fmt.Sprintf(":mute: Silence `%s` expired on `%s`.", silenceID, name), nil
}
