# CPU Throttling High

!!! danger "Severity: warning"
    **Target response: 30m.** A container is being CPU-throttled by the
    Linux CFS scheduler for a significant fraction of its runtime — latency
    without the CPU-usage graph looking "hot."

## What this alert means

CFS throttling = the kernel capping a container at its CPU *limit*. The
container can be throttled hard while sitting well under 100% average
usage, so `HighCPUUsage` won't catch it. Throttling shows up as latency,
timeouts, and slow health checks.

```promql
# Fraction of scheduler periods in which the container was throttled.
sum by (namespace, pod, container) (rate(container_cpu_cfs_throttled_periods_total[5m]))
/
sum by (namespace, pod, container) (rate(container_cpu_cfs_periods_total[5m]))
> 0.25
```

Sustained throttling >25% means the container spends a quarter of its
scheduling periods waiting for CPU it's not allowed to use — often the
hidden cause of a co-firing `HighAPILatency`.

## Quick diagnostics

```promql
# WHERE: Grafana → Explore (Prometheus) or Prometheus /graph.
# WHAT: the throttled-periods ratio for the alerting container over time.
# READ: 0 = healthy. >0.1 = throttled 10%+ of periods (noticeable). >0.25
#   = the alert threshold, real latency impact. Rising trend = getting worse.
rate(container_cpu_cfs_throttled_seconds_total{namespace="<namespace>",pod="<pod>",container="<container>"}[5m])
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: the CPU request vs limit for the throttled container.
# READ: a low limit relative to request (or a limit barely above request)
#   throttles under normal bursts. limit == request with bursty work is a
#   classic throttling setup. No limit set = shouldn't throttle (recheck).
kubectl get pod -n <namespace> <pod> -o jsonpath='{range .spec.containers[*]}{.name}: req={.resources.requests.cpu} lim={.resources.limits.cpu}{"\n"}{end}'
```

```promql
# WHERE: Grafana → Explore or Prometheus /graph.
# WHAT: actual CPU usage vs the limit, to size a fix.
# READ: if usage rides just under the limit and throttling is high, the
#   limit is too low — raise it. If usage is spiky, the limit is fine on
#   average but too low for bursts — raise the limit, not the request.
sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="<namespace>",pod="<pod>"}[5m]))
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning | No — chat only | 30m | Latency / timeouts under load |

## Diagnostic steps

1. **Confirm throttling is current** (ratio query above).
2. **Check limits** — request vs limit for the container.
3. **Correlate** — does it line up with a latency alert or a traffic peak?
4. **Right-size** — raise the CPU limit (or remove it for latency-critical, well-behaved workloads) and redeploy.

## Common causes & fixes

| Symptom | Diagnosis | Fix |
|---|---|---|
| Limit ≈ request, bursty app | High throttle ratio at peaks | Raise limit to cover bursts |
| Limit far below usage | Usage rides the ceiling | Raise limit to p95 usage + headroom |
| Throttling only at deploy | Startup CPU spike | Raise limit or add startup probe slack |

## Escalation

1. **Primary** — `@sre-oncall` in `#mm-incidents`.
2. **Service owner** — to agree new limits if the workload is theirs.

## Required Prometheus labels

Diagnostics use `namespace`, `pod`, `container`. Provided by cAdvisor
(`container_cpu_cfs_*`).

## Related runbooks

- [High API Latency](high-api-latency.md) — throttling is a frequent hidden cause.
- [High CPU Usage](high-cpu-usage.md) — the sibling that fires on raw usage, not throttling.
