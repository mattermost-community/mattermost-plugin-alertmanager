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

func init() {
	_ = json.NewDecoder(strings.NewReader(manifestString)).Decode(&Manifest)
}
