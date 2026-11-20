package root

import "embed"

// RunbooksFS embeds the contents of runbooks/ into the plugin binary so the
// scaffold slash command can iterate the canonical list at runtime. The
// list of receivers /alertmanager scaffold creates is whatever .md files
// are present in this directory at build time — drop a new one in, rebuild,
// the new runbook gets included automatically.
//
// runbooks/mattermost-operator/ holds one page per Mattermost pack alert. Those
// are not receivers: the pack receiver links to them by alertname.

//go:embed runbooks/*.md runbooks/mattermost-operator/*.md
var RunbooksFS embed.FS
