# Mattermost High CPU

!!! warning "Severity: Warning"
    **Target response: 15 minutes.**
    The mattermost container has used more than 85% of its CPU limit for
    ten minutes. Past the limit the kernel throttles it, and API latency
    climbs for every user on that pod.

## What this alert means

```promql
sum by (namespace, pod) (
  rate(container_cpu_usage_seconds_total{namespace="<namespace>", pod=~"<installation_mattermost_com_installation>-.*", container="mattermost"}[5m])
)
/
sum by (namespace, pod) (
  kube_pod_container_resource_limits{namespace="<namespace>", pod=~"<installation_mattermost_com_installation>-.*", container="mattermost", resource="cpu"} > 0
) > 0.85
```

The threshold is `mm-cpu-limit-percent` on `config add` or `format rules`
(default 85). A container with no CPU limit never fires this
alert; [CPU Limit Missing](mattermost-cpu-limit-missing.md) covers it.

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: live CPU use of every pod in the namespace.
# READ: compare the alerting pod with the other Mattermost pods. One pod
#   far above the rest = something on that pod (a long job, a hot channel,
#   a plugin). Every pod high = the install needs more CPU or replicas.
kubectl top pod -n <namespace> --containers
```

```promql
# WHERE: Prometheus or Grafana Explore.
# WHAT: slowest API handlers on the installation right now.
# READ: one handler dominating points at the workload driving CPU, for
#   example search, file uploads or exports.
topk(5, sum by (handler) (rate(mattermost_api_time_sum{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"}[5m])))
```

## Diagnostic steps

### 1. One pod or all pods?

Compare the pods in the first quick diagnostic. A single hot pod usually
runs a job: the job server runs on every pod unless the operator splits it
out.

```bash
kubectl logs -n <namespace> <pod> -c mattermost --since=15m | grep -iE '"job|worker|reindex|export' | tail -20
```

### 2. Is the pod being throttled?

```promql
rate(container_cpu_cfs_throttled_periods_total{namespace="<namespace>", pod="<pod>", container="mattermost"}[5m])
/
rate(container_cpu_cfs_periods_total{namespace="<namespace>", pod="<pod>", container="mattermost"}[5m])
```

Above 0.25 means the container is waiting on its limit a quarter of the
time. See [CPU Throttling High](../cpu-throttling-high.md).

### 3. Did load change?

```promql
sum(rate(mattermost_api_time_count{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"}[5m]))
```

A step up in requests with no change in users points at an integration or
bot hammering the API.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| One pod hot, job logs | Bleve or Elasticsearch reindex, data export, compliance export | Let it finish, or schedule it off-hours |
| All pods hot, requests up | Real growth | Raise `spec.replicas`, or `spec.scheduling.resources.limits.cpu` |
| All pods hot, requests flat | Plugin or slow database query holding goroutines | Check [High HTTP Error Rate](mattermost-high-http-error-rate.md) and database latency |
| One integration dominates requests | Bot polling the API | Rate limit it (`RateLimitSettings`) or fix the integration |

## Escalation

Platform on-call if latency is user-visible or the alert holds for an hour.

## Required Prometheus labels

`namespace` and `pod`, from cAdvisor and kube-state-metrics. The alert
does not carry the installation name the `mattermost_*` queries need: it
is the pod name without its last two dash-separated parts.

## Related runbooks

- [CPU Limit Missing](mattermost-cpu-limit-missing.md)
- [High Memory](mattermost-high-memory.md)
- [High CPU Usage](../high-cpu-usage.md)
