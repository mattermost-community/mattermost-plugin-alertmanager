# Mattermost High Error Rate

!!! danger "Severity: Warning → Critical at sustained levels"
    **Target response: 10 minutes for warning, 5 minutes if critical.**
    A 5xx rate above 5% means roughly 1 in 20 API requests is failing.
    Users see intermittent message send failures, channel load errors,
    and broken integrations.

## What this alert means

More than 5% of Mattermost API calls are returning 5xx status codes over
a 5-minute window. The alert fires on:

```promql
sum(rate(mattermost_api_calls_total{status_code=~"5.."}[5m]))
/
sum(rate(mattermost_api_calls_total[5m]))
> 0.05
```

A 5xx response means the server encountered an error it couldn't handle —
not a client-side mistake. Unlike 4xx (expected errors: bad auth, not
found), 5xx is always a signal of a server-side problem worth
investigating.

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: error-level log lines from the last 15 minutes.
# READ: the first recurring error type is your most likely cause.
#   Look for: 'panic', 'database', 'plugin', 'timeout'. A panic
#   means a goroutine crashed — the pod may restart soon if this
#   keeps happening. A database error means DB connectivity is broken.
kubectl logs -n <namespace> -l app=mattermost --since=15m \
  | grep -iE '"level":"error"|"level":"fatal"|panic' | tail -30
```

```promql
# WHERE: Prometheus or Grafana.
# WHAT: 5xx rate broken down by API handler.
# READ: the handler with the highest 5xx rate is your bottleneck.
#   Cross-reference with recent deployments.
sum by (handler) (
  rate(mattermost_api_calls_total{status_code=~"5.."}[5m])
)
```

```bash
# WHERE: shell with kubectl access.
# WHAT: pod crash/restart check.
# READ: if a pod has OOMKilled or restarted recently, the 5xx errors
#   are likely caused by that pod being unavailable during the restart.
kubectl get pods -n <namespace> -l app=mattermost \
  -o custom-columns="NAME:.metadata.name,RESTARTS:.status.containerStatuses[0].restartCount,STATUS:.status.phase"
```

## Diagnostic steps

### 1. Identify the failing handler

The `handler` label in `mattermost_api_calls_total` tells you which
endpoint is failing. Common 5xx-generating endpoints:

| Handler | Common cause |
|---------|-------------|
| `/api/v4/posts` | DB write failure, plugin error on post creation |
| `/api/v4/files` | File storage backend (S3/MinIO) unavailable |
| `/api/v4/users/login` | DB connectivity, LDAP server down |
| Any handler | Plugin panic in a request hook |

### 2. Check database connectivity

```bash
# Can Mattermost reach the DB?
kubectl exec -n <namespace> deploy/mattermost -- \
  sh -c "nc -z <db-host> 5432 && echo 'DB reachable' || echo 'DB UNREACHABLE'"
```

If the DB is unreachable, this is the root cause. Fix DB connectivity
first — all other checks are moot until the DB is back.

### 3. Check file storage backend

```bash
# Can Mattermost reach S3/MinIO?
kubectl exec -n <namespace> deploy/mattermost -- \
  sh -c "curl -s -o /dev/null -w '%{http_code}' http://<minio-host>:9000/minio/health/live"
```

5xx errors on file endpoints (`/api/v4/files`) with S3 unreachable = file
storage is the cause. Check your cloud provider's S3 status or MinIO
health.

### 4. Check for a misbehaving plugin

```bash
kubectl logs -n <namespace> -l app=mattermost --since=15m \
  | grep -E "plugin.*error|plugin.*panic|plugin_id" | tail -20
```

A plugin panicking on the request path causes 500s for all requests that
trigger it. Disable the suspect plugin:

```bash
mmctl plugin disable <plugin-id>
```

### 5. Check for a recent deployment

```bash
kubectl rollout history deployment -n <namespace> mattermost
```

If a rollout just completed and 5xx errors started immediately after, roll
back:

```bash
kubectl rollout undo deployment -n <namespace> mattermost
```

## Common causes & fixes

### A. Database unavailable

| Symptom | Fix |
|---------|-----|
| DB connectivity check fails | Restore DB connectivity; check DB pod health |

### B. Plugin panic

| Symptom | Fix |
|---------|-----|
| `plugin.*panic` in logs | Disable the plugin immediately |

### C. File storage backend down

| Symptom | Fix |
|---------|-----|
| 5xx on `/api/v4/files` | Restore S3/MinIO; check credentials and bucket existence |

### D. Bad deployment

| Symptom | Fix |
|---------|-----|
| 5xx started immediately after rollout | Roll back with `kubectl rollout undo` |

## Escalation

1. **Platform on-call** — `@platform-oncall` immediately if error rate
   is above 20% or if errors have been above 5% for more than 15 minutes.
2. **Database team** — if DB connectivity is the root cause.
3. **Mattermost vendor support** — if the cause is a bug in a Mattermost
   release. Open a P1 ticket at `support.mattermost.com`.

## Required Prometheus labels

`mattermost_api_calls_total` with a `status_code` label is exported by
Mattermost when metrics are enabled. No additional labels are required.

## Related runbooks

- [Mattermost API High Latency](mattermost-api-high-latency.md)
- [Service Endpoint Down](service-endpoint-down.md)
- [Database Connectivity Loss](database-connectivity-loss.md)
