# Database Connectivity Loss

!!! danger "Severity: Critical"
    **Target response: 5 min.** The application can't reach its
    database. Writes (post, login, file upload) fail server-wide.
    Reads may or may not work depending on replica config.

## What this alert means

The application reports zero active database connections while its
pods are still healthy enough to be scraped by Prometheus. The alert
fires when:

```promql
mattermost_db_master_connections_total{namespace="mattermost"} == 0
  and on (namespace, pod)
up{namespace="mattermost", job=~"mattermost.*"} == 1
```

The `and on (namespace, pod) up == 1` clause means: only fire when
the app pod itself is healthy. If the whole pod is down, a different
alert (`PodNotReady` / scrape target down) is the right one.

Connection loss has several causes — DB outage, credential rotation,
network policy change, connection pool exhaustion. Diagnosing
narrows from "is the DB up?" to "is the network path clean?" to
"are the credentials still valid?"

## Quick diagnostics

Three commands to run before reading further:

```bash
# Is the DB actually accepting connections?
kubectl exec -n db deploy/postgres -- pg_isready -U $PGUSER
```

```bash
# Pod status — restarts, age, readiness
kubectl get pods -n db -l app=postgres -o wide
```

```bash
# How many active connections (vs max_connections)?
kubectl exec -n db deploy/postgres -- psql -c "SELECT count(*) FROM pg_stat_activity"
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Critical | Yes | 5 min | All writes fail server-wide. Logged-in users see errors on any state-changing action. |

## Diagnostic steps

### 1. Confirm the DB itself is up

If it's a managed DB (RDS, Cloud SQL, Azure DB):

```bash
# Open the provider console — look for the instance status
# Look for: maintenance windows, automated failovers, alarms on the DB itself
```

If it's a self-hosted DB on the same cluster:

```bash
kubectl get pods -n <db-namespace>
kubectl logs -n <db-namespace> <db-pod> --tail=200
```

A DB that's down is its own alert and its own runbook — escalate to the DB team if so.

### 2. Test connectivity from the app pod

```bash
kubectl exec -n mattermost deploy/<mattermost-deployment> -- \
  sh -c 'nc -zv $MM_SQLSETTINGS_DATASOURCE_HOST 5432'
```

Three possible outcomes:

- **`succeeded`** — L4 reachable. Issue is application-layer (credentials, pool exhaustion).
- **`refused`** — DB host is up but rejecting connections. Likely listener config or firewall.
- **`timeout`** — Network path is broken. NetworkPolicy or routing issue.

### 3. Check for recent credential rotation

```bash
# Recent change to the DB credential secret?
kubectl get secret -n mattermost <db-secret-name> -o jsonpath='{.metadata.resourceVersion}'

# When did the deployment last restart?
kubectl get pods -n mattermost -l app=mattermost \
  -o custom-columns='POD:.metadata.name,STARTED:.status.startTime'
```

If the secret's resource version is more recent than the pod's start time, credentials were rotated but the app didn't restart to pick them up. The app is still trying the old credentials.

### 4. Check NetworkPolicy changes

```bash
# Any recent NetworkPolicy changes in the mattermost or db namespace?
kubectl get networkpolicy -n mattermost
kubectl get networkpolicy -n <db-namespace>

# Recent NetworkPolicy events:
kubectl get events -n mattermost --field-selector reason=NetworkPolicyUpdated \
  --sort-by='.lastTimestamp' | tail -10
