# Alert Catalog

Every alert type the runbook library covers, grouped the same way as the
`/alertmanager config add <set>` group sets and the `groups:` in your Prometheus
rules.

Each runbook slug maps to a rendered page at
`/plugins/com.mattermost.alertmanager/public/runbooks/<slug>.html` and,
when the alert fires, its first three Quick-Diagnostics blocks are inlined
into the Mattermost post.

## Alert message anatomy (what an alert can carry)

These are the "options" every alert message is built from — set them in
the Prometheus rule's `labels` / `annotations`:

| Field | Where | Effect in the Mattermost post |
|---|---|---|
| `severity: critical` | label | Red sidebar, title `[CRITICAL:...]`, pages |
| `severity: warning` | label | Yellow sidebar, chat-only |
| `severity: info` | label | Blue sidebar, informational |
| (status = resolved) | auto | Green `[✓ RESOLVED:...]` variant |
| `runbook: <slug>` | label | Routes the alert to its per-runbook receiver |
| `summary` | annotation | Title line / one-liner |
| `description` | annotation | **Description:** block |
| `runbook_url` | annotation | **Runbook:** link (falls back to the plugin-hosted page) |
| `dashboard_url` | annotation | **Dashboard:** link (optional) |
| any other labels | label | Listed under **Details:** and used to auto-fill `<label>` placeholders in the inline diagnostics |

Diagnostic auto-fill labels: `alertname`, `app`, `cluster`, `container`,
`deployment`, `instance`, `job`, `namespace`, `node`, `pod`, `service`, and
`installation_mattermost_com_installation` (the Mattermost CR name).

## Compute (`compute`)

| Alert | slug | Severity | Fires when |
|---|---|---|---|
| High CPU Usage | `high-cpu-usage` | warning→critical | Container >80% CPU for 10m |
| High Memory Usage | `high-memory-usage` | warning→critical | Container >85% mem limit, or OOMKilled |
| Container OOMKilled | (uses `high-memory-usage`) | critical | Container killed for exceeding its memory limit |
| Pod CrashLoopBackOff | `pod-crashloopbackoff` | critical | Restart count climbing fast |
| Pod Not Ready | `pod-not-ready` | warning→critical | Readiness probe failing 5m+ |
| Deployment Replicas Unavailable | `deployment-replicas-unavailable` | critical | Available < desired replicas |
| Node Not Ready | `node-not-ready` | critical | Node `Ready=False` / pressure |
| CPU Throttling High | `cpu-throttling-high` | warning | Throttled >25% of CFS periods |
| Pod ImagePullBackOff | `image-pull-backoff` | critical | Image can't be pulled; pod never starts |
| Pods Unschedulable | `pods-unschedulable` | warning | Pods stuck Pending; scheduler can't place |

## Application (`application`)

| Alert | slug | Severity | Fires when |
|---|---|---|---|
| High HTTP 5xx Error Rate | `high-http-error-rate` | critical | 5xx >5% over 10m |
| High API Latency | `high-api-latency` | warning | p95 above SLO for 10m |
| Service Endpoint Down | `service-endpoint-down` | critical | Probe failing on a known endpoint |
| Request Rate Anomaly | `request-rate-anomaly` | warning | Sudden spike/drop in request rate |

## Database (`database`)

| Alert | slug | Severity | Fires when |
|---|---|---|---|
| Database Connectivity Loss | `database-connectivity-loss` | critical | Zero active DB connections |
| Database Replication Lag | `database-replication-lag` | warning→critical | Replica lag > N seconds |
| Database High Latency | `database-high-latency` | warning | p95 query time > N ms |
| Postgres Connections Near Max | `postgres-connections-near-max` | warning→critical | In-use connections >80% of max |

## Storage (`storage`)

| Alert | slug | Severity | Fires when |
|---|---|---|---|
| Persistent Volume Full | `persistent-volume-full` | warning→critical | PV/PVC usage >85% |
| Disk Fill Rate High | `disk-fill-rate-high` | warning | Projected to fill in <24h |

