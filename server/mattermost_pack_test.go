package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

var wantPackAlerts = []string{
	"MattermostServerDown",
	"MattermostPodCrashLooping",
	"MattermostHighCPU",
	"MattermostCPULimitMissing",
	"MattermostHighMemory",
	"MattermostMemoryLimitMissing",
	"MattermostDBReplicaLag",
	"MattermostDBWriterPoolEmpty",
	"MattermostHighHTTPErrorRate",
	"MattermostHighHTTPAuthErrorRate",
	"MattermostLoginFailureSpike",
	"MattermostWebSocketDrop",
	"MattermostSearchDown",
	"MattermostClusterUnhealthy",
	"MattermostLogsDropped",
}

// defaultPackAlerts is the pack without the opt-in MattermostDBReplicaLag.
var defaultPackAlerts = slices.DeleteFunc(slices.Clone(wantPackAlerts), func(name string) bool { return name == alertDBReplicaLag })

func testTarget(t *testing.T) mattermostTarget {
	t.Helper()
	target, err := newMattermostTarget("mattermost-crimson", "")
	if err != nil {
		t.Fatalf("newMattermostTarget: %v", err)
	}
	return target
}

func TestPackAlertnames(t *testing.T) {
	got, err := packAlertnames()
	if err != nil {
		t.Fatalf("packAlertnames: %v", err)
	}
	if !slices.Equal(got, wantPackAlerts) {
		t.Fatalf("alerts = %v, want %v", got, wantPackAlerts)
	}
}

// The operator rules carry no runbook label. That is why the pack routes by
// alertname, and a runbook label here would put the alerts on the wrong route.
func TestPackRulesCarryNoRunbookLabel(t *testing.T) {
	raw, err := readMattermostOperatorRules()
	if err != nil {
		t.Fatal(err)
	}
	if runbookLabelLine.MatchString(raw) {
		t.Fatal("operator rules must not set a runbook label")
	}
}

