# Persistent Volume Full

!!! warning "Severity: Warning → Critical at 95%"
    **Target response: 30 min warning, 5 min critical.** A PV is approaching capacity. Writes will fail when full, causing application errors.

## What this alert means

```promql
kubelet_volume_stats_used_bytes / kubelet_volume_stats_capacity_bytes > 0.85
```

A PVC's underlying volume is more than 85% full. Time-to-full depends on growth rate (see [Disk Fill Rate High](disk-fill-rate-high.md) for an earlier-warning version).

When the volume hits 100%, writes return ENOSPC. The application may crash, the database may go read-only, or the service may just throw 500s on every write request.

## Quick diagnostics

Three commands to run before reading further:

```bash
# WHERE: shell with kubectl context set. <namespace> and <pod> are
#   filled in by AM at alert time. Set MOUNT_PATH to the in-pod
#   path of the PV (find via `kubectl describe pod <pod>` →
#   look under Mounts: in the container spec).
# WHAT: df -h run from INSIDE the pod, scoped to the PV mount.
#   Most accurate live usage — kubelet metrics in Prometheus can
#   lag by minutes.
# READ: USE% column. >90% = act now. Confirms the alert isn't
#   stale data the metric hasn't cleared yet.
kubectl exec -n <namespace> <pod> -- df -h $MOUNT_PATH
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: top 10 biggest directories under the PV mount, sorted desc.
#   -x prevents crossing into other mounts.
# READ: usual culprits:
#     application data that didn't rotate (logs, uploads, cache)
#     a database that grew its WAL or didn't VACUUM
#     a forgotten backup directory the cleanup job missed
#     temp files from a job that crashed without cleanup
kubectl exec -n <namespace> <pod> -- du -hx --max-depth=2 $MOUNT_PATH | sort -hr | head -10
```

```promql
# WHERE: Grafana → Explore (Prometheus data source) or Prometheus /graph.
# WHAT: PVC usage percentage cluster-wide. kubelet exposes used
#   and capacity metrics for every PV it knows about.
# READ: result is a percentage (e.g., 92 = 92% full). Sort desc
#   or filter to one PVC for the time-series view:
#     (kubelet_volume_stats_used_bytes{persistentvolumeclaim="<pvc-name>"}
#      / kubelet_volume_stats_capacity_bytes{persistentvolumeclaim="<pvc-name>"}) * 100
#   This metric is best for TRENDING — for exact live state,
#   use df -h from inside the pod above.
(kubelet_volume_stats_used_bytes / kubelet_volume_stats_capacity_bytes) * 100
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning (>85%) | No | 30 min | Capacity planning needed |
| Critical (>95%) | Yes | 5 min | Imminent write failure |

## Diagnostic steps

### 1. Identify the PVC and its usage
```bash
kubectl get pvc -n <namespace>
kubectl describe pvc -n <namespace> <pvc-name>
```

### 2. What's filling it?
```bash
# From a pod with the PVC mounted:
kubectl exec -n <namespace> <pod-name> -- du -sh /path/to/mount/*
```
TODO — adjust for your mount path. Look for: log files growing, old backups, stale data, runaway uploads.

### 3. Growth rate over the last day
```promql
deriv(kubelet_volume_stats_used_bytes{persistentvolumeclaim="<pvc-name>"}[24h])
```

## Common causes & fixes

### A. Log files filling the volume
| Symptom | Diagnosis | Fix |
|---|---|---|
| `du` shows logs as the largest consumers | Application is logging too verbosely OR log rotation isn't working | Tune log verbosity; configure logrotate or the app's built-in rotation |

### B. Old backups or temporary files
| Symptom | Diagnosis | Fix |
|---|---|---|
| `/tmp/` or `/backups/` directories dominate usage | Cleanup job stopped working or never ran | Run cleanup manually; restore the cron/scheduled job |

### C. Capacity outgrown
| Symptom | Diagnosis | Fix |
|---|---|---|
| All data is legitimate; growth is organic | Volume was sized for older traffic | Expand the PVC: `kubectl edit pvc -n <ns> <name>` (if `allowVolumeExpansion: true` on the StorageClass). Otherwise create a new larger volume and migrate. |

### D. Runaway write (bug)
| Symptom | Diagnosis | Fix |
|---|---|---|
| Growth rate spiked suddenly without traffic change | An application bug is writing without bounds | Restart the offending pod; investigate code |

## Escalation

1. **Platform on-call** — `@platform-oncall`, PagerDuty `mattermost-platform`.
2. **Cloud team** — if storage provider issues. PagerDuty `cloud-platform`.

## Post-incident

1. Capacity planning review.
2. If a bug caused runaway write, file regression.
3. Verify volume expansion is supported on the StorageClass for future growth.

## Required Prometheus labels

The Quick diagnostics commands above use `<label>` placeholders that
Alertmanager fills in from each alert's labels at delivery time. For
this runbook to render copy-paste-runnable commands, your Prometheus
rule must emit:

- `namespace` — the Kubernetes namespace of the pod using the PV
- `pod` — the specific pod with the PV mount

When a label is missing, the rendered command shows `<no value>` in
that slot — still readable, just not auto-runnable. Add the label to
your rule and reload Prometheus.

## Related runbooks

- [Disk Fill Rate High](disk-fill-rate-high.md) — earlier warning based on projected fill time
- [Node Not Ready](node-not-ready.md) — when node-level disk fills (different alert, related cause)
