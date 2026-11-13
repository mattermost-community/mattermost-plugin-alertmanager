# Mattermost API High Latency

!!! warning "Severity: Warning"
    **Target response: 15 minutes.**
    API latency above 1 second is user-visible — message sends stall,
    channel loads spin, and mobile clients time out.

## What this alert means

The 95th-percentile Mattermost API response time has been above 1 second
for at least 5 minutes. The alert fires on:

```promql
histogram_quantile(0.95,
  sum by (le, handler) (
    rate(mattermost_api_time_elapsed_seconds_bucket[5m])
  )
) > 1
```

This is p95 across all API handlers. A single slow handler (e.g.
`/api/v4/posts` for a large channel) can lift the overall p95 without
affecting all users equally — check the `handler` label in the alert for
which endpoint is slow.

## Quick diagnostics

Three commands to run first:

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: CPU and memory usage of all Mattermost pods right now.
# READ: a pod pegged at its CPU limit is throttled — threads can't
#   serve requests until the scheduler grants them CPU time. That IS
#   the latency. Check limits with `kubectl describe pod <name>`.
kubectl top pods -n <namespace> -l app=mattermost --sort-by=cpu
```

```bash
# WHERE: shell with kubectl access.
# WHAT: recent plugin errors from Mattermost server logs.
# READ: a misbehaving plugin that runs synchronously on the request
#   path (e.g. a plugin with a slow OnMessageWillBePosted hook) will
#   show up here. If a single plugin_id repeats, disable it.
kubectl logs -n <namespace> -l app=mattermost --since=15m \
  | grep -E "plugin_id|plugin.*error|slow" | tail -30
```

```promql
# WHERE: Grafana → Explore or Prometheus /graph.
# WHAT: p99 latency per API handler, last 10 minutes.
# READ: sort descending. The top handler is your bottleneck. Cross-
#   reference with deployment history — did a new plugin or code version
#   go out recently?
histogram_quantile(0.99,
  sum by (le, handler) (
    rate(mattermost_api_time_elapsed_seconds_bucket[10m])
  )
)
```

## Diagnostic steps

### 1. Isolate the slow handler

The alert's `handler` label identifies which API endpoint is slow. Common
offenders:

| Handler | Likely cause |
|---------|-------------|
| `/api/v4/posts` | Large channel history, slow DB query, full-text search load |
| `/api/v4/channels` | High channel count per team, slow DB join |
| `/api/v4/users/search` | Elasticsearch or DB search under load |

### 2. Check database performance

```bash
# Mattermost DB slow query log (if enabled in config.json):
# Config: SqlSettings.QueryTimeout and SqlSettings.Trace
kubectl logs -n <namespace> -l app=mattermost --since=15m \
  | grep -E "slow|query.*ms|timeout" | tail -20
```

```promql
# p99 DB store time — a spike here explains API latency.
histogram_quantile(0.99,
  sum by (le) (
    rate(mattermost_db_store_time_elapsed_seconds_bucket[5m])
  )
)
```

### 3. Check for plugin hooks blocking the request path

```bash
kubectl logs -n <namespace> -l app=mattermost --since=15m \
  | grep "plugin.*hook.*took" | tail -20
```

If a plugin hook took more than a few hundred milliseconds, it's blocking
every request that passes through it. Disable the plugin with:
```bash
mmctl plugin disable <plugin-id>
```

### 4. Check for resource contention

```bash
kubectl describe pod -n <namespace> <mattermost-pod> \
  | grep -A4 "Limits:"
```

If `cpu` limits are set very close to `cpu` requests, CFS throttling is
likely. Increase the limit or add replicas.

## Common causes & fixes

### A. Database overload

| Symptom | Fix |
|---------|-----|
| DB p99 > 200ms correlates with API latency | Scale the DB, add read replicas, enable connection pooling |

### B. Plugin blocking request path

| Symptom | Fix |
|---------|-----|
| Plugin hook logs show > 100ms entries | Disable the plugin, file a bug with the plugin author |

### C. CPU throttling

| Symptom | Fix |
|---------|-----|
| Pod CPU at limit | Raise CPU limit or add Mattermost replicas |

### D. Full-text search under load

| Symptom | Fix |
|---------|-----|
| `/api/v4/users/search` or `/api/v4/posts/search` slow | Scale Elasticsearch, or temporarily disable FTS and use DB search |

## Escalation

1. **Platform on-call** — `@platform-oncall` if unresolved in 15 minutes.
2. **Mattermost vendor support** — if the slow handler is a core endpoint
   with no obvious infrastructure cause. Open a P1 ticket at
   `support.mattermost.com`.

## Required Prometheus labels

This runbook uses only the `handler` label from the `mattermost_api_time_elapsed_seconds_bucket` metric, which Mattermost exports automatically when metrics are enabled. No additional labels needed.

Enable Mattermost metrics: `ServiceSettings.EnableMetrics: true` in `config.json`.

## Related runbooks

- [Mattermost DB Slow Query](mattermost-db-slow-query.md)
- [High CPU Usage](high-cpu-usage.md)
