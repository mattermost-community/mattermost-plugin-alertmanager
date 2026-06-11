# Deployment Replicas Unavailable

!!! danger "Severity: Critical"
    **Target response: 5 min.** A Deployment has fewer Ready replicas than its spec calls for. Capacity is reduced; if no replicas are Ready, full outage.

## What this alert means

```promql
kube_deployment_status_replicas_available{namespace="<ns>"}
  <
kube_deployment_spec_replicas{namespace="<ns>"}
```

Sustained for 10+ minutes. The number of pods Kubernetes thinks are healthy and serving is less than what the manifest asks for.

This is usually a downstream effect of pods crashlooping, failing readiness, or the cluster being unable to schedule them. The interesting work is finding out which.

## Quick diagnostics

Three commands to run before reading further:

```bash
# What's the deployment showing? Compare DESIRED vs AVAILABLE
kubectl get deploy -n $NAMESPACE -o wide
```

```bash
# Find pods that aren't ready and why
kubectl describe pod -n $NAMESPACE -l app=$APP | grep -A 10 "Conditions:"
```

```bash
# Recent cluster events for this namespace
kubectl get events -n $NAMESPACE --sort-by='.lastTimestamp' | tail -20
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Critical | Yes | 5 min | Capacity loss. If 0 replicas: full outage. |

## Diagnostic steps

### 1. Which deployment, how many?
```bash
kubectl get deployment -n <namespace> <deployment-name>
```
Read the `READY` column (e.g., `1/3` means 1 of 3 expected are ready).

### 2. Pod-level state
```bash
kubectl get pods -n <namespace> -l app=<app-label> -o wide
```
Look for pods in `Pending`, `CrashLoopBackOff`, `Error`, or `Terminating` states.

### 3. Recent rollout history
```bash
kubectl rollout history deployment -n <namespace> <deployment-name>
kubectl rollout status deployment -n <namespace> <deployment-name>
```

### 4. Scheduling issues
```bash
kubectl get events -n <namespace> --sort-by='.lastTimestamp' | grep -iE "FailedScheduling|FailedCreate"
```

## Common causes & fixes

### A. Bad deploy mid-rollout
| Symptom | Diagnosis | Fix |
|---|---|---|
| New replicas fail to come up; old ones being terminated as part of rolling update | `kubectl rollout history` shows recent change | `kubectl rollout undo deployment -n <ns> <name>` |

### B. Pods crashlooping
| Symptom | Diagnosis | Fix |
|---|---|---|
| Pods cycling between Running and CrashLoopBackOff | See [Pod CrashLoopBackOff](pod-crashloopbackoff.md) | Address per that runbook |

### C. Cluster can't schedule new pods
| Symptom | Diagnosis | Fix |
|---|---|---|
| Pods stuck in Pending; events show `FailedScheduling: 0/N nodes available` | Node capacity exhausted or selector mismatch | Scale the node pool, OR fix node-selector/affinity rules |

### D. Image pull failures
| Symptom | Diagnosis | Fix |
|---|---|---|
| New pods stuck in `ImagePullBackOff` | Registry credential expired, or wrong image tag | Update pull secret; verify image tag exists |

## Escalation

1. **Platform on-call** — `@platform-oncall`, PagerDuty `mattermost-platform`.
2. **Cloud team** — if node-scheduling related, PagerDuty `cloud-platform`.

## Post-incident

1. Postmortem if the deployment serves user-facing traffic.
2. Review the deployment's `maxUnavailable` setting — was it too permissive?
3. Update this runbook if cause was novel.

## Related runbooks

- [Pod CrashLoopBackOff](pod-crashloopbackoff.md) — the most common cause
- [Pod Not Ready](pod-not-ready.md) — readiness flakiness reducing available count
- [Node Not Ready](node-not-ready.md) — when scheduling fails because a node is gone
