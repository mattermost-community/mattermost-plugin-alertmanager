# Alert Catalog

Every alert type the runbook library covers, grouped the same way as the
`/alertmanager add <set>` group sets and the `groups:` in your Prometheus
rules. Read it in chat with `/alertmanager docs alerts`.

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
`deployment`, `instance`, `job`, `namespace`, `node`, `pod`, `service`.

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

## Wiring a set into a channel

```
/alertmanager add <team> <channel> <am-url> security
/alertmanager add <team> <channel> <am-url> storage
/alertmanager validate security --simulate runbook=unexpected-container-image severity=warning
```

`all` wires every slug above (30) behind one shared webhook. See
`/alertmanager docs slash_commands` for the full command reference.
