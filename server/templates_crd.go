package main

import (
	"fmt"
	"strings"
)

// CRD (Prometheus Operator AlertmanagerConfig) rendering — the `--format=crd`
// counterpart to the file (alertmanager.yml) output. The notification body
// (color/title/text + inline quick diagnostics) is the SAME receiverBodyTemplate
// the file renderer uses, re-indented to the deeper AlertmanagerConfig nesting,
// so the two formats can never drift. See docs/KUBERNETES.md and
// docs/COMPATIBILITY.md. Targets AlertmanagerConfig v1alpha1
// (TargetAlertmanagerConfigAPIVersion).

// crdBodyIndent shifts the shared body from the file format's column (keys at 6
// spaces, block scalars at 8) to the CRD's deeper nesting (10 / 12). The CRD
// receiver fields sit under spec.receivers[].slackConfigs[] — four columns
// deeper than the file's top-level receivers list.
const crdBodyIndent = "    "

// crdQuickDiagIndent is the CRD equivalent of yamlBlockIndent (8 spaces) for the
// text: block scalar — four deeper, matching crdBodyIndent.
const crdQuickDiagIndent = "            "

// crdReceiverHeader is the CRD receiver's leading fields: the slackConfigs
// shape with a Secret-referenced apiURL (an AlertmanagerConfig cannot inline the
// webhook URL the way the file format's api_url can). receiverBodyTemplate is
// appended after this, supplying color/title/text.
const crdReceiverHeader = `    - name: {{NAME}}
      slackConfigs:
        - apiURL:
            name: {{SECRET}}
            key: url
          channel: '{{CHANNEL}}'
          sendResolved: true
          username: 'alertmanagerbot'
          iconURL: '{{ICON_URL}}'
`

// crdFallbackReceiver is the synthesized catch-all receiver the parent route
// points at. Unlike a per-runbook receiver it has no runbook context, so it
// gets a deliberately simple body (no severity color/diagnostics, and a title
// that says so) rather than the rich per-runbook template with an empty runbook
// link. One is emitted per group (per AlertmanagerConfig).
const crdFallbackReceiver = `    - name: {{NAME}}
      slackConfigs:
        - apiURL:
            name: {{SECRET}}
            key: url
          channel: '{{CHANNEL}}'
          sendResolved: true
          username: 'alertmanagerbot'
          iconURL: '{{ICON_URL}}'
          title: '[{{ .Status | toUpper }}:{{ .CommonLabels.alertname }}] (no runbook-specific template)'
          text: |-
            {{ range .Alerts -}}
            **Alert:** {{ .Labels.alertname }}{{ if .Labels.severity }} - {{ .Labels.severity }}{{ end }}{{ "\n" }}
            {{- if .Annotations.description }}**Description:** {{ .Annotations.description }}{{ "\n" }}{{ end -}}
            {{- range .Labels.SortedPairs }}{{ "\n" }}  • **{{ .Name }}:** ` + "`{{ .Value }}`" + `{{ end }}
            {{ end -}}
`

// crdEnvelope is the AlertmanagerConfig wrapper. {{API_VERSION}} is filled from
// TargetAlertmanagerConfigAPIVersion so the emitted apiVersion tracks the single
// source of truth in crd_versions.go.
const crdEnvelope = `apiVersion: {{API_VERSION}}
kind: AlertmanagerConfig
metadata:
  name: {{CR_NAME}}
  namespace: {{NAMESPACE}}
  labels:
    # Any label works while alertmanagerConfigSelector is match-all ({});
    # kept for clarity and in case the selector is ever narrowed.
    alertmanager-config: "true"
spec:
  route:
    # Top-level receiver is the fallback for any runbook in this group that
    # lacks a sub-route below — never let a labelled alert vanish silently.
    receiver: {{FALLBACK}}
    # Gate on the group's known runbooks (not runbook=~".+", which leaks other
    # categories in). Update alongside routes: below.
    matchers:
      - name: runbook
        value: "{{PARENT_MATCH}}"
        matchType: "=~"
    groupBy: ["alertname"]
    groupWait: 30s
    groupInterval: 5m
    repeatInterval: 4h
    routes:
{{SUBROUTES}}  receivers:
{{RECEIVERS}}`

// crdReceiverSpec is one receiver to render into an AlertmanagerConfig.
type crdReceiverSpec struct {
	slug              string // runbook base slug (the route matcher value)
	name              string // full receiver name (<slug>--<team>-<channel>)
	secretName        string // K8s Secret holding the webhook URL
	channel           string // destination channel (with or without leading #)
	runbookDefaultURL string // plugin-hosted runbook fallback URL
	iconURL           string // bot avatar URL
	custom            bool   // add-custom (non-runbook) receiver: no auto route, no runbook link
}

// indentBlock prefixes every non-empty line of s with prefix. Blank lines stay
// blank so YAML block scalars aren't polluted with trailing whitespace.
func indentBlock(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = prefix + ln
		}
	}
	return strings.Join(lines, "\n")
}

