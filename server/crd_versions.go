package main

// Kubernetes / Prometheus Operator deployment targets.
//
// The plugin's Kubernetes deployment path (docs/KUBERNETES.md) targets one
// specific set of Prometheus Operator CRD API versions — the latest release
// formatting, and only that. We deliberately do NOT maintain per-version
// renderers; when a future operator release graduates a served/storage
// apiVersion (a rare, announced event) these constants and the mirror table
// in docs/COMPATIBILITY.md get bumped together in one release.
//
// These constants are the single source of truth. docs/COMPATIBILITY.md
// quotes them verbatim and TestCompatibilityDocMatchesConstants fails the
// build if the two drift.
const (
	// TargetPrometheusOperatorVersion is the operator release the CRD shapes
	// in the docs were verified against.
	TargetPrometheusOperatorVersion = "v0.93.0"

	// TargetAlertmanagerConfigAPIVersion is the apiVersion the plugin's
	// AlertmanagerConfig examples use. Pinned to v1alpha1 on purpose: it is
	// the maintained storage version. v1beta1 has been stalled since 2022 and
	// there is no v1.
	TargetAlertmanagerConfigAPIVersion = "monitoring.coreos.com/v1alpha1"

	// TargetPrometheusRuleAPIVersion is the apiVersion for the sample alert
	// rules expressed as a PrometheusRule CR. GA.
	TargetPrometheusRuleAPIVersion = "monitoring.coreos.com/v1"
)
