# High Memory Usage

!!! danger "Severity: Warning → Critical at sustained levels"
    **Target response: 15 min warning, 5 min critical.** Memory
    pressure causes OOMKills (full container restarts) when it crosses
    the container's memory limit. Users see request failures
    correlated with the restart.

## What this alert means

A container is using more than 85% of its memory limit, sustained for
10+ minutes. The alert fires when:

```promql
container_memory_working_set_bytes / container_spec_memory_limit_bytes > 0.85
```

Working-set memory is what the kernel considers actively in-use
(closer to RSS). When this approaches the limit, the kernel's OOM
killer kills the most memory-hungry process in the cgroup — for a
container with a single process, that's a full restart.

The cascade: memory pressure → kernel pressure-stall increases → swap
exhaustion (or OOM if swap is off) → SIGKILL → container restart →
request failures during readiness probe re-acquire.

Critical-severity rule (`ContainerMemoryCritical`) fires at >95% for
20+ minutes — OOMKill is imminent if not already happening.

## Quick diagnostics

Three commands to run before reading further:

```bash
# WHERE: shell with kubectl context set.
# WHAT: top 20 pods cluster-wide by memory, sorted desc.
# READ: pods near or at their memory LIMIT (compare with
#   `kubectl describe pod`) are minutes from OOMKill. Pods near
#   their REQUEST but well under LIMIT are fine for now but
#   eating cluster capacity. The MEMORY(bytes) column shows RSS
#   — for the value the kernel actually compares against the
#   cgroup limit, see the PromQL below (working-set).
kubectl top pods -A --sort-by=memory | head -20
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: cluster events filtered to OOMKilling — kernel-level
#   events fired when a container hits its memory limit and
#   gets killed.
# READ: empty → no recent OOMs, the alert is preventive. Populated
#   → the alert is reactive, those pods just died and are likely
#   restarting; check their restart count:
#     kubectl get pod <name> -n <namespace> -o jsonpath='{.status.containerStatuses[].restartCount}'
kubectl get events -A --field-selector reason=OOMKilling --sort-by='.lastTimestamp' | tail -10
```

```promql
# WHERE: Grafana → Explore (Prometheus data source) or Prometheus /graph.
# WHAT: working-set memory by pod. Working-set ≈ "active" memory
#   the kernel won't reclaim without paging. This IS the value
#   the OOM killer compares against the cgroup limit (cAdvisor
#   exposes it from kernel memory.usage_in_bytes - inactive_file).
# READ: compare to each pod's memory limit. At >90% of limit,
#   OOMKill is minutes away. Filter to the failing pod:
#     container_memory_working_set_bytes{namespace="<namespace>",pod="<pod>"}
sum by (namespace, pod) (container_memory_working_set_bytes)
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning (>85% for 10m) | No | 15 min | Capacity loss probable when GC can't keep up |
| Critical (>95% for 20m) | Yes — page on-call | 5 min | OOMKill imminent, then request failures |

## Diagnostic steps

### 1. Confirm the alert is current

```bash
kubectl top pods -n <namespace> --sort-by=memory | head -10
```

Memory tends to climb monotonically and stay high. If `kubectl top`
shows the pod under-budget now, it OOMKilled and restarted — skip to
step 4.

### 2. Check container limits

```bash
kubectl describe pod -n <namespace> <pod-name> | grep -A2 "Limits:"
```

If memory limit is unset, the container will eat the node until
something else gives. If limit is set too low, you're seeing
right-sized pressure for the workload.

### 3. Look for OOMKill in pod state

```bash
kubectl describe pod -n <namespace> <pod-name> | grep -A3 "Last State"
```

A `Reason: OOMKilled` in Last State confirms the kernel killed the
container in the recent past. The current container is the restart.

### 4. Inspect memory by process inside the container

```bash
kubectl exec -n <namespace> <pod-name> -- \
  ps aux --sort=-%mem | head -10
```

For one-process containers (Go services typically), the main process
will dominate. For multi-process containers (Python with workers,
Java with JVM tooling), you can sometimes find a specific bad actor.

### 5. Inspect heap profile (language-specific)

For Go services:

```bash
# If pprof is enabled on the service:
kubectl port-forward -n <namespace> <pod-name> 6060:6060 &
go tool pprof -alloc_space http://localhost:6060/debug/pprof/heap
# (pprof) top 20
```

For JVM workloads:

```bash
# Trigger a heap dump (warning: pauses the JVM briefly)
kubectl exec -n <namespace> <pod-name> -- \
  jcmd 1 GC.heap_dump /tmp/heap.hprof
