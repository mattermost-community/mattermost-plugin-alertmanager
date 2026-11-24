package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

// The `mattermost` group can also carry a per-installation alert pack, for
// installs that give mm-namespace=. Those rules carry no `runbook` label, so one
// extra receiver routes them by alertname. samples/mattermost-operator-rules.yaml
// holds them with placeholders the plugin fills for one Mattermost CR. They need
// nothing from the Mattermost Operator beyond the installation label it already
// puts on its pods.
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

// packThresholds are the numbers the pack's rules compare against. Percents are
// whole numbers from 1 to 99, since a share can never exceed 100%.
type packThresholds struct {
	httpErrorPercent    int
	httpMinRPS          float64
	authErrorPercent    int
	authMinRPS          float64
	loginFailurePercent int
	loginMinFailures    float64
	loginSprayFailures  float64
	cpuLimitPercent     int
	memoryLimitPercent  int
	dbReplicaLagSeconds float64
}

var defaultPackThresholds = packThresholds{
	httpErrorPercent:    5,
	httpMinRPS:          1,
	authErrorPercent:    20,
	authMinRPS:          1,
	loginFailurePercent: 50,
	loginMinFailures:    20,
	loginSprayFailures:  50,
	cpuLimitPercent:     85,
	memoryLimitPercent:  90,
	dbReplicaLagSeconds: 5,
}

// thresholdFlag binds one mm- flag to a percent or a floor in packThresholds.
// minFloor is the smallest floor allowed: a login count of 0 would page on a
// single failure.
type thresholdFlag struct {
	flag     string
	help     string
	percent  *int
	floor    *float64
	minFloor float64
}

const (
	maxThresholdPercent = 99
	maxThresholdFloor   = 1e6
)

func (t *packThresholds) flags() []thresholdFlag {
	return []thresholdFlag{
		{flag: "mm-http-error-percent=", help: "5xx share of requests", percent: &t.httpErrorPercent},
		{flag: "mm-http-min-rps=", help: "requests/s before the 5xx share counts", floor: &t.httpMinRPS},
		{flag: "mm-auth-error-percent=", help: "401/403 share of requests", percent: &t.authErrorPercent},
		{flag: "mm-auth-min-rps=", help: "401/403 requests/s before that share counts", floor: &t.authMinRPS},
		{flag: "mm-login-failure-percent=", help: "failed share of logins", percent: &t.loginFailurePercent},
		{flag: "mm-login-min-failures=", help: "failed logins in 15m before that share counts", floor: &t.loginMinFailures, minFloor: 1},
		{flag: "mm-login-spray-failures=", help: "failed logins in 15m that page whatever the share", floor: &t.loginSprayFailures, minFloor: 1},
		{flag: "mm-cpu-limit-percent=", help: "CPU use against the limit", percent: &t.cpuLimitPercent},
		{flag: "mm-memory-limit-percent=", help: "memory use against the limit", percent: &t.memoryLimitPercent},
		{flag: "mm-db-replica-lag-seconds=", help: "replica lag", floor: &t.dbReplicaLagSeconds},
	}
}

// packThresholdHelp describes every threshold flag with its default.
func packThresholdHelp() string {
	th := defaultPackThresholds
	var b strings.Builder
	fmt.Fprintf(&b, "`mattermost` with `mm-namespace` only. Tune the pack. Percents take 1 to %d, the rest 0 to %s (login counts start at 1). ",
		maxThresholdPercent, formatFloor(maxThresholdFloor))
	for i, f := range th.flags() {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "`%s` (%s): %s", strings.TrimSuffix(f.flag, "="), f.current(), f.help)
	}
	b.WriteString(".")
	return b.String()
}

func (f thresholdFlag) current() string {
	if f.percent != nil {
		return strconv.Itoa(*f.percent)
	}
	return formatFloor(*f.floor)
}

