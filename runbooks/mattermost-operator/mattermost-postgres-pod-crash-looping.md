# Mattermost Postgres Pod Crash Looping

!!! warning "Severity: Warning"
    **Target response: 15 minutes.**
    A Postgres pod in the Mattermost namespace restarted more than three
    times in fifteen minutes. Each restart of the primary drops every
    Mattermost database connection.

## What this alert means

```promql
sum by (namespace, pod) (
  increase(kube_pod_container_status_restarts_total{namespace="<namespace>", pod=~".*postgres.*"}[15m])
) > 3
```

Plugin-only (`mm-postgres-pod=true`). The pod name match covers the
primary, replicas, and a Postgres exporter in the same namespace.

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: why each container in the pod last exited.
# READ: OOMKilled = memory limit too low for shared_buffers and work_mem.
#   Error = read the previous logs. If only the exporter container
#   restarts, Postgres itself is fine.
kubectl get pod -n <namespace> <pod> -o jsonpath='{range .status.containerStatuses[*]}{.name}{"\t"}{.restartCount}{"\t"}{.lastState.terminated.reason}{"\n"}{end}'
```

```bash
# WHERE: shell with kubectl access.
# WHAT: the last lines of the crashed Postgres container.
# READ: "PANIC" or "could not write" = disk. "out of memory" = memory.
kubectl logs -n <namespace> <pod> --previous --tail=50
```

## Diagnostic steps

### 1. Which container restarts?

Use the first quick diagnostic. An exporter sidecar that crashes on bad
credentials restarts the pod's count without touching the database.

### 2. Memory

`OOMKilled` means `shared_buffers` plus connection memory exceeds the
container limit. Each Mattermost pod opens up to `SqlSettings.MaxOpenConns`
connections, so adding Mattermost replicas raises Postgres memory use.

### 3. Disk

```bash
kubectl exec -n <namespace> <pod> -- df -h /bitnami/postgresql /var/lib/postgresql/data 2>/dev/null
```

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| `OOMKilled` | Memory limit too low | Raise the Postgres memory limit, or lower `max_connections`. Changing `max_connections` needs a Postgres restart; first lower `SqlSettings.MaxOpenConns` and restart the Mattermost pods, so pods × `MaxOpenConns` fits under the new limit |
| `No space left on device` | Volume full | Expand the PVC |
| Only the exporter restarts | Exporter credentials or version | Fix the exporter secret; Postgres is fine |
| Liveness probe kills it during recovery | Probe too strict | Raise the probe's `initialDelaySeconds` or `failureThreshold` |

## Escalation

Database on-call if the primary is the pod restarting.

## Required Prometheus labels

`namespace` and `pod`, from kube-state-metrics.

## Related runbooks

- [Postgres Pod Not Ready](mattermost-postgres-pod-not-ready.md)
- [DB Writer Pool Empty](mattermost-db-writer-pool-empty.md)
- [Pod CrashLoopBackOff](../pod-crashloopbackoff.md)