kubectl cp <namespace>/<pod-name>:/tmp/heap.hprof /tmp/
# Analyze with Eclipse MAT, VisualVM, or yourkit locally
```

For Python:

```bash
kubectl exec -n <namespace> <pod-name> -- \
  pip install memory_profiler && python -m memory_profiler <script>
```

## Common causes & fixes

### A. Recent deployment with a memory leak

| Symptom | Diagnosis | Fix |
|---|---|---|
| Memory climbs monotonically from deploy time | `kubectl rollout history` shows a recent revision; memory chart shows step or new slope | `kubectl rollout undo deployment -n <namespace> <name>` |

Common patterns: per-request goroutine leaks, unbounded caches, closure references holding the parent context.

### B. Memory limit too low for actual workload

| Symptom | Diagnosis | Fix |
|---|---|---|
| Memory at ~95% steady state across all pods, no leak pattern | Compare current limit to actual usage trend over a week | Raise the limit, redeploy. Don't raise without understanding why — could be masking a real leak. |

### C. Traffic spike with per-request allocations

| Symptom | Diagnosis | Fix |
|---|---|---|
| Memory spike correlates with `rate(http_requests_total[5m])` increase | Plot both metrics together in Grafana | Scale out horizontally; if you can't, accept the OOM and rely on retries |

### D. Heap fragmentation (Go/JVM)

| Symptom | Diagnosis | Fix |
|---|---|---|
| GC runs frequently but RSS doesn't drop | Compare `go_memstats_heap_inuse_bytes` vs `go_memstats_heap_sys_bytes` for Go; JVM equivalent metrics for JVM | Restart pod (workaround); investigate allocation hot paths for permanent fix |

### E. File descriptor or kernel buffer leak (rare)

| Symptom | Diagnosis | Fix |
|---|---|---|
| Process RSS is reasonable but cgroup memory keeps growing | `kubectl exec -- cat /proc/<pid>/status | grep VmRSS` vs `kubectl exec -- cat /sys/fs/cgroup/memory.current` show divergence | Restart pod, file bug in the application for unclosed FDs/socket buffers |

## Escalation

If unresolved within target response:

1. **Platform on-call** — `@platform-oncall` in `#mm-incidents`. PagerDuty service: `mattermost-platform`.
2. **Application team** — if heap profile points at app code. PagerDuty service: `<application-owning-team>`.
3. **Cloud team** — if multiple unrelated services hit memory pressure simultaneously, suspect node-level cause. PagerDuty service: `cloud-platform`.

**Severity ladder:**

| Time elapsed | Action |
|---|---|
| 0–15 min (warning) | Primary on-call works the alert |
| 15–30 min (warning) | Escalate to platform on-call |
| 0–5 min (critical) | Page primary, post in #mm-incidents |
| 5+ min (critical) | If OOMKilling continues, scale out OR raise limit as immediate mitigation; permanent fix is post-incident |

## Post-incident

1. **File a follow-up issue** identifying whether the cause was a
   leak (needs code fix), undersized limits (needs SRE adjustment), or
   genuine traffic growth (needs capacity planning).
2. **Update this runbook** with anything novel.
3. **For leaks: file a regression bug** against the service.
4. **For undersized limits: update the workload manifest** so the
   fix sticks across redeploys.

## Related runbooks

- [Pod CrashLoopBackOff](pod-crashloopbackoff.md) — when repeated OOMKills produce a crashloop
- [High CPU Usage](high-cpu-usage.md) — GC pressure can present as CPU spike rather than memory spike
- [Persistent Volume Full](persistent-volume-full.md) — when memory-mapped files cause cgroup pressure

## Appendix: useful PromQL queries

Top 10 memory consumers as fraction of limit:

```promql
topk(10,
  container_memory_working_set_bytes
  /
  container_spec_memory_limit_bytes
)
```

Memory growth rate (bytes per second) — useful for predicting OOM:

```promql
deriv(container_memory_working_set_bytes[10m])
```

OOMKill events in the last hour:

```promql
increase(kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}[1h])
```
