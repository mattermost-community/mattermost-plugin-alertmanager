package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	addOptionsPath   = "/autocomplete/add-options"
	helpTopicsPath   = "/autocomplete/help-topics"
	rulesOptionsPath = "/autocomplete/rules-options"
)

// Mattermost lists the next suggestion of every optional argument together, so
// the trailing options of `config add` are required dynamic lists that all ask
// the same handler. Each call returns one option at a time, in order.
const (
	// A free-typed slug, format, mm-namespace and `on`, plus room for namespace=
	// and webhook-host=.
	addOptionSlots       = 6
	addCustomOptionSlots = 4
	helpPathSlots        = 3
	// A group, a format, mm-namespace and namespace=, with one to spare.
	rulesOptionSlots = 5
)

func addOptionsFetchURL() string {
	return fmt.Sprintf("/plugins/%s%s", Manifest.Id, addOptionsPath)
}

func rulesOptionsFetchURL() string {
	return fmt.Sprintf("/plugins/%s%s", Manifest.Id, rulesOptionsPath)
}

func helpTopicsFetchURL() string {
	return fmt.Sprintf("/plugins/%s%s", Manifest.Id, helpTopicsPath)
}

func formatItems() []model.AutocompleteListItem {
	return []model.AutocompleteListItem{
		{Item: formatCRD, HelpText: "Prometheus Operator objects (an AlertmanagerConfig and a Secret, plus a PrometheusRule for any runbooks) to kubectl apply. Use this with kube-prometheus-stack. namespace= sets where they go, default monitoring."},
		{Item: formatStandard, HelpText: "Plain alertmanager.yml receivers and routes. Paste into a file-based Alertmanager (config file, Docker, VM)."},
	}
}

func rotationItem(cmd string) model.AutocompleteListItem {
	if cmd == "add-custom" {
		return model.AutocompleteListItem{Item: "on", HelpText: "Remind sysadmins to rotate this webhook once it is older than System Console → WebhookRotationDays. Without it, this receiver is never reminded."}
	}
	return model.AutocompleteListItem{Item: "on", HelpText: "Remind sysadmins to rotate these webhooks once they are older than System Console → WebhookRotationDays. Without it, these receivers are never reminded. Applies to this channel only."}
}

func mmNamespaceItem(value string) model.AutocompleteListItem {
	return model.AutocompleteListItem{
		Item:     value,
		Hint:     "<namespace>",
		HelpText: "Optional. The namespace your Mattermost runs in, for example mm-namespace=mattermost. Adds the alerts the Mattermost Operator generates, with their Prometheus rules.",
	}
}

func isMMNamespaceWord(word string) bool {
	return strings.HasPrefix(strings.ToLower(word), mmNamespaceFlag)
}

type optionGroup struct {
	items []model.AutocompleteListItem
	has   func(word string) bool
}

// addOptionGroups lists the trailing options of `config add` and
// `config add-custom` in the order the hover offers them.
func addOptionGroups(cmd string, mattermost bool) []optionGroup {
	groups := []optionGroup{{
		items: formatItems(),
		has:   func(w string) bool { return strings.EqualFold(w, formatStandard) || strings.EqualFold(w, formatCRD) },
	}}
	if mattermost {
		groups = append(groups, optionGroup{
			items: []model.AutocompleteListItem{mmNamespaceItem(mmNamespaceFlag)},
			has:   isMMNamespaceWord,
		})
	}
	return append(groups, optionGroup{
		items: []model.AutocompleteListItem{rotationItem(cmd)},
		has:   func(w string) bool { return strings.EqualFold(w, "on") },
	})
}

// Autocomplete requests carry text the user typed, and any signed-in user can
// call these endpoints directly. Real command lines are far shorter than these.
const (
	maxAutocompleteInput = 2048
	maxAutocompleteWords = 32
)

// commandWords splits what Mattermost has parsed so far into words, without the
// leading slash. ok is false when it is not this plugin's command.
func commandWords(parsed string) (words []string, ok bool) {
	if len(parsed) > maxAutocompleteInput {
		return nil, false
	}
	words = strings.Fields(parsed)
	if len(words) == 0 || len(words) > maxAutocompleteWords || !strings.EqualFold(strings.TrimPrefix(words[0], "/"), commandTrigger) {
		return nil, false
	}
	return words, true
}

// typedWord is the word the user is on now: whatever follows the parsed text.
// complete is true once a space follows it.
func typedWord(parsed, userInput string) (word string, complete bool) {
	typed, _ := strings.CutPrefix(userInput, parsed)
	word, _, complete = strings.Cut(typed, " ")
	return word, complete
}

