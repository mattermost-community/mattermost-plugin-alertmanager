package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// formatVerbs are the words under `/alertmanager format`.
var formatVerbs = []string{"diff", "export", "rules"}

// isRulesTarget reports whether word names a group, a runbook slug, or `all`.
func isRulesTarget(word string) bool {
	_, _, err := resolveAddTarget(word)
	return err == nil
}

// parseFormatArgs reads the words after `format <verb>`: an optional
// `standard` or `crd`, an optional group or runbook name, and an optional
// `namespace=<ns>`. Returns a user-facing message when the words are not
// understood.
func parseFormatArgs(words []string) (exportOptions, string) {
	var opts exportOptions
	namespace, rest := extractFlagValue(words, "namespace=")
	if namespace != "" {
		if err := validateCRDNamespace(namespace); err != nil {
			return opts, fmt.Sprintf(":warning: Invalid `namespace`: %v", err)
		}
		opts.namespace = namespace
	}
	opts.mmNamespace, rest = extractFlagValue(rest, mmNamespaceFlag)
	opts.mmName, rest = extractFlagValue(rest, mmNameFlag)

	tooMany := false
	var unknown []string
	for _, word := range rest {
		lower := strings.ToLower(word)
		switch {
		case lower == formatStandard || lower == formatCRD:
			tooMany = tooMany || opts.format != ""
			opts.format = lower
		case isRulesTarget(lower):
			tooMany = tooMany || opts.target != ""
			if lower != "all" {
				opts.target = lower
			}
		default:
			unknown = append(unknown, word)
		}
	}
	if tooMany || len(unknown) > 1 {
		return opts, ":warning: Too many words. Use `standard` or `crd`, a group or runbook name, and optionally `namespace=<ns>`."
	}
	if len(unknown) == 1 {
		return opts, fmt.Sprintf(":warning: Unknown format or target `%s`. Use `standard` or `crd`, or a group such as `security`.", unknown[0])
	}
	if opts.target != mattermostGroup && (opts.mmNamespace != "" || opts.mmName != "") {
		return opts, fmt.Sprintf(":warning: `mm-namespace` and `mm-name` only apply to `%s`.", mattermostGroup)
	}
	return opts, ""
}

// handleFormatGroup routes `/alertmanager format <export|diff|rules>`.
//
//	format export [standard|crd] [namespace=<ns>]   this channel's config plus its rules
//	format diff [standard|crd]                      Alertmanager config only, against what is loaded
//	format rules [standard|crd] [namespace=<ns>]    the sample Prometheus rules only
//	format rules <group|slug> [standard|crd] [namespace=<ns>]
//	                                                one group's rules, or one runbook's
//	format rules mattermost [mm-namespace=<ns>] [mm-name=<cr>] [standard|crd] [namespace=<ns>]
//	                                                the mattermost group's rules, plus the operator's
//	                                                Mattermost rules when mm-namespace is given
func (p *Plugin) handleFormatGroup(args *model.CommandArgs) (string, error) {
	fields := strings.Fields(args.Command)
	if len(fields) < 3 {
		return renderHelp([]string{"format"}), nil
	}
	verb := strings.ToLower(fields[2])
	if !slices.Contains(formatVerbs, verb) {
		return renderHelp([]string{"format"}), nil
	}

	opts, msg := parseFormatArgs(fields[3:])
	if msg != "" {
		return msg, nil
	}
	if opts.target != "" && verb != "rules" {
		return fmt.Sprintf(":warning: `%s` only applies to `format rules`.", opts.target), nil
	}
	switch verb {
	case "export":
		return p.handleExport(args, opts)
	case "diff":
		opts.diff = true
		return p.handleExport(args, opts)
	default:
		return p.handleFormatRules(args, opts)
	}
}
