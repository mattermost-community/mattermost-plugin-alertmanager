package root

import "embed"

// SamplesFS embeds the shipped Prometheus rules so server-side commands
// (e.g. /alertmanager format rules crd) can generate a ready-to-apply
// PrometheusRule CRD manifest without an outbound HTTP round-trip.
//
// mattermost-operator-rules.yaml is a verbatim copy of the rules the Mattermost
// Operator embeds, so the plugin can serve them until the operator ships them.

//go:embed samples/prometheus-rules.yaml samples/mattermost-operator-rules.yaml
var SamplesFS embed.FS
