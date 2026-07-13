# Alert Requirements

What each runbook's alert needs in order to **fire**. If the metric source
in the "Provided by" column isn't being scraped, the rule is valid but
never triggers. Read in chat with `/alertmanager docs requirements`.

Legend for "Extra tooling":
- **—** works with metrics a standard cluster already scrapes
  (kube-state-metrics, cAdvisor/kubelet, node-exporter, kube-apiserver).
- **⚠ named tool** requires deploying that tool first, or the alert is dark.

## Compute

| Runbook | Key metric(s) | Provided by | Extra tooling |
|---|---|---|---|
| `high-cpu-usage` | `container_cpu_usage_seconds_total` | cAdvisor (kubelet) | — |
| `high-memory-usage` | `container_memory_working_set_bytes`, `container_spec_memory_limit_bytes` | cAdvisor | — |
| `high-memory-usage` (OOMKilled) | `kube_pod_container_status_last_terminated_reason` | kube-state-metrics | — |
| `pod-crashloopbackoff` | `kube_pod_container_status_restarts_total` | kube-state-metrics | — |
| `pod-not-ready` | `kube_pod_status_ready` | kube-state-metrics | — |
| `deployment-replicas-unavailable` | `kube_deployment_status_replicas_available` | kube-state-metrics | — |
| `node-not-ready` | `kube_node_status_condition` | kube-state-metrics | — |
| `cpu-throttling-high` | `container_cpu_cfs_throttled_periods_total`, `..._periods_total` | cAdvisor | — |
| `image-pull-backoff` | `kube_pod_container_status_waiting_reason` | kube-state-metrics | — |
| `pods-unschedulable` | `kube_pod_status_unschedulable` | kube-state-metrics | — |

## Application

| Runbook | Key metric(s) | Provided by | Extra tooling |
|---|---|---|---|
| `high-http-error-rate` | `http_requests_total{status=~"5.."}` | app instrumentation | ⚠ app must expose request metrics |
| `high-api-latency` | `http_request_duration_seconds_bucket` | app instrumentation | ⚠ app must expose a latency histogram |
| `service-endpoint-down` | `probe_success` | blackbox-exporter | ⚠ blackbox-exporter + probe config |
| `request-rate-anomaly` | `http_requests_total` | app instrumentation | ⚠ app must expose request metrics |

## Database

| Runbook | Key metric(s) | Provided by | Extra tooling |
|---|---|---|---|
| `database-connectivity-loss` | `mattermost_db_master_connections_total` | Mattermost metrics endpoint | ⚠ enable MM metrics |
| `database-replication-lag` | `pg_replication_lag_seconds` | postgres-exporter | ⚠ postgres-exporter |
| `database-high-latency` | query-time histogram | postgres-exporter / app | ⚠ postgres-exporter or app metrics |
| `postgres-connections-near-max` | `pg_stat_activity_count`, `pg_settings_max_connections` | postgres-exporter | ⚠ postgres-exporter |

## Storage

| Runbook | Key metric(s) | Provided by | Extra tooling |
|---|---|---|---|
| `persistent-volume-full` | `kubelet_volume_stats_available_bytes`, `..._capacity_bytes` | kubelet | — |
| `disk-fill-rate-high` | `node_filesystem_avail_bytes` | node-exporter | ⚠ node-exporter |

## Networking

| Runbook | Key metric(s) | Provided by | Extra tooling |
|---|---|---|---|
| `ingress-high-5xx` | ingress 5xx counter | nginx/traefik ingress metrics | ⚠ ingress-controller metrics |
| `certificate-expiring-soon` | `probe_ssl_earliest_cert_expiry` | blackbox-exporter | ⚠ blackbox-exporter |
| `dns-resolution-failure` | `coredns_dns_responses_total` | CoreDNS metrics | ⚠ CoreDNS metrics scrape |

## Observability

| Runbook | Key metric(s) | Provided by | Extra tooling |
|---|---|---|---|
| `prometheus-scrape-target-down` | `up` | Prometheus (built-in) | — |
| `alertmanager-notification-failure` | `alertmanager_notifications_failed_total` | Alertmanager (built-in) | — |

## Security

| Runbook | Key metric(s) | Provided by | Extra tooling |
|---|---|---|---|
| `unexpected-container-image` | `kube_pod_container_info` | kube-state-metrics | — (tune allowlist regex) |
| `apiserver-auth-failure-spike` | `apiserver_request_total{code=~"401\|403"}` | kube-apiserver | — |
| `privileged-container-started` | `kyverno_policy_results_total` (or equiv) | policy engine | ⚠ **Kyverno / Gatekeeper** |
| `interactive-shell-in-container` | `falco_events{rule="Terminal shell in container"}` | Falco | ⚠ **Falco** |
| `rbac-privilege-escalation` | `falco_events{rule=~"...RoleBinding..."}` | Falco k8s-audit plugin | ⚠ **audit logs + Falco** |
| `security-tooling-down` | `up{job=~"falco\|kyverno.*"}` | the sensors' scrape targets | ⚠ sensors must expose `/metrics` |

## Bootstrapping checklist

To light up the **—** rows (most of the catalog) you need, at minimum:
kube-state-metrics, cAdvisor/kubelet, node-exporter, and kube-apiserver
scraped by Prometheus. Everything else is opt-in per the ⚠ column.

The three ⚠ **Falco/Kyverno** security rows are the highest-lift — they
need runtime/policy/audit tooling deployed. Until then those alerts (and
`security-tooling-down` watching them) stay dark. See
`/alertmanager docs alerts` for the plain catalog.
