# Mattermost DB Writer Pool Empty

!!! danger "Severity: Critical"
    **Target response: 5 minutes.**
    The server is up but holds no connections to its primary database.
    Posting, logging in and anything else that writes fails.

## What this alert means

```promql
mattermost_db_master_connections_total{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"} == 0
and on (namespace, pod)
up{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"} == 1
```

`mattermost_db_master_connections_total` is the number of open connections
in the pool for `SqlSettings.DataSource`, the writer. The alert needs the
process itself to be up, and the pool to stay empty for ten minutes. That
is past the default five-minute idle timeout, so a quiet server that
closed idle connections does not fire.

A missing read replica does not change this number. Replica problems show
up in [DB Replica Lag](mattermost-db-replica-lag.md).

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: database errors the alerting pod logged in the last 15 minutes.
# READ: "connection refused" or "i/o timeout" = network or the database is
#   down. "password authentication failed" = rotated credentials.
#   "too many connections" = the database is at max_connections.
kubectl logs -n <namespace> <pod> -c mattermost --since=15m | grep -iE 'pq:|sql|database|dial tcp|too many connections' | tail -30
```

```bash
# WHERE: shell with kubectl access.
# WHAT: the database settings on the Mattermost CR.
# READ: external.secret names the secret holding the connection string.
#   Check that secret's host and credentials against the database.
kubectl get mm -n <namespace> <installation_mattermost_com_installation> -o jsonpath='{.spec.database}{"\n"}'
```

## Diagnostic steps

### 1. Is the database up?

For a managed database (RDS, Azure Database for PostgreSQL), check its
status in the cloud console. For an in-cluster Postgres:

```bash
kubectl get pods -n <namespace> | grep -i postgres
```

If the Postgres pod is not Ready, work
[Postgres Pod Not Ready](mattermost-postgres-pod-not-ready.md) first.

### 2. Can the pod reach it?

```bash
kubectl run -n <namespace> pg-check --rm -it --restart=Never --image=postgres:16 -- \
  pg_isready -h <db-host> -p 5432
```

`accepting connections` with Mattermost still failing points at
credentials or TLS settings. `no response` points at the network: a
NetworkPolicy, a security group or a private endpoint.

### 3. Is the database full of connections?

```sql
SELECT count(*), max_conn FROM pg_stat_activity,
  (SELECT setting::int AS max_conn FROM pg_settings WHERE name = 'max_connections') s
GROUP BY max_conn;
```

Near `max_connections`, every new Mattermost connection is refused. See
[Postgres Connections Near Max](../postgres-connections-near-max.md).

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| `password authentication failed` | Credentials rotated without updating the secret | Update the database secret; the operator restarts the pods |
| `dial tcp ... i/o timeout` | Network path blocked | Check NetworkPolicy, security groups, private DNS |
| `too many connections` | Database at its connection limit | Lower `SqlSettings.MaxOpenConns` per pod or raise `max_connections` |
| Database in maintenance or failover | Planned or managed failover | Wait for failover; the pool reconnects on its own |

## Escalation

1. **Database on-call** immediately. Writes are failing.
2. **Platform on-call** if the network path is the cause.

## Required Prometheus labels

`namespace`, `pod`, and `installation_mattermost_com_installation`.

## Related runbooks

- [DB Replica Lag](mattermost-db-replica-lag.md)
- [Postgres Pod Not Ready](mattermost-postgres-pod-not-ready.md)
- [Database Connectivity Loss](../database-connectivity-loss.md)