// set parses value into the bound field. The value reaches PromQL only after
// it is re-formatted from a number, so no raw text from the command lands there.
func (f thresholdFlag) set(value string) error {
	if f.percent != nil {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > maxThresholdPercent {
			return fmt.Errorf("invalid `%s` value %q: use a whole percent from 1 to %d", f.flag, value, maxThresholdPercent)
		}
		*f.percent = n
		return nil
	}
	v, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(v) || v < f.minFloor || v > maxThresholdFloor {
		return fmt.Errorf("invalid `%s` value %q: use a number from %s to %s", f.flag, value, formatFloor(f.minFloor), formatFloor(maxThresholdFloor))
	}
	if v == 0 {
		v = 0 // -0 would render as "-0"
	}
	*f.floor = v
	return nil
}

// extractSingleFlag pulls prefix out of words. Unlike extractFlagValue it
// rejects an empty value or a repeated flag, either of which would otherwise
// leave a default in place without a word.
func extractSingleFlag(words []string, prefix string) (value string, found bool, rest []string, err error) {
	rest = make([]string, 0, len(words))
	for _, w := range words {
		after, ok := strings.CutPrefix(w, prefix)
		if !ok {
			rest = append(rest, w)
			continue
		}
		if found {
			return "", false, nil, fmt.Errorf("`%s` is given more than once", prefix)
		}
		if after == "" {
			return "", false, nil, fmt.Errorf("`%s` needs a value", prefix)
		}
		value, found = after, true
	}
	return value, found, rest, nil
}

// extractPackThresholds pulls the threshold flags out of words. thresholds is
// nil when none was typed, so the defaults apply.
func extractPackThresholds(words []string) ([]string, *packThresholds, error) {
	th := defaultPackThresholds
	given := false
	rest := words
	for _, f := range th.flags() {
		value, found, next, err := extractSingleFlag(rest, f.flag)
		if err != nil {
			return nil, nil, err
		}
		rest = next
		if !found {
			continue
		}
		given = true
		if err = f.set(value); err != nil {
			return nil, nil, err
		}
	}
	if !given {
		return rest, nil, nil
	}
	return rest, &th, nil
}

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
	thresholds  packThresholds
}

// mattermostOptions are the optional flags that widen or tune the pack.
type mattermostOptions struct {
	dbReplicas  bool
	postgresPod bool
	thresholds  *packThresholds // nil keeps the defaults
	given       bool            // any of the flags was typed
}

// extractMattermostOptions pulls mm-db-replicas=, mm-postgres-pod= and the
// threshold flags out of words. The first two take true or false.
func extractMattermostOptions(words []string) ([]string, mattermostOptions, error) {
	var opts mattermostOptions
	replicas, _, rest, err := extractSingleFlag(words, mmDBReplicasFlag)
	if err != nil {
		return nil, opts, err
	}
	postgres, _, rest, err := extractSingleFlag(rest, mmPostgresPodFlag)
	if err != nil {
		return nil, opts, err
	}
	if opts.dbReplicas, err = parseMMBool(mmDBReplicasFlag, replicas); err != nil {
		return nil, opts, err
	}
	if opts.postgresPod, err = parseMMBool(mmPostgresPodFlag, postgres); err != nil {
		return nil, opts, err
	}
	if rest, opts.thresholds, err = extractPackThresholds(rest); err != nil {
		return nil, opts, err
	}
	opts.given = replicas != "" || postgres != "" || opts.thresholds != nil
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
	if opts.thresholds != nil {
		t.thresholds = *opts.thresholds
	}
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
	return mattermostTarget{namespace: namespace, name: name, thresholds: defaultPackThresholds}, nil
}

