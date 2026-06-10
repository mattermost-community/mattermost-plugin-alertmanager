# Node Not Ready

!!! danger "Severity: Critical"
    **Target response: 5 min.** A Kubernetes node is reporting `Ready=False`. Pods on it may be evicted; scheduling stops.

## What this alert means

```promql
kube_node_status_condition{condition="Ready", status="false"} == 1
```

The kubelet on this node hasn't reported a healthy status for the past `node-monitor-grace-period` (default 40s). Common causes: kubelet crash, network partition, disk pressure, memory pressure, the node is fully down.

When `Ready=False` persists past `pod-eviction-timeout` (default 5m), Kubernetes starts evicting pods. They're rescheduled on other nodes if capacity exists.

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Critical | Yes | 5 min | Pods on the node potentially evicted; cluster capacity reduced |

## Diagnostic steps

### 1. Confirm the state
```bash
kubectl get nodes
kubectl describe node <node-name>
```
The Conditions section at the top shows `Ready`, `MemoryPressure`, `DiskPressure`, `PIDPressure`, `NetworkUnavailable`.

### 2. Why is the kubelet unhealthy?
```bash
# Open the cloud provider's console for the underlying VM
# Check: VM running state, recent maintenance events
# SSH to the node if possible: `journalctl -u kubelet -n 100`
```

### 3. Pods on the affected node
```bash
kubectl get pods --all-namespaces --field-selector spec.nodeName=<node-name>
```

### 4. Pressure conditions
```bash
kubectl describe node <node-name> | grep -A1 "MemoryPressure\|DiskPressure"
```

## Common causes & fixes

### A. Node-level resource pressure
| Symptom | Diagnosis | Fix |
|---|---|---|
| `MemoryPressure: True` or `DiskPressure: True` in Conditions | The node has run out of memory or disk | Drain the node so pods reschedule elsewhere: `kubectl drain <node-name> --ignore-daemonsets --delete-emptydir-data`. Investigate root cause (large logs filling disk, etc.) |

### B. Cloud-provider VM failure
| Symptom | Diagnosis | Fix |
|---|---|---|
| Provider console shows VM stopped, failed, or in maintenance | Hardware/hypervisor issue | If using a managed node pool with autoscaling: delete the failing node (provider replaces it). Otherwise: open a support ticket and replace manually. |

### C. Network partition
| Symptom | Diagnosis | Fix |
|---|---|---|
| Other nodes are healthy; this one is unreachable from cluster network | Node-level network or VPC issue | TODO — cloud-provider-specific fix |

### D. Kubelet crashed
| Symptom | Diagnosis | Fix |
|---|---|---|
| VM is up, but `kubectl describe node` shows kubelet not reporting | Kubelet process is dead but the host is fine | SSH if possible: `sudo systemctl restart kubelet`. If recurrent, look at kubelet logs for root cause. |

## Escalation

1. **Cloud team** — `@cloud-oncall`, PagerDuty `cloud-platform`. Node-level infra is theirs.
2. **Platform on-call** — if Mattermost capacity is affected, PagerDuty `mattermost-platform`.

## Post-incident

1. Postmortem if pods were evicted and caused user impact.
2. Review whether the cluster has spare capacity for an N-1 node loss. If not, scale up.

## Related runbooks

- [Deployment Replicas Unavailable](deployment-replicas-unavailable.md) — pods evicted from a not-ready node need re-scheduling capacity
- [Persistent Volume Full](persistent-volume-full.md) — disk pressure cause
