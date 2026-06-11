# Database High Latency

!!! warning "Severity: Warning"
    **Target response: 15 min.** p95 query latency above SLO. Application latency is degraded as a downstream effect.

## What this alert means

The 95th percentile of database query duration exceeds the SLO threshold (e.g., 100ms) sustained for 10+ minutes:

```promql
histogram_quantile(0.95,
  sum by (le) (rate(pg_query_duration_seconds_bucket[10m]))
) > 0.1
```

Slow database = slow API = bad user experience. The cause is usually one of: a runaway query, a missing index, replication catching up, or DB-side resource pressure.

## Quick diagnostics

Three commands to run before reading further. Run the SQL blocks via
any psql client connected to `$DATABASE_URL` — works for RDS, Azure
Database for PostgreSQL, Cloud SQL, self-hosted, or in-cluster.

```bash
# WHERE: shell with psql installed. <instance> is filled in by
#   AM at alert time. Auth via ~/.pgpass or PGUSER/PGPASSWORD.
# WHAT: top 5 slow queries by MEAN execution time. Requires
#   pg_stat_statements extension (default on most managed
#   Postgres; manual install on self-hosted via
#   `CREATE EXTENSION pg_stat_statements`).
# READ:
#   empty result → extension not installed, fall back to query 2.
#   queries with high mean_exec_time AND high calls → the queries
#     causing the alert. Optimize these first (add indexes, EXPLAIN
#     ANALYZE to spot seq scans, rewrite).
#   queries with high mean_exec_time but low calls → expensive
#     batch jobs, may be OK if infrequent.
psql "host=<instance> sslmode=require" -c "SELECT query, calls, mean_exec_time, total_exec_time FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT 5;"
```

```bash
# WHERE: shell with psql. Same connection params as above.
# WHAT: active long-running queries RIGHT NOW (vendor-agnostic,
#   no extension needed). Shows pid, duration, state, what each
#   query is waiting on.
# READ:
#   queries stuck in 'active' for many seconds → cause of the
#     latency spike. The wait_event column says what they're
#     waiting on (Lock, IO, etc.).
#   wait_event_type = "Lock" with wait_event = "transactionid"
#     → blocked on another transaction. Find the blocker:
#     SELECT blocked_locks.pid AS blocked_pid,
#            blocking_locks.pid AS blocking_pid
#     FROM pg_locks blocked_locks
#     JOIN pg_locks blocking_locks ON ...
#   Kill a stuck query: SELECT pg_terminate_backend(<pid>);
psql "host=<instance> sslmode=require" -c "SELECT pid, now() - query_start AS duration, state, wait_event_type, wait_event, left(query, 80) FROM pg_stat_activity WHERE state != 'idle' AND query NOT LIKE '%pg_stat_activity%' ORDER BY duration DESC LIMIT 5;"
```

```promql
# Run in Grafana → Explore (Prometheus data source) or Prometheus /graph.
# p95 query latency from postgres_exporter — same shape across in-cluster
# Postgres, RDS, Azure DB, Cloud SQL (any flavor with the exporter wired).
histogram_quantile(0.95, sum by (le) (rate(pg_stat_statements_mean_time_seconds_bucket[5m])))
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning | No | 15 min | Application latency degradation |

## Diagnostic steps

### 1. Identify slow queries
```sql
-- Postgres
SELECT query, calls, mean_exec_time, max_exec_time
FROM pg_stat_statements
ORDER BY mean_exec_time DESC
LIMIT 10;
```

### 2. Is there a missing index?
TODO — for the slowest query, run `EXPLAIN ANALYZE` and look for sequential scans.

### 3. DB-side resource pressure
TODO — CPU, IOPS, memory on the DB host. Check the provider console or DB exporter metrics.

### 4. Recent schema or query changes
TODO — release notes for any service that talks to this DB.

## Common causes & fixes

### A. Missing index after schema change
| Symptom | Diagnosis | Fix |
|---|---|---|
| One query consistently slow, EXPLAIN shows seq scan over a large table | Recent migration added a column queried without an index | Add the index (concurrently in production): `CREATE INDEX CONCURRENTLY ...` |

### B. Lock contention
| Symptom | Diagnosis | Fix |
|---|---|---|
| Latency spikes correspond with long-running transactions | TODO — query for blocking locks | Kill the blocking query or wait for it to complete |

### C. DB resource exhaustion
| Symptom | Diagnosis | Fix |
|---|---|---|
| DB CPU or IOPS at 100% | Capacity overrun | Scale up or scale out the DB |

### D. Vacuum / autovacuum pressure (Postgres)
| Symptom | Diagnosis | Fix |
|---|---|---|
| Latency spikes correlate with autovacuum runs | A large table is being vacuumed during peak | Tune autovacuum frequency for that table |

## Escalation

1. **Database team** — `@dba-oncall`, PagerDuty `data-platform`.

## Post-incident

1. If a query plan regressed, file a follow-up for whoever shipped the change.
2. Update this runbook with the specific query if it's a recurring offender.

## Required Prometheus labels

The Quick diagnostics commands above use `<label>` placeholders that
Alertmanager fills in from each alert's labels at delivery time. For
this runbook to render copy-paste-runnable commands, your Prometheus
rule must emit:

- `instance` — the DB hostname (e.g.,
  `postgres-prod.internal.example.com`, `db.rds.amazonaws.com`)

When a label is missing, the rendered command shows `<no value>` in
that slot — still readable, just not auto-runnable. Add the label to
your rule and reload Prometheus.

## Related runbooks

- [High API Latency](high-api-latency.md) — the downstream effect
- [Database Connectivity Loss](database-connectivity-loss.md) — when slow becomes unresponsive
- [Database Replication Lag](database-replication-lag.md) — replica lag often correlates with master slowness