## Networking (`networking`)

| Alert | slug | Severity | Fires when |
|---|---|---|---|
| Ingress High 5xx | `ingress-high-5xx` | critical | LB/ingress 5xx spike |
| Certificate Expiring Soon | `certificate-expiring-soon` | warning | TLS cert expires <14d |
| DNS Resolution Failure | `dns-resolution-failure` | critical | Cluster DNS lookups failing |

## Observability (`observability`)

| Alert | slug | Severity | Fires when |
|---|---|---|---|
| Prometheus Scrape Target Down | `prometheus-scrape-target-down` | warning | `up == 0` for 5m+ |
| Alertmanager Notification Failure | `alertmanager-notification-failure` | critical | Delivery failures climbing |

## Security (`security`)

Several require tooling beyond stock Prometheus/kube-state-metrics — the
rule is valid but never fires without it.

| Alert | slug | Severity | Fires when | Requires |
|---|---|---|---|---|
| Unexpected Container Image | `unexpected-container-image` | warning | Image outside the registry allowlist | kube-state-metrics |
| API Server Auth Failure Spike | `apiserver-auth-failure-spike` | warning | 401/403 spike at the API server | apiserver metrics |
| Privileged / Root Container Started | `privileged-container-started` | critical | Privileged/root/hostPath container starts | Kyverno/Gatekeeper |
| Interactive Shell in Container | `interactive-shell-in-container` | warning | Shell spawned in a running container | Falco |
| RBAC Privilege Escalation | `rbac-privilege-escalation` | critical | cluster-admin binding / wildcard role created | audit logs + Falco |
| Security Tooling Down | `security-tooling-down` | critical | Falco/Kyverno/audit sensor stopped reporting | Falco/Kyverno scrape targets |

## Mattermost Application (`mattermost`)

Rules for the Mattermost server itself. Requires `MetricsSettings.Enable: true` in Mattermost `config.json`. Metric names may vary by Mattermost version — verify from your `/metrics` endpoint before deploying.

| Alert | slug | Severity | Fires when | Requires |
|---|---|---|---|---|
| Mattermost API High Latency | `mattermost-api-high-latency` | warning | p95 API response > 1s for 5m | Mattermost metrics |
| Mattermost High Login Failures | `mattermost-high-login-failures` | warning | > 10 failed logins in 10m | Mattermost metrics |
| Mattermost WebSocket Drop | `mattermost-websocket-drop` | warning | Active WS connections drop > 50% vs 10m ago | Mattermost metrics |
| Mattermost DB Slow Query | `mattermost-db-slow-query` | warning | p99 DB store op > 500ms for 5m | Mattermost metrics |
| Mattermost High Error Rate | `mattermost-high-error-rate` | warning | > 5% of API calls return 5xx for 5m | Mattermost metrics |

### Mattermost operator alerts (`mattermost` with `mm-namespace=<ns>`)

Kubernetes installs only. These 13 alerts, 14 with `mm-db-replicas=true`, come from the Mattermost Operator's own PrometheusRule. Two more, with `mm-postgres-pod=true`, are plugin-only. They carry no `runbook` label and route by alert name. See [Kubernetes](KUBERNETES.md#mattermost-operator-alerts).

Each alert has its own runbook. The `mattermost-server` receiver links the post to it and shows its first diagnostic command. An alert that sets its own `runbook_url` links there instead; the diagnostic command still shows.

"Mattermost metrics" needs an Enterprise build with a license that includes metrics. Without one there are no `mattermost_*` series, so alerts on them never fire, while `MattermostServerDown` (which reads `up`) fires as soon as Prometheus scrapes the dead `/metrics` port. See [Alert Requirements](ALERT_REQUIREMENTS.md).

