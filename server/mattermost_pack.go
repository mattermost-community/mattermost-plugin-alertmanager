package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

// The `mattermost` group can also carry the alert set the Mattermost Operator
// embeds in its PrometheusRule, for installs that give mm-namespace=. Those rules
// carry no `runbook` label, so one extra receiver routes them by alertname.
// samples/mattermost-operator-rules.yaml is a verbatim copy of the operator's
// file; the plugin only fills in its placeholders, the same way the operator
// does, so the two never diverge in meaning.
//
// The pack is a stand-in for monitoring the Mattermost Operator does not ship
// yet (draft mattermost-operator PR #466). If the operator ever creates these
// rules itself, stop adding the pack: the groups, alert names and labels match,
// so removing it leaves nothing behind.
const (
	mattermostGroup = "mattermost"
	// packReceiverSlug names the receiver that claims the operator alerts. Entries
	// created before the rename used legacyPackReceiverSlug and still work.
	packReceiverSlug            = "mattermost-server"
	legacyPackReceiverSlug      = "mattermost-operator"
	mattermostOperatorRulesFile = "samples/mattermost-operator-rules.yaml"
	mattermostPostgresRulesFile = "samples/mattermost-postgres-rules.yaml"
	defaultMattermostName       = "mattermost"
	mmNamespaceFlag             = "mm-namespace="
	mmNameFlag                  = "mm-name="
	mmDBReplicasFlag            = "mm-db-replicas="
	mmPostgresPodFlag           = "mm-postgres-pod="

	// alertDBReplicaLag is the one operator alert that is off unless asked for.
	// Mattermost only reports replica lag when SqlSettings.ReplicaLagSettings has
	// a lag query per replica, so without that the alert can never fire.
	alertDBReplicaLag = "MattermostDBReplicaLag"
)

// Operator defaults for prometheusRule.thresholds. Keep in step with the
// operator's pkg/mattermost/monitoring.go.
const (
	defaultHTTPErrorPercent    = 5
	defaultHTTPMinRPS          = 1
	defaultLoginFailurePercent = 50
	defaultLoginMinFailures    = 20
	defaultCPULimitPercent     = 85
	defaultMemoryLimitPercent  = 90
	defaultDBReplicaLagSeconds = 5
)

var (
	packAlertLine    = regexp.MustCompile(`(?m)^\s*-\s+alert:\s+([A-Za-z0-9_]+)\s*$`)
	packPlaceholder  = regexp.MustCompile(`__[A-Z][A-Z_]*__`)
	mattermostCRName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// mattermostTarget identifies the Mattermost install the pack's rules watch.
type mattermostTarget struct {
	namespace string
	name      string
	// dbReplicas adds MattermostDBReplicaLag (mm-db-replicas=true).
	dbReplicas bool
	// postgresPod adds the in-cluster Postgres pod alerts (mm-postgres-pod=true).
	postgresPod bool
}

// mattermostOptions are the optional flags that widen the pack.
type mattermostOptions struct {
	dbReplicas  bool
	postgresPod bool
	given       bool // any of the flags was typed
}

// extractMattermostOptions pulls mm-db-replicas= and mm-postgres-pod= out of
// words. Each takes true or false.
func extractMattermostOptions(words []string) ([]string, mattermostOptions, error) {
	var opts mattermostOptions
	replicas, rest := extractFlagValue(words, mmDBReplicasFlag)
	postgres, rest := extractFlagValue(rest, mmPostgresPodFlag)
	var err error
	if opts.dbReplicas, err = parseMMBool(mmDBReplicasFlag, replicas); err != nil {
		return nil, opts, err
	}
	if opts.postgresPod, err = parseMMBool(mmPostgresPodFlag, postgres); err != nil {
		return nil, opts, err
	}
	opts.given = replicas != "" || postgres != ""
	return rest, opts, nil
}

func parseMMBool(flag, value string) (bool, error) {
	switch strings.ToLower(value) {
	case "":
		return false, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid `%s` value %q: use true or false", flag, value)
	}
}

// withOptions sets the optional alert groups on a validated target.
func (t mattermostTarget) withOptions(opts mattermostOptions) mattermostTarget {
	t.dbReplicas = opts.dbReplicas
	t.postgresPod = opts.postgresPod
	return t
}

// newMattermostTarget validates the namespace and CR name before they reach a
// PromQL string. An empty name means the operator's usual CR name.
func newMattermostTarget(namespace, name string) (mattermostTarget, error) {
	if namespace == "" {
		return mattermostTarget{}, fmt.Errorf("`mm-namespace=<ns>` is required: the namespace your Mattermost runs in")
	}
	if err := validateCRDNamespace(namespace); err != nil {
		return mattermostTarget{}, fmt.Errorf("invalid `mm-namespace`: %w", err)
	}
	if name == "" {
		name = defaultMattermostName
	}
	if len(name) > 63 || !mattermostCRName.MatchString(name) {
		return mattermostTarget{}, fmt.Errorf("invalid `mm-name` %q: use the Mattermost custom resource name (lowercase letters, digits and '-')", name)
	}
	return mattermostTarget{namespace: namespace, name: name}, nil
}

// kindFor is the receiver kind a scaffold add gives the receiver for slug.
func kindFor(slug string) receiverKind {
	if slug == packReceiverSlug {
		return kindPack
	}
	return kindRunbook
}

// packFor returns the alertConfig.Pack value for a receiver slug.
func packFor(slug string) string {
	if slug == packReceiverSlug {
		return packReceiverSlug
	}
	return ""
}

func readMattermostOperatorRules() (string, error) {
	raw, err := root.SamplesFS.ReadFile(mattermostOperatorRulesFile)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", mattermostOperatorRulesFile, err)
	}
	return string(raw), nil
}

