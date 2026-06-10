# Disk Fill Rate High

!!! warning "Severity: Warning"
    **Target response: 30 min.** A PV's growth rate projects it to fill within 24 hours. Earlier warning than the "PV Full" alert.

## What this alert means

```promql
predict_linear(kubelet_volume_stats_available_bytes[6h], 24*3600) < 0
```

Linear regression on the last 6 hours of usage predicts that the volume will run out in less than 24 hours. This is the "act now, not at 95% full" alert.

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning | No | 30 min | Preventive — disk fill in <24h if growth continues |

## Diagnostic steps

### 1. Identify the PVC and current usage
```bash
kubectl get pvc -n <namespace>
```

### 2. Growth rate over recent windows
TODO — Prometheus query for hourly delta. Was the growth gradual or recent spike?

### 3. What changed?
TODO — recent deploy, new feature enabling more data, traffic increase.

## Common causes & fixes

### A. Sudden growth from new code path
| Symptom | Fix |
|---|---|
| Growth jumped after a deploy | Investigate the new feature; consider feature flag rollback |

### B. Organic growth outrunning capacity
| Symptom | Fix |
|---|---|
| Growth is steady, no recent change | Expand the PV proactively (`kubectl edit pvc`) |

### C. Log retention not enforced
| Symptom | Fix |
|---|---|
| Logs / temp data piling up | Configure rotation, run cleanup |

## Escalation

1. **Platform on-call** — `@platform-oncall`.

## Related runbooks

- [Persistent Volume Full](persistent-volume-full.md) — the alert that fires when this one was ignored
