package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
	"MattermostLoginFailureSpike",
	"MattermostSearchDown",
	"MattermostClusterUnhealthy",
	"MattermostLogsDropped",
}

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
	out, err := renderMattermostOperatorRules(testTarget(t))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if left := packPlaceholder.FindString(out); left != "" {
		t.Fatalf("placeholder left unfilled: %s", left)
	}

	for _, want := range []string{
		`namespace="mattermost-crimson"`,
		`service="mattermost-metrics"`,
		`pod=~"mattermost-[^-]+-[^-]+"`,
		"> 0.85",
		"> 0.9",
		"> 0.05",
		"> 0.5",
		"> 5\n",
		"85% of limit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered rules missing %q", want)
		}
	}

	var doc struct {
		Groups []struct {
			Rules []struct {
				Alert       string            `yaml:"alert"`
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
	if !strings.Contains(out, `service="mm-prod-metrics"`) || !strings.Contains(out, `pod=~"mm-prod-[^-]+-[^-]+"`) {
		t.Fatal("custom name did not reach the service and pod selector")
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
			out = append(out, r.Alert)
		}
	}
	return out
}

func TestMattermostRulesFileMergesOperatorAlerts(t *testing.T) {
	p := rulesTestPlugin()
	target := testTarget(t)

	base, err := p.mattermostRulesFile("monitoring", nil, formatStandard)
	if err != nil {
		t.Fatal(err)
	}
	baseAlerts := ruleAlertNames(t, base.data)
	if len(baseAlerts) != len(scaffoldSets[mattermostGroup]) || slices.Contains(baseAlerts, "MattermostServerDown") {
		t.Fatalf("without mm-namespace want only the group's runbook rules, got %v", baseAlerts)
	}

	crd, err := p.mattermostRulesFile("monitoring", &target, formatCRD)
	if err != nil {
		t.Fatal(err)
	}
	if crd.count != len(baseAlerts)+len(wantPackAlerts) {
		t.Fatalf("count = %d, want %d", crd.count, len(baseAlerts)+len(wantPackAlerts))
	}
	var obj struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name      string            `yaml:"name"`
			Namespace string            `yaml:"namespace"`
			Labels    map[string]string `yaml:"labels"`
		} `yaml:"metadata"`
		Spec struct {
			Groups []struct {
				Rules []struct {
					Alert string `yaml:"alert"`
				} `yaml:"rules"`
			} `yaml:"groups"`
		} `yaml:"spec"`
	}
	if err = yaml.Unmarshal(crd.data, &obj); err != nil {
		t.Fatalf("manifest is not valid YAML: %v", err)
	}
	if obj.Kind != "PrometheusRule" || obj.Metadata.Namespace != "monitoring" || obj.Metadata.Name != prometheusRuleObject+"-"+mattermostGroup {
		t.Fatalf("unexpected object: %+v", obj)
	}
	var got []string
	for _, g := range obj.Spec.Groups {
		for _, r := range g.Rules {
			got = append(got, r.Alert)
		}
	}
	for _, want := range append(slices.Clone(baseAlerts), wantPackAlerts...) {
		if !slices.Contains(got, want) {
			t.Errorf("merged manifest missing %s", want)
		}
	}
	if packPlaceholder.MatchString(string(crd.data)) {
		t.Fatal("manifest still carries a placeholder")
	}
	if !strings.Contains(crd.section(), "remove them from this file") {
		t.Fatalf("section missing the operator hand-off:\n%s", crd.section())
	}

	plain, err := p.mattermostRulesFile("monitoring", &target, formatStandard)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain.data), "kind: PrometheusRule") || !strings.Contains(string(plain.data), "groups:") {
		t.Fatalf("standard rules should be a bare groups file:\n%s", plain.data)
	}
	if n := len(ruleAlertNames(t, plain.data)); n != len(baseAlerts)+len(wantPackAlerts) {
		t.Fatalf("standard merged file has %d alerts", n)
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
			custom:     true,
			pack:       packReceiverSlug,
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
// shared severity colors and the description body.
func TestPackReceiverRendersSharedBody(t *testing.T) {
	out := renderCRDReceiver(packCRDSpecs()[1])
	for _, want := range []string{`eq .CommonLabels.severity "critical"`, "danger", "good", "sendResolved: true", "**Description:**"} {
		if !strings.Contains(out, want) {
			t.Errorf("pack receiver missing %q", want)
		}
	}
	if strings.Contains(out, "Quick diagnostics") {
		t.Error("a pack receiver has no runbook diagnostics")
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
	rules, reason := p.rulesFileForChannel("monitoring", []alertConfig{
		{Name: "mattermost-server--ops-alerts", Team: "ops", Channel: "alerts", Custom: true, Pack: packReceiverSlug},
	}, formatCRD)
	if rules != nil || !strings.Contains(reason, "format rules "+mattermostGroup) {
		t.Fatalf("rules=%v reason=%q", rules, reason)
	}
}

// CI runs promtool against the rendered rules. Set RENDER_PACK_RULES_DIR to
// write them out; the test is a no-op otherwise.
func TestWritePackRulesForPromtool(t *testing.T) {
	dir := os.Getenv("RENDER_PACK_RULES_DIR")
	if dir == "" {
		t.Skip("RENDER_PACK_RULES_DIR not set")
	}
	out, err := renderMattermostOperatorRules(testTarget(t))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "mattermost-operator-rules.yaml"), []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}

	target := testTarget(t)
	merged, err := rulesTestPlugin().mattermostRulesFile("monitoring", &target, formatStandard)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "mattermost-group-rules.yaml"), merged.data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The route claims the 13 operator alerts and nothing else, including the
// chart's own MattermostDown and the older runbook alerts.
func TestPackRegexMatchesOnlyPackAlerts(t *testing.T) {
	expr, err := packAlertRegex()
	if err != nil {
		t.Fatal(err)
	}
	// Alertmanager anchors matchers itself; the explicit ^$ is for readers.
	re := regexp.MustCompile(expr)
	for _, name := range wantPackAlerts {
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
	if fmt.Sprint(names(packOnly)) == "" {
		t.Fatal("no objects rendered")
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

	rules, msg := p.rulesForTarget("monitoring", formatCRD, exportOptions{target: "security"})
	if msg != "" {
		t.Fatal(msg)
	}
	if rules.name != prometheusRuleObject+"-security.yaml" {
		t.Fatalf("name = %q", rules.name)
	}
	plain, msg := p.rulesForTarget("monitoring", formatStandard, exportOptions{target: "security"})
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

	one, msg := p.rulesForTarget("monitoring", formatStandard, exportOptions{target: "high-cpu-usage"})
	if msg != "" || one.count == 0 || one.count >= rules.count+len(scaffoldSets["compute"]) {
		t.Fatalf("single runbook rules: %+v %q", one, msg)
	}

	mm, msg := p.rulesForTarget("monitoring", formatCRD, exportOptions{target: mattermostGroup})
	if msg != "" || mm.count != len(scaffoldSets[mattermostGroup]) {
		t.Fatalf("mattermost without a namespace: %+v %q", mm, msg)
	}
	if _, msg = p.rulesForTarget("monitoring", formatCRD, exportOptions{target: mattermostGroup, mmNamespace: "Bad_NS"}); !strings.Contains(msg, "mm-namespace") {
		t.Fatalf("bad namespace message = %q", msg)
	}
}
