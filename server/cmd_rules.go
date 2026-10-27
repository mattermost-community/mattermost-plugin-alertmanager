package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

// prometheusRuleObject is the PrometheusRule metadata.name for the full
// sample set. A per-group add uses this plus the group name so applying
// compute does not replace the database object.
const prometheusRuleObject = "mattermost-alertmanager-rules"

// runbookLabelLine is the join key inside a sample rule. The value is the
// scaffold slug, so a group add can keep only that group's rules.
var runbookLabelLine = regexp.MustCompile(`(?m)^[ \t]+runbook:[ \t]+([A-Za-z0-9-]+)[ \t]*$`)

// handleFormatRules points users at the shipped sample Prometheus alerting
// rules — a browsable HTML view and the raw file to download. This is the
// upstream half of the pipeline the plugin does NOT own: the plugin creates the
// Alertmanager receivers and routes, but SOMETHING in the user's Prometheus
// has to emit alerts carrying the `runbook:` label. This command hands them a
// ready starting set so they don't have to go dig it out of the repo.
//
// With `crd` the command instead DMs a ready-to-apply PrometheusRule CRD
// manifest (monitoring.coreos.com/v1) that wraps the same groups block, for
// teams running Prometheus Operator.
func (p *Plugin) handleFormatRules(args *model.CommandArgs, opts exportOptions) (string, error) {
	namespace := opts.namespace
	if namespace == "" {
		namespace = defaultCRDNamespace
	}

	if opts.format == formatCRD {
		return p.handleRulesOperator(args.UserId, namespace)
	}

	base := fmt.Sprintf("%s/plugins/%s/public/help", p.siteURL(), Manifest.Id)
	viewURL := base + "/prometheus-rules.html"
	rawURL := base + "/prometheus-rules.yaml"

	crdURL := base + "/mattermost-prometheus-rules-crd.yaml"

	return fmt.Sprintf(
		"**Sample Prometheus alerting rules** — the upstream half of the pipeline.\n\n"+
			"The plugin creates the Alertmanager receivers and routes, but your **Prometheus** has to emit the alerts that carry the `runbook:` label. These samples give you one working rule for every runbook (37 rules across 36 runbooks) so you don't start from a blank page.\n\n"+
			"- 📖 [View the rules in your browser](%s)\n"+
			"- ⬇️ [Download `prometheus-rules.yaml`](%s) — plain format, add to your Prometheus `rule_files:` glob\n"+
			"- ⬇️ [Download `mattermost-prometheus-rules-crd.yaml`](%s) — PrometheusRule CRD (`monitoring.coreos.com/v1`, the only version this plugin ships), namespace `monitoring`. It contains every group: compute, application, database, storage, networking, observability, security, and mattermost. For a different namespace run `/alertmanager format rules crd namespace=<ns>` instead\n\n"+
			"⚠️ Every threshold is a demo-cluster placeholder — tune to your traffic and SLOs before production. The `runbook:` label on each rule is the join key that routes it to the matching runbook in chat.",
		viewURL, rawURL, crdURL,
	), nil
}

