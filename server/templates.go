package main

import (
	"strings"
)

// One canonical slack_configs format, baked in. Every receiver gets it.
// Pluggable post-format templates were dropped because every receiver in
// this plugin corresponds to a runbook (a documented failure mode) — the
// rendering shape is always the same regardless of which runbook.
//
// Plugin-level substitutions, done at render time via strings.NewReplacer:
//   {{NAME}}            — receiver name (= runbook slug, e.g. high-cpu-usage)
//   {{URL}}             — Mattermost incoming webhook URL (the api_url value)
//   {{CHANNEL}}         — destination channel slug with leading #
//   {{RUNBOOK_DEFAULT}} — plugin-hosted runbook fallback URL, baked from the
//                         current Mattermost site URL + receiver slug
//   {{QUICK_DIAGNOSTICS}}— rendered "## Quick diagnostics" markdown for this
//                          runbook (first 3 fenced code blocks). Empty when
//                          the runbook lacks the section, in which case the
//                          alert renders without inline diagnostics.
//
// After substitution, what's emitted is a valid Alertmanager slack_configs
// block where title:/text: contain Alertmanager-evaluated Go templates
// ({{ .Status }}, {{ range .Alerts }}, etc.) — those run inside Alertmanager
// at alert-delivery time, not here. The plugin never sees an alert payload.

// Title format (v1.0.3+):
//   - Firing, severity known   →  [CRITICAL:HighCPUUsage] (3 firing) (label=val, ...)
//   - Firing, severity missing →  [ALERT:HighCPUUsage] (label=val, ...)
//   - Resolved                 →  [✓ RESOLVED:HighCPUUsage] (label=val, ...)
//
// Lead with severity (uppercased) because SRE eyes scan brackets first
// at 3am — "do I care about this alert?" is the question, and severity
// answers it faster than the AM state. AlertName follows the colon so
// the runbook context is one glance away. Firing count appears in
// parens only when > 1 (single-alert groups are the common case and
// "(1 firing)" is noise). Diff-labels (CommonLabels minus GroupLabels)
// still appear in the trailing parens so alerts firing for different
// label sets remain visually distinguishable in the channel.
//
// `.CommonLabels.severity` is empty when the group has alerts of mixed
// severities — falls back to ALERT so the missing-severity case is
// visible without inventing a level.
//
// Body per alert is "rich" format:
//   - Alert: <name> - <severity> line
//   - Description: <annotation>
//   - Details: full label dump as bullets
//   - Runbook URL (annotation or plugin fallback)
//   - Dashboard URL (optional)
//
// Matches the verbose-context format common across Prometheus/AM
// installs — full labels visible without clicking through to AM.
const canonicalTemplate = `- name: {{NAME}}
  slack_configs:
    - api_url: '{{URL}}'
      channel: '{{CHANNEL}}'
      send_resolved: true
      # username/icon_url here are payload-level overrides — they belong
      # to the slack_configs payload AM sends, which Mattermost honors
      # when EnablePostUsernameOverride / EnablePostIconOverride are on
      # (plugin force-enables both at OnActivate). Belt-and-suspenders
      # with the Username/IconURL fields stored on the webhook record:
      # if either drifts, the other still carries the right values.
      username: 'alertmanagerbot'
      icon_url: '{{ICON_URL}}'
` + receiverBodyTemplate

