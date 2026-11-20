# Mattermost High Memory

!!! warning "Severity: Warning"
    **Target response: 15 minutes.**
    The mattermost container's working set is above 90% of its memory
    limit. At 100% the kernel OOM-kills it, every connection on the pod
    drops, and the pod restarts.

## What this alert means

```promql
sum by (namespace, pod) (
  container_memory_working_set_bytes{namespace="<namespace>", pod=~"<installation>-.*", container="mattermost"}
)
/
sum by (namespace, pod) (
  kube_pod_container_resource_limits{namespace="<namespace>", pod=~"<installation>-.*", container="mattermost", resource="memory"} > 0
) > 0.9
```

Working set is the number the OOM killer uses. The threshold is
`prometheusRule.thresholds.memoryLimitPercent` on the Mattermost CR
(default 90).

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: memory per pod in this installation, plus OOM kills so far.
# READ: compare the alerting pod with the rest. The second command prints
#   OOMKilled for any container that already hit the limit.
kubectl top pod -n <namespace> -l installation.mattermost.com/installation=<installation_mattermost_com_installation> --containers
kubectl get pod -n <namespace> <pod> -o jsonpath='{.status.containerStatuses[?(@.name=="mattermost")].lastState.terminated.reason}{"\n"}'
```

```promql
# WHERE: Prometheus or Grafana Explore.
# WHAT: Go heap in use on the pod, from the server's own metrics.
# READ: heap close to the working set = the server holds the memory.
#   Working set far above heap = file cache or plugin processes.
go_memstats_heap_inuse_bytes{namespace="<namespace>", pod="<pod>"}
```

## Diagnostic steps

### 1. Steady growth or a spike?

```promql
container_memory_working_set_bytes{namespace="<namespace>", pod="<pod>", container="mattermost"}
```

Graph it over a day. A climb that only resets on restart suggests a leak.
A spike that falls back suggests a job or a burst of large uploads.

### 2. Plugins

Each server plugin is a separate process in the same container, and counts
against the same limit.

```bash
kubectl exec -n <namespace> <pod> -c mattermost -- ps -eo rss,args --sort=-rss | head -10
```

A plugin process near the top is the first suspect.

### 3. Recent change

A new version or a newly enabled plugin right before the climb is the most
likely cause.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| Plugin process with large RSS | Plugin memory use | Update or disable the plugin |
| Spike during a job | Export, import or reindex | Let it finish; raise the limit if it OOMs |
| Steady climb across days | Leak in a version | Upgrade or contact support with a heap profile |
| Every pod high, users up | Real growth | Raise `spec.scheduling.resources.limits.memory` or `spec.replicas` |

## Escalation

1. **Platform on-call** if a pod has already been OOM-killed.
2. **Mattermost support** for suspected leaks, with the version and the
   memory graph.

## Required Prometheus labels

`namespace` and `pod`, from cAdvisor and kube-state-metrics.

## Related runbooks

- [Memory Limit Missing](mattermost-memory-limit-missing.md)
- [Pod Crash Looping](mattermost-pod-crash-looping.md)
- [High Memory Usage](../high-memory-usage.md)
