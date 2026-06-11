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

Three commands to run before reading further. The SQL blocks work
whether your Postgres is RDS, Azure DB for PostgreSQL, Cloud SQL, or
in-cluster — connect via psql to the primary's `$DATABASE_URL`.

```bash
# WHERE: shell with psql, connected to the PRIMARY. <instance>
#   is filled in by AM at alert time — note this should be the
#   primary's hostname, not a replica's. Auth via ~/.pgpass.
# WHAT: which replicas are connected, what state they're in,
#   and how far behind they are across three lag dimensions.
# READ: three lag columns to understand:
#   write_lag → time for WAL to be written to disk on the replica
#     (fsync ack). Network-bound.
#   flush_lag → time for WAL to be flushed past OS buffer on the
#     replica. Disk-IO bound.
#   replay_lag → time for the replica to APPLY the WAL. This is
#     what reads-from-replica feel. CPU/query-replay bound.
#   write_lag low + replay_lag high → replica's apply process is
#     slow; check replica's CPU/disk. write_lag high → network or
#     primary fsync issue. All low → no actual lag, alert may be
#     stale.
psql "host=<instance> sslmode=require" -c "SELECT client_addr, application_name, state, write_lag, flush_lag, replay_lag FROM pg_stat_replication;"
```

```bash
# WHERE: shell with psql, connected to the REPLICA. <instance>
#   should be the replica's hostname here (not the primary's).
# WHAT: confirm the replica is actually receiving WAL and how
#   far behind in real time.
# READ:
#   is_replica = false → you're connected to a primary, not a
#     replica. Reconnect to the correct host.
#   received and replayed close together → replay is keeping up
#     with what's arriving. The lag is in WAL transmission;
#     check the primary's replication slot and network.
#   received far ahead of replayed → WAL is arriving but replica
#     can't keep up applying. Check replica's CPU; the alert is
#     about apply-side slowness.
#   replay_age in seconds → human-readable lag. >30s = warning;
#     >300s = critical, the replica is effectively stale for reads.
psql "host=<instance> sslmode=require" -c "SELECT pg_is_in_recovery() AS is_replica, pg_last_wal_receive_lsn() AS received, pg_last_wal_replay_lsn() AS replayed, now() - pg_last_xact_replay_timestamp() AS replay_age;"
```

```promql
# Run in Grafana → Explore (Prometheus data source) or Prometheus /graph.
# WAL lag in seconds via postgres_exporter — same metric whether the DB
# is RDS, Azure DB, Cloud SQL, or in-cluster (with the exporter wired).
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

## Required Prometheus labels

The Quick diagnostics commands above use `<label>` placeholders that
Alertmanager fills in from each alert's labels at delivery time. For
this runbook to render copy-paste-runnable commands, your Prometheus
rule must emit:

- `instance` — the DB hostname; use the PRIMARY's hostname for
  command 1 and the REPLICA's for command 2 (your alert needs to
  fire from the right scrape target — typically two separate rules
  with one labeling `instance` as the primary and the other as
  the replica)

When a label is missing, the rendered command shows `<no value>` in
that slot — still readable, just not auto-runnable. Add the label to
your rule and reload Prometheus.

## Related runbooks

- [Database Connectivity Loss](database-connectivity-loss.md) — when master goes fully down
- [Database High Latency](database-high-latency.md) — when slow queries on master cascade to replicas
