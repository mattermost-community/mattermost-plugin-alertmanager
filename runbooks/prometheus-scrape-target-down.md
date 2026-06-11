# Prometheus Scrape Target Down

!!! warning "Severity: Warning"
    **Target response: 15 min.** Prometheus can't scrape a target. Any alert that depends on that target's metrics is now blind — silent failure.

## What this alert means

```promql
up{job="<job>"} == 0
```

Sustained 5+ minutes. Prometheus tried to scrape the target's `/metrics` endpoint and got an error (connection refused, timeout, HTTP 5xx).

Critically: alerts based on metrics from this target won't fire correctly while it's down — they'll either fire spuriously (if the absent metric is interpreted as zero) or fail to fire at all (if the alert query returns no data). Restoring the scrape target is also restoring observability.

## Quick diagnostics

Three commands to run before reading further:

```bash
# WHERE: shell with curl + jq. Set PROMETHEUS_URL or port-forward
#   to the Prometheus service first
#   (`kubectl port-forward -n monitoring svc/prometheus 9090:9090`).
# WHAT: queries Prometheus' targets API, filters to any target
#   whose health != "up", returns its labels and last scrape error.
# READ: each result has the failing target's labels (job, instance,
#   etc.) and lastError. Common errors:
#   "connection refused" → target not listening or wrong port
#   "no such host" → DNS / service discovery issue
#   "tls: handshake failure" → cert problem on the target
#   "server returned HTTP status 401" → auth failure
#   "context deadline exceeded" → target too slow (raise
#     scrape_timeout or fix the target's metrics handler)
curl -s http://prometheus:9090/api/v1/targets | jq '.data.activeTargets[] | select(.health!="up") | {labels: .labels, lastError: .lastError}'
```

```bash
# WHERE: shell with kubectl context set. <namespace> and <service>
#   are filled in by AM at alert time.
# WHAT: endpoints for the failing service.
# READ: ENDPOINTS column shows pod IP:port pairs backing the
#   service. <none> = the selector doesn't match any pod, or no
#   pods are Ready. Prometheus can't scrape what doesn't have
#   endpoints — fix the pods first, not Prometheus.
kubectl get endpoints -n <namespace> <service> -o wide
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: find the ServiceMonitor (Prometheus Operator CRD) that
#   targets this service. Filters all SMs to those whose YAML
#   mentions the service name.
# READ: confirm the scrape config wires the SM to the right
#   service + port. Look for:
#     matchLabels matching service labels
#     endpoints[].port matching the named port on the service
#     endpoints[].path matching the metrics endpoint
#   Common regression: renaming a service port without updating
#   the SM. The SM still exists but points at a port name that
#   no longer resolves.
kubectl get servicemonitor -A -o yaml | grep -B 5 -A 10 "<service>"
```

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

## Required Prometheus labels

The Quick diagnostics commands above use `<label>` placeholders that
Alertmanager fills in from each alert's labels at delivery time. For
this runbook to render copy-paste-runnable commands, your Prometheus
rule must emit:

- `namespace` — the Kubernetes namespace of the failing scrape target
- `service` — the Kubernetes Service name backing the scrape target

When a label is missing, the rendered command shows `<no value>` in
that slot — still readable, just not auto-runnable. Add the label to
your rule and reload Prometheus.

## Related runbooks

- [Pod CrashLoopBackOff](pod-crashloopbackoff.md), [Pod Not Ready](pod-not-ready.md) — when target's pod is unhealthy
