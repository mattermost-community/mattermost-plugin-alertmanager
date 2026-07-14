# Pods Unschedulable

!!! danger "Severity: warning"
    **Target response: 20m.** One or more pods are stuck `Pending` —
    the scheduler can't place them. Capacity, taints, affinity, or
    resource requests are blocking placement.

## What this alert means

The scheduler evaluated every node and none fit. Common blockers:
insufficient CPU/memory, node selectors/affinity with no matching node,
taints without tolerations, or unbound PVCs.

```promql
kube_pod_status_unschedulable == 1
```

Pending pods = capacity you asked for but didn't get. During a scale-up
or node loss, this is why the extra replicas never arrive.

## Quick diagnostics

```bash
# WHERE: shell with kubectl context set.
# WHAT: the scheduler's own reason for not placing the pod.
# READ: "Insufficient cpu/memory" = cluster is full. "didn't match node
#   selector/affinity" = placement rules too strict. "had untolerated
#   taint" = needs a toleration. "unbound PersistentVolumeClaim" = storage.
kubectl describe pod -n <namespace> <pod> | grep -A5 -i "Events\|FailedScheduling"
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: node allocatable vs requested — is the cluster actually full?
# READ: if every node's CPU/memory requests are near allocatable, you need
#   more nodes (or smaller requests). Free capacity present = it's a
#   placement-rule problem, not raw capacity.
kubectl describe nodes | grep -A5 "Allocated resources"
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: the pod's resource requests, nodeSelector, affinity, tolerations.
# READ: an oversized request (more than any node has), or a nodeSelector
#   for a label no node carries, pinpoints the blocker.
kubectl get pod -n <namespace> <pod> -o jsonpath='req={.spec.containers[0].resources.requests} nodeSelector={.spec.nodeSelector} tolerations={.spec.tolerations}{"\n"}'
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning | No — chat only | 20m | Requested capacity not delivered |

Escalate if the unschedulable pods are for a critical service scaling up
during an incident.

## Common causes & fixes

| Symptom | Diagnosis | Fix |
|---|---|---|
| Insufficient cpu/memory | node allocation full | Add nodes / lower requests |
| No matching node | nodeSelector/affinity | Relax the rule or label a node |
| Untolerated taint | describe event | Add toleration or untaint |
| Unbound PVC | pending PVC | Fix storage class / provisioner |

## Escalation

1. **Primary** — `@sre-oncall` in `#mm-incidents`.
2. **Cloud/platform** — for cluster-autoscaler / node-pool capacity.

## Required Prometheus labels

Diagnostics use `namespace`, `pod`. From `kube_pod_status_unschedulable`
(kube-state-metrics).

## Related runbooks

- [Node Not Ready](node-not-ready.md) — lost nodes shrink schedulable capacity.
- [Deployment Replicas Unavailable](deployment-replicas-unavailable.md) — the deployment-level view.
