# Mattermost DB Slow Query

!!! warning "Severity: Warning"
    **Target response: 15 minutes.**
    Slow database queries are the most common cause of Mattermost API
    latency. A p99 above 500ms means ~1% of operations are user-visibly
    slow; left unchecked it compounds across request chains.

## What this alert means

The 99th-percentile Mattermost database store operation time has exceeded
500ms for at least 5 minutes. The alert fires on:

```promql
histogram_quantile(0.99,
  sum by (le) (
    rate(mattermost_db_store_time_elapsed_seconds_bucket[5m])
  )
) > 0.5
```

This covers all database store calls Mattermost makes internally — posts,
channels, user lookups, sessions, etc. A spike here propagates directly
to API latency because most API handlers make at least one synchronous DB
call on the request path.

## Quick diagnostics

```bash
# WHERE: shell with access to your PostgreSQL database.
# WHAT: currently running queries, sorted by duration.
# READ: any query running > 1s is your immediate target. The 'query'
#   column shows the SQL. 'wait_event' shows what the query is blocked
#   on (e.g. Lock = lock contention, IO = disk). A query stuck in
#   'Lock' means something else is holding a lock — look at the query
#   blocking it.
psql -c "
SELECT pid, now() - pg_stat_activity.query_start AS duration,
       wait_event, state, left(query, 120)
FROM pg_stat_activity
WHERE state != 'idle'
  AND query_start < now() - interval '1 second'
ORDER BY duration DESC
LIMIT 20;
"
```

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: DB-related log entries from Mattermost.
# READ: look for 'slow query', 'timeout', or 'deadlock'. The 'store'
#   field tells you which data access pattern triggered it (e.g.,
#   'PostStore.Save', 'ChannelStore.GetChannels').
kubectl logs -n <namespace> -l app=mattermost --since=15m \
  | grep -iE "slow|timeout|deadlock|query.*ms" | tail -30
```

```bash
# WHERE: shell with access to PostgreSQL.
# WHAT: tables with sequential scans in the last stats reset window.
# READ: a high seq_scan on a large table = missing index. Compare
#   seq_scan vs idx_scan: if seq_scan >> idx_scan, you need an index.
psql -c "
SELECT relname, seq_scan, idx_scan,
       pg_size_pretty(pg_relation_size(relid)) AS size
FROM pg_stat_user_tables
ORDER BY seq_scan DESC
LIMIT 15;
"
```

## Diagnostic steps

### 1. Check for active long-running queries or locks

```bash
psql -c "
SELECT pid, usename, wait_event_type, wait_event, state,
       now() - query_start AS duration, left(query, 200)
FROM pg_stat_activity
WHERE state = 'active'
ORDER BY duration DESC NULLS LAST
LIMIT 10;
"
```

If a query has been running for minutes, consider canceling it (only if
it's safe — don't cancel migrations in progress):

```bash
# Cancel gracefully first
psql -c "SELECT pg_cancel_backend(<pid>);"
# Terminate if cancel doesn't work
psql -c "SELECT pg_terminate_backend(<pid>);"
```

### 2. Check for table bloat or VACUUM lag

```bash
psql -c "
SELECT relname, n_dead_tup, n_live_tup,
       last_vacuum, last_autovacuum, last_analyze
FROM pg_stat_user_tables
WHERE n_dead_tup > 10000
ORDER BY n_dead_tup DESC
LIMIT 10;
"
```

High `n_dead_tup` with no recent `last_autovacuum` means autovacuum is
falling behind. Manually vacuum the worst table:

```bash
psql -c "VACUUM ANALYZE mattermost.<table_name>;"
```

### 3. Check connection pool saturation

```bash
psql -c "
SELECT count(*) AS active_connections,
       (SELECT setting::int FROM pg_settings WHERE name = 'max_connections') AS max_connections
FROM pg_stat_activity
WHERE state = 'active';
"
```

If active connections is near `max_connections`, Mattermost is queuing
DB requests. Solutions: increase `max_connections`, add PgBouncer, or
enable Mattermost's built-in connection pool settings.

### 4. Identify slow query patterns

```bash
# Requires pg_stat_statements extension enabled:
psql -c "
SELECT calls, mean_exec_time, max_exec_time, left(query, 200)
FROM pg_stat_statements
ORDER BY mean_exec_time DESC
LIMIT 10;
"
```

Bring the slowest queries to the Mattermost database team — many have
known index fixes or query optimizations in newer releases.

## Common causes & fixes

### A. Missing index on a frequently-queried column

| Symptom | Fix |
|---------|-----|
| `seq_scan` very high on Posts or Channels table | Check Mattermost DB schema docs for required indexes; apply missing ones |

### B. Connection pool exhaustion

| Symptom | Fix |
|---------|-----|
| Active connections near `max_connections` | Add PgBouncer, or increase `SqlSettings.MaxOpenConns` carefully |

### C. Table bloat from high write volume

| Symptom | Fix |
|---------|-----|
| `n_dead_tup` very high, autovacuum falling behind | Run `VACUUM ANALYZE` manually; increase `autovacuum_scale_factor` for high-write tables |

### D. Lock contention

| Symptom | Fix |
|---------|-----|
| Queries stuck in `wait_event: Lock` | Find the blocking PID; cancel it if safe |

## Escalation

1. **DBA / database team** — for index or schema changes.
2. **Platform on-call** — `@platform-oncall` if the slowdown is causing
   broad user impact.

## Required Prometheus labels

`mattermost_db_store_time_elapsed_seconds_bucket` is a histogram exported
by Mattermost when metrics are enabled. The `store` label on the metric
identifies which store operation was slow — add `by (le, store)` to the
PromQL to break it down per operation.

## Related runbooks

- [Mattermost API High Latency](mattermost-api-high-latency.md)
- [Database High Latency](database-high-latency.md)
