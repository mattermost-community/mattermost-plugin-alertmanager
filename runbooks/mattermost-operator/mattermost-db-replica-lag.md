# Mattermost DB Replica Lag

!!! warning "Severity: Warning"
    **Target response: 15 minutes.**
    A read replica is more than five seconds behind the primary. Users can
    read stale data: a message they just posted is missing on refresh, or
    an unread count is wrong.

## What this alert means

```promql
max by (namespace, installation_mattermost_com_installation) (
  mattermost_db_replica_lag_time{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"}
) > 5
```

Mattermost runs the lag query in `SqlSettings.ReplicaLagSettings` against
each replica and exports the answer in seconds. Without that setting the
series does not exist, which is why this alert is opt-in
(`mm-db-replicas=true`). The threshold is
`prometheusRule.thresholds.dbReplicaLagSeconds` on the Mattermost CR.

## Quick diagnostics

```promql
# WHERE: Prometheus or Grafana Explore.
# WHAT: lag per replica, labelled by the node name in ReplicaLagSettings.
# READ: one replica lagging = that replica. Every replica lagging = the
#   primary is producing WAL faster than they can replay it, usually a
#   bulk job.
mattermost_db_replica_lag_time{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"}
```

```sql
-- WHERE: psql connected to the lagging replica.
-- WHAT: how far behind replay is, measured on the replica itself.
-- READ: matches the alert = real lag. Near zero = the lag query in
--   ReplicaLagSettings is measuring something else.
SELECT now() - pg_last_xact_replay_timestamp() AS replay_lag;
```

## Diagnostic steps

### 1. Check the primary's write load

```sql
-- On the primary.
SELECT client_addr, state, sent_lsn, replay_lsn,
       pg_wal_lsn_diff(sent_lsn, replay_lsn) AS bytes_behind
FROM pg_stat_replication;
```

`bytes_behind` growing means the replica cannot keep up with the WAL.

### 2. Look for a long query on the replica

```sql
-- On the replica.
SELECT pid, now() - query_start AS runtime, left(query, 80)
FROM pg_stat_activity
WHERE state = 'active' ORDER BY runtime DESC LIMIT 5;
```

A long read on a hot-standby replica can pause replay until it finishes.

### 3. Check the replica's resources

For RDS or Azure, check replica CPU, IOPS and `ReplicaLag` in the cloud
console. For in-cluster Postgres, check the replica pod's CPU and disk.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| Every replica lags during a job | Bulk import, export, or data retention | Wait; schedule bulk jobs off-hours |
| One replica lags, IOPS maxed | Replica undersized | Scale the replica's instance or storage |
| Replay paused, long query | Query conflicts with replay | Set `max_standby_streaming_delay`, or end the query |
| Alert fires with no replica traffic | Wrong lag query in `ReplicaLagSettings` | Fix the query, compare with the second quick diagnostic |

If lag stays high, Mattermost can be pointed at the primary for reads by
removing the replica from `SqlSettings.DataSourceReplicas` until it
catches up. Every read then lands on the primary, so first confirm with
database on-call that it has CPU and connection headroom: its active
connections plus each pod's `MaxOpenConns` must stay under
`max_connections`. If the primary's write load is what is causing the
lag, this makes it worse.

## Escalation

Database on-call if lag stays above threshold for 30 minutes, or keeps
growing.

## Required Prometheus labels

`namespace` and `installation_mattermost_com_installation`.

## Related runbooks

- [DB Writer Pool Empty](mattermost-db-writer-pool-empty.md)
- [Database Replication Lag](../database-replication-lag.md)
