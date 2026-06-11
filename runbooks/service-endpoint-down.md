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
# WHERE: shell with kubectl context set. <namespace> and <service>
#   are filled in by AM at alert time. Spins up an ephemeral pod
#   with curl baked in — works regardless of whether your own
#   workload images have curl.
# WHAT: hit the service's /healthz endpoint from INSIDE the
#   cluster (same network the service serves).
# READ:
#   200 → service IS reachable from inside; alert may be stale
#     or you're testing the wrong path. Try the actual app path.
#   Connection refused → service has no Ready endpoints (next
#     command will confirm).
#   Timeout → service exists but its handler is hung (alive but
#     non-functional). Check the backing pod's logs.
kubectl run -it --rm httptest --image=curlimages/curl --restart=Never -- curl -v http://<service>.<namespace>/healthz
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: endpoints for the failing service. Subsets[].Addresses
#   lists Ready pod IPs that receive traffic.
# READ: empty Addresses → no Ready pods backing the service.
#   That's why the probe fails. Pivot to:
#     pod-not-ready runbook (pods exist but failing readiness), or
#     pod-crashloopbackoff (pods restart-looping)
#   NotReadyAddresses also worth scanning — pods exist but
#   haven't passed readiness yet (slow startup, dependency wait).
kubectl get endpoints -n <namespace> <service>
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: service spec details — selector, ports, type.
# READ: confirm:
#   Selector matches actual pod labels (#1 cause of phantom-empty
#     endpoints — labels changed without updating the service)
#   Type=ClusterIP → ClusterIP must be routable from the test pod
#   Type=LoadBalancer → check ExternalIP. <pending> = cloud
#     provider hasn't provisioned the LB yet (look at events).
#   Ports[].targetPort matches a port the pod actually listens on
kubectl describe svc -n <namespace> <service>
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

## Required Prometheus labels

The Quick diagnostics commands above use `<label>` placeholders that
Alertmanager fills in from each alert's labels at delivery time. For
this runbook to render copy-paste-runnable commands, your Prometheus
rule must emit:

- `namespace` — the Kubernetes namespace of the failing Service
- `service` — the Kubernetes Service name (typically matches the
  Service resource's `metadata.name`)

When a label is missing, the rendered command shows `<no value>` in
that slot — still readable, just not auto-runnable. Add the label to
your rule and reload Prometheus.

## Related runbooks

- [Pod CrashLoopBackOff](pod-crashloopbackoff.md), [Pod Not Ready](pod-not-ready.md) — when no pods are ready
- [Ingress High 5xx](ingress-high-5xx.md) — when LB is the issue
- [DNS Resolution Failure](dns-resolution-failure.md) — when probe fails on DNS
