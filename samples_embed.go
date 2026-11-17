package root

import "embed"

// SamplesFS embeds samples/prometheus-rules.yaml so server-side commands
// (e.g. /alertmanager rules --format=operator) can generate a ready-to-apply
// PrometheusRule CRD manifest without an outbound HTTP round-trip.

//go:embed samples/prometheus-rules.yaml
var SamplesFS embed.FS
