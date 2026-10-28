package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// docTopics maps the old `/alertmanager docs <topic>` argument to its source
// file so a typed topic still lands on the right rendered page. Explicit map so
// embedded files can be renamed without breaking the argument vocabulary.
var docTopics = map[string]string{
	"alerts":         "ALERT_CATALOG.md",
	"alert-catalog":  "ALERT_CATALOG.md",
	"requirements":   "ALERT_REQUIREMENTS.md",
	"architecture":   "ARCHITECTURE.md",
	"compatibility":  "COMPATIBILITY.md",
	"configuration":  "CONFIGURATION.md",
	"development":    "DEVELOPMENT.md",
	"kubernetes":     "KUBERNETES.md",
	"rotation":       "ROTATION.md",
	"slash_commands": "SLASH_COMMANDS.md",
	"slash-commands": "SLASH_COMMANDS.md",
}

// handleDocs links to the rendered documentation site. Docs are no longer
// printed in chat. A topic argument is still accepted so old muscle memory and
// saved messages keep working, and it links straight to that page.
func (p *Plugin) handleDocs(args *model.CommandArgs) (string, error) {
	var b strings.Builder
	fields := strings.Fields(args.Command)
	if len(fields) >= 3 {
		if filename, ok := docTopics[strings.ToLower(fields[2])]; ok {
			fmt.Fprintf(&b, ":book: [%s](%s)\n\n", filename, p.helpPageURL(filename))
			b.WriteString("`/alertmanager docs <topic>` is deprecated. Docs are not printed in chat anymore. Open the site and use the sidebar.\n\n")
		}
	}
	fmt.Fprintf(&b, ":book: [Documentation](%s)\n", p.docsHomeURL())
	fmt.Fprintf(&b, ":notebook: [Runbook library](%s)\n", p.runbookLibraryURL())
	fmt.Fprintf(&b, ":chart_with_upwards_trend: [Sample Prometheus rules](%s)\n", p.helpPageURL("prometheus-rules.md"))
	return b.String(), nil
}

// helpPageURL is the on-server HTML page for an embedded doc. Mattermost
// does not linkify a bare docs/FILE.md path in a post.
func (p *Plugin) helpPageURL(filename string) string {
	slug := strings.ToLower(strings.TrimSuffix(filename, ".md"))
	return fmt.Sprintf("%s/plugins/%s/public/help/%s.html", p.siteURL(), Manifest.Id, slug)
}

// docsHomeURL is the documentation landing page. It is home.html because
// Mattermost's public file handler 404s a directory or index.html URL.
func (p *Plugin) docsHomeURL() string {
	return p.helpPageURL("home")
}

func (p *Plugin) runbookLibraryURL() string {
	return fmt.Sprintf("%s/plugins/%s/public/runbooks/home.html", p.siteURL(), Manifest.Id)
}
