# Mattermost Pod Crash Looping

!!! danger "Severity: Critical"
    **Target response: 5 minutes.**
    A Mattermost pod restarted more than three times in five minutes.
    Every restart drops that pod's WebSocket connections and fails its
    in-flight requests.

## What this alert means

```promql
sum by (namespace, pod) (
  rate(kube_pod_container_status_restarts_total{namespace="<namespace>", pod=~"<installation_mattermost_com_installation>-.*"}[5m])
) * 300 > 3
```

Restart counts come from kube-state-metrics, so this alert fires even
when Mattermost never gets far enough to serve metrics.

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: why each container last exited.
# READ: OOMKilled = memory limit too low (see High Memory). Error with
#   exit code 1 = the server failed at startup; read the previous logs.
#   Completed or exit 0 = something stopped it cleanly, usually a
#   failing liveness probe.
kubectl get pod -n <namespace> <pod> -o jsonpath='{range .status.containerStatuses[*]}{.name}{"\t"}{.restartCount}{"\t"}{.lastState.terminated.reason}{"\t"}{.lastState.terminated.exitCode}{"\n"}{end}'
```

```bash
# WHERE: shell with kubectl access.
# WHAT: the last lines the crashed container wrote.
# READ: the last error or panic before the exit is the cause. Database
#   or file store errors at startup are the most common.
kubectl logs -n <namespace> <pod> -c mattermost --previous --tail=60
```

```bash
# WHERE: shell with kubectl access.
# WHAT: recent events for the pod.
# READ: "Liveness probe failed" means the server hung, not crashed.
kubectl get events -n <namespace> --field-selector involvedObject.name=<pod> --sort-by=.lastTimestamp | tail -15
```

## Diagnostic steps

### 1. Read the exit reason

Use the first quick diagnostic. `OOMKilled` sends you to
[High Memory](mattermost-high-memory.md). Anything else, read the
previous container's logs.

### 2. Check what the server needs at startup

Mattermost exits at startup when it cannot reach the database, the file
store, or a valid license.

```bash
kubectl logs -n <namespace> <pod> -c mattermost --previous | grep -iE 'database|sql|filestore|s3|license|fatal|panic' | tail -20
```

### 3. Check for a recent change

```bash
kubectl rollout history deploy -n <namespace> <installation_mattermost_com_installation>
kubectl get mm -n <namespace> <installation_mattermost_com_installation> -o jsonpath='{.spec.image}:{.spec.version}{"\n"}'
```

A crash loop that starts right after a version change points at the
upgrade: a failed database migration or a plugin that no longer loads.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| `OOMKilled` | Memory limit too low | Raise `spec.scheduling.resources.limits.memory` on the Mattermost CR |
| `pq:` or `dial tcp` errors at startup | Database unreachable or wrong credentials | Check the database secret and network path; see [DB Writer Pool Empty](mattermost-db-writer-pool-empty.md) |
| Migration error after upgrade | Failed schema migration | Contact support with the log before changing `spec.version`. A partly migrated schema may not run on the old version; rolling back can mean restoring the pre-upgrade database backup |
| `Liveness probe failed` in events | Server hung under load | Check CPU and database latency before raising probe timeouts |
| Plugin panic in logs | A plugin crashes the server | Disable the plugin (`PluginSettings.PluginStates`) and restart |

## Escalation

1. **Platform on-call** immediately if more than one pod is crash looping.
2. **Database on-call** if the logs show database errors.
3. **Mattermost support** for migration failures, with the full previous log.

## Required Prometheus labels

`namespace` and `pod`, from kube-state-metrics.

## Related runbooks

- [Mattermost Server Down](mattermost-server-down.md)
- [High Memory](mattermost-high-memory.md)
- [Pod CrashLoopBackOff](../pod-crashloopbackoff.md)
