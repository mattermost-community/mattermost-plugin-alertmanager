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
const (
	mattermostGroup = "mattermost"
	// packReceiverSlug names the receiver that claims the operator alerts. Entries
	// created before the rename used legacyPackReceiverSlug and still work.
	packReceiverSlug            = "mattermost-server"
	legacyPackReceiverSlug      = "mattermost-operator"
	mattermostOperatorRulesFile = "samples/mattermost-operator-rules.yaml"
	defaultMattermostName       = "mattermost"
	mmNamespaceFlag             = "mm-namespace="
	mmNameFlag                  = "mm-name="
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

// packAlertRegex is the anchored alertname regex a route uses to claim the pack.
func packAlertRegex() (string, error) {
	names, err := packAlertnames()
	if err != nil {
		return "", err
	}
	return "^(" + strings.Join(names, "|") + ")$", nil
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
		"__SERVICE__", t.name+"-metrics",
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

	if left := packPlaceholder.FindString(out); left != "" {
		return "", fmt.Errorf("%s has a placeholder the plugin does not fill: %s", mattermostOperatorRulesFile, left)
	}
	return out, nil
}

// percentFraction renders a 0-100 percent as the ratio PromQL compares against.
func percentFraction(percent int) string {
	return strconv.FormatFloat(float64(percent)/100, 'f', -1, 64)
}

// mattermostRulesFile builds the rules for the `mattermost` group. With a target
// it appends the operator's alerts for that install. namespace is where the
// PrometheusRule object lands, not where Mattermost runs.
func (p *Plugin) mattermostRulesFile(namespace string, t *mattermostTarget, format string) (*rulesFile, error) {
	name := prometheusRuleObject + "-" + mattermostGroup
	slugs := scaffoldSets[mattermostGroup]
	if t == nil {
		return p.buildRulesFile(namespace, name, "the `mattermost` group", slugs, format)
	}

	body, err := renderMattermostOperatorRules(*t)
	if err != nil {
		return nil, err
	}
	names, err := packAlertnames()
	if err != nil {
		return nil, err
	}
	scope := fmt.Sprintf("the `mattermost` group and Mattermost `%s/%s`", t.namespace, t.name)
	rules, err := p.buildRulesFileWithExtra(namespace, name, scope, slugs, format, strings.TrimPrefix(body, "groups:\n"), len(names))
	if err != nil {
		return nil, err
	}
	rules.tail = fmt.Sprintf(
		"Do not also apply the full `%s` object or another export or add that covers the same runbooks, or these alerts fire twice. "+
			"The `mattermost.*` groups are the rules the Mattermost Operator generates: once your operator creates its own PrometheusRule, remove them from this file. "+
			"MattermostHighErrorRate and MattermostHighLoginFailures overlap the operator's HTTP error and login alerts.",
		prometheusRuleObject,
	)
	return rules, nil
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
