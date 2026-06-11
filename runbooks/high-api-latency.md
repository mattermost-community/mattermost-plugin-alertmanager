# High API Latency

!!! warning "Severity: Warning"
    **Target response: 15 min.** Sustained p95/p99 latency above SLO. Users perceive slowness on every request to the affected endpoint.

## What this alert means

The 95th percentile of HTTP request duration for this service exceeds the SLO threshold (e.g., 2s) for 10+ minutes:

```promql
histogram_quantile(0.95,
  sum by (le, service) (rate(http_request_duration_seconds_bucket{namespace="<ns>"}[10m]))
) > 2.0
```

Latency this high doesn't usually mean total failure (those are caught by error-rate alerts), but it does mean a degraded experience. Users perceive a "slow site" — clicks take seconds, posts appear to hang. Often correlates with backend pressure (DB, cache, slow upstream).

## Quick diagnostics

Three commands to run before reading further:

```promql
# WHERE: Grafana → Explore (Prometheus data source) or Prometheus /graph.
# WHAT: p95 HTTP request duration per handler over the last 5
#   minutes. Histogram quantile requires the service to expose
#   _bucket metrics (standard for Go/Java/Python with the
#   prometheus client libs).
# READ: result is per-handler latency in seconds. Compare against
#   each handler's SLO. A handler usually at 50ms now at 2s = root
#   cause; one usually at 1s still at 1s = noise. Sort to find
#   the worst:
#     sort_desc(histogram_quantile(0.95, sum by (handler, le) (rate(http_request_duration_seconds_bucket[5m]))))
histogram_quantile(0.95, sum by (handler, le) (rate(http_request_duration_seconds_bucket[5m])))
```

```bash
# WHERE: shell with kubectl context set. <namespace> and <app>
#   are filled in by AM at alert time.
# WHAT: top pods by CPU in the affected service's namespace,
#   sorted desc.
# READ: pods near their CPU LIMIT (compare with `kubectl describe
#   pod`) → latency is throttle-induced; raise limit or add
#   replicas. Pods well under their limit → latency is downstream
#   (DB, cache, external API). Use the runbook below for next steps.
kubectl top pod -n <namespace> -l app=<app> --sort-by=cpu
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: last 5 deploy revisions for the affected service.
# READ: a revision created within the alert window (~30 min) and
#   the latency spike following it = strong cause hypothesis.
#   Roll back to test:
#     kubectl rollout undo deployment/<name> -n <namespace>
#   If no recent deploys, look downstream (DB query times,
#   external API latency, cache hit rates).
kubectl rollout history deployment -n <namespace> --limit 5
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning (p95 > 2s sustained 10m) | No | 15 min | Perceived slowness; user complaints |

## Diagnostic steps

### 1. Confirm and characterize
TODO — open Prometheus, look at the p95 trend over the last hour vs baseline. Sudden jump or gradual climb?

### 2. Which endpoint?
```promql
topk(10,
  histogram_quantile(0.95,
    sum by (le, route) (rate(http_request_duration_seconds_bucket{namespace="<ns>"}[5m]))
  )
)
```

### 3. Concurrent slowness in downstream services?
TODO — check DB latency, cache latency, upstream service alerts firing at the same time.

### 4. Recent deploy correlation
```bash
kubectl rollout history deployment -n <namespace> <service-name>
```

### 5. CPU throttling check
```promql
rate(container_cpu_cfs_throttled_seconds_total[5m])
```
TODO — high throttling causes latency without showing as CPU exhaustion.

## Common causes & fixes

### A. Slow database queries
| Symptom | Diagnosis | Fix |
|---|---|---|
| DB query time metric correlates with API latency | Check pg_stat_statements / slow query log | Add an index, tune query, or scale up |

### B. CPU throttling under traffic
| Symptom | Diagnosis | Fix |
|---|---|---|
| `container_cpu_cfs_throttled_seconds_total` high | CPU limit too low; getting cgroup-throttled | Increase limit or scale out |

### C. Recent deploy regression
| Symptom | Diagnosis | Fix |
|---|---|---|
| Latency stepped up at deploy time | `kubectl rollout history` | `kubectl rollout undo` |

### D. TODO — additional cause specific to your services

## Escalation

1. Service owning team's on-call.
2. **Database team** if DB-related.
3. **Platform on-call** if cluster-resource-related.

## Post-incident

1. SLO miss tracking.
2. Update this runbook with cause novelty.
3. Consider whether p99 (or p99.9) deserves a separate alert for outlier-experience tracking.

## Required Prometheus labels

The Quick diagnostics commands above use `<label>` placeholders that
Alertmanager fills in from each alert's labels at delivery time. For
this runbook to render copy-paste-runnable commands, your Prometheus
rule must emit:

- `namespace` — the Kubernetes namespace of the failing service
- `app` — the application label of the failing service (typically the
  value of `app.kubernetes.io/name` or your team's app label
  convention)

When a label is missing, the rendered command shows `<no value>` in
that slot — still readable, just not auto-runnable. Add the label to
your rule and reload Prometheus.

## Related runbooks

- [High HTTP 5xx Error Rate](high-http-error-rate.md) — when latency degrades into timeouts (504s)
- [Database High Latency](database-high-latency.md) — most common upstream cause
- [High CPU Usage](high-cpu-usage.md) — when CPU pressure causes scheduler latency
