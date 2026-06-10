// Package main is the Mattermost plugin entrypoint. ClientMain spawns the
// plugin process; the rest of the package lives across configuration.go,
// commands.go, etc.
package main

import "github.com/mattermost/mattermost/server/public/plugin"

func main() {
	plugin.ClientMain(&Plugin{})
}
