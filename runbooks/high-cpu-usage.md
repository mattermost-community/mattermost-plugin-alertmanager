# High CPU Usage

!!! danger "Severity: Warning → Critical at sustained levels"
    **Target response: 15 minutes for warning, 5 minutes for critical.**
    Sustained CPU pressure causes user-visible latency, request
    queuing, and eventual request timeouts.

## What this alert means

A container, pod, or node has been running above 80% CPU utilization
for 10+ minutes. The alert fires when:

```promql
sum by (namespace, pod, container) (
  rate(container_cpu_usage_seconds_total[5m])
) > 0.8
```

This is **per-container CPU as a fraction of allocated limit**, not a
fraction of node capacity. A pod with a 2-core limit using 1.6 cores
sustained hits this threshold, even if the node has 30 idle cores.

Sustained CPU above the limit causes:

- **CFS throttling** — Linux's CPU scheduler caps the container at its
  limit, pausing threads until the next 100ms accounting window. Visible
  in `container_cpu_cfs_throttled_seconds_total`.
- **Request latency** — threads waiting on the scheduler can't serve
  requests, so p95/p99 latency spikes.
- **Memory pressure** — when CPU-bound code runs longer, it holds
  allocations longer, pushing the heap up.

The alert is warning-severity by default; if a container is over 95%
for 20+ minutes, a sibling rule (`ContainerCPUCritical`) fires at
critical-severity.

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning (>80% for 10m) | No — chat only | 15 min | Possible latency, no full outages |
| Critical (>95% for 20m) | Yes — on-call paged | 5 min | Request timeouts likely; user-facing degradation |

## Diagnostic steps

Run these in order. Stop as soon as the cause is obvious.

### 1. Confirm the alert is current

The Alertmanager UI shows actively firing alerts. The Mattermost post
that linked you here might be stale (group_wait + repeat_interval
delays).

```bash
# From any host with kubectl access — list the firing pods
kubectl top pods -n <namespace> --sort-by=cpu | head -10
```

If the alert says `<pod-name>` but `kubectl top` shows it under-budget
now, the spike has passed. Move to step 5 (find the cause of the
historical spike).

### 2. Check container limits vs actual usage

```bash
kubectl describe pod -n <namespace> <pod-name> | grep -A2 "Limits:"
```

Expected output shape:

```
Limits:
  cpu:     2
  memory:  4Gi
Requests:
  cpu:     500m
  memory:  1Gi
```

If `Limits.cpu` is unset, the container can saturate the node — the
CPU alert is a symptom of missing limits, not a sized-too-small
container.

### 3. Look for CFS throttling

```bash
# Throttled time as a fraction of total runtime
kubectl exec -n <namespace> <pod-name> -- \
  sh -c 'cat /sys/fs/cgroup/cpu.stat'
```

You're looking at `throttled_time` vs `usage_usec`. If
`throttled_time / usage_usec > 0.1`, the container is being throttled
heavily — Linux is pausing it to enforce the CPU limit.

### 4. Inspect the workload itself

```bash
# Top processes inside the container
kubectl exec -n <namespace> <pod-name> -- top -bn1 | head -20
```

If one PID dominates CPU, you've found the offender. If it's the main
process broadly, you have a workload-shape problem (see causes A and B
below).

### 5. Check recent deployment and traffic patterns

```bash
# Was there a recent deploy?
kubectl rollout history deployment -n <namespace> <deployment-name>

# Traffic against the affected service over the last hour:
# Open Prometheus and query:
#   sum by (pod) (rate(http_requests_total{namespace="<namespace>"}[5m]))
```

A traffic spike at the same time as the CPU spike suggests load. A
CPU spike without traffic change suggests a code regression.

## Common causes & fixes

### A. Recent deployment with a regression

| Symptom | Diagnosis | Fix |
|---|---|---|
| CPU spike started within minutes of a `helm upgrade` or operator image bump | Check `kubectl rollout history` — the active revision is recent | `kubectl rollout undo deployment -n <namespace> <name>` |

Common patterns: an N+1 query introduced in a new release, a regex
catastrophic backtrack, a tight loop without a sleep, accidentally
disabled caching.

### B. Real traffic increase

