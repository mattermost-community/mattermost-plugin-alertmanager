# Disk Fill Rate High

!!! warning "Severity: Warning"
    **Target response: 30 min.** A PV's growth rate projects it to fill within 24 hours. Earlier warning than the "PV Full" alert.

## What this alert means

```promql
predict_linear(kubelet_volume_stats_available_bytes[6h], 24*3600) < 0
```

Linear regression on the last 6 hours of usage predicts that the volume will run out in less than 24 hours. This is the "act now, not at 95% full" alert.

## Quick diagnostics

Three commands to run before reading further:

```bash
# WHERE: shell on the affected node — SSH directly, or
#   `kubectl debug node/<node> -it --image=ubuntu`.
# WHAT: human-readable disk usage per mount point.
# READ: Use% column. The mount that's >85% (or that matches the
#   alert's mountpoint label) is the one filling. Avail tells
#   you how many bytes left — GB = hours of runway; MB = minutes.
df -h
```

```bash
# WHERE: shell on the affected node.
# WHAT: top 10 biggest directories under root, sorted desc. The
#   -x flag stops du at filesystem boundaries so it doesn't walk
#   into /proc, /sys, or other special mounts.
# READ: usual suspects:
#     /var/lib/docker → image/log buildup (docker system prune)
#     /var/lib/containerd → same, containerd flavor
#     /var/log → logs not rotating (check logrotate, journald)
#     /var/lib/journal → systemd journal too verbose (set
#       SystemMaxUse= in journald.conf)
#     /tmp → forgotten artifacts from a process that should clean up
du -hx --max-depth=2 / 2>/dev/null | sort -hr | head -10
```

```promql
# WHERE: Grafana → Explore (Prometheus data source) or Prometheus /graph.
# WHAT: predict_linear extrapolates the last 1h of available-bytes
#   data forward 4 hours. Returns the predicted byte count at T+4h
#   based on the current consumption rate.
# READ: NEGATIVE value → projection says you hit 0 bytes within
#   4 hours, act now. Small positive (<10 GiB) → 4-8 hours of
#   runway. Confirm with the actual `df -h` above before making
#   capacity decisions — predict_linear gets fooled by recent log
#   churn or backup jobs that aren't representative of steady state.
predict_linear(node_filesystem_avail_bytes{mountpoint="/"}[1h], 4 * 3600)
```

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