// receiverBodyTemplate is the color/title/text notification body — the rich
// part (severity sidebar color, templated title, text with runbook link +
// inline quick diagnostics). It is shared verbatim by the file
// (alertmanager.yml, canonicalTemplate above) and the CRD (AlertmanagerConfig)
// receiver renderers so the two output formats can never drift. Authored at the
// file format's indentation; the CRD renderer re-indents it to the deeper
// AlertmanagerConfig nesting depth.
const receiverBodyTemplate = `      # Sidebar color. Mattermost honors Slack-named colors
      # (good/warning/danger) plus arbitrary hex codes. Mapping:
      #   firing + warning  -> yellow
      #   firing + critical -> red
      #   firing + info     -> blue hex
      #   firing + (else)   -> red (safer default for unlabeled)
      #   resolved          -> green
      color: '{{ if eq .Status "firing" }}{{ if eq .CommonLabels.severity "warning" }}warning{{ else if eq .CommonLabels.severity "critical" }}danger{{ else if eq .CommonLabels.severity "info" }}#3b82f6{{ else }}danger{{ end }}{{ else }}good{{ end }}'
      title: |-
        {{- if eq .Status "firing" -}}
          {{- if .CommonLabels.severity -}}
            [{{ .CommonLabels.severity | toUpper }}:{{ .CommonLabels.alertname }}]
          {{- else -}}
            [ALERT:{{ .CommonLabels.alertname }}]
          {{- end -}}
          {{- $count := len .Alerts.Firing -}}
          {{- if gt $count 1 }} ({{ $count }} firing){{- end -}}
        {{- else -}}
          [✓ RESOLVED:{{ .CommonLabels.alertname }}]
        {{- end -}}
        {{- if gt (len .CommonLabels) (len .GroupLabels) -}}
          {{ " " }}(
          {{- with .CommonLabels.Remove .GroupLabels.Names }}
            {{- range $index, $label := .SortedPairs -}}
              {{ if $index }}, {{ end }}
              {{- $label.Name }}="{{ $label.Value -}}"
            {{- end }}
          {{- end -}}
          )
        {{- end }}
      text: |-
        {{ range .Alerts -}}
        **Alert:** {{ .Labels.alertname }}{{ if .Labels.severity }} - {{ .Labels.severity }}{{ end }}{{ "\n\n" }}
        {{- if .Annotations.description -}}
        **Description:** {{ .Annotations.description }}{{ "\n\n" }}
        {{- end -}}
        **Details:**{{ "\n" }}
        {{- range .Labels.SortedPairs -}}
        {{ "\n" }}  • **{{ .Name }}:** ` + "`{{ .Value }}`" + `
        {{- end -}}
        {{ "\n\n" }}
        {{- if .Annotations.runbook_url -}}
        **Runbook:** {{ .Annotations.runbook_url }}{{ "\n" }}
        {{- else -}}
        **Runbook:** {{RUNBOOK_DEFAULT}}{{ "\n" }}
        {{- end -}}
        {{- if .Annotations.dashboard_url }}**Dashboard:** {{ .Annotations.dashboard_url }}{{ "\n" }}{{ end -}}
        {{QUICK_DIAGNOSTICS}}
        {{ end -}}
`

// yamlBlockIndent is the leading whitespace that aligns content
// inside the slack_configs `text: |-` literal block. Hardcoded here
// because the template's indent is itself hardcoded — if the YAML
// block ever gets re-indented, this constant moves with it.
const yamlBlockIndent = "        "

// renderReceiverYAML substitutes the plugin-level placeholders and
// returns a slack_configs YAML block ready to paste under receivers: in
// alertmanager.yml. Channel name conventionally takes a leading # in
// slack_configs; tolerate either form on input.
//
// iconURL is the bot avatar URL injected into the slack_configs payload
// override; same value the webhook record uses, just defended at the
// payload level too.
//
// Inline quick diagnostics are looked up by the receiver's base slug
// (the runbook identifier extracted from a possibly channel-suffixed
// name). Multi-line content is re-indented to match the YAML literal
// block's column position so the generated YAML parses correctly.
func renderReceiverYAML(name, webhookURL, channel, runbookDefaultURL, iconURL string) string {
	if !strings.HasPrefix(channel, "#") {
		channel = "#" + channel
	}

	// Quick diagnostics block: empty string when the runbook lacks
	// the "## Quick diagnostics" section, otherwise multi-line
	// markdown re-indented for the YAML literal block.
	diagnostics := loadQuickDiagnosticsForSlug(receiverBaseSlug(name))
	diagText := formatQuickDiagnosticsForAlert(diagnostics)
	if diagText != "" {
		diagText = indentForYAMLBlock(diagText, yamlBlockIndent)
	}

	r := strings.NewReplacer(
		"{{NAME}}", name,
		"{{URL}}", webhookURL,
		"{{CHANNEL}}", channel,
		"{{RUNBOOK_DEFAULT}}", runbookDefaultURL,
		"{{ICON_URL}}", iconURL,
		"{{QUICK_DIAGNOSTICS}}", diagText,
	)
	return r.Replace(canonicalTemplate)
}

// indentForYAMLBlock applies `indent` to every line of `s` except the
// first. Used to align a multi-line substitution into a YAML literal
// block whose first line gets indentation from the surrounding
// template text — only subsequent lines need the prefix.
func indentForYAMLBlock(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}
