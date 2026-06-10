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
//
// After substitution, what's emitted is a valid Alertmanager slack_configs
// block where title:/text: contain Alertmanager-evaluated Go templates
// ({{ .Status }}, {{ range .Alerts }}, etc.) — those run inside Alertmanager
// at alert-delivery time, not here. The plugin never sees an alert payload.

// Title puts diff-labels (CommonLabels minus GroupLabels) in parens so
// alerts firing for different label sets are visually distinguishable
// in the channel.
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
      color: '{{ if eq .Status "firing" }}danger{{ else }}good{{ end }}'
      title: |-
        [{{ .Status | toUpper }}{{ if eq .Status "firing" }}:{{ .Alerts.Firing | len }}{{ end }}] {{ .CommonLabels.alertname }}
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
        {{ end -}}
`

// renderReceiverYAML substitutes the plugin-level placeholders and
// returns a slack_configs YAML block ready to paste under receivers: in
// alertmanager.yml. Channel name conventionally takes a leading # in
// slack_configs; tolerate either form on input.
//
// iconURL is the bot avatar URL injected into the slack_configs payload
// override; same value the webhook record uses, just defended at the
// payload level too.
func renderReceiverYAML(name, webhookURL, channel, runbookDefaultURL, iconURL string) string {
	if !strings.HasPrefix(channel, "#") {
		channel = "#" + channel
	}
	r := strings.NewReplacer(
		"{{NAME}}", name,
		"{{URL}}", webhookURL,
		"{{CHANNEL}}", channel,
		"{{RUNBOOK_DEFAULT}}", runbookDefaultURL,
		"{{ICON_URL}}", iconURL,
	)
	return r.Replace(canonicalTemplate)
}
