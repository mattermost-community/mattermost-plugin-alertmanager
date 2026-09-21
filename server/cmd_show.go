package main

import (
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// handleShow is the unified receiver inspection command. It dispatches to
// the appropriate existing handler based on its arguments:
//
//	/alertmanager show                           → list view (same as `list`)
//	/alertmanager show <name>                    → detail card (same as `config <name>`)
//	/alertmanager show export                    → DM channel config (same as `export`)
//	/alertmanager show export --format=crd       → DM CRD bundle
//	/alertmanager show export --namespace=x      → DM config in namespace x
//	/alertmanager show diff                      → diff against AM's loaded config
//
// The legacy flag forms (--export, --diff-against-loaded) still work so that
// bookmarked commands and muscle memory aren't broken. `config` and `export`
// remain as working aliases — show is an additive entry point, not a replacement.
func (p *Plugin) handleShow(args *model.CommandArgs) (string, error) {
	fields := strings.Fields(args.Command)
	rest := fields[2:]

	// Collect positional args (non-flag tokens) to identify a keyword or name.
	positional := make([]string, 0, len(rest))
	for _, a := range rest {
		if !strings.HasPrefix(a, "--") {
			positional = append(positional, a)
		}
	}

	keyword := ""
	if len(positional) > 0 {
		keyword = strings.ToLower(positional[0])
	}

	// "export" keyword OR --export flag → delegate to handleExport, forwarding
	// any --format= / --namespace= / --diff-against-loaded flags.
	if keyword == "export" || containsFlag(rest, "--export") {
		var exportCmd strings.Builder
		exportCmd.WriteString("/alertmanager export")
		if format, _ := extractFlagValue(rest, "--format="); format != "" {
			exportCmd.WriteString(" --format=" + format)
		}
		if ns, _ := extractFlagValue(rest, "--namespace="); ns != "" {
			exportCmd.WriteString(" --namespace=" + ns)
		}
		if containsFlag(rest, "--diff-against-loaded") {
			exportCmd.WriteString(" --diff-against-loaded")
		}
		newArgs := *args
		newArgs.Command = exportCmd.String()
		return p.handleExport(&newArgs)
	}

	if len(positional) == 0 {
		// No keyword or receiver name: show the channel receiver list.
		return p.handleList(args)
	}

	// "diff" keyword → export diffed against AM's loaded config.
	if keyword == "diff" {
		newArgs := *args
		newArgs.Command = "/alertmanager export --diff-against-loaded"
		return p.handleExport(&newArgs)
	}

	// Receiver name provided: show the detail card.
	// Rewrite the command string so handleConfig parses fields[2] as the name.
	newArgs := *args
	newArgs.Command = "/alertmanager config " + positional[0]
	return p.handleConfig(&newArgs)
}
