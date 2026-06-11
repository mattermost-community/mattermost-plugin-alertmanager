# Ingress High 5xx

!!! danger "Severity: Critical"
    **Target response: 5 min.** The cluster ingress / load balancer is returning 5xx responses at a high rate. User traffic is failing at the edge.

## What this alert means

```promql
sum(rate(nginx_ingress_controller_requests{status=~"5.."}[5m]))
/
sum(rate(nginx_ingress_controller_requests[5m]))
> 0.05
```

(Adjust metric names for your ingress controller — traefik, contour, ALB, etc.)

5xx at the ingress means the LB or ingress controller couldn't successfully proxy the request to an upstream pod. Different from app-level 5xx — this is the gateway saying "I couldn't reach a backend."

## Quick diagnostics

Three commands to run before reading further:

```bash
# Ingress controller logs — what's it complaining about?
kubectl logs -n ingress-nginx -l app.kubernetes.io/name=ingress-nginx --tail=200 | grep -E "[45][0-9]{2}"
```

```bash
# Do the upstream services even have endpoints?
kubectl get endpoints -A | grep -v "<none>"
```

```promql
# 5xx rate by ingress — narrow to the failing hostname
sum by (ingress) (rate(nginx_ingress_controller_requests{status=~"5.."}[5m]))
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Critical | Yes | 5 min | Edge-level failures — user-visible regardless of which service was the target |

## Diagnostic steps

### 1. Identify which backend
TODO — break the 5xx rate down by `ingress` / `service` label.

### 2. Are the backends healthy?
```bash
kubectl get endpoints -n <namespace> <service-name>
```

### 3. Ingress controller logs
TODO — `kubectl logs -n <ingress-namespace> <ingress-controller-pod>` and look for backend timeouts / connection refused.

### 4. Ingress controller resource pressure
TODO — `kubectl top pod -n <ingress-namespace>`. Is the controller itself CPU/memory starved?

## Common causes & fixes

### A. Backend service has no Ready endpoints
| Symptom | Fix |
|---|---|
| `kubectl get endpoints` returns empty | See [Service Endpoint Down](service-endpoint-down.md) |

### B. Backend slow → ingress timeouts
| Symptom | Fix |
|---|---|
| Ingress logs show "upstream timed out" | See [High API Latency](high-api-latency.md). Consider raising timeout temporarily. |

### C. Ingress controller resource exhaustion
| Symptom | Fix |
|---|---|
| Ingress pod itself OOMKilling or CPU-throttled | Scale the ingress deployment; tune limits |

### D. SSL/TLS handshake failures
| Symptom | Fix |
|---|---|
| 5xx with TLS errors in logs | See [Certificate Expiring Soon](certificate-expiring-soon.md) |

## Escalation

1. **Platform on-call** — `@platform-oncall`.
2. **Network team** — if LB-level (cloud provider's LB).

## Related runbooks

- [Service Endpoint Down](service-endpoint-down.md), [High HTTP 5xx Error Rate](high-http-error-rate.md), [Certificate Expiring Soon](certificate-expiring-soon.md)
