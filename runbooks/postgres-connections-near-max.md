# Postgres Connections Near Max

!!! danger "Severity: warning"
    **Target response: 20m.** Postgres is approaching `max_connections`.
    When it hits the ceiling, new connections are refused and the app
    starts throwing "too many clients" — a self-inflicted outage.

## What this alert means

Every Postgres connection costs a backend process and memory. When
in-use connections approach `max_connections`, the next client (including
Mattermost) gets `FATAL: sorry, too many clients already`.

```promql
# Fraction of max_connections currently in use.
sum(pg_stat_activity_count) / on() pg_settings_max_connections > 0.8
```

At >80% you're one traffic spike or connection leak away from refusal.
Mattermost without a working DB connection fails writes immediately.

## Quick diagnostics

```sql
-- WHERE: psql against the affected DB (use $DATABASE_URL or the admin DSN).
-- WHAT: connection count by state and application, plus the configured max.
-- READ: many 'idle in transaction' = a leak (app holding txns open); many
--   'idle' from one app = an oversized pool; 'active' near max = genuine load.
SELECT state, application_name, count(*)
FROM pg_stat_activity GROUP BY state, application_name ORDER BY count(*) DESC;
```

```sql
-- WHERE: psql against the affected DB.
-- WHAT: longest-running / oldest transactions holding connections.
-- READ: an 'idle in transaction' row with a large age is a leaked
--   connection — the app opened a txn and never committed. That's the
--   usual culprit behind creeping connection counts.
SELECT pid, state, now()-xact_start AS xact_age, left(query,80)
FROM pg_stat_activity WHERE state <> 'idle' ORDER BY xact_start LIMIT 15;
```

```promql
# WHERE: Grafana → Explore or Prometheus /graph.
# WHAT: connection count over time vs the max.
# READ: a steady climb that never drops = a leak (won't self-heal, will
#   hit the ceiling). A spike tracking traffic = capacity; scale the pooler
#   or raise max_connections with memory headroom.
sum(pg_stat_activity_count)
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning | No — chat only | 20m | Imminent connection refusals → write failures |

Escalate to **critical/page** at >95% or once refusals appear in app logs.

## Diagnostic steps

1. **Confirm headroom** (ratio query) — how close to the ceiling.
2. **Classify** — leak (`idle in transaction`, climbing) vs. load (`active`, tracks traffic).
3. **Relieve** — terminate leaked backends (`pg_terminate_backend(pid)`); for load, add/adjust a connection pooler (PgBouncer).
4. **Right-size** — raise `max_connections` only with RAM to back it (~10 MB/conn); prefer pooling.

## Common causes & fixes

| Symptom | Diagnosis | Fix |
|---|---|---|
| Growing `idle in transaction` | App leaks txns | Fix app; kill leaked pids as stopgap |
| One app with huge idle pool | Oversized client pool | Lower pool size / front with PgBouncer |
| `active` near max under load | Genuine capacity | Add PgBouncer; scale read replicas |

## Escalation

1. **Primary** — `@dba-oncall` in `#mm-incidents`.
2. **Service owner** — if a specific app is leaking connections.

## Required Prometheus labels

Diagnostics are cluster-level (no per-pod labels). Requires the Postgres
exporter (`pg_stat_activity_count`, `pg_settings_max_connections`).

## Related runbooks

- [Database Connectivity Loss](database-connectivity-loss.md) — what happens when the ceiling is hit.
- [Database High Latency](database-high-latency.md) — connection saturation and latency often co-fire.
