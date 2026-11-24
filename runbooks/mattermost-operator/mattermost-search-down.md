# Mattermost Search Down

!!! warning "Severity: Warning"
    **Target response: 30 minutes.**
    Elasticsearch or OpenSearch is enabled for this installation and has
    been unhealthy for five minutes. Search returns errors or falls back
    to slower database search, and new posts are not indexed.

## What this alert means

```promql
mattermost_search_engine_status{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"} == 0
```

The gauge is 1 when the search engine is healthy or not configured, and 0
only when it is enabled and unhealthy. An installation without
Elasticsearch never fires.

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: search engine errors from the alerting pod.
# READ: "connection refused" or "no such host" = the cluster is down or
#   moved. 401 or 403 = credentials. "cluster_block_exception" = the
#   cluster went read-only, usually because a disk is full.
kubectl logs -n <namespace> <pod> -c mattermost --since=15m | grep -iE 'elasticsearch|opensearch|search engine' | tail -20
```

```bash
# WHERE: any shell that can reach the search cluster.
# WHAT: the cluster's own health.
# READ: red = shards unassigned, search is failing. yellow = replicas
#   missing, search still works. Unreachable = network or the cluster.
#   curl prompts for the password, so it stays out of shell history.
curl -s -u "<es-user>" "https://<es-host>:9200/_cluster/health?pretty"
```

## Diagnostic steps

### 1. Is the cluster up?

Use the second quick diagnostic. For a managed service (Amazon OpenSearch,
Elastic Cloud), also check the provider's console.

### 2. Is the cluster read-only?

```bash
curl -s -u "<es-user>" "https://<es-host>:9200/_cat/allocation?v"
```

A data node above the flood-stage watermark (95% disk by default) makes
every index read-only.

### 3. Did the settings change?

Check `ElasticsearchSettings.ConnectionURL`, username and password in the
System Console. A rotated password breaks the connection without any
other symptom.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| Connection refused | Cluster down | Restore the cluster; Mattermost reconnects on its own |
| 401 / 403 | Credentials rotated | Update `ElasticsearchSettings.Password` |
| `cluster_block_exception` | Disk at flood stage | Free disk, then clear `index.blocks.read_only_allow_delete` |
| Healthy cluster, still alerting | Index missing after restore | Run a bulk index from System Console → Elasticsearch |

After the cluster recovers, posts written during the outage may be
missing from search. Run a bulk index if users report missing results.

## Escalation

Search platform owner if the cluster is red for more than 30 minutes.

## Required Prometheus labels

`namespace`, `pod`, and `installation_mattermost_com_installation`.

## Related runbooks

- [Persistent Volume Full](../persistent-volume-full.md)
- [Service Endpoint Down](../service-endpoint-down.md)
