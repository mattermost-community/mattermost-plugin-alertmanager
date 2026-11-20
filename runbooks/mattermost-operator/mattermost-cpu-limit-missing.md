# Mattermost CPU Limit Missing

!!! info "Severity: Warning"
    **Target response: next business day.**
    Nothing is broken. The mattermost container runs without a CPU limit,
    so High CPU cannot fire for it, and one busy pod can starve its
    neighbours on the node.

## What this alert means

```promql
count by (namespace, pod) (
  rate(container_cpu_usage_seconds_total{namespace="<namespace>", pod=~"<installation>-.*", container="mattermost"}[5m])
)
unless
count by (namespace, pod) (
  kube_pod_container_resource_limits{namespace="<namespace>", pod=~"<installation>-.*", container="mattermost", resource="cpu"} > 0
)
```

The pod is using CPU, and kube-state-metrics reports no CPU limit for it.

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: the resources the mattermost container actually runs with.
# READ: no "limits" key, or limits without "cpu", confirms the alert.
#   Note the requests too: the limit you set should be at or above them.
kubectl get pod -n <namespace> <pod> -o jsonpath='{.spec.containers[?(@.name=="mattermost")].resources}{"\n"}'
```

## Diagnostic steps

### 1. Find where the resources come from

```bash
kubectl get mm -n <namespace> <installation> -o jsonpath='{.spec.scheduling.resources}{"\n"}'
kubectl get limitrange -n <namespace>
```

The operator copies `spec.scheduling.resources` from the Mattermost CR into
the Deployment. A namespace LimitRange can add a default limit; if one
exists and the pod still has none, the pod predates it.

### 2. Pick a limit

```promql
max_over_time(sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="<namespace>", container="mattermost"}[5m]))[7d:5m])
```

Set the limit comfortably above the seven-day peak. Two times the peak is a
reasonable start.

## Fix

```yaml
# Mattermost CR
spec:
  scheduling:
    resources:
      requests:
        cpu: "1"
      limits:
        cpu: "2"
```

The operator rolls the Deployment after the change. The alert resolves once
the new pods run.

If you deliberately run without CPU limits, silence this alert for the
installation instead of deleting the rule, so a missing memory limit still
alerts.

## Required Prometheus labels

`namespace` and `pod`, from cAdvisor and kube-state-metrics.

## Related runbooks

- [High CPU](mattermost-high-cpu.md)
- [Memory Limit Missing](mattermost-memory-limit-missing.md)
