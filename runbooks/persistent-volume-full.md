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
# Actual current usage from inside the mounted pod
kubectl exec -n $NAMESPACE $POD -- df -h $MOUNT_PATH
```

```bash
# What's filling the volume?
kubectl exec -n $NAMESPACE $POD -- du -hx --max-depth=2 $MOUNT_PATH | sort -hr | head -10
```

```promql
# PVC usage % across the cluster
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

## Related runbooks

- [Disk Fill Rate High](disk-fill-rate-high.md) — earlier warning based on projected fill time
- [Node Not Ready](node-not-ready.md) — when node-level disk fills (different alert, related cause)