// renderCRDReceiver renders one entry for an AlertmanagerConfig spec.receivers
// list. The header carries the CRD-shaped fields (Secret-backed apiURL, camel
// case), then the shared receiverBodyTemplate — re-indented and with its
// runbook/diagnostics placeholders filled exactly as renderReceiverYAML does.
func renderCRDReceiver(spec crdReceiverSpec) string {
	channel := spec.channel
	if !strings.HasPrefix(channel, "#") {
		channel = "#" + channel
	}

	header := strings.NewReplacer(
		"{{NAME}}", spec.name,
		"{{SECRET}}", spec.secretName,
		"{{CHANNEL}}", channel,
		"{{ICON_URL}}", spec.iconURL,
	).Replace(crdReceiverHeader)

	// Same body as the file format, shifted to the CRD's nesting depth. Fill
	// {{RUNBOOK_SECTION}} BEFORE re-indenting so its continuation lines pick up
	// the CRD body indent uniformly. Custom receivers omit the runbook fallback.
	body := strings.Replace(receiverBodyTemplate, "{{RUNBOOK_SECTION}}", runbookSection(spec.runbookDefaultURL, spec.custom), 1)
	body = indentBlock(body, crdBodyIndent)
	diagText := formatQuickDiagnosticsForAlert(loadQuickDiagnosticsForSlug(spec.slug))
	if diagText != "" {
		diagText = indentForYAMLBlock(diagText, crdQuickDiagIndent)
	}
	body = strings.NewReplacer(
		"{{QUICK_DIAGNOSTICS}}", diagText,
	).Replace(body)

	return header + body
}

// renderCRDFallbackReceiver renders the synthesized catch-all receiver with the
// simple crdFallbackReceiver body (no runbook-specific color/diagnostics).
func renderCRDFallbackReceiver(spec crdReceiverSpec) string {
	channel := spec.channel
	if !strings.HasPrefix(channel, "#") {
		channel = "#" + channel
	}
	return strings.NewReplacer(
		"{{NAME}}", spec.name,
		"{{SECRET}}", spec.secretName,
		"{{CHANNEL}}", channel,
		"{{ICON_URL}}", spec.iconURL,
	).Replace(crdFallbackReceiver)
}

// renderAlertmanagerConfig assembles a full AlertmanagerConfig for one group of
// receivers that share a webhook/channel (the plugin's one-shared-webhook-per-
// group model). fallback is a synthesized catch-all receiver (already included
// in specs) that the parent route points at. The parent =~ matcher enumerates
// the group's slugs so this CR only claims its own alerts.
func renderAlertmanagerConfig(crName, namespace, fallbackReceiver string, specs []crdReceiverSpec) string {
	var subroutes, receivers strings.Builder
	slugs := make([]string, 0, len(specs))

	for _, s := range specs {
		// The fallback receiver (no runbook slug) gets no sub-route and a
		// simple body — it is only the parent route's catch-all.
		if s.slug == "" {
			receivers.WriteString(renderCRDFallbackReceiver(s))
			continue
		}
		if s.custom {
			// Custom (add-custom) receiver: no runbook label, so emit NO auto
			// matcher and keep it OUT of the parent =~ gate. The receiver is
			// still defined; the operator adds their own matcher (and must widen
			// the parent route's matchers to admit the labels their alerts carry,
			// since this CR gates entry on the runbook label a custom alert lacks).
			fmt.Fprintf(&subroutes,
				"      # Custom receiver %q (add-custom) — no matcher generated; wire your own,\n"+
					"      # and widen the parent route's matchers above to admit its labels:\n"+
					"      #  - matchers: [{name: alertname, value: \"MyCustomAlert\", matchType: \"=\"}]\n"+
					"      #    receiver: %s\n      #    continue: true\n",
				s.name, s.name)
			receivers.WriteString(renderCRDReceiver(s))
			continue
		}
		slugs = append(slugs, s.slug)
		fmt.Fprintf(&subroutes,
			"      - matchers: [{name: runbook, value: \"%s\", matchType: \"=\"}]\n        receiver: %s\n        continue: true\n",
			s.slug, s.name)
		receivers.WriteString(renderCRDReceiver(s))
	}

	parentMatch := "^(" + strings.Join(slugs, "|") + ")$"

	return strings.NewReplacer(
		"{{API_VERSION}}", TargetAlertmanagerConfigAPIVersion,
		"{{CR_NAME}}", crName,
		"{{NAMESPACE}}", namespace,
		"{{FALLBACK}}", fallbackReceiver,
		"{{PARENT_MATCH}}", parentMatch,
		"{{SUBROUTES}}", subroutes.String(),
		"{{RECEIVERS}}", receivers.String(),
	).Replace(crdEnvelope)
}

// renderWebhookSecret renders the companion Secret an AlertmanagerConfig's
// slackConfigs.apiURL references. Must live in the same namespace as the CR.
func renderWebhookSecret(secretName, namespace, webhookURL string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
stringData:
  url: %q
`, secretName, namespace, webhookURL)
}
