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
# WHERE: shell with kubectl context set. Assumes CoreDNS for
#   in-cluster DNS — adjust the namespace + selector for kube-dns
#   or other resolvers if you run them.
# WHAT: spin up an ephemeral busybox pod and resolve
#   kubernetes.default via the in-cluster DNS service.
# READ: success → in-cluster DNS works for at least one client;
#   the alert's source may be specific to one node or pod's
#   network namespace. Failure ("can't find", "connection refused")
#   → DNS itself broken. Test an EXTERNAL host too (substitute
#   google.com) to differentiate intra-cluster vs upstream/forward.
kubectl run -it --rm dnstest --image=busybox --restart=Never -- nslookup kubernetes.default
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: CoreDNS pod status. Default install has 2 replicas in
#   kube-system labeled k8s-app=kube-dns.
# READ: STATUS=Running + READY=1/1 for all replicas = healthy.
#   READY=0/1 with Running → pod alive but readiness probe
#     failing (likely overloaded or hitting a config error).
#   CrashLoopBackOff → CoreDNS process can't start, check
#     logs (next command) and the corefile ConfigMap.
#   Only 1 replica when expected 2 → schedule/eviction issue.
kubectl get pods -n kube-system -l k8s-app=kube-dns -o wide
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: last 100 log lines from all CoreDNS pods, filtered to
#   error-related entries.
# READ: common patterns:
#     "i/o timeout" on an upstream → forward DNS dead, check the
#       cluster's upstream resolver (often a VPC resolver pointed
#       at via /etc/resolv.conf on nodes)
#     "no such host" → likely benign noise from invalid lookups
#     "loop detected" → corefile has a forwarding loop, fix the
#       Corefile ConfigMap (often a self-reference)
#     "permission denied" / "operation not permitted" → seccomp
#       or NetworkPolicy blocking CoreDNS's outbound DNS
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