```

If a NetworkPolicy was applied recently, it might be blocking egress to the DB.

### 5. Inspect connection pool state

```bash
# Mattermost-specific metric: master pool state
curl -sS http://<mattermost-pod>:8067/metrics | grep mattermost_db_master
```

Look for `mattermost_db_master_connections_total` and
`mattermost_db_master_connection_attempts_total` over time. If
attempts are climbing but `connections_total` stays at zero, the app
is trying and failing.

## Common causes & fixes

### A. DB failover or maintenance window

| Symptom | Diagnosis | Fix |
|---|---|---|
| Connectivity loss correlates with DB provider's maintenance window or visible failover event | Provider console shows recent state change; might be ongoing | Wait for failover to complete; app should auto-reconnect once the new endpoint is reachable. If app doesn't reconnect: `kubectl rollout restart deployment -n mattermost mattermost` |

### B. Credentials rotated without app restart

| Symptom | Diagnosis | Fix |
|---|---|---|
| App logs show `password authentication failed`; secret resource version is more recent than pod start | The new password isn't loaded by the running app | `kubectl rollout restart deployment -n mattermost mattermost` |

### C. NetworkPolicy / firewall change blocking egress

| Symptom | Diagnosis | Fix |
|---|---|---|
| `nc -zv $DB_HOST $DB_PORT` times out; recent NetworkPolicy events in either namespace | A new or modified NetworkPolicy is dropping the egress packets | Identify the offending policy: `kubectl get networkpolicy -n mattermost -o yaml`. If recent, revert it. Add an explicit egress allow rule for the DB host/port if needed. |

### D. Connection pool exhaustion

| Symptom | Diagnosis | Fix |
|---|---|---|
| `nc -zv` succeeds; logs show "connection pool exhausted" or "too many connections"; `mattermost_db_master_connections_total` is at its configured max | The app is using all its pool slots and queueing requests | Short-term: increase `MM_SQLSETTINGS_MAXOPENCONNS`. Long-term: investigate why the app is holding connections — slow queries, missing transaction commits, connection leaks |

### E. DB at max_connections globally

| Symptom | Diagnosis | Fix |
|---|---|---|
| Multiple unrelated apps lose DB connectivity at the same time | The DB itself is at its `max_connections` cap (Postgres default: 100) | Identify connection-hogging clients. Increase `max_connections` if all clients are legitimate. Consider PgBouncer or RDS Proxy for connection multiplexing. |

### F. DNS resolution failure for DB hostname

| Symptom | Diagnosis | Fix |
|---|---|---|
| Logs show "no such host" or "name resolution failed"; nc reports the same | CoreDNS in the cluster is failing for this name | See [DNS Resolution Failure](dns-resolution-failure.md) |

### G. TLS handshake failure (rare)

| Symptom | Diagnosis | Fix |
|---|---|---|
| Logs show "TLS handshake error" or "x509: certificate has expired" | The DB requires TLS and the cert chain on the app side is broken | Refresh the CA bundle in the app's secret/configmap; redeploy |

## Escalation

If unresolved within 5 minutes:

1. **Database team** — `@dba-oncall` in `#mm-incidents`. PagerDuty service: `data-platform`. The DB team owns DB-side issues (capacity, failover, slow queries).
2. **Platform on-call** — `@platform-oncall`. PagerDuty service: `mattermost-platform`. Owns the app-side state (credentials, pool config, restart authority).
3. **Network team** — if NetworkPolicy or routing issue. PagerDuty service: `network-ops`.

**Severity ladder:**

| Time elapsed | Action |
|---|---|
| 0–5 min | Primary on-call works the alert. If DB itself is firing alarms, the DB team owns it. |
| 5–15 min | Page secondary. Declare incident if Mattermost is fully unusable. |
| 15+ min | Incident commander engaged. Public status page update. |

## Post-incident

1. **Postmortem** — a database outage is a Tier 1 incident. Full
   timeline, impact assessment, contributing factors, action items.
2. **Update this runbook** if the cause category was novel.
3. **Process changes** — did the DB team notify the app team about
   the upcoming maintenance window? Was the credential rotation
   coordinated? Was the NetworkPolicy change reviewed?
4. **Resilience changes** — should the app retry harder on
   connection loss? Should there be a circuit-breaker before
   user-visible errors? Should reads route to a replica during master
   outages?

## Related runbooks

- [Database High Latency](database-high-latency.md) — when DB is reachable but slow
- [Database Replication Lag](database-replication-lag.md) — when replica reads diverge
- [DNS Resolution Failure](dns-resolution-failure.md) — when cluster DNS breaks
- [High HTTP 5xx Error Rate](high-http-error-rate.md) — the downstream effect of DB loss

## Appendix: useful PromQL queries

Connection count over time per pod:

```promql
mattermost_db_master_connections_total{namespace="mattermost"}
```

Failed connection attempts (Postgres-side):

```promql
rate(pg_stat_database_conflicts_total[5m])
```

Time since last successful connection (per pod):

```promql
time() - mattermost_db_master_last_successful_connection_timestamp
```
