# Mattermost High HTTP Error Rate

!!! warning "Severity: Warning"
    **Target response: 10 minutes.**
    More than 5% of this installation's API requests are returning 5xx,
    with at least one request a second. Users see failed sends, channels
    that will not load, or broken integrations.

## What this alert means

```promql
(
  sum by (namespace, installation_mattermost_com_installation) (rate(mattermost_api_time_count{namespace="<namespace>", installation_mattermost_com_installation="<installation>", status_code=~"5.."}[5m]))
  /
  sum by (namespace, installation_mattermost_com_installation) (rate(mattermost_api_time_count{namespace="<namespace>", installation_mattermost_com_installation="<installation>"}[5m]))
) > 0.05
and
sum by (namespace, installation_mattermost_com_installation) (rate(mattermost_api_time_count{namespace="<namespace>", installation_mattermost_com_installation="<installation>"}[5m])) > 1
```

The thresholds are `prometheusRule.thresholds.httpErrorPercent` and
`httpMinRequestsPerSecond` on the Mattermost CR.

Only 5xx counts. Expired sessions (401), permission denials (403) and
clients polling deleted resources (404) do not fire this alert, so a page
means the server failed a request it should have served. WebSocket
upgrades are not in the denominator. This is the per-installation form of
[Mattermost High Error Rate](../mattermost-high-error-rate.md), with a
request-rate floor so a quiet installation cannot page on one failure.

## Quick diagnostics

```promql
# WHERE: Prometheus or Grafana Explore.
# WHAT: 5xx per second by status code for this installation.
# READ: 500 = an application error; read the logs. 502 or 504 = a
#   proxy in front of Mattermost timed out or lost the pod. 503 = the
#   server is shedding load or a dependency is down.
sum by (status_code) (rate(mattermost_api_time_count{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>", status_code=~"5.."}[5m]))
```

```promql
# WHERE: Prometheus or Grafana Explore.
# WHAT: the API handlers producing the 5xx.
# READ: one handler = one feature or integration. Many handlers = a
#   shared dependency (database, file store, a plugin).
topk(10, sum by (handler, status_code) (rate(mattermost_api_time_count{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>", status_code=~"5.."}[5m])))
```

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: error-level log lines from every pod of the installation.
# READ: the first recurring error is the likely cause.
kubectl logs -n <namespace> -l installation.mattermost.com/installation=<installation_mattermost_com_installation> -c mattermost --since=15m --prefix \
  | grep -E '"level":"error"' | tail -30
```

## Diagnostic steps

### 1. One handler or many?

Use the second quick diagnostic. One handler points at a feature, a
plugin hook or an integration calling it. Many handlers at once point at
something they all share, so go to step 2.

### 2. Find the dependency

Database errors in the logs point at [DB Writer Pool Empty](mattermost-db-writer-pool-empty.md).
File store errors point at S3 or Azure Blob credentials. A plugin name in
the stack points at that plugin.

### 3. Rule out a single bad pod

```promql
sum by (pod) (rate(mattermost_api_time_count{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>", status_code=~"5.."}[5m]))
```

One pod carrying most of the 5xx while the others are clean is a pod
problem, not a cluster-wide one. Check its restarts and its node before
touching shared dependencies.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| 5xx with `pq:` errors | Database | See [DB Writer Pool Empty](mattermost-db-writer-pool-empty.md) |
| 5xx on file endpoints | File store credentials or network | Check `FileSettings` and the bucket |
| 5xx from one pod only | Pod on a bad node or stuck after an OOM | Check `kubectl describe pod`; delete the pod so the ReplicaSet replaces it |
| 502/504 spike during a rollout | Ingress routing to terminating pods | Wait for the rollout; if it persists, check readiness probes and ingress timeouts |
| 5xx after an upgrade | Regression | Contact support. Don't just lower `spec.version`: once the new version migrated the schema, a downgrade needs the database backup taken before the upgrade |

## Escalation

1. **Platform on-call** for sustained 5xx.
2. **Plugin or integration owner** when one handler or plugin carries the errors.

## Required Prometheus labels

`namespace` and `installation_mattermost_com_installation`.

## Related runbooks

- [Mattermost High Error Rate](../mattermost-high-error-rate.md)
- [DB Writer Pool Empty](mattermost-db-writer-pool-empty.md)
- [High HTTP Error Rate](../high-http-error-rate.md)
