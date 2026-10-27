package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/mattermost/mattermost-plugin-alertmanager/server/alertmanager"
)

// silenceScope is what a TTL applies to. all is capped at 1h because it
// matches every runbook in the channel and is the easy one to leave on
// through a real incident. An alert or a group names the thing, so it
// may run out to 72h.
type silenceScope int

const (
	silenceScopeAlert silenceScope = iota
	silenceScopeGroup
	silenceScopeAll
)

const (
	silenceHalfHour = 30 * time.Minute
	silenceHour     = time.Hour
	silenceMaxHours = 72
)

// alertNamePattern is a Prometheus alertname. It rejects spaces and regex
// characters so the value can be an exact matcher.
var alertNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (p *Plugin) handleSilence(args *model.CommandArgs) (string, error) {
	if err := p.requireChannelTeamAdmin(args.UserId, args.ChannelId); err != nil {
		return err.Error(), nil
	}

	fields := strings.Fields(args.Command)
	if len(fields) != 4 {
		return silenceUsage(), nil
	}

	scope, target, err := classifySilenceTarget(fields[3])
	if err != nil {
		return ":warning: " + err.Error(), nil
	}
	// all matches every runbook receiver in the channel and quiets every
	// other channel on that Alertmanager. A team admin can mute one alert
	// or one group. The channel-wide mute stays with system admins, and
	// the 1h cap still applies.
	if scope == silenceScopeAll {
		if adminErr := p.requireSystemAdmin(args.UserId); adminErr != nil {
			return "Silencing every runbook in this channel (`all`) requires system_admin, and the TTL cannot be longer than 1h. A team admin can still silence one alert or one group.", nil
		}
	}
	duration, err := parseSilenceDuration(fields[2], scope)
	if err != nil {
		return silenceTimeRejected(fields[2], scope, err), nil
	}

	scoped := p.configsForCurrentChannel(args)
	if len(scoped) == 0 {
		return emptyScopeMessage("silence"), nil
	}

	relevant, matchers, err := silencePlan(scope, target, scoped)
	if err != nil {
		return ":warning: " + err.Error(), nil
	}

	actor := silenceActor(p, args.UserId)
	comment := fmt.Sprintf("Mattermost /alertmanager silence by %s for %s", actor, duration)
	endsAt := time.Now().Add(duration)

	var b strings.Builder
	fmt.Fprintf(&b, ":mute: Silencing **%s** for %s.\n\n", silenceTargetLabel(scope, target), duration)
	okCount := 0
	for _, g := range groupByAMURL(relevant) {
		id, cerr := alertmanager.CreateSilence(g.URL, g.User, g.Password, actor, comment, endsAt, matchers)
		if cerr != nil {
			p.auditLog("silence.create", args.UserId, target, args.ChannelId, "failure")
			fmt.Fprintf(&b, "- `%s` — :x: %v\n", g.URL, cerr)
			continue
		}
		okCount++
		p.auditLog("silence.create", args.UserId, target, args.ChannelId, "success")
		rec := silenceRecord{ChannelID: args.ChannelId, AMURL: g.URL, CreatedBy: args.UserId}
		if recErr := p.recordSilence(id, rec, duration); recErr != nil {
			p.API.LogWarn("Could not record silence for channel-scoped expire", "silence_id", id, "error", recErr.Error())
			fmt.Fprintf(&b, "- `%s` — silence `%s` until %s. It could not be recorded here, so end it early in Alertmanager.\n",
				g.URL, id, endsAt.UTC().Format(time.RFC3339))
			continue
		}
		fmt.Fprintf(&b, "- `%s` — silence `%s` until %s. End it early: `/alertmanager silence expire %s %s`\n",
			g.URL, id, endsAt.UTC().Format(time.RFC3339), g.Receivers[0].Name, id)
	}
	if okCount == 0 {
		return b.String(), nil
	}
	b.WriteString("\nAlertmanager drops matching alerts until that time. Other channels that receive the same alert on this Alertmanager go quiet too.\n")
	return b.String(), nil
}

func silenceUsage() string {
	return silenceAllowedTimes("")
}

// silenceTimeRejected is the ephemeral reply for a TTL the command will
// not apply. ExecuteCommand posts it in the channel where the command
// ran, and only the caller can see it.
func silenceTimeRejected(raw string, scope silenceScope, cause error) string {
	reason := cause.Error()
	if scope == silenceScopeAll {
		reason = fmt.Sprintf("`%s` was not applied for `all`. %s", raw, cause.Error())
	}
	return silenceAllowedTimes(reason)
}

func silenceAllowedTimes(reason string) string {
	var b strings.Builder
	if reason != "" {
		b.WriteString(":warning: ")
		b.WriteString(reason)
		b.WriteString("\n\n")
	}
	b.WriteString("`/alertmanager silence create <time> <alert|group|all>`\n\n")
	b.WriteString("Allowed times:\n")
	b.WriteString("- An alert (`HighCPUUsage`) or a group (`compute`): `30m`, or a whole hour from `1h` through `72h` (`2h`, `3h`, … `72h`). Team admin.\n")
	b.WriteString("- `all`: `30m` or `1h` only. System admin. Anything longer is rejected.\n\n")
	b.WriteString("`90m`, `45m`, `1.5h`, and anything over `72h` are rejected.\n")
	b.WriteString("A custom receiver has no runbook label, so a group or `all` does not match it. Silence that alert by its alert name.")
	return b.String()
}

