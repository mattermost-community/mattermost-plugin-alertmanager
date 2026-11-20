# Mattermost High HTTP Error Rate

!!! warning "Severity: Warning"
    **Target response: 10 minutes.**
    More than 5% of this installation's HTTP requests are returning
    errors, with at least one request a second. Users see failed sends,
    channels that will not load, or broken integrations.

## What this alert means

```promql
(
  sum by (namespace, installation_mattermost_com_installation) (rate(mattermost_http_errors_total{namespace="<namespace>", installation_mattermost_com_installation="<installation>"}[5m]))
  /
  sum by (namespace, installation_mattermost_com_installation) (rate(mattermost_http_requests_total{namespace="<namespace>", installation_mattermost_com_installation="<installation>"}[5m]))
) > 0.05
and
sum by (namespace, installation_mattermost_com_installation) (rate(mattermost_http_requests_total{namespace="<namespace>", installation_mattermost_com_installation="<installation>"}[5m])) > 1
```

The thresholds are `prometheusRule.thresholds.httpErrorPercent` and
`httpMinRequestsPerSecond` on the Mattermost CR.

**`mattermost_http_errors_total` counts 4xx too.** The server increments it
for every request that ends in an application error, so 401, 403 and 404
count the same as 500. The first job is to find out which you have. This
is the main difference from [Mattermost High Error Rate](../mattermost-high-error-rate.md),
which only counts 5xx.

## Quick diagnostics

```promql
# WHERE: Prometheus or Grafana Explore.
# WHAT: requests per second by status code for this installation.
# READ: mostly 5xx = the server is failing; open the runbook. Mostly
#   401 or 403 = clients with expired tokens or a misconfigured
#   integration. Mostly 404 = a client polling something deleted.
sum by (status_code) (rate(mattermost_api_time_count{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>", status_code!~"2.."}[5m]))
```

```promql
# WHERE: Prometheus or Grafana Explore.
# WHAT: the API handlers producing the errors.
# READ: one handler = one feature or integration. Many handlers with 5xx
#   = a shared dependency (database, file store, a plugin).
topk(10, sum by (handler, status_code) (rate(mattermost_api_time_count{namespace="<namespace>", status_code!~"2.."}[5m])))
```

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: error-level log lines from every pod of the installation.
# READ: the first recurring error is the likely cause.
kubectl logs -n <namespace> -l installation.mattermost.com/installation=<installation_mattermost_com_installation> -c mattermost --since=15m --prefix \
  | grep -E '"level":"error"' | tail -30
```

## Diagnostic steps

### 1. Client errors or server errors?

Use the first quick diagnostic. If 4xx dominate, go to step 2. If 5xx
dominate, go to step 3.

### 2. 4xx: find the client

```bash
kubectl logs -n <namespace> -l installation.mattermost.com/installation=<installation> -c mattermost --since=15m \
  | grep -E '"status_code":"?4' | grep -oE '"(user_id|ip_addr|path)":"[^"]*"' | sort | uniq -c | sort -rn | head -20
```

A single IP or user behind most errors is a bot or integration with a bad
token. Fix or revoke it. Nothing on the server needs to change.

### 3. 5xx: find the dependency

Database errors in the logs point at [DB Writer Pool Empty](mattermost-db-writer-pool-empty.md).
File store errors point at S3 or Azure Blob credentials. A plugin name in
the stack points at that plugin.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| Mostly 401 | Expired session or token in an integration | Rotate the token |
| Mostly 404 from one client | Client polling a deleted resource | Fix the client |
| 5xx with `pq:` errors | Database | See [DB Writer Pool Empty](mattermost-db-writer-pool-empty.md) |
| 5xx on file endpoints | File store credentials or network | Check `FileSettings` and the bucket |
| 5xx after an upgrade | Regression | Contact support. Don't just lower `spec.version`: once the new version migrated the schema, a downgrade needs the database backup taken before the upgrade |

If 4xx noise keeps paging, raise `httpErrorPercent` on the CR.

## Escalation

1. **Platform on-call** for sustained 5xx.
2. **Integration owner** for 4xx traced to one client.

## Required Prometheus labels

`namespace` and `installation_mattermost_com_installation`.

## Related runbooks

- [Mattermost High Error Rate](../mattermost-high-error-rate.md)
- [DB Writer Pool Empty](mattermost-db-writer-pool-empty.md)
- [High HTTP Error Rate](../high-http-error-rate.md)