| Symptom | Diagnosis | Fix |
|---|---|---|
| CPU spike correlates with request rate increase in Prometheus | Compare `rate(http_requests_total[5m])` to baseline | Scale horizontally: `kubectl scale deployment -n <namespace> <name> --replicas=N` |

If you autoscale via HPA, check that the HPA's metric is actually
firing:

```bash
kubectl get hpa -n <namespace>
kubectl describe hpa -n <namespace> <hpa-name>
```

A common bug: HPA points at the wrong metric (e.g., `cpu` but limits
aren't set, so CPU% calculation breaks).

### C. Missing CPU limits

| Symptom | Diagnosis | Fix |
|---|---|---|
| `kubectl describe pod` shows no `Limits.cpu` | Container can saturate the node | Add limits in the workload's manifest; redeploy |

Don't set limits much higher than requests — the gap is where CFS
throttling lives. Aim for limits ≤ 2× requests as a starting heuristic.

### D. Plugin / dependency hot loop (Mattermost-specific)

| Symptom | Diagnosis | Fix |
|---|---|---|
| CPU spike is in a single Mattermost pod, logs show a `plugin_id` repeatedly | A plugin is busy-waiting or pathologically reentering | Disable the plugin: `mmctl plugin disable <plugin-id>` |

### E. Garbage collection pressure (Go/Java workloads)

| Symptom | Diagnosis | Fix |
|---|---|---|
| `top` shows runtime processes (Go scheduler, JVM GC threads) using majority of CPU | Heap under pressure, GC running constantly | Increase memory limit (give GC more room) OR investigate allocation hot-spots |

For Go workloads, `GODEBUG=gctrace=1` reveals GC frequency. For JVM,
check GC logs. If GC dominates, more memory is usually the answer —
the CPU spike is downstream of a memory pressure problem.

## Escalation

If unresolved within the target response time:

1. **Platform on-call** — `@platform-oncall` in
   [#mm-incidents](mattermost://channels/mm-incidents). PagerDuty
   service: `mattermost-platform`.
2. **Cloud team** — if multiple unrelated services are affected at
   the same time, suspect node-level pressure. PagerDuty service:
   `cloud-platform`.
3. **Mattermost vendor support** — if the cause is suspected in the
   Mattermost binary itself (unusual). Open a P1 ticket at
   `support.mattermost.com`.

**Severity ladder:**

| Time elapsed | Action |
|---|---|
| 0–15 min (warning) | Primary on-call works the alert |
| 15–30 min (warning) | Escalate to platform on-call if unable to diagnose |
| 0–5 min (critical) | Page primary on-call immediately |
| 5–15 min (critical) | Page secondary on-call, post status in #mm-incidents |
| 15+ min (critical) | Engage incident commander, declare incident |

## Post-incident

After the immediate fix lands:

1. **File a follow-up issue** with root cause and what was changed.
   Use the team's incident template.
2. **Update this runbook** if the cause wasn't already covered — the
   most underused improvement loop in SRE is "I just fixed this; let
   me add it to the playbook for the next person." Open a merge
   request against this repo.
3. **If a rollback was needed, file a regression bug** against the
   service that shipped the offending change. Don't let a regression
   sit unfixed just because the rollback worked.
4. **Consider whether the alert thresholds are right.** A real
   incident that fires below your threshold (you found out from a
   user, not from PagerDuty) is a tuning signal.

## Related runbooks

- [Pod Not Ready](pod-not-ready.md) — when high CPU causes the
  readiness probe to fail
- Container OOM-killed (TODO)
- HPA not scaling (TODO)

## Appendix: useful PromQL queries

Find the top 10 CPU-consuming containers in the last 5 minutes:

```promql
topk(10,
  sum by (namespace, pod, container) (
    rate(container_cpu_usage_seconds_total[5m])
  )
)
```

CPU usage as a fraction of limit (the alert's actual expression):

```promql
sum by (namespace, pod, container) (
  rate(container_cpu_usage_seconds_total[5m])
)
/
sum by (namespace, pod, container) (
  kube_pod_container_resource_limits{resource="cpu"}
)
```

CFS throttling rate — fraction of time the container is being throttled:

```promql
rate(container_cpu_cfs_throttled_periods_total[5m])
/
rate(container_cpu_cfs_periods_total[5m])
```
