# High HTTP 5xx Error Rate

!!! danger "Severity: Critical"
    **Target response: 5 min.** More than 5% of HTTP requests are
    returning 5xx errors. Users see failures on the affected
    endpoints.

## What this alert means

The ratio of 5xx responses to total responses over the last 5 minutes
exceeds 5%, sustained for 10+ minutes. The alert fires when:

```promql
sum by (service, namespace) (rate(http_requests_total{status=~"5..", namespace="<ns>"}[5m]))
/
sum by (service, namespace) (rate(http_requests_total{namespace="<ns>"}[5m]))
> 0.05
```

A 5xx (500–599) indicates a server-side error — distinct from 4xx
which is the client's fault. Sustained 5xx means the service can't
process requests correctly, regardless of how the user formed them.

The exact error code matters for diagnosis:

| Code | Typical cause |
|---|---|
| 500 Internal Server Error | Unhandled exception in app code |
| 502 Bad Gateway | Upstream service unreachable (often a load balancer's view of a dead pod) |
| 503 Service Unavailable | Capacity exhausted, or readiness probe just failed |
| 504 Gateway Timeout | Upstream service responded too slowly |

## Quick diagnostics

Three commands to run before reading further:

```promql
# Run in Grafana → Explore (Prometheus data source) or in Prometheus'
# own UI at /graph. Returns services sorted by 5xx ratio, worst first.
# A value of 0.05 = 5% of that service's requests returning 5xx;
# 0.30 = 30%. Raw 5xx counts mislead — 3 errors/s on a 1 req/s service
# is "on fire", same 3 errors/s on a 1000 req/s service is noise.
sort_desc(
  sum by (service) (rate(http_requests_total{code=~"5.."}[5m]))
  /
  sum by (service) (rate(http_requests_total[5m]))
)
```

```bash
# WHERE: shell with kubectl context set. <namespace> is filled
#   in by AM at alert time.
# WHAT: last 3 deploy revisions for the failing service.
# READ: a revision created within the alert window (last ~30
#   min) and the 5xx spike following it = strong cause hypothesis.
#   Roll back to test:
#     kubectl rollout undo deployment/<name> -n <namespace>
#   If no recent deploys, the cause is upstream — check the
#   service's downstream dependencies (DB query times, external
#   API latency, cache outage).
kubectl rollout history deployment -n <namespace> --limit 3
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: last 200 log lines from the failing service's pods,
#   filtered to error/panic/stack patterns. <service> is filled
#   in by AM at alert time.
# READ: scan for:
#   panic / stack trace → bug in the code, find the file:line
#     where the panic originates
#   "ERROR" lines with downstream failure → external dependency
#     issue (DB, cache, API)
#   "EOF" / "connection reset" → backend pod restarting under load
#   Timestamps tell you if errors are still happening or have
#   stopped (alert may be lagging real state by minutes).
kubectl logs -n <namespace> -l app=<service> --tail=200 | grep -E "ERROR|panic|stack"
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Critical (>5% sustained 10m) | Yes | 5 min | User-visible failures on affected requests |

## Diagnostic steps

### 1. Confirm and characterize

```bash
# Open Prometheus, query the error rate breakdown by status code:
sum by (status) (rate(http_requests_total{namespace="<ns>"}[5m]))
```

Is the spike concentrated on one status code? (Suggests a specific failure mode.) Or spread across 5xx codes? (Suggests broad outage.)

### 2. Identify the affected service

If the alert specifies a service in labels, jump to step 3. Otherwise:

```bash
# Rank services by 5xx rate
topk(10,
  sum by (service) (rate(http_requests_total{status=~"5..", namespace="<ns>"}[5m]))
)
```

### 3. Recent deployment check

```bash
kubectl rollout history deployment -n <namespace> <service-name>
```

If the latest revision deployed within the alert's `for:` window, suspect a regression. Default response: roll back first, diagnose offline.

### 4. Read recent error logs

```bash
kubectl logs -n <namespace> -l app=<service-name> --tail=200 --timestamps \
  | grep -iE "error|panic|exception|stack"
```

Look for repeated error signatures. A single error per request type spread across requests is different from one stack trace 1000 times.

### 5. Check upstream dependencies

Most 5xx come from upstream failures. Check the service's downstream:

```bash
# DB reachability from inside the pod
kubectl exec -n <namespace> <pod> -- \
  sh -c 'nc -zv $DB_HOST $DB_PORT'

# Cache reachability
kubectl exec -n <namespace> <pod> -- \
  sh -c 'nc -zv $REDIS_HOST 6379'

# Any other service this one calls
kubectl exec -n <namespace> <pod> -- \
  sh -c 'curl -sv -m 3 $UPSTREAM_SERVICE_URL'
```

## Common causes & fixes

### A. Recent deployment regression

| Symptom | Diagnosis | Fix |
|---|---|---|
| Error rate jumped at deploy time | `kubectl rollout history` shows recent revision change matching the spike | `kubectl rollout undo deployment -n <namespace> <name>` |

### B. Upstream dependency outage

| Symptom | Diagnosis | Fix |
|---|---|---|
| 502/504 dominant in the breakdown; logs show upstream connection errors | The DB, cache, or upstream API is unreachable or slow | Diagnose the upstream — usually a separate alert is firing for it; coordinate with that team |

### C. Capacity exhaustion

| Symptom | Diagnosis | Fix |
|---|---|---|
| 503s dominant; corresponds with a traffic increase or pod reduction | Compare `rate(http_requests_total[5m])` to baseline + check replica count | Scale out: `kubectl scale deployment -n <namespace> <name> --replicas=N` |

### D. Database performance degradation

| Symptom | Diagnosis | Fix |
|---|---|---|
| Logs show DB query timeouts; downstream alerts about DB latency | Query the DB's slow-query log; check replica lag if reads are slow | Tune query, add an index, or fail over the DB. See [Database High Latency](database-high-latency.md). |

### E. Plugin or feature flag induced failure (Mattermost-specific)

| Symptom | Diagnosis | Fix |
|---|---|---|
| Error logs include a specific `plugin_id` or feature flag name | A misbehaving plugin is throwing or a feature flag was flipped | Disable via System Console → Plugins, or via `mmctl plugin disable <id>`. Revert the feature flag. |

### F. Certificate or auth expiry

| Symptom | Diagnosis | Fix |
|---|---|---|
| 5xx with logs showing TLS handshake failures or token-expired errors | A cert renewed, a token rotated, or SSO config changed | Refresh the cert/token; redeploy if needed |

## Escalation

If unresolved within 5 minutes:

1. **Service owning team's on-call** — `@<service-team>-oncall` in `#mm-incidents`. PagerDuty service: depends on the service.
2. **Database team** — if DB is the upstream cause. PagerDuty service: `data-platform`.
3. **Cloud team** — if network policies, ingress, or infra-side. PagerDuty service: `cloud-platform`.

**Severity ladder:**

| Time elapsed | Action |
|---|---|
| 0–5 min | Primary on-call works the alert; roll back if a deploy is the obvious trigger |
| 5–15 min | Page secondary, post status in #mm-incidents |
| 15+ min | Declare incident, start status page updates, engage incident commander |

## Post-incident

1. **File a postmortem.** 5xx alerts on user-facing services are usually customer-visible — write up the timeline, impact assessment, and fix.
2. **Update this runbook** if the cause was novel.
3. **For regression**: file a code-side bug. The deploy gate that let it through is also worth examining — were tests insufficient? Was canary skipped?
4. **For capacity**: file a follow-up to make HPA actually engage, or to raise the floor of replicas if HPA reacted too slowly.

## Required Prometheus labels

The Quick diagnostics commands above use `<label>` placeholders that
Alertmanager fills in from each alert's labels at delivery time. For
this runbook to render copy-paste-runnable commands, your Prometheus
rule must emit:

- `namespace` — the Kubernetes namespace of the failing service
- `service` — the application/service identifier used by your
  `app=<service>` selector (typically matches the K8s Service name
  and the `app.kubernetes.io/name` pod label)

When a label is missing, the rendered command shows `<no value>` in
that slot — still readable, just not auto-runnable. Add the label to
your rule and reload Prometheus.

## Related runbooks

- [High API Latency](high-api-latency.md) — 504s often present after sustained latency
- [Service Endpoint Down](service-endpoint-down.md) — 502/503 storm often follows a service going fully down
- [Database Connectivity Loss](database-connectivity-loss.md) — sudden 500 spike when DB drops
- [Pod CrashLoopBackOff](pod-crashloopbackoff.md) — 502s when a pod is crashlooping

## Appendix: useful PromQL queries

Error rate per service over the last hour:

```promql
sum by (service) (rate(http_requests_total{status=~"5..", namespace="<ns>"}[5m]))
/
sum by (service) (rate(http_requests_total{namespace="<ns>"}[5m]))
```

Most common status codes in the last 5 minutes:

```promql
topk(5,
  sum by (status) (rate(http_requests_total{namespace="<ns>"}[5m]))
)
```

Error rate by endpoint (route) — useful when one endpoint is broken but others are fine:

```promql
topk(10,
  sum by (route) (rate(http_requests_total{status=~"5..", namespace="<ns>"}[5m]))
)
```
