# Mattermost WebSocket Drop

!!! warning "Severity: Warning"
    **Target response: 10 minutes.**
    Active WebSocket connections on this installation fell below half of
    their level ten minutes ago. Users stop getting new messages, typing
    indicators and unread badges until their client reconnects.

## What this alert means

```promql
sum by (namespace, installation_mattermost_com_installation) (
  mattermost_http_websockets_total{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"}
)
<
0.5 * sum by (namespace, installation_mattermost_com_installation) (
  mattermost_http_websockets_total{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"} offset 10m
)
```

Connections are summed across pods, so a pod that restarts under a new
name still counts as a drop. The drop must hold for five minutes, which
lets a normal rolling update recover first. The general runbook
[Mattermost WebSocket Drop](../mattermost-websocket-drop.md) covers the
same signal without the installation scope.

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: pod age and restarts for this installation.
# READ: a pod younger than about 15 minutes, or new restarts = connections
#   dropped with that pod. All pods old and steady = the drop is in front
#   of Mattermost: ingress, load balancer, or the network.
kubectl get pods -n <namespace> -l installation.mattermost.com/installation=<installation_mattermost_com_installation> \
  -o custom-columns='NAME:.metadata.name,START:.status.startTime,RESTARTS:.status.containerStatuses[0].restartCount,READY:.status.containerStatuses[0].ready'
```

```promql
# WHERE: Prometheus or Grafana Explore.
# WHAT: connections per pod over the last hour.
# READ: one pod falling to zero = that pod. Every pod falling together =
#   something shared, such as the ingress.
sum by (pod) (mattermost_http_websockets_total{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"})
```

## Diagnostic steps

### 1. Rollout?

```bash
kubectl rollout status deploy -n <namespace> <installation_mattermost_com_installation> --timeout=5s
```

A rollout in progress explains the drop. Silence the alert during planned
upgrades.

### 2. Ingress or load balancer

```bash
kubectl get ingress -n <namespace> -o yaml | grep -iE 'timeout|proxy-read|proxy-send'
kubectl logs -n ingress-nginx -l app.kubernetes.io/name=ingress-nginx --since=15m | grep -i websocket | tail -20
```

The ingress read timeout must be longer than the WebSocket ping interval.
An ingress controller restart also drops every connection at once.

### 3. Pod restarts

A restart drops every connection on that pod. Work
[Pod Crash Looping](mattermost-pod-crash-looping.md) or
[High Memory](mattermost-high-memory.md) if the pod was OOM-killed.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| New pod, rollout running | Planned rollout | Expected; silence during maintenance |
| New pod, no rollout | Restart or OOM kill | See Pod Crash Looping |
| All pods steady, ingress restarted | Ingress controller restart | Expected once; investigate if repeated |
| Drops on a fixed interval | Load balancer idle timeout | Raise the timeout to 3600s |

## Escalation

Platform on-call if connections do not recover within 15 minutes.

## Required Prometheus labels

`namespace` and `installation_mattermost_com_installation`.

## Related runbooks

- [Mattermost WebSocket Drop](../mattermost-websocket-drop.md)
- [Pod Crash Looping](mattermost-pod-crash-looping.md)
- [Ingress High 5xx](../ingress-high-5xx.md)
