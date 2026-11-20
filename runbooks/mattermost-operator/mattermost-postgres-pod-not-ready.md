# Mattermost Postgres Pod Not Ready

!!! danger "Severity: Critical"
    **Target response: 5 minutes.**
    A Postgres pod in the Mattermost namespace has not been Ready for five
    minutes. If it is the primary, Mattermost cannot write. If it is a
    replica, reads move to the other replicas or fail.

## What this alert means

```promql
kube_pod_status_ready{namespace="<namespace>", pod=~".*postgres.*", condition="true"} == 0
unless on (namespace, pod)
kube_pod_status_phase{namespace="<namespace>", pod=~".*postgres.*", phase="Succeeded"} == 1
```

Any pod whose name contains `postgres` counts, so the primary, replicas and
an exporter are all covered. Finished pods, such as a backup Job, are left
out. This alert is plugin-only (`mm-postgres-pod=true`); the operator does
not ship it.

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: the pod's conditions and its latest events.
# READ: Unschedulable = no node fits. FailedMount or FailedAttachVolume =
#   the volume is stuck. Readiness probe failed = Postgres runs but does
#   not accept connections, often recovery or a full disk.
kubectl describe pod -n <namespace> <pod> | sed -n '/Conditions:/,$p' | tail -30
```

```bash
# WHERE: shell with kubectl access.
# WHAT: the last lines Postgres logged.
# READ: "database system is starting up" or "redo in progress" = recovery,
#   wait. "No space left on device" = the volume is full.
kubectl logs -n <namespace> <pod> --tail=50
```

## Diagnostic steps

### 1. Primary or replica?

Pod names usually say so: `-primary-0` or `-read-0` for charts such as
Bitnami. If you cannot tell, ask a Ready instance:

```bash
kubectl exec -n <namespace> <pod> -- psql -U postgres -tAc 'SELECT pg_is_in_recovery();'
```

`f` is the primary.

### 2. Is the volume the problem?

```bash
kubectl get pvc -n <namespace> | grep -i postgres
kubectl exec -n <namespace> <pod> -- df -h /bitnami/postgresql /var/lib/postgresql/data 2>/dev/null
```

A volume at 100% stops Postgres from writing WAL, and the pod never gets
Ready. See [Persistent Volume Full](../persistent-volume-full.md).

### 3. Is it scheduled?

`Pending` with `Unschedulable` means no node fits the requests, or the
volume is bound to a zone with no free node.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| `No space left on device` | Volume full | Expand the PVC; clear old WAL only with the database team |
| `redo in progress` | Crash recovery after restart | Wait; large WAL takes minutes |
| `FailedAttachVolume` | Volume stuck on the old node | Delete the old VolumeAttachment, or wait for the cloud detach |
| `Unschedulable` | No node with room or in the volume's zone | Add capacity in that zone |
| Replica not Ready, primary fine | Replica cannot stream from the primary | Check replication credentials and the primary's `pg_hba.conf` |

## Escalation

1. **Database on-call** immediately if the primary is not Ready.
2. **Platform on-call** for scheduling and volume problems.

## Required Prometheus labels

`namespace` and `pod`, from kube-state-metrics.

## Related runbooks

- [Postgres Pod Crash Looping](mattermost-postgres-pod-crash-looping.md)
- [DB Writer Pool Empty](mattermost-db-writer-pool-empty.md)
- [Pod Not Ready](../pod-not-ready.md)