// addOptionItems returns the next option of `config add` or `config add-custom`:
// format, then mm-namespace (mattermost target only), then `on`. Once the user
// starts typing a word, every option not yet used is returned so Mattermost can
// match it by prefix, which lets them skip an option or type them in any order.
func addOptionItems(parsed, userInput string) []model.AutocompleteListItem {
	empty := []model.AutocompleteListItem{}
	words, ok := commandWords(parsed)
	// words: trigger, config, add|add-custom, <am-url>, ...
	if !ok || len(words) < 4 || !strings.EqualFold(words[1], "config") {
		return empty
	}
	cmd := strings.ToLower(words[2])
	if cmd != "add" && cmd != "add-custom" {
		return empty
	}
	args := words[3:]
	mattermost := cmd == "add" && slices.ContainsFunc(args, func(w string) bool { return strings.EqualFold(w, mattermostGroup) })

	var remaining []optionGroup
	for _, g := range addOptionGroups(cmd, mattermost) {
		if !slices.ContainsFunc(args, g.has) {
			remaining = append(remaining, g)
		}
	}

	cur, complete := typedWord(parsed, userInput)
	if cur == "" {
		// Straight after the URL the user is choosing a target, not an option.
		if len(remaining) == 0 || (cmd == "add" && len(args) == 1) {
			return empty
		}
		return remaining[0].items
	}

	var items []model.AutocompleteListItem
	for _, g := range remaining {
		for _, it := range g.items {
			if it.Item == mmNamespaceFlag && isMMNamespaceWord(cur) {
				it = mmNamespaceItem(cur)
			}
			items = append(items, it)
		}
	}
	// A finished flag such as namespace=monitoring is not offered anywhere, but
	// Mattermost only moves on to the next option once something consumes it.
	known := slices.ContainsFunc(items, func(it model.AutocompleteListItem) bool { return strings.EqualFold(it.Item, cur) })
	if complete && !known && (strings.Contains(cur, "=") || isFreeTarget(cmd, args, cur)) {
		items = append(items, model.AutocompleteListItem{Item: cur})
	}
	if items == nil {
		return empty
	}
	return items
}

// isFreeTarget reports whether cur is a runbook slug typed in place of a group.
// The target list only knows the groups, so without this a typed slug would stop
// the options from showing.
func isFreeTarget(cmd string, args []string, cur string) bool {
	return cmd == "add" && len(args) == 1 && !strings.EqualFold(cur, "all") && !isKnownSet(strings.ToLower(cur))
}

// helpTopicItems returns the commands one level below the help path typed so
// far, so `help config` offers add, list, remove and the rest.
func helpTopicItems(parsed string) []model.AutocompleteListItem {
	items := []model.AutocompleteListItem{}
	words, ok := commandWords(parsed)
	if !ok || len(words) < 2 || !strings.EqualFold(words[1], "help") {
		return items
	}
	tokens := words[2:]
	path, consumed := resolveHelpPath(tokens)
	if consumed < len(tokens) {
		return items
	}
	for _, child := range childHelpTopics(path) {
		name := child.Path[strings.LastIndex(child.Path, " ")+1:]
		if strings.HasPrefix(name, "<") {
			continue
		}
		items = append(items, model.AutocompleteListItem{Item: name, HelpText: child.Summary})
	}
	return items
}

func (p *Plugin) handleAutocompleteAddOptions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p.writeAutocompleteItems(w, "add options", addOptionItems(q.Get("parsed"), q.Get("user_input")))
}

func (p *Plugin) handleAutocompleteHelpTopics(w http.ResponseWriter, r *http.Request) {
	p.writeAutocompleteItems(w, "help topics", helpTopicItems(r.URL.Query().Get("parsed")))
}

func (p *Plugin) writeAutocompleteItems(w http.ResponseWriter, what string, items []model.AutocompleteListItem) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(items); err != nil {
		p.API.LogWarn("autocomplete: could not write "+what, "err", err.Error())
	}
}

func helpWordItem() model.AutocompleteListItem {
	return model.AutocompleteListItem{Item: "help", HelpText: "Show help for this command: its options, who can run it, and examples."}
}

