# Pod Not Ready

!!! warning "Severity: Warning → Critical"
    **Target response: 15 min warning, 5 min critical (>50% of replicas).** A pod is failing its readiness probe, removing it from the Service's endpoint list. Capacity drops without a full outage.

## What this alert means

One or more pods are reporting `Ready=False` in their conditions — the readiness probe is failing. Kubernetes removes non-ready pods from Service endpoints, so traffic stops routing to them.

```promql
kube_pod_status_ready{condition="false", namespace="<ns>"} == 1
```

Sustained for 5+ minutes per affected pod.

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

## Related runbooks

- [Deployment Replicas Unavailable](deployment-replicas-unavailable.md) — when many pods are not-ready
- [High CPU Usage](high-cpu-usage.md) / [High Memory Usage](high-memory-usage.md) — resource pressure causing probe timeouts
