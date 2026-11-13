package main

import (
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"sync"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

// scaffoldSet maps a set name to the slugs it includes. `all` covers
// every embedded runbook (resolved at runtime); the category names
// scope to subsets. Adding a new set means adding an entry here.
//
// The category boundaries match the headings in runbooks/INDEX.md.
var scaffoldSets = map[string][]string{
	"all": nil, // resolved at runtime to the full embedded list
	"compute": {
		"high-cpu-usage",
		"high-memory-usage",
		"pod-crashloopbackoff",
		"pod-not-ready",
		"deployment-replicas-unavailable",
		"node-not-ready",
		"cpu-throttling-high",
		"image-pull-backoff",
		"pods-unschedulable",
	},
	"application": {
		"high-http-error-rate",
		"high-api-latency",
		"service-endpoint-down",
		"request-rate-anomaly",
	},
	"database": {
		"database-connectivity-loss",
		"database-replication-lag",
		"database-high-latency",
		"postgres-connections-near-max",
	},
	"storage": {
		"persistent-volume-full",
		"disk-fill-rate-high",
	},
	"networking": {
		"ingress-high-5xx",
		"certificate-expiring-soon",
		"dns-resolution-failure",
	},
	"observability": {
		"prometheus-scrape-target-down",
		"alertmanager-notification-failure",
	},
	"security": {
		"unexpected-container-image",
		"apiserver-auth-failure-spike",
		"privileged-container-started",
		"interactive-shell-in-container",
		"rbac-privilege-escalation",
		"security-tooling-down",
	},
	"mattermost": {
		"mattermost-api-high-latency",
		"mattermost-high-login-failures",
		"mattermost-websocket-drop",
		"mattermost-db-slow-query",
		"mattermost-high-error-rate",
	},
}

// scaffoldSetNames returns the sorted list of known set names for help
// text and error messages. `all` is the canonical "full set" name and
// is listed first; categories follow alphabetically.
func scaffoldSetNames() []string {
	names := []string{"all"}
	for k := range scaffoldSets {
		if k == "all" {
			continue
		}
		names = append(names, k)
	}
	sort.Strings(names[1:])
	return names
}

// runbookSlugs returns the shipped runbook slugs in alphabetical order. The
// embedded directory never changes at runtime, so it is read once. Callers get
// their own copy and may modify it.
func runbookSlugs() []string {
	return slices.Clone(shippedRunbookSlugs())
}

var shippedRunbookSlugs = sync.OnceValue(loadRunbookSlugs)

// loadRunbookSlugs reads the embedded runbooks/ directory and returns the
// slugs (filename without .md, lowercased). Filters out INDEX.md and TEMPLATE.md
// which are meta files.
func loadRunbookSlugs() []string {
	skip := map[string]bool{"INDEX.md": true, "TEMPLATE.md": true}

	var slugs []string
	_ = fs.WalkDir(root.RunbooksFS, "runbooks", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".md") || skip[name] {
			return nil
		}
		slug := strings.ToLower(strings.TrimSuffix(name, ".md"))
		slugs = append(slugs, slug)
		return nil
	})
	sort.Strings(slugs)
	return slugs
}

// resolveAddTarget classifies the [target] arg of /alertmanager config add as
// either a group set keyword or an individual runbook slug. Returns
// (groupName, slugs) where groupName is the unit name baked into the
// shared webhook's display name + each receiver's GroupName field, and
// slugs is the runbooks to create receivers for (whole set for groups,
// single-element for individual).
//
// "all" resolves to every embedded runbook (groupName = "all").
// Category names (compute, application, ...) resolve to their subset.
// Otherwise we check if the arg matches a known runbook slug — if so,
// it's an individual add and groupName = the slug itself.
// Anything else is an error with a discoverability hint.
func resolveAddTarget(target string) (groupName string, slugs []string, err error) {
	target = strings.ToLower(strings.TrimSpace(target))

	if target == "all" {
		return "all", runbookSlugs(), nil
	}
	if setSlugs, ok := scaffoldSets[target]; ok && setSlugs != nil {
		return target, setSlugs, nil
	}
	// Individual add path: must match a known runbook slug exactly.
	if slices.Contains(runbookSlugs(), target) {
		return target, []string{target}, nil
	}
	return "", nil, fmt.Errorf("unknown target `%s` — must be a category set (`%s`) OR a specific runbook slug (e.g. `high-cpu-usage`). Run `/alertmanager config add` with no args for the full list",
		target, strings.Join(scaffoldSetNames(), "`, `"))
}
