package main

import (
	"fmt"
	"sort"
	"strings"
)

// packRunbookDir is where the pack's per-alert runbooks live, both in the
// embedded source (runbooks/<dir>/) and the rendered site (public/runbooks/<dir>/).
const packRunbookDir = "mattermost-operator"

// packRunbookLanding is the rendered index of the pack runbooks.
const packRunbookLanding = "home.html"

// packQuickDiagnosticLimit is one command per alert, not the three a runbook
// receiver gets: the pack receiver carries a branch per alert, so every
// command is repeated in the generated Alertmanager config.
const packQuickDiagnosticLimit = 1

// packRunbookSlugs maps every alert the pack routes to its runbook page.
// TestPackRunbooksCoverRoutedAlerts keeps it equal to the rule files and the
// runbooks/mattermost-operator/ directory.
var packRunbookSlugs = map[string]string{
	"MattermostServerDown":              "mattermost-server-down",
	"MattermostPodCrashLooping":         "mattermost-pod-crash-looping",
	"MattermostHighCPU":                 "mattermost-high-cpu",
	"MattermostCPULimitMissing":         "mattermost-cpu-limit-missing",
	"MattermostHighMemory":              "mattermost-high-memory",
	"MattermostMemoryLimitMissing":      "mattermost-memory-limit-missing",
	"MattermostDBReplicaLag":            "mattermost-db-replica-lag",
	"MattermostDBWriterPoolEmpty":       "mattermost-db-writer-pool-empty",
	"MattermostHighHTTPErrorRate":       "mattermost-high-http-error-rate",
	"MattermostLoginFailureSpike":       "mattermost-login-failure-spike",
	"MattermostWebSocketDrop":           "mattermost-websocket-drop",
	"MattermostSearchDown":              "mattermost-search-down",
	"MattermostClusterUnhealthy":        "mattermost-cluster-unhealthy",
	"MattermostLogsDropped":             "mattermost-logs-dropped",
	"MattermostPostgresPodNotReady":     "mattermost-postgres-pod-not-ready",
	"MattermostPostgresPodCrashLooping": "mattermost-postgres-pod-crash-looping",
}

// sortedPackAlertnames keeps the generated template stable across renders.
func sortedPackAlertnames() []string {
	names := make([]string, 0, len(packRunbookSlugs))
	for name := range packRunbookSlugs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// packRunbookPageURL swaps the page in a plugin runbook URL for a page in the
// pack directory. runbookDefaultURL always ends in /<slug>.html, so everything
// up to the last slash is the runbook site root.
func packRunbookPageURL(runbookDefaultURL, page string) string {
	i := strings.LastIndex(runbookDefaultURL, "/")
	return runbookDefaultURL[:i+1] + packRunbookDir + "/" + page
}

// packRunbookSection links each pack alert to its own runbook. An alert's own
// http(s) runbook_url wins; an alertname outside the pack gets no link.
func packRunbookSection(runbookDefaultURL string) string {
	var b strings.Builder
	b.WriteString(`{{- if ` + runbookURLGate + ` -}}` + runbookLine("{{SAN_RUNBOOK_URL}}"))
	for _, name := range sortedPackAlertnames() {
		url := packRunbookPageURL(runbookDefaultURL, packRunbookSlugs[name]+".html")
		fmt.Fprintf(&b, "\n        {{- else if eq .Labels.alertname %q -}}%s", name, runbookLine(url))
	}
	b.WriteString(`
        {{- end -}}`)
	return b.String()
}

// loadPackQuickDiagnostics reads the first Quick diagnostics block of one pack
// runbook. A missing page or section returns nil.
func loadPackQuickDiagnostics(slug string) []quickDiagnostic {
	blocks := loadQuickDiagnosticsForSlug(packRunbookDir + "/" + slug)
	if len(blocks) > packQuickDiagnosticLimit {
		blocks = blocks[:packQuickDiagnosticLimit]
	}
	return blocks
}

// packQuickDiagnostics is the {{QUICK_DIAGNOSTICS}} body for a pack receiver:
// one branch per alertname, each holding that runbook's first command. indent
// is the continuation indent of the YAML literal block it is inlined into.
func packQuickDiagnostics(indent string) string {
	var branches []string
	for _, name := range sortedPackAlertnames() {
		diag := formatQuickDiagnosticsForAlert(loadPackQuickDiagnostics(packRunbookSlugs[name]))
		if diag != "" {
			branches = append(branches, fmt.Sprintf("eq .Labels.alertname %q -}}\n%s\n", name, diag))
		}
	}
	if len(branches) == 0 {
		return ""
	}
	return indentForYAMLBlock("{{- if "+strings.Join(branches, "{{- else if ")+"{{- end }}", indent)
}
