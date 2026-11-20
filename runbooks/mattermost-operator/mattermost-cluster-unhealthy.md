# Mattermost Cluster Unhealthy

!!! warning "Severity: Warning"
    **Target response: 15 minutes.**
    A Mattermost pod's gossip health score has been above zero for ten
    minutes. Pods in a high availability install share cache invalidations
    and events over gossip. When it lags, users on different pods see
    different data: stale channel lists, missing messages until refresh.

## What this alert means

```promql
mattermost_cluster_cluster_health_score{namespace="<namespace>", installation_mattermost_com_installation="<installation>"} > 0
```

The score comes from the memberlist library Mattermost uses for gossip. 0
is healthy. Higher means the node is missing its gossip timing budget,
usually because it cannot reach its peers fast enough. A single-pod
install has no cluster and stays at 0.

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: cluster and gossip log lines from the alerting pod.
# READ: "failed to ping" or "suspect" naming another pod = this pod cannot
#   reach that peer on the gossip port. Repeated "node joined/left" = pods
#   restarting or flapping.
kubectl logs -n <namespace> <pod> -c mattermost --since=15m | grep -iE 'cluster|gossip|memberlist|suspect' | tail -30
```

```promql
# WHERE: Prometheus or Grafana Explore.
# WHAT: the score on every pod of the installation.
# READ: one pod high = that pod's network or CPU. Every pod high = the
#   network between pods.
mattermost_cluster_cluster_health_score{namespace="<namespace>"}
```

## Diagnostic steps

### 1. Can pods reach each other?

Gossip uses `ClusterSettings.GossipPort` (8074 by default) over TCP and UDP.

```bash
kubectl get networkpolicy -n <namespace> -o yaml | grep -B2 -A8 -E '8074|ports'
```

A NetworkPolicy that admits only 8065 between Mattermost pods breaks
gossip while the site keeps serving.

### 2. Is the pod starved for CPU?

A throttled pod misses gossip deadlines even with a healthy network. Check
[High CPU](mattermost-high-cpu.md).

### 3. Are pods flapping?

```bash
kubectl get pods -n <namespace> -l installation.mattermost.com/installation=<installation> -o wide
```

Pods that restart rejoin the cluster each time, which raises the score on
their peers.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| "failed to ping" a peer | NetworkPolicy or security group blocks 8074 | Allow TCP and UDP 8074 between Mattermost pods |
| One pod high, CPU throttled | CPU starvation | Raise the CPU limit |
| Score high during a rollout | Pods joining and leaving | Expected; clears after the rollout |
| Pods on different nodes only | Node-to-node network | Check the CNI and MTU |

## Escalation

Platform on-call if users report inconsistent data, or the score stays
high after a rollout.

## Required Prometheus labels

`namespace`, `pod`, and `installation_mattermost_com_installation`.

## Related runbooks

- [High CPU](mattermost-high-cpu.md)
- [Pod Crash Looping](mattermost-pod-crash-looping.md)
