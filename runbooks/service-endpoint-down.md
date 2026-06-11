# Service Endpoint Down

!!! danger "Severity: Critical"
    **Target response: 5 min.** A monitored service endpoint is not responding. Users can't reach it.

## What this alert means

A Prometheus blackbox probe or service-up metric reports 0 for a known endpoint:

```promql
probe_success{instance="<endpoint>"} == 0
```

OR the Service's selector is matching zero ready Pods:

```promql
kube_endpoint_address_available{namespace="<ns>", endpoint="<name>"} == 0
```

The endpoint is dark. Distinct from "high error rate" (where some requests work) — this is "all requests fail."

## Quick diagnostics

Three commands to run before reading further:

```bash
# Try hitting the service directly from inside the cluster
kubectl run -it --rm httptest --image=curlimages/curl --restart=Never -- curl -v http://$SERVICE.$NAMESPACE/healthz
```

```bash
# Does the service have ANY ready endpoints?
kubectl get endpoints -n $NAMESPACE $SERVICE
```

```bash
# Service details — selector, ports, type
kubectl describe svc -n $NAMESPACE $SERVICE
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Critical | Yes | 5 min | 100% failure for traffic destined to this endpoint |

## Diagnostic steps

### 1. Confirm with a direct probe
```bash
curl -sv -m 5 https://<endpoint>/health
```
TODO — what does a known-good health response look like?

### 2. Are any pods Ready behind the Service?
```bash
kubectl get endpoints -n <namespace> <service-name>
kubectl get pods -n <namespace> -l <selector-from-service>
```

### 3. Recent changes
TODO — Service selector changes, network policy changes, ingress reconfig.

## Common causes & fixes

### A. All pods crashlooping or not-ready
| Symptom | Diagnosis | Fix |
|---|---|---|
| `kubectl get endpoints` shows zero addresses | All matching pods are unhealthy | See [Pod CrashLoopBackOff](pod-crashloopbackoff.md) or [Pod Not Ready](pod-not-ready.md) |

### B. Service selector doesn't match any pods
| Symptom | Diagnosis | Fix |
|---|---|---|
| Pods exist with `Running, Ready`, but endpoints is empty | Recent change to Service `.spec.selector` or pod labels caused mismatch | Verify labels: `kubectl get pods -n <ns> --show-labels`. Restore the selector. |

### C. Ingress / load balancer misconfig
| Symptom | Diagnosis | Fix |
|---|---|---|
| Service is healthy but external probe fails | Issue is at the ingress/LB layer | TODO — check ingress resource, cloud LB health |

### D. TODO

## Escalation

1. Service owning team's on-call.
2. **Network/ingress team** if external probe fails but cluster service is healthy.

## Post-incident

1. Postmortem if user impact.
2. Update this runbook.

## Related runbooks

- [Pod CrashLoopBackOff](pod-crashloopbackoff.md), [Pod Not Ready](pod-not-ready.md) — when no pods are ready
- [Ingress High 5xx](ingress-high-5xx.md) — when LB is the issue
- [DNS Resolution Failure](dns-resolution-failure.md) — when probe fails on DNS
