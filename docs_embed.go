package root

import "embed"

// DocsFS embeds the contents of docs/ into the plugin binary so
// /alertmanager docs <topic> can serve them at runtime without needing
// the bundle's docs/ directory on disk.
//
// Keep all docs as plain markdown under docs/. The embed pattern matches
// docs/*.md exactly — nested directories are not picked up.

//go:embed docs/*.md
var DocsFS embed.FS