func TestRenderMattermostOperatorRules(t *testing.T) {
	out, err := renderMattermostOperatorRules(testTarget(t).withOptions(mattermostOptions{dbReplicas: true}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if left := packPlaceholder.FindString(out); left != "" {
		t.Fatalf("placeholder left unfilled: %s", left)
	}

	for _, want := range []string{
		`namespace="mattermost-crimson"`,
		`installation_mattermost_com_installation="mattermost"`,
		`pod=~"mattermost-[^-]+-[^-]+"`,
		"> 0.85",
		"> 0.9",
		"> 0.05",
		"> 0.2\n",
		"> 0.5",
		"> 5\n",
		"> 50\n",
		"85% of limit",
		"namespace: mattermost-crimson",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered rules missing %q", want)
		}
	}

	var doc struct {
		Groups []struct {
			Rules []struct {
				Alert       string            `yaml:"alert"`
				Record      string            `yaml:"record"`
				Expr        string            `yaml:"expr"`
				Labels      map[string]string `yaml:"labels"`
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered rules are not valid YAML: %v", err)
	}
	var got []string
	for _, g := range doc.Groups {
		for _, r := range g.Rules {
			if r.Record != "" {
				if r.Record != packCoverageRecord || r.Labels["namespace"] != "mattermost-crimson" {
					t.Errorf("unexpected recording rule %s %v", r.Record, r.Labels)
				}
				continue
			}
			got = append(got, r.Alert)
			if strings.TrimSpace(r.Expr) == "" {
				t.Errorf("%s has an empty expr", r.Alert)
			}
			if r.Labels["severity"] != "warning" && r.Labels["severity"] != "critical" {
				t.Errorf("%s severity = %q", r.Alert, r.Labels["severity"])
			}
			if r.Annotations["summary"] == "" || r.Annotations["description"] == "" {
				t.Errorf("%s needs summary and description", r.Alert)
			}
		}
	}
	if !slices.Equal(got, wantPackAlerts) {
		t.Fatalf("rendered alerts = %v, want %v", got, wantPackAlerts)
	}
}

func TestRenderMattermostOperatorRulesCustomName(t *testing.T) {
	target, err := newMattermostTarget("chat", "mm-prod")
	if err != nil {
		t.Fatal(err)
	}
	out, err := renderMattermostOperatorRules(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `installation_mattermost_com_installation="mm-prod"`) || !strings.Contains(out, `pod=~"mm-prod-[^-]+-[^-]+"`) {
		t.Fatal("custom name did not reach the installation and pod selector")
	}
}

func TestMattermostRulesMatchInstallationNotService(t *testing.T) {
	target, err := newMattermostTarget("chat", "mm")
	if err != nil {
		t.Fatal(err)
	}
	out, err := renderMattermostOperatorRules(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "service=") || strings.Contains(out, "$labels.service") || strings.Contains(out, ", service)") {
		t.Fatal("rules must not depend on the scraped Service name")
	}
	if !strings.Contains(out, `up{namespace="chat", installation_mattermost_com_installation="mm"}`) {
		t.Fatal("up must be scoped by namespace and installation")
	}
	if _, msg := parseFormatArgs([]string{"security", "mm-namespace=chat"}); !strings.Contains(msg, "only apply") {
		t.Fatalf("mm-namespace outside mattermost: %q", msg)
	}
}

// Every series selector must carry the namespace, so two installs that share a
// CR name in different namespaces never share an alert.
func TestMattermostRulesAlwaysScopeByNamespace(t *testing.T) {
	raw, err := readMattermostOperatorRules()
	if err != nil {
		t.Fatal(err)
	}
	selector := regexp.MustCompile(`\b(?:up|mattermost_\w+|kube_\w+|container_\w+)\{[^}]*\}`)
	commentLine := regexp.MustCompile(`(?m)^\s*#.*$`)
	for _, m := range selector.FindAllString(commentLine.ReplaceAllString(raw, ""), -1) {
		if !strings.Contains(m, `namespace="__NAMESPACE__"`) {
			t.Errorf("selector without namespace: %s", m)
		}
	}
}

func TestNewMattermostTargetValidation(t *testing.T) {
	cases := []struct {
		name, namespace, crName, wantErr string
	}{
		{"missing namespace", "", "", "mm-namespace"},
		{"bad namespace", "Bad_NS", "", "mm-namespace"},
		{"bad name", "chat", "Mattermost", "mm-name"},
		{"promql breakout", "chat", `x"}] or vector(1) #`, "mm-name"},
		{"too long", "chat", strings.Repeat("a", 64), "mm-name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newMattermostTarget(tc.namespace, tc.crName)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
	target, err := newMattermostTarget("chat", "")
	if err != nil || target.name != defaultMattermostName {
		t.Fatalf("default name = %q, err %v", target.name, err)
	}
}

func rulesTestPlugin() *Plugin {
	p := &Plugin{}
	p.API = &ruleDMAPI{uploads: map[string]string{}}
	return p
}

func ruleAlertNames(t *testing.T, data []byte) []string {
	t.Helper()
	var doc struct {
		Groups []struct {
			Name  string `yaml:"name"`
			Rules []struct {
				Alert string `yaml:"alert"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("rules are not valid YAML: %v\n%s", err, data)
	}
	var out []string
	for _, g := range doc.Groups {
		for _, r := range g.Rules {
			if r.Alert != "" {
				out = append(out, r.Alert)
			}
		}
	}
	return out
}

// packCoverageRecord is the series the pack records for its namespace and the
// group's overlapping rules exclude.
const packCoverageRecord = "mattermost_alertmanager:pack_namespace"

type ruleObject struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec struct {
		Groups []struct {
			Rules []struct {
				Alert  string            `yaml:"alert"`
				Record string            `yaml:"record"`
				Expr   string            `yaml:"expr"`
				Labels map[string]string `yaml:"labels"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	} `yaml:"spec"`
}

func decodeRuleObjects(t *testing.T, data []byte) []ruleObject {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var objs []ruleObject
	for {
		var obj ruleObject
		err := dec.Decode(&obj)
		if errors.Is(err, io.EOF) {
			return objs
		}
		if err != nil {
			t.Fatalf("manifest is not valid YAML: %v\n%s", err, data)
		}
		objs = append(objs, obj)
	}
}

func (o ruleObject) alerts() []string {
	var out []string
	for _, g := range o.Spec.Groups {
		for _, r := range g.Rules {
			if r.Alert != "" {
				out = append(out, r.Alert)
			}
		}
	}
	return out
}

// The pack is its own PrometheusRule per namespace. The group's rules keep
// covering every other install: they count per namespace and skip namespaces
// carrying the pack's coverage marker, so rendering two packs never pages twice.
func TestMattermostRulesFileSplitsPackPerNamespace(t *testing.T) {
	p := rulesTestPlugin()
	target := testTarget(t)

	base, err := p.mattermostRulesFile(testRulesContext(formatStandard), nil)
	if err != nil {
		t.Fatal(err)
	}
	baseAlerts := ruleAlertNames(t, base.data)
	if len(baseAlerts) != len(scaffoldSets[mattermostGroup]) || slices.Contains(baseAlerts, "MattermostServerDown") {
		t.Fatalf("without mm-namespace want only the group's runbook rules, got %v", baseAlerts)
	}
	wantTotal := len(baseAlerts) + len(defaultPackAlerts)

	crd, err := p.mattermostRulesFile(testRulesContext(formatCRD), &target)
	if err != nil {
		t.Fatal(err)
	}
	if crd.count != wantTotal || crd.name != packRuleObjectName("mattermost-crimson")+".yaml" {
		t.Fatalf("name = %q count = %d, want %d", crd.name, crd.count, wantTotal)
	}
	objs := decodeRuleObjects(t, crd.data)
	if len(objs) != 2 {
		t.Fatalf("want the group object and the pack object, got %d", len(objs))
	}
	group, pack := objs[0], objs[1]
	if group.Metadata.Name != prometheusRuleObject+"-"+mattermostGroup || pack.Metadata.Name != prometheusRuleObject+"-mattermost-mattermost-crimson" {
		t.Fatalf("object names = %q, %q", group.Metadata.Name, pack.Metadata.Name)
	}
	for _, o := range objs {
		if o.Kind != "PrometheusRule" || o.Metadata.Namespace != "monitoring" {
			t.Fatalf("unexpected object: %+v", o.Metadata)
		}
	}
	if !slices.Equal(group.alerts(), baseAlerts) {
		t.Fatalf("group object = %v, want the same rules as without mm-namespace %v", group.alerts(), baseAlerts)
	}
	if !slices.Equal(pack.alerts(), defaultPackAlerts) {
		t.Fatalf("pack object = %v, want %v", pack.alerts(), defaultPackAlerts)
	}

	marked := false
	for _, g := range pack.Spec.Groups {
		for _, r := range g.Rules {
			if r.Record == packCoverageRecord && r.Labels["namespace"] == "mattermost-crimson" {
				marked = true
			}
		}
	}
	if !marked {
		t.Fatal("the pack must record its namespace so the group's rules skip it")
	}
	overlapping := map[string]bool{"MattermostHighLoginFailures": true, "MattermostWebSocketDrop": true, "MattermostHighErrorRate": true}
	for _, g := range group.Spec.Groups {
		for _, r := range g.Rules {
			if !strings.Contains(r.Expr, "by (namespace") && !strings.Contains(r.Expr, "by (le, namespace") {
				t.Errorf("%s must count per namespace: %s", r.Alert, r.Expr)
			}
			skips := strings.Contains(r.Expr, "unless on (namespace) "+packCoverageRecord)
			if skips != overlapping[r.Alert] {
				t.Errorf("%s skips pack namespaces = %v, want %v", r.Alert, skips, overlapping[r.Alert])
			}
		}
	}
	if packPlaceholder.MatchString(string(crd.data)) {
		t.Fatal("manifest still carries a placeholder")
	}
	if s := crd.section(); !strings.Contains(s, "two PrometheusRules") || !strings.Contains(s, "pages twice") || strings.Contains(s, "Operator") {
		t.Fatalf("section should explain the two objects without leaning on the operator:\n%s", s)
	}

	other, err := newMattermostTarget("ops", "mm-ops")
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.mattermostRulesFile(testRulesContext(formatCRD), &other)
	if err != nil {
		t.Fatal(err)
	}
	secondObjs := decodeRuleObjects(t, second.data)
	if secondObjs[0].Metadata.Name != group.Metadata.Name || secondObjs[1].Metadata.Name == pack.Metadata.Name {
		t.Fatalf("a second namespace must reuse the group object and get its own pack object: %q %q", secondObjs[0].Metadata.Name, secondObjs[1].Metadata.Name)
	}

	plain, err := p.mattermostRulesFile(testRulesContext(formatStandard), &target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain.data), "kind: PrometheusRule") || !strings.Contains(string(plain.data), "groups:") {
		t.Fatalf("standard rules should be a bare groups file:\n%s", plain.data)
	}
	if n := len(ruleAlertNames(t, plain.data)); n != wantTotal || plain.name != crd.name {
		t.Fatalf("standard file %q has %d alerts, want %d", plain.name, n, wantTotal)
	}
	if !strings.Contains(plain.section(), "only its `mattermost.*` groups") {
		t.Fatalf("standard section should say how to add a second namespace:\n%s", plain.section())
	}
}

func TestPackRoutesYAML(t *testing.T) {
	entry := alertConfig{Name: "mattermost-server--ops-alerts", Custom: true, Pack: packReceiverSlug}
	out := assembleRoutesYAML([]alertConfig{entry})

	var doc struct {
		Routes []struct {
			Matchers []string `yaml:"matchers"`
			Receiver string   `yaml:"receiver"`
			Continue bool     `yaml:"continue"`
		} `yaml:"routes"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("routes are not valid YAML: %v\n%s", err, out)
	}
	if len(doc.Routes) != 1 {
		t.Fatalf("want one live route, got %d:\n%s", len(doc.Routes), out)
	}
	route := doc.Routes[0]
	if route.Receiver != entry.Name || !route.Continue {
		t.Fatalf("route = %+v", route)
	}
	match := route.Matchers[0]
	if !strings.HasPrefix(match, `alertname=~"^(`) {
		t.Fatalf("matcher = %q", match)
	}
	for _, name := range wantPackAlerts {
		if !strings.Contains(match, name) {
			t.Errorf("route does not match %s", name)
		}
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "#  - matchers") {
			t.Fatalf("pack receiver got a commented stub: %q", line)
		}
	}
}

func packCRDSpecs() []crdReceiverSpec {
	return []crdReceiverSpec{
		{name: "ops-alerts-fallback", channel: "ops-alerts", secretName: "alertmanager-webhook-ops-alerts"},
		{
			slug:       packReceiverSlug,
			name:       "mattermost-server--ops-alerts",
			channel:    "ops-alerts",
			secretName: "alertmanager-webhook-ops-alerts",
			kind:       kindPack,
		},
	}
}

func TestPackAlertmanagerConfig(t *testing.T) {
	out := renderAlertmanagerConfig("mattermost-alertmanager-ops-alerts", "monitoring", "ops-alerts-fallback", packCRDSpecs())

	var cr struct {
		Spec struct {
			Route struct {
				Receiver string `yaml:"receiver"`
				Matchers []struct {
					Name      string `yaml:"name"`
					Value     string `yaml:"value"`
					MatchType string `yaml:"matchType"`
				} `yaml:"matchers"`
				Routes []struct {
					Matchers []struct {
						Name      string `yaml:"name"`
						Value     string `yaml:"value"`
						MatchType string `yaml:"matchType"`
					} `yaml:"matchers"`
					Receiver string `yaml:"receiver"`
				} `yaml:"routes"`
			} `yaml:"route"`
			Receivers []struct {
				Name string `yaml:"name"`
			} `yaml:"receivers"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(out), &cr); err != nil {
		t.Fatalf("AlertmanagerConfig is not valid YAML: %v\n%s", err, out)
	}
	route := cr.Spec.Route
	if route.Receiver != "ops-alerts-fallback" {
		t.Fatalf("parent receiver = %q", route.Receiver)
	}
	if len(route.Matchers) != 1 || route.Matchers[0].Name != "alertname" || route.Matchers[0].MatchType != "=~" {
		t.Fatalf("parent must gate on alertname, got %+v", route.Matchers)
	}
	if len(route.Routes) != 1 || route.Routes[0].Receiver != "mattermost-server--ops-alerts" {
		t.Fatalf("sub-routes = %+v", route.Routes)
	}
	if route.Routes[0].Matchers[0].Value != route.Matchers[0].Value {
		t.Fatal("sub-route and parent gate disagree on the alert names")
	}
	if len(cr.Spec.Receivers) != 2 {
		t.Fatalf("want fallback plus pack receiver, got %d", len(cr.Spec.Receivers))
	}
	if strings.Contains(out, "runbook") && strings.Contains(out, "name: runbook") {
		t.Fatal("pack CR must not gate on a runbook label")
	}
}

// A pack alert has no runbook label, and the pack receiver must still render the
// shared severity colors and the description body, plus per-alert diagnostics.
func TestPackReceiverRendersSharedBody(t *testing.T) {
	out := renderCRDReceiver(packCRDSpecs()[1])
	for _, want := range []string{`eq .CommonLabels.severity "critical"`, "danger", "good", "sendResolved: true", "**Description:**", "Quick diagnostics"} {
		if !strings.Contains(out, want) {
			t.Errorf("pack receiver missing %q", want)
		}
	}
}

func TestMattermostTargetKeepsPackOutOfTheGroup(t *testing.T) {
	group, slugs, err := resolveAddTarget(mattermostGroup)
	if err != nil || group != mattermostGroup || slices.Contains(slugs, packReceiverSlug) {
		t.Fatalf("resolve = %q %v %v", group, slugs, err)
	}
	if _, _, err = resolveAddTarget(packReceiverSlug); err == nil {
		t.Fatal("the pack receiver must not be addable as its own target")
	}
	if _, _, err = resolveAddTarget(legacyPackReceiverSlug); err == nil {
		t.Fatal("the legacy pack name must not be addable")
	}
	if slices.Contains(runbookSlugs(), packReceiverSlug) {
		t.Fatal("the pack must not collide with a runbook slug")
	}
	for _, reserved := range []string{packReceiverSlug, legacyPackReceiverSlug} {
		if _, err := validateCustomReceiverName(reserved, "ops", "alerts"); err == nil {
			t.Fatalf("add-custom must reject the reserved name %q", reserved)
		}
	}
	if packFor("high-cpu-usage") != "" || packFor(packReceiverSlug) != packReceiverSlug {
		t.Fatal("packFor must only tag the pack receiver")
	}
}

func TestParseFormatArgsPack(t *testing.T) {
	opts, msg := parseFormatArgs([]string{"mattermost", "crd", "mm-namespace=chat", "mm-name=mm", "namespace=monitoring"})
	if msg != "" {
		t.Fatalf("unexpected message: %s", msg)
	}
	if opts.target != mattermostGroup || opts.format != formatCRD || opts.mmNamespace != "chat" || opts.mmName != "mm" || opts.namespace != "monitoring" {
		t.Fatalf("opts = %+v", opts)
	}
	if _, msg := parseFormatArgs([]string{"mm-namespace=chat"}); !strings.Contains(msg, "only apply") {
		t.Fatalf("mm-namespace without mattermost must be refused, got %q", msg)
	}
	if opts, msg := parseFormatArgs([]string{"mattermost"}); msg != "" || opts.target != mattermostGroup || opts.mmNamespace != "" {
		t.Fatalf("plain mattermost must be accepted: %+v %q", opts, msg)
	}
}

func TestRulesFileForChannelPointsPackUsersToFormatRules(t *testing.T) {
	p := &Plugin{}
	p.API = &ruleDMAPI{uploads: map[string]string{}}
	rules, reason := p.rulesFileForChannel(testRulesContext(formatCRD), []alertConfig{
		{Name: "mattermost-server--ops-alerts", Team: "ops", Channel: "alerts", Custom: true, Pack: packReceiverSlug},
	})
	if rules != nil || !strings.Contains(reason, "format rules "+mattermostGroup) {
		t.Fatalf("rules=%v reason=%q", rules, reason)
	}
}

func TestRulesFileForChannelSaysWhenThePackRulesAreLeftOut(t *testing.T) {
	p := &Plugin{}
	p.API = &ruleDMAPI{uploads: map[string]string{}}
	runbook := alertConfig{Name: scaffoldSets["compute"][0] + "--ops-alerts", Team: "ops", Channel: "alerts"}
	pack := alertConfig{Name: "mattermost-server--ops-alerts", Team: "ops", Channel: "alerts", Custom: true, Pack: packReceiverSlug}

	rules, _ := p.rulesFileForChannel(testRulesContext(formatCRD), []alertConfig{runbook, pack})
	if rules == nil || !strings.Contains(rules.section(), "format rules "+mattermostGroup) {
		t.Fatalf("a channel with runbooks and the pack should be told the operator alerts are missing: %+v", rules)
	}
	alone, _ := p.rulesFileForChannel(testRulesContext(formatCRD), []alertConfig{runbook})
	if strings.Contains(alone.section(), "format rules "+mattermostGroup) {
		t.Fatalf("a channel without the pack should not get the note:\n%s", alone.section())
	}
}

// CI runs promtool against the rendered rules. Set RENDER_PACK_RULES_DIR to
// write them out; the test is a no-op otherwise.
func TestWritePackRulesForPromtool(t *testing.T) {
	dir := os.Getenv("RENDER_PACK_RULES_DIR")
	if dir == "" {
		t.Skip("RENDER_PACK_RULES_DIR not set")
	}
	for name, opts := range map[string]mattermostOptions{
		"default": {},
		"all":     {dbReplicas: true, postgresPod: true},
	} {
		target := testTarget(t).withOptions(opts)
		out, err := renderMattermostOperatorRules(target)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "mattermost-operator-rules-"+name+".yaml"), []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
		merged, err := rulesTestPlugin().mattermostRulesFile(testRulesContext(formatStandard), &target)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "mattermost-group-rules-"+name+".yaml"), merged.data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// The route claims the operator and Postgres pod alerts and nothing else, including the
// chart's own MattermostDown and the older runbook alerts.
func TestPackRegexMatchesOnlyPackAlerts(t *testing.T) {
	expr, err := packAlertRegex()
	if err != nil {
		t.Fatal(err)
	}
	// Alertmanager anchors matchers itself; the explicit ^$ is for readers.
	re := regexp.MustCompile(expr)
	postgres, err := postgresAlertnames()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append(slices.Clone(wantPackAlerts), postgres...) {
		if !re.MatchString(name) {
			t.Errorf("%s should match", name)
		}
	}
	for _, name := range []string{
		"MattermostDown", "MattermostPodRestarting", "MattermostPostgresNotReady",
		"MattermostAPIHighLatency", "KubePodCrashLooping", "Watchdog",
		"MattermostServerDownTest", "XMattermostServerDown", "",
	} {
		if re.MatchString(name) {
			t.Errorf("%q must not match", name)
		}
	}
}

// A pack added to a channel that already has a runbook group must not reuse that
// group's object names, or applying it overwrites the group's AlertmanagerConfig.
func TestPackCRDObjectsDoNotCollideWithRunbookGroup(t *testing.T) {
	p := &Plugin{}
	p.API = siteURLAPI{}
	p.setConfiguration(newConfiguration(nil, "", 0, "", "", 0))

	runbook := alertConfig{Name: "mattermost-api-high-latency--ops-alerts", Team: "ops", Channel: "alerts", WebhookID: "hook1"}
	pack := alertConfig{Name: "mattermost-server--ops-alerts", Team: "ops", Channel: "alerts", WebhookID: "hook2", Custom: true, Pack: packReceiverSlug}

	alone, _ := p.assembleCRDManifest([]alertConfig{runbook}, "monitoring")
	packOnly, count := p.assembleCRDManifest([]alertConfig{pack}, "monitoring")
	both, _ := p.assembleCRDManifest([]alertConfig{runbook, pack}, "monitoring")
	if count != 1 {
		t.Fatalf("pack manifest has %d AlertmanagerConfigs", count)
	}

	names := func(manifest string) []string {
		var out []string
		for line := range strings.SplitSeq(manifest, "\n") {
			if rest, ok := strings.CutPrefix(line, "  name: "); ok {
				out = append(out, rest)
			}
		}
		return out
	}
	if len(names(packOnly)) == 0 || len(names(alone)) == 0 {
		t.Fatal("no objects rendered")
	}
	for _, name := range names(packOnly) {
		if !strings.HasSuffix(name, "-"+packReceiverSlug) {
			t.Errorf("pack object %q must carry the pack suffix", name)
		}
		if slices.Contains(names(alone), name) {
			t.Errorf("pack object %q collides with the runbook group", name)
		}
	}
	for _, name := range names(alone) {
		if !slices.Contains(names(both), name) {
			t.Errorf("adding the pack renamed the runbook group's object %q", name)
		}
	}
	if got, want := len(names(both)), len(names(alone))+len(names(packOnly)); got != want {
		t.Fatalf("combined export has %d objects, want %d", got, want)
	}
}

func TestParseFormatArgsTargets(t *testing.T) {
	cases := []struct {
		name       string
		words      []string
		wantTarget string
		wantFormat string
		wantMsg    string
	}{
		{"group then format", []string{"security", "crd"}, "security", formatCRD, ""},
		{"format then group", []string{"crd", "security"}, "security", formatCRD, ""},
		{"group alone", []string{"compute"}, "compute", "", ""},
		{"runbook slug", []string{"high-cpu-usage", "crd"}, "high-cpu-usage", formatCRD, ""},
		{"all is the full set", []string{"all", "crd"}, "", formatCRD, ""},
		{"case insensitive", []string{"Security", "CRD"}, "security", formatCRD, ""},
		{"two targets", []string{"security", "compute"}, "", "", "Too many words"},
		{"two formats", []string{"crd", "standard"}, "", "", "Too many words"},
		{"unknown word", []string{"nope"}, "", "", "Unknown format or target"},
		{"mm flag on another group", []string{"security", "mm-namespace=chat"}, "", "", "only apply"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts, msg := parseFormatArgs(c.words)
			if c.wantMsg != "" {
				if !strings.Contains(msg, c.wantMsg) {
					t.Fatalf("msg = %q, want one containing %q", msg, c.wantMsg)
				}
				return
			}
			if msg != "" || opts.target != c.wantTarget || opts.format != c.wantFormat {
				t.Fatalf("opts = %+v msg = %q", opts, msg)
			}
		})
	}
}

func TestRulesForTargetIsPerGroup(t *testing.T) {
	p := rulesTestPlugin()

	rules, msg := p.rulesForTarget(testRulesContext(formatCRD), rulesOptions{target: "security"})
	if msg != "" {
		t.Fatal(msg)
	}
	if rules.name != prometheusRuleObject+"-security.yaml" {
		t.Fatalf("name = %q", rules.name)
	}
	plain, msg := p.rulesForTarget(testRulesContext(formatStandard), rulesOptions{target: "security"})
	if msg != "" {
		t.Fatal(msg)
	}
	alerts := ruleAlertNames(t, plain.data)
	if len(alerts) == 0 || len(alerts) != rules.count || slices.Contains(alerts, "MattermostAPIHighLatency") || slices.Contains(alerts, "HighCPUUsage") {
		t.Fatalf("security rules = %v (count %d)", alerts, rules.count)
	}
	if !strings.Contains(rules.section(), "`security`") {
		t.Fatalf("section should name the group:\n%s", rules.section())
	}

	one, msg := p.rulesForTarget(testRulesContext(formatStandard), rulesOptions{target: "high-cpu-usage"})
	if msg != "" || one.count == 0 || one.count >= rules.count+len(scaffoldSets["compute"]) {
		t.Fatalf("single runbook rules: %+v %q", one, msg)
	}

	mm, msg := p.rulesForTarget(testRulesContext(formatCRD), rulesOptions{target: mattermostGroup})
	if msg != "" || mm.count != len(scaffoldSets[mattermostGroup]) {
		t.Fatalf("mattermost without a namespace: %+v %q", mm, msg)
	}
	if _, msg = p.rulesForTarget(testRulesContext(formatCRD), rulesOptions{target: mattermostGroup, mmNamespace: "Bad_NS"}); !strings.Contains(msg, "mm-namespace") {
		t.Fatalf("bad namespace message = %q", msg)
	}
}

func TestDBReplicaLagIsOptIn(t *testing.T) {
	alerts := func(opts mattermostOptions) []string {
		out, err := renderMattermostOperatorRules(testTarget(t).withOptions(opts))
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Groups []struct {
				Name  string `yaml:"name"`
				Rules []struct {
					Alert string `yaml:"alert"`
				} `yaml:"rules"`
			} `yaml:"groups"`
		}
		if err = yaml.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("not valid YAML after dropping the alert: %v\n%s", err, out)
		}
		var names []string
		for _, g := range doc.Groups {
			if len(g.Rules) == 0 {
				t.Errorf("group %s is empty", g.Name)
			}
			for _, r := range g.Rules {
				if r.Alert != "" {
					names = append(names, r.Alert)
				}
			}
		}
		return names
	}

	if got := alerts(mattermostOptions{}); !slices.Equal(got, defaultPackAlerts) {
		t.Fatalf("default alerts = %v, want %v", got, defaultPackAlerts)
	}
	if got := alerts(mattermostOptions{dbReplicas: true}); !slices.Equal(got, wantPackAlerts) {
		t.Fatalf("with mm-db-replicas alerts = %v, want %v", got, wantPackAlerts)
	}
}

func TestPostgresPodRules(t *testing.T) {
	p := rulesTestPlugin()
	plain := testTarget(t)
	withPG := testTarget(t).withOptions(mattermostOptions{postgresPod: true})

	base, err := p.mattermostRulesFile(testRulesContext(formatStandard), &plain)
	if err != nil {
		t.Fatal(err)
	}
	with, err := p.mattermostRulesFile(testRulesContext(formatStandard), &withPG)
	if err != nil {
		t.Fatal(err)
	}
	postgres, err := postgresAlertnames()
	if err != nil {
		t.Fatal(err)
	}
	baseAlerts := ruleAlertNames(t, base.data)
	withAlerts := ruleAlertNames(t, with.data)
	for _, name := range postgres {
		if slices.Contains(baseAlerts, name) {
			t.Errorf("%s must be off by default", name)
		}
		if !slices.Contains(withAlerts, name) {
			t.Errorf("%s missing with mm-postgres-pod=true", name)
		}
	}
	if len(withAlerts) != len(baseAlerts)+len(postgres) || with.count != len(withAlerts) {
		t.Fatalf("count = %d, alerts = %d, want %d", with.count, len(withAlerts), len(baseAlerts)+len(postgres))
	}
	if !strings.Contains(string(with.data), `namespace="mattermost-crimson"`) || packPlaceholder.MatchString(string(with.data)) {
		t.Fatal("postgres rules must carry the namespace and no placeholder")
	}
}

func TestExtractMattermostOptions(t *testing.T) {
	rest, opts, err := extractMattermostOptions([]string{"mattermost", "mm-db-replicas=true", "mm-postgres-pod=TRUE", "crd"})
	if err != nil || !opts.dbReplicas || !opts.postgresPod || !opts.given || !slices.Equal(rest, []string{"mattermost", "crd"}) {
		t.Fatalf("rest=%v opts=%+v err=%v", rest, opts, err)
	}
	_, opts, err = extractMattermostOptions([]string{"mm-db-replicas=false"})
	if err != nil || opts.dbReplicas || !opts.given {
		t.Fatalf("false must parse and count as given: %+v %v", opts, err)
	}
	if _, opts, err = extractMattermostOptions([]string{"mattermost"}); err != nil || opts.given {
		t.Fatalf("no flags: %+v %v", opts, err)
	}
	if _, _, err = extractMattermostOptions([]string{"mm-postgres-pod=yes"}); err == nil || !strings.Contains(err.Error(), "true or false") {
		t.Fatalf("bad value error = %v", err)
	}
}

func TestPackThresholdFlags(t *testing.T) {
	rest, opts, err := extractMattermostOptions([]string{
		"mattermost", "mm-http-error-percent=10", "mm-http-min-rps=0.5", "mm-auth-error-percent=30", "mm-auth-min-rps=2",
		"mm-login-failure-percent=40", "mm-login-min-failures=10", "mm-login-spray-failures=200",
		"mm-cpu-limit-percent=95", "mm-memory-limit-percent=80", "mm-db-replicas=true", "mm-db-replica-lag-seconds=30", "crd",
	})
	if err != nil || !opts.given || opts.thresholds == nil || !slices.Equal(rest, []string{"mattermost", "crd"}) {
		t.Fatalf("rest=%v opts=%+v err=%v", rest, opts, err)
	}
	out, err := renderMattermostOperatorRules(testTarget(t).withOptions(opts))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		") > 0.1\n", "> 0.5\n", ") > 0.3\n", "> 2\n", ") > 0.4\n", "> 10\n", "> 200\n",
		") > 0.95\n", ") > 0.8\n", "> 30\n", "401/403 rate > 30%", "over 200 failures", "95% of limit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered rules missing %q", want)
		}
	}

	if _, opts, err = extractMattermostOptions([]string{"mattermost"}); err != nil || opts.thresholds != nil || opts.given {
		t.Fatalf("no threshold flags must keep the defaults: %+v %v", opts, err)
	}
	if testTarget(t).withOptions(opts).thresholds != defaultPackThresholds {
		t.Fatal("a target without threshold flags must use the defaults")
	}

	for _, bad := range []string{
		"mm-http-error-percent=0", "mm-http-error-percent=101", "mm-cpu-limit-percent=8.5", "mm-auth-error-percent=x",
		"mm-http-min-rps=-1", "mm-login-spray-failures=NaN", "mm-auth-min-rps=Inf", "mm-db-replica-lag-seconds=1e400",
		`mm-login-min-failures=1"}`,
	} {
		if _, _, err := extractMattermostOptions([]string{bad}); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
	if _, msg := parseFormatArgs([]string{"security", "mm-cpu-limit-percent=90"}); !strings.Contains(msg, "only apply") {
		t.Fatalf("threshold flag on another group must be refused, got %q", msg)
	}
	for _, flag := range thresholdFlagNames() {
		if !strings.Contains(packThresholdHelp, "`"+strings.TrimSuffix(flag, "=")+"`") {
			t.Errorf("help does not describe %s", flag)
		}
	}
}

func TestParseFormatArgsPackOptions(t *testing.T) {
	opts, msg := parseFormatArgs([]string{"mattermost", "mm-namespace=chat", "mm-db-replicas=true", "mm-postgres-pod=true", "crd"})
	if msg != "" || !opts.mmOptions.dbReplicas || !opts.mmOptions.postgresPod {
		t.Fatalf("opts = %+v msg = %q", opts, msg)
	}
	if _, msg = parseFormatArgs([]string{"security", "mm-postgres-pod=true"}); !strings.Contains(msg, "only apply") {
		t.Fatalf("flags on another group must be refused, got %q", msg)
	}
	if _, msg = parseFormatArgs([]string{"mattermost", "mm-db-replicas=maybe"}); !strings.Contains(msg, "true or false") {
		t.Fatalf("bad value must be refused, got %q", msg)
	}
	plugin := rulesTestPlugin()
	if _, errMsg := plugin.rulesForTarget(testRulesContext(formatCRD), rulesOptions{target: mattermostGroup, mmOptions: mattermostOptions{postgresPod: true, given: true}}); !strings.Contains(errMsg, "mm-namespace") {
		t.Fatalf("flags without mm-namespace must ask for it, got %q", errMsg)
	}
}

// A comment sits above the alert it describes. Dropping a rule must drop its
// comment and keep the next rule's, not hand the comment to a neighbour.
func TestFilterKeepsEachCommentWithItsAlert(t *testing.T) {
	raw, err := root.SamplesFS.ReadFile("samples/prometheus-rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	kept := slices.DeleteFunc(slices.Clone(scaffoldSets[mattermostGroup]), func(slug string) bool {
		return slug == "mattermost-websocket-drop" || slug == "mattermost-high-login-failures" || slug == "mattermost-high-error-rate"
	})
	out, _, err := filterPrometheusRules(string(raw), kept)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "MattermostHighLoginFailures") || strings.Contains(out, "MattermostWebSocketDrop") || strings.Contains(out, "MattermostHighErrorRate") {
		t.Fatalf("dropped rules left comments behind:\n%s", out)
	}
	idx := strings.Index(out, "# MattermostDBSlowQuery")
	alert := strings.Index(out, "- alert: MattermostDBSlowQuery")
	if idx < 0 || alert < 0 || idx > alert {
		t.Fatalf("MattermostDBSlowQuery lost its comment:\n%s", out)
	}
}