// packAlertnames lists the alerts in the embedded operator rules, in file order.
func packAlertnames() ([]string, error) {
	raw, err := readMattermostOperatorRules()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, m := range packAlertLine.FindAllStringSubmatch(raw, -1) {
		names = append(names, m[1])
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%s has no alerts", mattermostOperatorRulesFile)
	}
	return names, nil
}

// postgresAlertnames lists the alerts in the plugin-only Postgres pod rules.
func postgresAlertnames() ([]string, error) {
	raw, err := root.SamplesFS.ReadFile(mattermostPostgresRulesFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", mattermostPostgresRulesFile, err)
	}
	var names []string
	for _, m := range packAlertLine.FindAllStringSubmatch(string(raw), -1) {
		names = append(names, m[1])
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%s has no alerts", mattermostPostgresRulesFile)
	}
	return names, nil
}

// packRoutedAlertnames is every alert the pack route claims, including the
// opt-in ones. Receivers do not record which flags `format rules` used, so the
// route, silences and the channel's alert list all take the full set.
func packRoutedAlertnames() ([]string, error) {
	names, err := packAlertnames()
	if err != nil {
		return nil, err
	}
	postgres, err := postgresAlertnames()
	if err != nil {
		return nil, err
	}
	return append(names, postgres...), nil
}

// packAlertRegex is the anchored alertname regex a route uses to claim the pack,
// so one route keeps working whichever flags a later `format rules` used.
func packAlertRegex() (string, error) {
	names, err := packRoutedAlertnames()
	if err != nil {
		return "", err
	}
	return "^(" + strings.Join(names, "|") + ")$", nil
}

