package main

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/prometheus/alertmanager/api/v2/models"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

// shippedAlertRunbooks maps each alert in the sample rules to its runbook label.
var shippedAlertRunbooks = sync.OnceValue(func() map[string]string {
	raw, err := root.SamplesFS.ReadFile("samples/prometheus-rules.yaml")
	if err != nil {
		return map[string]string{}
	}
	out := map[string]string{}
	alert := ""
	for line := range strings.SplitSeq(string(raw), "\n") {
		if m := packAlertLine.FindStringSubmatch(line); m != nil {
			alert = m[1]
			continue
		}
		if m := runbookLabelLine.FindStringSubmatch(line); m != nil && alert != "" {
			out[alert] = m[1]
		}
	}
	return out
})

// packAlertSet is the pack's alert names as a set. It is empty if the embedded
// rules cannot be read.
func packAlertSet() map[string]bool {
	names, err := packAlertnames()
	if err != nil {
		return map[string]bool{}
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// channelAlertNames lists the alerts the channel's receivers are meant to carry:
// the shipped rules for its runbook receivers, plus the pack's alerts when it
// has a pack receiver. Custom receivers add nothing, since the plugin does not
// know which alerts they take.
func channelAlertNames(configs []alertConfig) []string {
	runbooks := map[string]bool{}
	for _, slug := range channelRunbookSlugs(configs) {
		runbooks[slug] = true
	}
	var names []string
	for alert, runbook := range shippedAlertRunbooks() {
		if runbooks[runbook] {
			names = append(names, alert)
		}
	}
	if len(receiversOfKind(configs, kindPack)) > 0 {
		for name := range packAlertSet() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return slices.Compact(names)
}

// receiversForAlert returns the receivers that carry the named alert. Empty
// means the alert is not in this channel's rule set.
func receiversForAlert(name string, configs []alertConfig) []alertConfig {
	runbook, shipped := shippedAlertRunbooks()[name]
	inPack := packAlertSet()[name]
	var out []alertConfig
	for _, c := range configs {
		switch c.resolvedKind() {
		case kindRunbook:
			if shipped && receiverBaseSlug(c.Name) == runbook {
				out = append(out, c)
			}
		case kindPack:
			if inPack {
				out = append(out, c)
			}
		case kindCustom:
		}
	}
	return out
}

func receiversOfKind(configs []alertConfig, kind receiverKind) []alertConfig {
	var out []alertConfig
	for _, c := range configs {
		if c.resolvedKind() == kind {
			out = append(out, c)
		}
	}
	return out
}

// silenceTouchesChannel reports whether any matcher of s selects one of the
// channel's runbooks or alert names. Silences on other labels are not shown, since
// the plugin cannot tell whether they hide this channel's alerts.
func silenceTouchesChannel(s *models.GettableSilence, runbooks, alerts []string) bool {
	if s == nil {
		return false
	}
	for _, m := range s.Matchers {
		if m == nil || (m.IsEqual != nil && !*m.IsEqual) {
			continue
		}
		var candidates []string
		switch derefStr(m.Name) {
		case "runbook":
			candidates = runbooks
		case "alertname":
			candidates = alerts
		default:
			continue
		}
		value := derefStr(m.Value)
		if m.IsRegex == nil || !*m.IsRegex {
			if slices.Contains(candidates, value) {
				return true
			}
			continue
		}
		if len(value) > maxSilenceRegexLen {
			continue
		}
		re, err := regexp.Compile("^(?:" + value + ")$")
		if err != nil {
			continue
		}
		if slices.ContainsFunc(candidates, re.MatchString) {
			return true
		}
	}
	return false
}

// maxSilenceRegexLen bounds the regexes compiled from Alertmanager's silence
// list, which anyone with access to that Alertmanager can write.
const maxSilenceRegexLen = 1024

func alertNotInChannelMessage(name string, configs []alertConfig) string {
	names := channelAlertNames(configs)
	if len(names) == 0 {
		return fmt.Sprintf("`%s` cannot be silenced here: this channel has no runbook or pack receivers, so the plugin does not know which alerts it carries.", name)
	}
	sample := names
	if len(sample) > 5 {
		sample = sample[:5]
	}
	return fmt.Sprintf("`%s` is not an alert this channel's receivers carry. This channel covers %d alert(s), for example `%s`. Group names also work.", name, len(names), strings.Join(sample, "`, `"))
}
