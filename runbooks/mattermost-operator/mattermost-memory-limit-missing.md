# Mattermost Memory Limit Missing

!!! info "Severity: Warning"
    **Target response: next business day.**
    Nothing is broken. The mattermost container runs without a memory
    limit, so High Memory cannot fire for it. A leak grows until the node
    runs out of memory, and the kernel then kills whichever process it
    picks, which may not be Mattermost.

## What this alert means

```promql
count by (namespace, pod) (
  container_memory_working_set_bytes{namespace="<namespace>", pod=~"<installation>-.*", container="mattermost"}
)
unless
count by (namespace, pod) (
  kube_pod_container_resource_limits{namespace="<namespace>", pod=~"<installation>-.*", container="mattermost", resource="memory"} > 0
)
```

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: the resources the mattermost container actually runs with.
# READ: no "limits" key, or limits without "memory", confirms the alert.
kubectl get pod -n <namespace> <pod> -o jsonpath='{.spec.containers[?(@.name=="mattermost")].resources}{"\n"}'
```

## Diagnostic steps

### 1. Find where the resources come from

```bash
kubectl get mm -n <namespace> <installation> -o jsonpath='{.spec.scheduling.resources}{"\n"}'
kubectl get limitrange -n <namespace>
```

The operator copies `spec.scheduling.resources` from the Mattermost CR into
the Deployment.

### 2. Pick a limit

```promql
max_over_time(max by (pod) (container_memory_working_set_bytes{namespace="<namespace>", container="mattermost"})[7d:5m])
```

Start at 1.5 times the seven-day peak. Server plugins run inside the same
container, so leave room for them.

## Fix

```yaml
# Mattermost CR
spec:
  scheduling:
    resources:
      requests:
        memory: 2Gi
      limits:
        memory: 4Gi
```

Setting the memory request equal to the limit gives the pod the Guaranteed
QoS class for memory, which makes it the last to be evicted under node
pressure.

## Required Prometheus labels

`namespace` and `pod`, from cAdvisor and kube-state-metrics.

## Related runbooks

- [High Memory](mattermost-high-memory.md)
- [CPU Limit Missing](mattermost-cpu-limit-missing.md)