// dropAlert removes one alert's block from a rules document. The block runs until
// the next line at the same or a shallower indent.
func dropAlert(doc, alert string) string {
	lines := strings.Split(doc, "\n")
	kept := make([]string, 0, len(lines))
	skipIndent := -1
	for _, line := range lines {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if skipIndent >= 0 {
			if strings.TrimSpace(line) == "" || indent > skipIndent {
				continue
			}
			skipIndent = -1
		}
		if m := packAlertLine.FindStringSubmatch(line); m != nil && m[1] == alert {
			skipIndent = indent
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// renderMattermostOperatorRules fills the operator's placeholders for one install
// and returns the `groups:` document, without the file's leading comments.
func renderMattermostOperatorRules(t mattermostTarget) (string, error) {
	raw, err := readMattermostOperatorRules()
	if err != nil {
		return "", err
	}
	_, groups, found := strings.Cut(raw, "\ngroups:")
	if !found {
		return "", fmt.Errorf("%s has no groups block", mattermostOperatorRulesFile)
	}

	out := strings.NewReplacer(
		"__NAMESPACE__", t.namespace,
		"__INSTALLATION__", t.name,
		// Deployment pods are <name>-<replicaset-hash>-<suffix>. The anchored
		// pattern keeps "mm" from matching a differently named "mm-test".
		"__POD_SELECTOR__", t.name+"-[^-]+-[^-]+",
		"__HTTP_ERROR_RATIO__", percentFraction(defaultHTTPErrorPercent),
		"__HTTP_ERROR_PERCENT__", strconv.Itoa(defaultHTTPErrorPercent),
		"__HTTP_MIN_RPS__", strconv.Itoa(defaultHTTPMinRPS),
		"__LOGIN_FAILURE_RATIO__", percentFraction(defaultLoginFailurePercent),
		"__LOGIN_FAILURE_PERCENT__", strconv.Itoa(defaultLoginFailurePercent),
		"__LOGIN_MIN_FAILURES__", strconv.Itoa(defaultLoginMinFailures),
		"__CPU_LIMIT_RATIO__", percentFraction(defaultCPULimitPercent),
		"__CPU_LIMIT_PERCENT__", strconv.Itoa(defaultCPULimitPercent),
		"__MEMORY_LIMIT_RATIO__", percentFraction(defaultMemoryLimitPercent),
		"__MEMORY_LIMIT_PERCENT__", strconv.Itoa(defaultMemoryLimitPercent),
		"__DB_REPLICA_LAG_SECONDS__", strconv.Itoa(defaultDBReplicaLagSeconds),
	).Replace("groups:" + groups)

	if !t.dbReplicas {
		out = dropAlert(out, alertDBReplicaLag)
	}

	if left := packPlaceholder.FindString(out); left != "" {
		return "", fmt.Errorf("%s has a placeholder the plugin does not fill: %s", mattermostOperatorRulesFile, left)
	}
	return out, nil
}

// renderMattermostPostgresRules fills the namespace in the Postgres pod rules and
// returns their `groups:` body, ready to append to the operator groups.
func renderMattermostPostgresRules(t mattermostTarget) (string, error) {
	raw, err := root.SamplesFS.ReadFile(mattermostPostgresRulesFile)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", mattermostPostgresRulesFile, err)
	}
	_, groups, found := strings.Cut(string(raw), "\ngroups:\n")
	if !found {
		return "", fmt.Errorf("%s has no groups block", mattermostPostgresRulesFile)
	}
	out := strings.ReplaceAll(groups, "__NAMESPACE__", t.namespace)
	if left := packPlaceholder.FindString(out); left != "" {
		return "", fmt.Errorf("%s has a placeholder the plugin does not fill: %s", mattermostPostgresRulesFile, left)
	}
	return out, nil
}

// percentFraction renders a 0-100 percent as the ratio PromQL compares against.
func percentFraction(percent int) string {
	return strconv.FormatFloat(float64(percent)/100, 'f', -1, 64)
}

// supersededByPack lists the group's runbook slugs the pack replaces. Both
// monitor the same signal as a pack alert, but unscoped and across every install.
// With a target they are dropped, so each signal alerts once.
var supersededByPack = map[string]bool{
	"mattermost-websocket-drop":      true, // MattermostWebSocketDrop
	"mattermost-high-login-failures": true, // MattermostLoginFailureSpike
}

// slugsWithoutPack returns slugs minus the ones the pack covers.
func slugsWithoutPack(slugs []string) []string {
	kept := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		if !supersededByPack[slug] {
			kept = append(kept, slug)
		}
	}
	return kept
}

// mattermostRulesFile builds the rules for the `mattermost` group. With a target
// it appends the operator's alerts for that install. namespace is where the
// PrometheusRule object lands, not where Mattermost runs.
func (p *Plugin) mattermostRulesFile(rc rulesContext, t *mattermostTarget) (*rulesFile, error) {
	spec := rulesSpec{
		rulesContext: rc,
		name:         prometheusRuleObject + "-" + mattermostGroup,
		scope:        "the `mattermost` group",
		slugs:        scaffoldSets[mattermostGroup],
	}
	if t == nil {
		return p.buildRules(spec)
	}

	body, err := renderMattermostOperatorRules(*t)
	if err != nil {
		return nil, err
	}
	body = strings.TrimPrefix(body, "groups:\n")
	if t.postgresPod {
		postgres, pgErr := renderMattermostPostgresRules(*t)
		if pgErr != nil {
			return nil, pgErr
		}
		body = strings.TrimRight(body, "\n") + "\n" + postgres
	}
	spec.slugs = slugsWithoutPack(spec.slugs)
	spec.scope = fmt.Sprintf("the `mattermost` group and Mattermost `%s/%s`", t.namespace, t.name)
	spec.extraGroups = body
	spec.extraCount = len(packAlertLine.FindAllStringSubmatch(body, -1))
	spec.tail = fmt.Sprintf(
		"Do not also apply the full `%s` object or another export or add that covers the same runbooks, or these alerts fire twice. "+
			"The `mattermost.*` groups are the rules the Mattermost Operator generates: once your operator creates its own PrometheusRule, remove them from this file. "+
			"The group's WebSocket drop and login failure rules are left out, because the operator alerts cover them. "+
			"MattermostHighErrorRate overlaps the operator's HTTP error alert. "+
			"If the Mattermost Operator ships these rules itself, remove the `mattermost.*` groups and do not add them again.",
		prometheusRuleObject,
	)
	return p.buildRules(spec)
}

// packRoutesYAML renders the live alertname route for each pack receiver, ready to
// sit under `routes:`.
func packRoutesYAML(entries []alertConfig) string {
	if len(entries) == 0 {
		return ""
	}
	match, err := packAlertRegex()
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, ac := range entries {
		fmt.Fprintf(&b, "  - matchers: [alertname=~%q]\n", match)
		fmt.Fprintf(&b, "    receiver: %s\n", ac.Name)
		b.WriteString("    continue: true\n")
		b.WriteString("    group_by: ['alertname', 'namespace', 'pod']\n")
	}
	return b.String()
}