// handleRulesOperator generates a PrometheusRule CRD manifest from the
// embedded samples file and DMs it to the caller as a downloadable file.
// No secrets are involved — the manifest contains only alerting rules.
func (p *Plugin) handleRulesOperator(userID, namespace string) (string, error) {
	rawBytes, err := root.SamplesFS.ReadFile("samples/prometheus-rules.yaml")
	if err != nil {
		return fmt.Sprintf(":warning: Could not read embedded rules file: %v", err), nil
	}
	extras, err := p.loadImageAllowlist()
	if err != nil {
		return fmt.Sprintf(":warning: Could not read the image allowlist: %v", err), nil
	}
	rawYAML, err := applyImageAllowlist(string(rawBytes), extras)
	if err != nil {
		return fmt.Sprintf(":warning: Could not apply the image allowlist: %v", err), nil
	}

	manifest := buildPrometheusRuleManifest(rawYAML, namespace, prometheusRuleObject)
	extraNote := ""
	if len(extras) > 0 {
		extraNote = fmt.Sprintf("\n\nIncludes %d extra image-registry prefix(es) from `/alertmanager admin allowlist`: `%s`.", len(extras), strings.Join(extras, "`, `"))
	}

	dm, appErr := p.API.GetDirectChannel(p.BotUserID, userID)
	if appErr != nil {
		return fmt.Sprintf(":warning: Could not open bot DM channel: %v", appErr.Error()), nil
	}

	info, appErr := p.API.UploadFile([]byte(manifest), dm.Id, "mattermost-prometheus-rules.yaml")
	if appErr != nil {
		return fmt.Sprintf(":warning: Could not upload manifest: %v", appErr.Error()), nil
	}

	post := &model.Post{
		UserId:    p.BotUserID,
		ChannelId: dm.Id,
		Message: fmt.Sprintf(
			"PrometheusRule CRD containing all 37 alert rules across 36 runbooks (`%s`).\n\n"+
				"Review, then apply into the `%s` namespace:\n\n"+
				"```\nkubectl apply -n %s -f mattermost-prometheus-rules.yaml\n```\n\n"+
				"Adjust `release: kube-prometheus-stack` to your Helm release name. Every threshold is a demo placeholder — tune before production.%s",
			TargetPrometheusRuleAPIVersion, namespace, namespace, extraNote,
		),
		FileIds: []string{info.Id},
	}
	if _, appErr := p.API.CreatePost(post); appErr != nil {
		return fmt.Sprintf(":warning: Could not send DM: %v", appErr.Error()), nil
	}

	return fmt.Sprintf(
		":page_facing_up: **Sent `mattermost-prometheus-rules.yaml` (PrometheusRule CRD, `%s`) to your DM with `@%s`.** Review and apply:\n\n"+
			"```\nkubectl apply -n %s -f mattermost-prometheus-rules.yaml\n```\n\n"+
			"Adjust the `release` label to your Helm release name before applying.%s",
		TargetPrometheusRuleAPIVersion, webhookUsername, namespace, extraNote,
	), nil
}

