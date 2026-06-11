# DNS Resolution Failure

!!! danger "Severity: Critical"
    **Target response: 5 min.** Cluster DNS is failing to resolve names. Symptom: services can't talk to each other, even when both are healthy.

## What this alert means

```promql
rate(coredns_dns_responses_total{rcode=~"SERVFAIL|REFUSED"}[5m]) > 1
```

CoreDNS (or your cluster DNS) is returning errors at a non-trivial rate. Services that look up other services by name (the K8s normal case) start failing with "no such host" errors.

DNS failures present as connectivity loss but the underlying network is fine — it's name resolution that's broken.

## Quick diagnostics

Three commands to run before reading further:

```bash
# Can a pod resolve kubernetes.default at all?
kubectl run -it --rm dnstest --image=busybox --restart=Never -- nslookup kubernetes.default
```

```bash
# CoreDNS pod status
kubectl get pods -n kube-system -l k8s-app=kube-dns -o wide
```

```bash
# Recent CoreDNS errors
kubectl logs -n kube-system -l k8s-app=kube-dns --tail=100 | grep -iE "error|fail|refused"
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Critical | Yes | 5 min | Cluster-wide — anything that resolves names |

## Diagnostic steps

### 1. Confirm with a test resolution from a pod
```bash
kubectl run -i --rm --restart=Never dnstest --image=busybox -- \
  nslookup kubernetes.default.svc.cluster.local
```

### 2. CoreDNS pod health
```bash
kubectl get pods -n kube-system -l k8s-app=kube-dns
kubectl logs -n kube-system -l k8s-app=kube-dns --tail=100
```

### 3. CoreDNS configmap recently changed?
```bash
kubectl get configmap -n kube-system coredns -o yaml
kubectl get events -n kube-system --field-selector involvedObject.name=coredns | tail -10
```

### 4. NodeLocalDNS (if deployed)?
```bash
kubectl get pods -n kube-system -l k8s-app=node-local-dns
```

## Common causes & fixes

### A. CoreDNS pods crashed or all not-ready
| Symptom | Fix |
|---|---|
| `kubectl get pods -l k8s-app=kube-dns` shows 0 ready | Restart: `kubectl rollout restart deployment -n kube-system coredns` |

### B. CoreDNS configmap regression
| Symptom | Fix |
|---|---|
| Recent edit to the corefile is causing parse errors | Revert via `kubectl edit configmap -n kube-system coredns` |

### C. Upstream resolver failure
| Symptom | Fix |
|---|---|
| External lookups fail but cluster-internal works | The cloud provider's DNS resolver is down; engage cloud support |

### D. NodeLocalDNS daemonset failure
| Symptom | Fix |
|---|---|
| Failure correlates with NodeLocalDNS pod state | Restart NodeLocalDNS pods |

## Escalation

1. **Platform on-call**.
2. **Cloud team** if upstream resolver.

## Related runbooks

- [Database Connectivity Loss](database-connectivity-loss.md) — when DB hostname won't resolve
- [Service Endpoint Down](service-endpoint-down.md) — when service-to-service can't resolve
