# Node Not Ready

!!! danger "Severity: Critical"
    **Target response: 5 min.** A Kubernetes node is reporting `Ready=False`. Pods on it may be evicted; scheduling stops.

## What this alert means

```promql
kube_node_status_condition{condition="Ready", status="false"} == 1
```

The kubelet on this node hasn't reported a healthy status for the past `node-monitor-grace-period` (default 40s). Common causes: kubelet crash, network partition, disk pressure, memory pressure, the node is fully down.

When `Ready=False` persists past `pod-eviction-timeout` (default 5m), Kubernetes starts evicting pods. They're rescheduled on other nodes if capacity exists.

## Quick diagnostics

Three commands to run before reading further:

```bash
# WHERE: shell with kubectl context set. <node> is filled in by AM
#   at alert time.
# WHAT: full description of the affected node, filtered to the
#   Conditions block (status of Ready, DiskPressure, MemoryPressure,
#   PIDPressure, NetworkUnavailable).
# READ: conditions to watch:
#   Ready=False → the trigger; the Message names the underlying cause
#   DiskPressure=True → node rejects new pods due to disk fullness
#   MemoryPressure=True → similar, memory
#   PIDPressure=True → out of process IDs (rare but real)
#   NetworkUnavailable=True → CNI failure
#   LastHeartbeatTime shows how long since kubelet last reported in.
kubectl describe node <node> | grep -A 20 "Conditions:"
```

```bash
# WHERE: SSH onto the affected node, OR
#   `kubectl debug node/<node> -it --image=ubuntu` and chroot
#   into /host. journalctl needs root on the host.
# WHAT: last 10 minutes of kubelet's systemd journal logs.
# READ: error/warning lines you'll see:
#   "container runtime is down" → containerd/docker dead, restart it
#   "Failed to talk to apiserver" → control-plane connectivity
#   "evicting pod" → node shedding pods due to pressure
#   "CSI driver" errors → storage plugin issue
#   "NetworkPlugin cni failed" → CNI plugin failure
journalctl -u kubelet --since "10 minutes ago" --no-pager | tail -50
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: all nodes in the cluster with status, age, version, IPs.
# READ: cross-reference — is this isolated to one node or are
#   others Ready=Unknown / NotReady too? Multiple → control-
#   plane–to–node network is the issue, bigger incident.
#   Only this node → focus on its kubelet/runtime/host.
kubectl get nodes -o wide
```

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

## Required Prometheus labels

The Quick diagnostics commands above use `<label>` placeholders that
Alertmanager fills in from each alert's labels at delivery time. For
this runbook to render copy-paste-runnable commands, your Prometheus
rule must emit:

- `node` — the failing node name (e.g.,
  `ip-10-0-12-47.ec2.internal`, `node-pool-prod-abc123`)

When a label is missing, the rendered command shows `<no value>` in
that slot — still readable, just not auto-runnable. Add the label to
your rule and reload Prometheus.

## Related runbooks

- [Deployment Replicas Unavailable](deployment-replicas-unavailable.md) — pods evicted from a not-ready node need re-scheduling capacity
- [Persistent Volume Full](persistent-volume-full.md) — disk pressure cause
