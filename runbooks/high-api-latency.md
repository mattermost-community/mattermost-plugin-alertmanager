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

## Related runbooks

- [High HTTP 5xx Error Rate](high-http-error-rate.md) — when latency degrades into timeouts (504s)
- [Database High Latency](database-high-latency.md) — most common upstream cause
- [High CPU Usage](high-cpu-usage.md) — when CPU pressure causes scheduler latency