// buildPrometheusRuleManifest wraps the raw prometheus-rules.yaml content
// (which starts with comments and then a bare `groups:` block) into a
// PrometheusRule CRD. The groups content is indented 2 extra spaces to nest
// under `spec:`, preserving all inline comments.
func buildPrometheusRuleManifest(rawYAML, namespace, name string) string {
	var b strings.Builder
	b.WriteString("# PrometheusRule CRD — Prometheus Operator " + TargetPrometheusRuleAPIVersion + "\n")
	b.WriteString("# Generated by the Alertmanager plugin\n")
	b.WriteString("# Adjust the 'release' label to match your Helm release name.\n")
	b.WriteString("# Apply with:\n")
	b.WriteString("#   kubectl apply -n " + namespace + " -f " + name + ".yaml\n")
	b.WriteString("---\n")
	b.WriteString("apiVersion: " + TargetPrometheusRuleAPIVersion + "\n")
	b.WriteString("kind: PrometheusRule\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	b.WriteString("  namespace: " + namespace + "\n")
	b.WriteString("  labels:\n")
	b.WriteString("    release: kube-prometheus-stack\n")
	b.WriteString("spec:\n")

	// Skip the file header comments and find the groups: block.
	// Every line in the groups block gets 2 extra spaces so it nests correctly
	// under spec:. Inline comments (lines starting with spaces then #) are
	// preserved unchanged — they just get the same 2-space indent.
	inGroups := false
	for line := range strings.SplitSeq(rawYAML, "\n") {
		if !inGroups {
			if strings.HasPrefix(line, "groups:") {
				inGroups = true
				b.WriteString("  groups:\n")
			}
			continue
		}
		if line == "" {
			b.WriteByte('\n')
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}

// filterPrometheusRules keeps the sample rules whose runbook label is in
// slugs. Group banners and rules for other slugs are dropped. The result
// is a bare groups: document, which buildPrometheusRuleManifest wraps.
func filterPrometheusRules(raw string, slugs []string) (string, int, error) {
	want := make(map[string]bool, len(slugs))
	for _, slug := range slugs {
		want[slug] = true
	}

	lines := strings.Split(raw, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "groups:") {
			start = i
			break
		}
	}
	if start < 0 {
		return "", 0, fmt.Errorf("sample rules have no groups block")
	}

	type ruleGroup struct {
		header []string
		rules  [][]string
	}
	var groups []ruleGroup
	gi := -1
	var rule []string
	flush := func() {
		if gi < 0 || len(rule) == 0 {
			rule = nil
			return
		}
		match := runbookLabelLine.FindStringSubmatch(strings.Join(rule, "\n"))
		if match != nil && want[match[1]] {
			groups[gi].rules = append(groups[gi].rules, append([]string{}, rule...))
		}
		rule = nil
	}
	for _, line := range lines[start+1:] {
		if strings.HasPrefix(line, "  - name:") {
			flush()
			groups = append(groups, ruleGroup{header: []string{line}})
			gi = len(groups) - 1
			continue
		}
		if gi < 0 {
			continue
		}
		if strings.HasPrefix(line, "      - alert:") {
			flush()
			rule = []string{line}
			continue
		}
		if rule != nil {
			rule = append(rule, line)
			continue
		}
		groups[gi].header = append(groups[gi].header, line)
	}
	flush()

	var b strings.Builder
	b.WriteString("groups:\n")
	n := 0
	for _, g := range groups {
		if len(g.rules) == 0 {
			continue
		}
		for _, header := range g.header {
			b.WriteString(header)
			b.WriteByte('\n')
		}
		for _, one := range g.rules {
			for _, line := range one {
				b.WriteString(line)
				b.WriteByte('\n')
			}
			n++
		}
	}
	if n == 0 {
		return "", 0, fmt.Errorf("no sample rules match these runbooks")
	}
	return b.String(), n, nil
}

// rulesFile is the Prometheus rules file that rides along in the same DM as a
// config bundle, so one message holds everything to apply.
type rulesFile struct {
	name   string // file name in the DM
	data   []byte
	count  int
	scope  string // "the `compute` add", "this channel"
	format string // formatStandard or formatCRD
	apply  string // markdown telling the user how to apply it
}

// section is the part of the combined DM message that describes the rules file.
func (r *rulesFile) section() string {
	return fmt.Sprintf(
		"\n\n**Prometheus rules** for %s (%d rule(s), %s) are in `%s`.\n\n%s\n\n"+
			"Apply it once. Do not also apply the full `%s` object or another export or add that covers the same runbooks, or these alerts fire twice. "+
			"Adjust the `release` label to your Helm release name. Thresholds are demo placeholders.",
		r.scope, r.count, rulesKind(r.format), r.name, r.apply, prometheusRuleObject,
	)
}

// rulesFileForAdd builds the rules for the runbooks one add just created. The
// object name includes the group so a later add of a different group does not
// replace this one.
func (p *Plugin) rulesFileForAdd(namespace, groupName string, slugs []string, format string) (*rulesFile, error) {
	name := prometheusRuleObject
	if groupName != "all" {
		name = prometheusRuleObject + "-" + groupName
	}
	return p.buildRulesFile(namespace, name, fmt.Sprintf("the `%s` add", groupName), slugs, format)
}

// rulesFileForChannel builds the rules for every runbook the channel's receivers
// cover, so an export leaves nothing unfired. The object is named per channel so
// exporting two channels does not make one overwrite the other. Custom receivers
// have no sample rules, so a channel with only those returns nil and a reason.
func (p *Plugin) rulesFileForChannel(namespace string, scoped []alertConfig, format string) (*rulesFile, string) {
	var slugs []string
	for _, c := range scoped {
		if !c.Custom {
			slugs = append(slugs, receiverBaseSlug(c.Name))
		}
	}
	if len(slugs) == 0 {
		return nil, ":information_source: No rules file: this channel has only custom receivers, and the sample rules are written for the shipped runbooks. Write the Prometheus rule for your own alert and give it the labels your route matches."
	}
	name := prometheusRuleObject + "-" + dnsLabel(scoped[0].Team) + "-" + dnsLabel(scoped[0].Channel)
	rules, err := p.buildRulesFile(namespace, name, "this channel", slugs, format)
	if err != nil {
		p.API.LogWarn("export: couldn't build the rules file", "err", err.Error())
		return nil, fmt.Sprintf(":warning: Couldn't build the rules file for this channel (%v). Run `/alertmanager format rules` for the full set.", err)
	}
	return rules, ""
}

// dnsLabel lowercases s and swaps anything outside [a-z0-9-] for `-`, so a
// Mattermost team or channel name is safe inside a Kubernetes object name.
func dnsLabel(s string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, s)
	return strings.Trim(out, "-")
}

// buildRulesFile filters the sample rules to slugs. For crd it wraps them in a
// PrometheusRule named name; for standard it keeps the plain rules file.
func (p *Plugin) buildRulesFile(namespace, name, scope string, slugs []string, format string) (*rulesFile, error) {
	rawBytes, err := root.SamplesFS.ReadFile("samples/prometheus-rules.yaml")
	if err != nil {
		return nil, fmt.Errorf("read sample rules: %w", err)
	}
	extras, err := p.loadImageAllowlist()
	if err != nil {
		return nil, fmt.Errorf("read image allowlist: %w", err)
	}
	rawYAML, err := applyImageAllowlist(string(rawBytes), extras)
	if err != nil {
		return nil, fmt.Errorf("apply image allowlist: %w", err)
	}
	filtered, n, err := filterPrometheusRules(rawYAML, slugs)
	if err != nil {
		return nil, err
	}

	rules := &rulesFile{name: name + ".yaml", count: n, scope: scope, format: format}
	if format == formatCRD {
		rules.data = []byte(buildPrometheusRuleManifest(filtered, namespace, name))
		rules.apply = fmt.Sprintf("```\nkubectl apply -n %s -f %s\n```", namespace, rules.name)
	} else {
		rules.data = []byte(filtered)
		rules.apply = "Add it to your Prometheus `rule_files:` glob and reload Prometheus."
	}
	return rules, nil
}

// attachRules uploads the rules file to the DM and returns its file ID. A
// failure is logged and returns "" so the config files still go out.
func (p *Plugin) attachRules(dmChannelID string, rules *rulesFile) string {
	info, appErr := p.API.UploadFile(rules.data, dmChannelID, rules.name)
	if appErr != nil {
		p.API.LogWarn("couldn't upload the rules file to the DM (config files still delivered)", "err", appErr.Error())
		return ""
	}
	return info.Id
}

// rulesReply is the line the in-channel reply adds about the rules file.
func rulesReply(rules *rulesFile, reason string, delivered bool) string {
	switch {
	case reason != "":
		return "\n\n" + reason + "\n"
	case rules == nil:
		return ""
	case delivered:
		return fmt.Sprintf("\n\n:page_facing_up: The Prometheus rules (`%s`, %d rule(s)) are attached to the same DM.\n", rules.name, rules.count)
	default:
		return "\n\n:warning: The rules file was not sent because the DM failed. Run `/alertmanager format rules` for the sample rules.\n"
	}
}

func rulesKind(format string) string {
	if format == formatCRD {
		return "`" + TargetPrometheusRuleAPIVersion + "` PrometheusRule"
	}
	return "plain rules file"
}