// packRuleObjectName is the PrometheusRule holding one namespace's pack. Only
// one Mattermost runs per namespace, so packs for two installs never collide.
func packRuleObjectName(namespace string) string {
	return prometheusRuleObject + "-" + mattermostGroup + "-" + namespace
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

// renderMattermostOperatorRules fills the pack's placeholders for one install
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

	th := t.thresholds
	out := strings.NewReplacer(
		"__NAMESPACE__", t.namespace,
		"__INSTALLATION__", t.name,
		// Deployment pods are <name>-<replicaset-hash>-<suffix>. The anchored
		// pattern keeps "mm" from matching a differently named "mm-test".
		"__POD_SELECTOR__", t.name+"-[^-]+-[^-]+",
		"__HTTP_ERROR_RATIO__", percentFraction(th.httpErrorPercent),
		"__HTTP_ERROR_PERCENT__", strconv.Itoa(th.httpErrorPercent),
		"__HTTP_MIN_RPS__", formatFloor(th.httpMinRPS),
		"__AUTH_ERROR_RATIO__", percentFraction(th.authErrorPercent),
		"__AUTH_ERROR_PERCENT__", strconv.Itoa(th.authErrorPercent),
		"__AUTH_MIN_RPS__", formatFloor(th.authMinRPS),
		"__LOGIN_FAILURE_RATIO__", percentFraction(th.loginFailurePercent),
		"__LOGIN_FAILURE_PERCENT__", strconv.Itoa(th.loginFailurePercent),
		"__LOGIN_MIN_FAILURES__", formatFloor(th.loginMinFailures),
		"__LOGIN_SPRAY_FAILURES__", formatFloor(th.loginSprayFailures),
		"__CPU_LIMIT_RATIO__", percentFraction(th.cpuLimitPercent),
		"__CPU_LIMIT_PERCENT__", strconv.Itoa(th.cpuLimitPercent),
		"__MEMORY_LIMIT_RATIO__", percentFraction(th.memoryLimitPercent),
		"__MEMORY_LIMIT_PERCENT__", strconv.Itoa(th.memoryLimitPercent),
		"__DB_REPLICA_LAG_SECONDS__", formatFloor(th.dbReplicaLagSeconds),
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

// formatFloor renders a floor as plain decimal PromQL accepts.
func formatFloor(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// mattermostRulesFile builds the rules for the `mattermost` group. With a target
// it adds the pack for that install as its own object. The group's rules that
// overlap the pack skip any namespace carrying the pack's coverage marker, so
// they stay in place for the other installs on the cluster. rc.namespace is where
// the PrometheusRule objects land, not where Mattermost runs.
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
	spec.scope = fmt.Sprintf("the `mattermost` group and Mattermost `%s/%s`", t.namespace, t.name)
	spec.extraGroups = body
	spec.extraName = packRuleObjectName(t.namespace)
	spec.extraCount = len(packAlertLine.FindAllStringSubmatch(body, -1))
	overlap := fmt.Sprintf("Do not also apply the full `%s` object or another export or add that covers the same runbooks, or these alerts fire twice. ", prometheusRuleObject)
	onePerNamespace := fmt.Sprintf("The pack is per namespace: running this for another Mattermost CR in `%s` replaces this install's pack. ", t.namespace)
	if rc.format == formatCRD {
		spec.tail = overlap + onePerNamespace + fmt.Sprintf(
			"The file holds two PrometheusRules: `%s`, the group's cluster-wide rules, which are the same for every namespace, and `%s`, the alerts for this install. "+
				"For another Mattermost namespace, run this again with its `mm-namespace` and apply that file too. "+
				"The cluster-wide 5xx, login failure and WebSocket rules skip every namespace whose pack sees its install, so nothing pages twice.",
			spec.name, spec.extraName,
		)
	} else {
		spec.tail = overlap + onePerNamespace + fmt.Sprintf(
			"The file holds the group's cluster-wide rules and this install's groups, whose names end in `.%s`. "+
				"For another Mattermost namespace, run this again and copy only the groups ending in that namespace into this file: "+
				"the cluster-wide groups are the same in both, and Prometheus rejects a group name it sees twice. "+
				"It replaces `%s.yaml` from earlier versions of the plugin: remove that file.",
			t.namespace, spec.name,
		)
	}
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