// addHelpWord lets a command take a trailing `help`, which the server already
// accepts (`config add help` is the same as `help config add`). Commands whose
// first argument is free text are left alone, since Mattermost would not offer
// a list there.
func addHelpWord(n *model.AutocompleteData) {
	helpItem := helpWordItem()
	for _, c := range n.SubCommands {
		addHelpWord(c)
	}
	if len(n.SubCommands) > 0 || n.Trigger == "help" || n.Trigger == commandTrigger {
		return
	}
	if len(n.Arguments) == 0 {
		n.AddStaticListArgument("", false, []model.AutocompleteListItem{helpItem})
		return
	}
	list, ok := n.Arguments[0].Data.(*model.AutocompleteStaticListArg)
	if !ok || len(list.PossibleArguments) == 0 {
		return
	}
	// A duration list is sorted by length of time, and a word would break that.
	if _, err := time.ParseDuration(list.PossibleArguments[0].Item); err == nil {
		return
	}
	list.PossibleArguments = append(list.PossibleArguments, helpItem)
}

func rulesFormatItems() []model.AutocompleteListItem {
	return []model.AutocompleteListItem{
		{Item: formatCRD, HelpText: "DM a ready-to-apply PrometheusRule (monitoring.coreos.com/v1) for Prometheus Operator. namespace=<ns> sets where it goes, default monitoring."},
		{Item: formatStandard, HelpText: "Plain Prometheus rules file. With no group, links to browse or download the 36 sample rules."},
	}
}

func rulesGroupItems() []model.AutocompleteListItem {
	var items []model.AutocompleteListItem
	for _, name := range scaffoldSetNames() {
		help := "DM only the " + name + " group's rules."
		switch name {
		case "all":
			help = "Every group's rules, all 36. Same as leaving the group out."
		case mattermostGroup:
			help = "DM the mattermost group's rules. Add mm-namespace=<ns> to include the 13 rules the Mattermost Operator generates for that install, plus mm-db-replicas=true and mm-postgres-pod=true for the optional ones."
		}
		items = append(items, model.AutocompleteListItem{Item: name, HelpText: help})
	}
	return items
}

// rulesOptionItems returns the next option of `format rules`: a group, then
// mm-namespace for the mattermost group, then the format last. As with `config add`, a
// word being typed gets every option not yet used so Mattermost can match it by
// prefix.
func rulesOptionItems(parsed, userInput string) []model.AutocompleteListItem {
	empty := []model.AutocompleteListItem{}
	words, ok := commandWords(parsed)
	// words: trigger, format, rules, ...
	if !ok || len(words) < 3 || !strings.EqualFold(words[1], "format") || !strings.EqualFold(words[2], "rules") {
		return empty
	}
	args := words[3:]
	isFormat := func(w string) bool { return strings.EqualFold(w, formatStandard) || strings.EqualFold(w, formatCRD) }
	hasFormat := slices.ContainsFunc(args, isFormat)
	targetIdx := slices.IndexFunc(args, func(w string) bool { return !isFormat(w) && isRulesTarget(strings.ToLower(w)) })
	hasMM := slices.ContainsFunc(args, isMMNamespaceWord)

	var remaining [][]model.AutocompleteListItem
	if targetIdx < 0 {
		groups := rulesGroupItems()
		if len(args) == 0 {
			groups = append(groups, helpWordItem())
		}
		remaining = append(remaining, groups)
	}
	if targetIdx >= 0 && strings.EqualFold(args[targetIdx], mattermostGroup) && !hasMM {
		remaining = append(remaining, []model.AutocompleteListItem{mmNamespaceItem(mmNamespaceFlag)})
	}
	if !hasFormat {
		remaining = append(remaining, rulesFormatItems())
	}

	cur, complete := typedWord(parsed, userInput)
	if cur == "" {
		if len(remaining) == 0 {
			return empty
		}
		return remaining[0]
	}

	var items []model.AutocompleteListItem
	for _, group := range remaining {
		for _, it := range group {
			if it.Item == mmNamespaceFlag && isMMNamespaceWord(cur) {
				it = mmNamespaceItem(cur)
			}
			items = append(items, it)
		}
	}
	// A finished flag such as namespace=monitoring, or a runbook slug in place of
	// a group, is not in any list, but Mattermost only moves on once it is read.
	known := slices.ContainsFunc(items, func(it model.AutocompleteListItem) bool { return strings.EqualFold(it.Item, cur) })
	if complete && !known && (strings.Contains(cur, "=") || (targetIdx < 0 && isRulesTarget(strings.ToLower(cur)))) {
		items = append(items, model.AutocompleteListItem{Item: cur})
	}
	if items == nil {
		return empty
	}
	return items
}

func (p *Plugin) handleAutocompleteRulesOptions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p.writeAutocompleteItems(w, "rules options", rulesOptionItems(q.Get("parsed"), q.Get("user_input")))
}
