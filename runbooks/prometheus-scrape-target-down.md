# Prometheus Scrape Target Down

!!! warning "Severity: Warning"
    **Target response: 15 min.** Prometheus can't scrape a target. Any alert that depends on that target's metrics is now blind — silent failure.

## What this alert means

```promql
up{job="<job>"} == 0
```

Sustained 5+ minutes. Prometheus tried to scrape the target's `/metrics` endpoint and got an error (connection refused, timeout, HTTP 5xx).

Critically: alerts based on metrics from this target won't fire correctly while it's down — they'll either fire spuriously (if the absent metric is interpreted as zero) or fail to fire at all (if the alert query returns no data). Restoring the scrape target is also restoring observability.

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning | No | 15 min | Observability blind spot; downstream alerts unreliable |

## Diagnostic steps

### 1. Which target?
TODO — open Prometheus's targets UI: `<prometheus-url>/targets` and look for the failing one.

### 2. Why is it failing?
The targets page shows the error per endpoint: `connection refused`, `i/o timeout`, `server returned HTTP status 5xx`.

### 3. Is the target's pod actually serving metrics?
```bash
kubectl exec -n <namespace> <pod> -- curl -sv http://localhost:<metrics-port>/metrics | head -5
```

## Common causes & fixes

### A. Target pod is down
| Symptom | Fix |
|---|---|
| Pod is in CrashLoopBackOff or not-Ready | See [Pod CrashLoopBackOff](pod-crashloopbackoff.md) — restore the pod and the scrape comes back |

### B. Metrics endpoint disabled
| Symptom | Fix |
|---|---|
| Pod is healthy but `/metrics` returns 404 | App-side feature flag is off OR metrics port isn't exposed in the manifest |

### C. NetworkPolicy blocking Prometheus
| Symptom | Fix |
|---|---|
| Pod is healthy, metrics endpoint works locally, but Prometheus can't reach it | A new NetworkPolicy is blocking the scrape source. Add a policy allowing Prometheus to scrape. |

### D. Target port changed
| Symptom | Fix |
|---|---|
| Scrape config and pod manifest disagree on port number | Update the ServiceMonitor or scrape_configs |

## Escalation

1. **Service-owning team** — they own the target.
2. **Platform on-call** if Prometheus itself is misconfigured.

## Related runbooks

- [Pod CrashLoopBackOff](pod-crashloopbackoff.md), [Pod Not Ready](pod-not-ready.md) — when target's pod is unhealthy