func silenceTargetLabel(scope silenceScope, target string) string {
	switch scope {
	case silenceScopeAlert:
		return target
	case silenceScopeGroup:
		return target + " group"
	case silenceScopeAll:
		return "every runbook in this channel"
	default:
		return target
	}
}

// classifySilenceTarget accepts all, a scaffold group, or one alertname.
// all is checked before groups because scaffoldSets also has an all key,
// and that key means every runbook, which is the short TTL.
func classifySilenceTarget(raw string) (silenceScope, string, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, "", fmt.Errorf("name an alert, a group, or all")
	}
	lower := strings.ToLower(raw)
	if lower == "all" {
		return silenceScopeAll, "all", nil
	}
	if slugs, ok := scaffoldSets[lower]; ok && slugs != nil {
		return silenceScopeGroup, lower, nil
	}
	if !alertNamePattern.MatchString(raw) {
		return 0, "", fmt.Errorf("`%s` is not an alert name, a group, or all. Alert names look like HighCPUUsage. Groups: application, compute, database, mattermost, networking, observability, security, storage", raw)
	}
	return silenceScopeAlert, raw, nil
}

// parseSilenceDuration allows 30m or whole hours 1 through 72. all cannot
// exceed 1h. 90m, 45m, and fractional hours are rejected on purpose: the
// choices stay readable in the command history.
func parseSilenceDuration(raw string, scope silenceScope) (time.Duration, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return 0, fmt.Errorf("a time is required")
	}
	var d time.Duration
	switch s {
	case "30m", "30min", "30mins":
		d = silenceHalfHour
	default:
		hours, ok := parseWholeHours(s)
		if !ok {
			return 0, fmt.Errorf("`%s` is not an allowed time", raw)
		}
		if hours < 1 || hours > silenceMaxHours {
			return 0, fmt.Errorf("`%s` is outside 1h through 72h", raw)
		}
		d = time.Duration(hours) * silenceHour
	}
	if scope == silenceScopeAll && d > silenceHour {
		return 0, fmt.Errorf("`%s` is longer than the 1 hour cap on all", raw)
	}
	return d, nil
}

func parseWholeHours(s string) (int, bool) {
	for _, suffix := range []string{"hours", "hour", "hrs", "hr", "h"} {
		stem, ok := strings.CutSuffix(s, suffix)
		if !ok || stem == "" {
			continue
		}
		n, err := strconv.Atoi(stem)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// silencePlan picks the receivers whose Alertmanager should receive the
// silence, and the matchers. An alert matches alertname on every backend
// this channel uses. A group or all matches the runbook label, so alerts
// that have no runbook keep firing.
func silencePlan(scope silenceScope, target string, configs []alertConfig) ([]alertConfig, []alertmanager.SilenceMatcher, error) {
	switch scope {
	case silenceScopeAlert:
		return configs, []alertmanager.SilenceMatcher{{
			Name:  "alertname",
			Value: target,
		}}, nil
	case silenceScopeGroup:
		slugs := groupRunbooksInChannel(target, configs)
		if len(slugs) == 0 {
			return nil, nil, fmt.Errorf("no `%s` receivers are bound to this channel", target)
		}
		regex, err := runbookMatcher(slugs)
		if err != nil {
			return nil, nil, err
		}
		return filterByRunbooks(configs, slugs), []alertmanager.SilenceMatcher{{
			Name:  "runbook",
			Value: regex,
			Regex: true,
		}}, nil
	case silenceScopeAll:
		slugs := channelRunbookSlugs(configs)
		if len(slugs) == 0 {
			return nil, nil, fmt.Errorf("this channel has no runbook receivers to silence")
		}
		regex, err := runbookMatcher(slugs)
		if err != nil {
			return nil, nil, err
		}
		return filterByRunbooks(configs, slugs), []alertmanager.SilenceMatcher{{
			Name:  "runbook",
			Value: regex,
			Regex: true,
		}}, nil
	default:
		return nil, nil, fmt.Errorf("unhandled silence scope %d", scope)
	}
}

func groupRunbooksInChannel(group string, configs []alertConfig) []string {
	want := map[string]bool{}
	for _, slug := range scaffoldSets[group] {
		want[slug] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, slug := range channelRunbookSlugs(configs) {
		if want[slug] && !seen[slug] {
			seen[slug] = true
			out = append(out, slug)
		}
	}
	return out
}

func channelRunbookSlugs(configs []alertConfig) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range configs {
		if c.Custom {
			continue
		}
		slug := receiverBaseSlug(c.Name)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
	}
	return out
}

func filterByRunbooks(configs []alertConfig, slugs []string) []alertConfig {
	want := map[string]bool{}
	for _, slug := range slugs {
		want[slug] = true
	}
	out := make([]alertConfig, 0, len(configs))
	for _, c := range configs {
		if c.Custom {
			continue
		}
		if want[receiverBaseSlug(c.Name)] {
			out = append(out, c)
		}
	}
	return out
}

func runbookMatcher(slugs []string) (string, error) {
	if len(slugs) == 0 {
		return "", fmt.Errorf("no runbooks to silence")
	}
	parts := make([]string, len(slugs))
	for i, slug := range slugs {
		parts[i] = regexp.QuoteMeta(slug)
	}
	return "^(" + strings.Join(parts, "|") + ")$", nil
}

func silenceActor(p *Plugin, userID string) string {
	user, appErr := p.API.GetUser(userID)
	if appErr != nil || user == nil || user.Username == "" {
		return userID
	}
	return strings.Map(func(r rune) rune {
		if r < 32 || r == '`' {
			return -1
		}
		return r
	}, user.Username)
}
