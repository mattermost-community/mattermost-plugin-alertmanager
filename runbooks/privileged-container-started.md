# Privileged / Root Container Started

!!! danger "Severity: critical"
    **Target response: 15m.** A container started `privileged`, as UID 0,
    or with a hostPath mount — the standard setup for a container escape
    to the node.

## What this alert means

A privileged container shares the host's kernel capabilities; root +
hostPath lets a process reach the node filesystem. Legitimate uses exist
(CNI, CSI, node-exporter) — those should be allowlisted. Anything else
starting privileged is a policy violation and a potential escape.

```promql
# Requires a policy engine (Kyverno/Gatekeeper) OR kube-state-metrics
# security-context series. Example against a Kyverno violation counter:
sum by (namespace, policy, rule) (
  increase(kyverno_policy_results_total{rule=~".*privileged.*|.*run-as-non-root.*", policy_result="fail"}[10m])
) > 0
```

**Dependency:** vanilla kube-state-metrics does not expose
`securityContext.privileged` by default. This alert needs a policy engine
(Kyverno/Gatekeeper) or a custom exporter. Without one, it will never
fire — see Required labels.

## Quick diagnostics

```bash
# WHERE: shell with kubectl context set.
# WHAT: dump the security context of every container in the pod.
# READ: privileged=true, runAsUser=0/absent, allowPrivilegeEscalation=true,
#   or capabilities.add including SYS_ADMIN/NET_ADMIN = the risky settings.
kubectl get pod -n <namespace> <pod> -o jsonpath='{range .spec.containers[*]}{.name}: {.securityContext}{"\n"}{end}'
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: check for hostPath / host namespace access on the pod.
# READ: hostPath mounts, hostNetwork/hostPID/hostIPC = true widen the blast
#   radius from container to node. Combined with privileged, treat as an
#   active escape risk.
kubectl get pod -n <namespace> <pod> -o jsonpath='hostNetwork={.spec.hostNetwork} hostPID={.spec.hostPID} volumes={.spec.volumes}{"\n"}'
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: who owns this pod, so you know if it's an allowlisted system
#   workload or a user workload that shouldn't be privileged.
# READ: kube-system CNI/CSI DaemonSets = expected; a Deployment in an app
#   namespace running privileged = policy violation to remediate.
kubectl get pod -n <namespace> <pod> -o jsonpath='{.metadata.ownerReferences}{"\n"}'
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Critical | Yes — page on-call | 15m | Container-to-node escape path is open |

## Diagnostic steps

1. **Confirm the settings** (security-context query above).
2. **Classify** — allowlisted system workload vs. user workload.
3. **Attribute** — owner ref + who applied it (GitOps history / audit).
4. **Contain** — if not allowlisted, delete the pod and scale its owner to 0; the admission policy should block recreation once enforced.

## Common causes & fixes

| Symptom | Diagnosis | Fix |
|---|---|---|
| App pod privileged | securityContext.privileged=true | Remove it; grant only the specific capability needed |
| Runs as root | runAsUser 0 / unset | Set runAsNonRoot + a non-zero UID |
| hostPath to /var/run/docker.sock | volumes list | Remove; this is a full node takeover primitive |

## Escalation

1. **Primary** — `@sre-oncall` in `#mm-incidents`.
2. **Security** — `@security-oncall` immediately if the workload is
   unrecognized or on production — assume escape attempt.

## Required Prometheus labels

Diagnostics use `namespace`, `policy`, `rule` (Kyverno) or your policy
engine's equivalents. **Requires Kyverno/Gatekeeper/OPA** — not available
from stock kube-state-metrics.

## Related runbooks

- [Unexpected Container Image](unexpected-container-image.md) — rogue images often ask for privilege.
- [Interactive Shell in Container](interactive-shell-in-container.md) — what an attacker does once privileged.
