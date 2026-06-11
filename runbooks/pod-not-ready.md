# Pod Not Ready

!!! warning "Severity: Warning → Critical"
    **Target response: 15 min warning, 5 min critical (>50% of replicas).** A pod is failing its readiness probe, removing it from the Service's endpoint list. Capacity drops without a full outage.

## What this alert means

One or more pods are reporting `Ready=False` in their conditions — the readiness probe is failing. Kubernetes removes non-ready pods from Service endpoints, so traffic stops routing to them.

```promql
kube_pod_status_ready{condition="false", namespace="<ns>"} == 1
```

Sustained for 5+ minutes per affected pod.

## Quick diagnostics

Three commands to run before reading further:

```bash
# WHERE: shell with kubectl context + jq. <namespace> and <pod>
#   are filled in by AM at alert time.
# WHAT: pod's status conditions as JSON. The 4 standard conditions
#   are PodScheduled, Initialized, ContainersReady, Ready —
#   evaluated top-down.
# READ: find the FIRST condition with Status=False — that's the
#   bottleneck:
#   PodScheduled=False → no node fits (resources/affinity/taints)
#   Initialized=False → init container failed
#   ContainersReady=False → one+ main containers not Ready
#   Ready=False (others True) → readiness probe failing
#   The Message field on the False condition narrows further.
kubectl get pod -n <namespace> <pod> -o jsonpath='{.status.conditions}' | jq
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: readiness probe config + recent failure events for the pod.
# READ: the Readiness: section shows the actual check kubelet
#   runs (httpGet path/port, exec command, tcpSocket). Below it,
#   events include lines like:
#     "Readiness probe failed: HTTP probe failed with statuscode: 503"
#   That's the EXACT response the probe got. Tells you whether
#   the probe config is wrong or the app's readiness logic is.
kubectl describe pod -n <namespace> <pod> | grep -A 10 "Readiness:"
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: last 200 log lines from the pod's main container. Runs
#   even when the pod is NotReady (the container is still alive,
#   just failing readiness checks).
# READ: look for errors right before the readiness probe failure
#   timestamps. Common patterns:
#     app started but can't reach a downstream dependency
#     a config error left a feature flag in a startup-blocking state
#     a health check endpoint expects DB connectivity that's missing
kubectl logs -n <namespace> <pod> --tail=200
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning (1 pod) | No | 15 min | Reduced capacity, no user impact |
| Critical (>50% of replicas) | Yes | 5 min | Likely user-facing — request failures |

## Diagnostic steps

### 1. Identify the affected pod(s)
```bash
kubectl get pods -n <namespace> -o wide | grep -v Running
```

### 2. Why is readiness failing?
```bash
kubectl describe pod -n <namespace> <pod-name>
```
The Events section at the bottom shows recent probe failures. Common patterns: `Readiness probe failed: HTTP probe failed with statuscode: 503` or `connection refused`.

### 3. Check pod logs for app-level signal
```bash
kubectl logs -n <namespace> <pod-name> --tail=200
```

### 4. Probe configuration sanity check
```bash
kubectl describe pod -n <namespace> <pod-name> | grep -A5 "Readiness:"
```
TODO: Check probe path, timeout, initial-delay are reasonable for this service's startup time.

## Common causes & fixes

### A. Slow startup beating initial-delay
| Symptom | Diagnosis | Fix |
|---|---|---|
| Probe fails repeatedly right after pod start, then succeeds | App needed more time than `initialDelaySeconds` allows | Increase `initialDelaySeconds`, OR convert to a `startupProbe` with looser thresholds |

### B. Dependency unreachable during readiness check
| Symptom | Diagnosis | Fix |
|---|---|---|
| Probe endpoint depends on DB/cache that's flaky | Readiness probe should check only what the pod itself needs, not deep transitive deps | Tighten the readiness check; move deep-dep verification to a separate `/health/deep` endpoint |

### C. Memory or CPU pressure
| Symptom | Diagnosis | Fix |
|---|---|---|
| Probe times out under load | `kubectl top pod` shows high resource use | See [High CPU Usage](high-cpu-usage.md) or [High Memory Usage](high-memory-usage.md) |

### D. TODO — additional cause category specific to your services

## Escalation

If unresolved within target response:

1. **Service owning team's on-call** — TODO
2. **Platform on-call** — `@platform-oncall`, PagerDuty service `mattermost-platform`.

## Post-incident

1. File a follow-up issue with root cause.
2. Update this runbook if a novel cause was hit.
3. Tune probe parameters if they fire too aggressively.

## Required Prometheus labels

The Quick diagnostics commands above use `<label>` placeholders that
Alertmanager fills in from each alert's labels at delivery time. For
this runbook to render copy-paste-runnable commands, your Prometheus
rule must emit:

- `namespace` — the Kubernetes namespace of the failing pod
- `pod` — the specific pod that isn't passing readiness

When a label is missing, the rendered command shows `<no value>` in
that slot — still readable, just not auto-runnable. Add the label to
your rule and reload Prometheus.

## Related runbooks

- [Deployment Replicas Unavailable](deployment-replicas-unavailable.md) — when many pods are not-ready
- [High CPU Usage](high-cpu-usage.md) / [High Memory Usage](high-memory-usage.md) — resource pressure causing probe timeouts
