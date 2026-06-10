package root

import "embed"

// RunbooksFS embeds the contents of runbooks/ into the plugin binary so the
// scaffold slash command can iterate the canonical list at runtime. The
// list of receivers /alertmanager scaffold creates is whatever .md files
// are present in this directory at build time — drop a new one in, rebuild,
// the new runbook gets included automatically.

//go:embed runbooks/*.md
var RunbooksFS embed.FS