| Alert | Severity | Fires when | Needs | Runbook |
|---|---|---|---|---|
| MattermostServerDown | critical | One pod's metrics endpoint unscrapable for 5m | Mattermost metrics | [Server Down](../runbooks/mattermost-operator/mattermost-server-down.md) |
| MattermostPodCrashLooping | critical | More than 3 restarts in 5m | kube-state-metrics | [Pod Crash Looping](../runbooks/mattermost-operator/mattermost-pod-crash-looping.md) |
| MattermostHighCPU | warning | CPU above 85% of limit for 10m | kube-state-metrics | [High CPU](../runbooks/mattermost-operator/mattermost-high-cpu.md) |
| MattermostCPULimitMissing | warning | Container runs without a CPU limit | kube-state-metrics | [CPU Limit Missing](../runbooks/mattermost-operator/mattermost-cpu-limit-missing.md) |
| MattermostHighMemory | warning | Working set above 90% of limit for 10m | kube-state-metrics | [High Memory](../runbooks/mattermost-operator/mattermost-high-memory.md) |
| MattermostMemoryLimitMissing | warning | Container runs without a memory limit | kube-state-metrics | [Memory Limit Missing](../runbooks/mattermost-operator/mattermost-memory-limit-missing.md) |
| MattermostDBWriterPoolEmpty | critical | Process up, no writer connections for 10m | Mattermost metrics | [DB Writer Pool Empty](../runbooks/mattermost-operator/mattermost-db-writer-pool-empty.md) |
| MattermostDBReplicaLag | warning | Replica lag over threshold (opt-in) | `ReplicaLagSettings` | [DB Replica Lag](../runbooks/mattermost-operator/mattermost-db-replica-lag.md) |
| MattermostHighHTTPErrorRate | warning | 5xx responses above 5% of API requests | Mattermost metrics | [High HTTP Error Rate](../runbooks/mattermost-operator/mattermost-high-http-error-rate.md) |
| MattermostLoginFailureSpike | warning | Most logins failing, at least 20 in 15m | Mattermost metrics | [Login Failure Spike](../runbooks/mattermost-operator/mattermost-login-failure-spike.md) |
| MattermostWebSocketDrop | warning | Connections below half of 10m ago | Mattermost metrics | [WebSocket Drop](../runbooks/mattermost-operator/mattermost-websocket-drop.md) |
| MattermostSearchDown | warning | Elasticsearch enabled and unhealthy | Mattermost metrics | [Search Down](../runbooks/mattermost-operator/mattermost-search-down.md) |
| MattermostClusterUnhealthy | warning | Gossip health score above 0 for 10m | Mattermost metrics | [Cluster Unhealthy](../runbooks/mattermost-operator/mattermost-cluster-unhealthy.md) |
| MattermostLogsDropped | warning | Log records dropped or blocked for 5m | Mattermost metrics | [Logs Dropped](../runbooks/mattermost-operator/mattermost-logs-dropped.md) |
| MattermostPostgresPodNotReady | critical | A `*postgres*` pod not Ready for 5m (plugin-only) | kube-state-metrics | [Postgres Pod Not Ready](../runbooks/mattermost-operator/mattermost-postgres-pod-not-ready.md) |
| MattermostPostgresPodCrashLooping | warning | More than 3 restarts in 15m (plugin-only) | kube-state-metrics | [Postgres Pod Crash Looping](../runbooks/mattermost-operator/mattermost-postgres-pod-crash-looping.md) |

Thresholds live in the operator's rules and are filled in by the plugin. Read `samples/mattermost-operator-rules.yaml` for the exact expressions.

## Wiring a set into a channel

```
/alertmanager config add <am-url> mattermost
/alertmanager config add <am-url> security
/alertmanager config add <am-url> storage
/alertmanager config validate security simulate runbook=unexpected-container-image severity=warning
```

`all` wires every slug above (35) behind one shared webhook. See
[Slash commands](SLASH_COMMANDS.md) for the full command reference.
