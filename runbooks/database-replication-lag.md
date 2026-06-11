# Database Replication Lag

!!! warning "Severity: Warning → Critical at higher thresholds"
    **Target response: 15 min warning, 5 min critical.** A replica is falling behind the master. Reads served from this replica return stale data.

## What this alert means

```promql
pg_replication_lag_seconds > 30   # warning
pg_replication_lag_seconds > 300  # critical
```

(MySQL: `mysql_slave_lag_seconds`. Other DBs have equivalent metrics.)

The replica is N seconds behind the master in applying writes. Applications reading from the replica see data that's at least N seconds stale. For most apps this is invisible noise; for read-after-write workflows it causes user-visible inconsistencies ("I just posted but it's not showing up").

## Quick diagnostics

Three commands to run before reading further:

```sql
-- From the PRIMARY: which replica is lagging, by how much?
SELECT client_addr, application_name, state, replay_lag
FROM pg_stat_replication;
```

```bash
# Replica pod status
kubectl get pods -n db -l role=replica -o wide
```

```promql
# WAL lag in seconds (postgres_exporter metric)
pg_replication_lag_seconds
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning (>30s) | No | 15 min | Read-after-write inconsistencies; cache invalidation lag |
| Critical (>5m) | Yes | 5 min | Replica essentially useless for reads; failover risk if master fails |

## Diagnostic steps

### 1. Confirm the lag and which replica
TODO — provider-specific dashboard URL or PromQL.

### 2. Is the master under heavy write load?
```promql
rate(pg_stat_database_xact_commit[5m])
```
A 10x write spike causes replicas to fall behind even if the network and replica are healthy.

### 3. Is the replica struggling with hardware?
TODO — IOPS metric, CPU utilization on the replica.

### 4. Replication slot or network issue?
TODO — provider-specific query for replication slot health.

## Common causes & fixes

### A. Write spike on master
| Symptom | Diagnosis | Fix |
|---|---|---|
| Lag started climbing with a write-rate increase | Bulk import, migration, or organic growth | Wait it out if temporary. Throttle bulk operations. |

### B. Replica IOPS saturated
| Symptom | Diagnosis | Fix |
|---|---|---|
| Replica's disk IOPS at 100%; master is normal | Replica hardware undersized | Upgrade replica's instance class / IOPS allocation |

### C. Replication stalled
| Symptom | Diagnosis | Fix |
|---|---|---|
| Lag growing monotonically without master load increase; lag is in WAL bytes not just seconds | Replication slot stuck or replica process hung | Restart the replica process or fail over to a fresh one |

### D. TODO — your DB-specific cause

## Escalation

1. **Database team** — `@dba-oncall`, PagerDuty `data-platform`.

## Post-incident

1. Capacity review if hardware-shaped.
2. If a bulk operation caused it, schedule those for low-traffic windows in future.

## Related runbooks

- [Database Connectivity Loss](database-connectivity-loss.md) — when master goes fully down
- [Database High Latency](database-high-latency.md) — when slow queries on master cascade to replicas
