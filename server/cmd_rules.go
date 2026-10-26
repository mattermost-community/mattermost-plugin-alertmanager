package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"

	root "github.com/mattermost/mattermost-plugin-alertmanager"
)

// handleRules points users at the shipped sample Prometheus alerting rules —
// a browsable HTML view and the raw file to download. This is the upstream
// half of the pipeline the plugin does NOT own: the plugin creates the
// Alertmanager receivers and routes, but SOMETHING in the user's Prometheus
// has to emit alerts carrying the `runbook:` label. This command hands them a
// ready starting set so they don't have to go dig it out of the repo.
//
// With --format=operator the command instead DMs a ready-to-apply
// PrometheusRule CRD manifest (monitoring.coreos.com/v1) that wraps the
// same groups block — for teams running Prometheus Operator.
func (p *Plugin) handleRules(args *model.CommandArgs) (string, error) {
	fields := strings.Fields(args.Command)
	rest := fields[2:]

	format, rest := extractFlagValue(rest, "--format=")
	namespace, _ := extractFlagValue(rest, "--namespace=")
	if namespace == "" {
		namespace = defaultCRDNamespace
	} else if err := validateCRDNamespace(namespace); err != nil {
		return fmt.Sprintf(":warning: Invalid `--namespace`: %v", err), nil
	}

	if format == "operator" {
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
			"- ⬇️ [Download `mattermost-prometheus-rules-crd.yaml`](%s) — PrometheusRule CRD (`monitoring.coreos.com/v1`), namespace `monitoring`. For a different namespace run `/alertmanager rules --format=operator --namespace=<ns>` instead\n\n"+
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

	manifest := buildPrometheusRuleManifest(string(rawBytes), namespace)

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
				"Adjust `release: kube-prometheus-stack` to your Helm release name. Every threshold is a demo placeholder — tune before production.",
			TargetPrometheusRuleAPIVersion, namespace, namespace,
		),
		FileIds: []string{info.Id},
	}
	if _, appErr := p.API.CreatePost(post); appErr != nil {
		return fmt.Sprintf(":warning: Could not send DM: %v", appErr.Error()), nil
	}

	return fmt.Sprintf(
		":page_facing_up: **Sent `mattermost-prometheus-rules.yaml` (PrometheusRule CRD, `%s`) to your DM with `@%s`.** Review and apply:\n\n"+
			"```\nkubectl apply -n %s -f mattermost-prometheus-rules.yaml\n```\n\n"+
			"Adjust the `release` label to your Helm release name before applying.",
		TargetPrometheusRuleAPIVersion, webhookUsername, namespace,
	), nil
}

// buildPrometheusRuleManifest wraps the raw prometheus-rules.yaml content
// (which starts with comments and then a bare `groups:` block) into a
// PrometheusRule CRD. The groups content is indented 2 extra spaces to nest
// under `spec:`, preserving all inline comments.
func buildPrometheusRuleManifest(rawYAML, namespace string) string {
	var b strings.Builder
	b.WriteString("# PrometheusRule CRD — Prometheus Operator " + TargetPrometheusRuleAPIVersion + "\n")
	b.WriteString("# Generated by /alertmanager rules --format=operator\n")
	b.WriteString("# Adjust the 'release' label to match your Helm release name.\n")
	b.WriteString("# Apply with:\n")
	b.WriteString("#   kubectl apply -n " + namespace + " -f mattermost-prometheus-rules.yaml\n")
	b.WriteString("---\n")
	b.WriteString("apiVersion: " + TargetPrometheusRuleAPIVersion + "\n")
	b.WriteString("kind: PrometheusRule\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: mattermost-alertmanager-rules\n")
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
