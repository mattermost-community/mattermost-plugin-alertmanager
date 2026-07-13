# API Server Auth Failure Spike

!!! danger "Severity: warning"
    **Target response: 20m.** A spike in `401`/`403` responses from the
    Kubernetes API server — credential brute force, a stolen or expired
    token, or a broken RBAC change hammering the API.

## What this alert means

Every request to the API server is authenticated and authorized. A
sustained rate of `401` (bad/expired credentials) or `403` (authenticated
but not permitted) is abnormal and worth a look.

```promql
# Rate of auth failures across the API server.
sum(rate(apiserver_request_total{code=~"401|403"}[5m])) > 1
```

Benign causes exist (a rotated token not yet redeployed), but a *spike*
is also exactly what credential stuffing or a compromised-but-underprivileged
token looks like as it probes for access.

## Quick diagnostics

```promql
# WHERE: Grafana → Explore (Prometheus) or Prometheus /graph.
# WHAT: break the failures down by response code and, if your apiserver
#   exposes it, by the requesting identity/useragent.
# READ: all 401 = credential problem (expired/rotated/bad token).
#   Mostly 403 = an identity authenticating fine but probing resources it
#   can't touch — more suspicious. A single useragent dominating = one
#   actor; spread across many = broader issue or a bad rollout.
sum by (code, useragent) (rate(apiserver_request_total{code=~"401|403"}[5m]))
```

```bash
# WHERE: shell with cluster-admin kubectl context.
# WHAT: recent auth failures from the API server audit log (path varies
#   by distro; adjust). Shows the user, source IP, and resource.
# READ: repeated failures from one user/IP against sensitive resources
#   (secrets, clusterrolebindings) = treat as an attack, not a misconfig.
grep -E '"(responseStatus)":.*"code":(401|403)' /var/log/kubernetes/audit.log | tail -50
```

```bash
# WHERE: shell with kubectl context.
# WHAT: list service-account tokens that may have expired/rotated recently.
# READ: a workload using a token whose secret was just rotated will 401 in
#   a tight loop — correlate the failing identity with a recent rotation.
kubectl get secrets -A --field-selector type=kubernetes.io/service-account-token -o wide | head -30
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning | No — chat only | 20m | Possible credential abuse or broken auth |

Escalate to **critical/page** if 403s target secrets/RBAC resources or the
source is external to the cluster.

## Diagnostic steps

1. **Classify** — 401 vs 403 split (query above). 401 → credentials; 403 → authorization.
2. **Attribute** — identify the failing identity + source IP from audit logs.
3. **Correlate** — recent token rotation, cert expiry, or RBAC change (`kubectl get events`, GitOps history)?
4. **Contain if malicious** — revoke the token/binding, rotate the affected credential, block the source IP at the network layer.

## Common causes & fixes

| Symptom | Diagnosis | Fix |
|---|---|---|
| Uniform 401 from one workload | Rotated token not redeployed | Restart the workload to pick up the new token |
| 403 probing many resources | One identity testing access | Revoke, investigate as compromise |
| Burst after an RBAC merge | Over-tight Role | Fix the Role binding |

## Escalation

1. **Primary** — `@sre-oncall` in `#mm-incidents`.
2. **Security** — `@security-oncall` if the pattern looks like probing
   (403s against secrets/RBAC) or the source is off-cluster.

## Required Prometheus labels

Diagnostics use API-server-level series (`code`, `useragent`) from
`apiserver_request_total`. No per-pod labels required.

## Related runbooks

- [RBAC Privilege Escalation](rbac-privilege-escalation.md) — the follow-on if probing succeeds.
