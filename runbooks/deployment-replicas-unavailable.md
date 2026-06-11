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
# WHERE: shell with kubectl context set. <namespace> is filled in
#   by AM at alert time.
# WHAT: deployments in <namespace> showing READY (current/desired),
#   UP-TO-DATE (replicas at the latest revision), AVAILABLE
#   (replicas passing readiness for ≥MinReadySeconds), image.
# READ:
#   DESIRED > READY → you're hitting the alert.
#   DESIRED > UP-TO-DATE → a rollout is in flight.
#   READY = 0 with DESIRED > 0 → no pods can come up (bad image,
#     missing config, no fitting nodes).
#   AVAILABLE < READY for >30s → pods came up but aren't yet
#     passing readiness probes.
kubectl get deploy -n <namespace> -o wide
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: pod conditions for the failing app's pods. Conditions
#   are PodScheduled, Initialized, ContainersReady, Ready.
# READ: find the first condition with Status=False — that's
#   the bottleneck:
#     PodScheduled=False → no node fits (resources, affinity, taints)
#     Initialized=False → init container failed
#     ContainersReady=False → container probe failing
#     Ready=False (others True) → readiness probe failing
#   The Reason and Message under each False condition narrow further.
kubectl describe pod -n <namespace> -l app=<app> | grep -A 10 "Conditions:"
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: last 20 cluster events in <namespace>, time-sorted.
# READ: events surface reasons conditions don't show in detail.
#   Look for Warning type events:
#     FailedScheduling → no node has capacity
#     FailedMount → PVC didn't bind
#     BackOff → image pull or container crash
#     Unhealthy → probe failure (with the actual response code)
#     ErrImagePull → image not found or registry unreachable
kubectl get events -n <namespace> --sort-by='.lastTimestamp' | tail -20
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

## Required Prometheus labels

The Quick diagnostics commands above use `<label>` placeholders that
Alertmanager fills in from each alert's labels at delivery time. For
this runbook to render copy-paste-runnable commands, your Prometheus
rule must emit:

- `namespace` — the Kubernetes namespace of the failing deployment
- `app` — the application label of the failing deployment (typically
  the value of `app.kubernetes.io/name` or your team's app label
  convention)

When a label is missing, the rendered command shows `<no value>` in
that slot — still readable, just not auto-runnable. Add the label to
your rule and reload Prometheus.

## Related runbooks

- [Pod CrashLoopBackOff](pod-crashloopbackoff.md) — the most common cause
- [Pod Not Ready](pod-not-ready.md) — readiness flakiness reducing available count
- [Node Not Ready](node-not-ready.md) — when scheduling fails because a node is gone
