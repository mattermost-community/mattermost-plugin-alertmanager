// Package root holds the embedded plugin manifest, mirroring the layout other
// Mattermost plugins use so build/manifest can extract id/version. Server code
// lives under server/; this file exists only to expose Manifest as a Go value.
package root

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

//go:embed plugin.json
var manifestString string

var Manifest model.Manifest

// RepoURL is the canonical GitHub URL for this plugin's source, used by
// Go code that needs to render links into the repository (slash commands,
// docs renderer, etc.). The default below is the upstream fallback —
// the Makefile overrides it at compile time via:
//
//	-ldflags "-X 'github.com/mattermost/mattermost-plugin-alertmanager.RepoURL=<resolved-url>'"
//
// where <resolved-url> comes from $GITHUB_REPOSITORY in CI or `git
// remote get-url origin` locally. Lets fork/move/rename of the repo
// flow into the binary without source edits.
//
// Kept as a separate var (not Manifest.HomepageURL) because the embedded
// manifest is parsed at init time from plugin.json source — which carries
// __PLUGIN_REPO_URL__ placeholders that get substituted only at bundle
// time, not at compile time. Using this var sidesteps the placeholder leak.
var RepoURL = "https://github.com/mattermost/mattermost-plugin-alertmanager"

func init() {
	_ = json.NewDecoder(strings.NewReader(manifestString)).Decode(&Manifest)
}
