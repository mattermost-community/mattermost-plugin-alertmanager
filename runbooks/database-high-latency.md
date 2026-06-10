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

## Related runbooks

- [High API Latency](high-api-latency.md) — the downstream effect
- [Database Connectivity Loss](database-connectivity-loss.md) — when slow becomes unresponsive
- [Database Replication Lag](database-replication-lag.md) — replica lag often correlates with master slowness
