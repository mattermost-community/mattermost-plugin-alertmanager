# Interactive Shell in Container

!!! danger "Severity: warning"
    **Target response: 15m.** A shell was spawned inside a running
    container (`kubectl exec`, or a process spawning `/bin/sh`) —
    hands-on-keyboard activity in a workload that should be immutable.

## What this alert means

Production containers should not have humans (or attacker payloads)
opening shells in them. This alert fires on a Falco runtime rule that
detects a terminal/shell spawned inside a container.

```promql
# Requires Falco exporting events to Prometheus.
sum by (k8s_ns_name, k8s_pod_name) (
  rate(falco_events{rule="Terminal shell in container"}[5m])
) > 0
```

**Dependency:** this is a *runtime* signal. Vanilla Prometheus cannot see
it — you need **Falco** (or an equivalent eBPF runtime sensor) shipping
events. Without Falco this alert never fires. See Required labels.

## Quick diagnostics

```bash
# WHERE: shell with kubectl context set to the affected cluster.
# WHAT: who currently has an exec session / what's running in the pod.
# READ: an interactive shell (bash/sh with a tty) that you can't tie to a
#   known operator action = investigate. A one-off debug by a named
#   engineer during an incident = probably benign, confirm in chat.
kubectl exec -n <namespace> <pod> -- ps -eo pid,ppid,user,args 2>/dev/null | head -30
```

```bash
# WHERE: shell with cluster-admin context.
# WHAT: recent exec calls from the audit log against this pod.
# READ: the `user` field tells you WHO exec'd. A service account or an
#   unknown user exec-ing into prod = red flag; a human on-call = confirm.
grep -E '"verb":"create".*"subresource":"exec".*<pod>' /var/log/kubernetes/audit.log | tail -20
```

```bash
# WHERE: Falco logs / Falco sidecar.
# WHAT: the full Falco event that fired, with proc.cmdline and user.
# READ: shows the exact command and parent process. A shell whose parent
#   is the app process (not kubectl-exec) suggests code execution via a
#   vuln, not an operator — escalate.
kubectl logs -n falco -l app.kubernetes.io/name=falco --since=15m | grep -i "shell in container"
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning | No — chat only | 15m | Possible hands-on-keyboard intrusion |

Escalate to **critical/page** if the shell's parent is the application
process (RCE indicator) or the exec came from a service account.

## Diagnostic steps

1. **Attribute** — audit log `user` field: named human vs. service account vs. unknown.
2. **Confirm intent** — ask in `#mm-incidents` whether an operator is actively debugging.
3. **Inspect the session** — parent process (Falco event); app-process parent = RCE, not exec.
4. **Contain if unexplained** — snapshot the pod, revoke the acting identity, isolate with a deny-all NetworkPolicy, open IR.

## Common causes & fixes

| Symptom | Diagnosis | Fix |
|---|---|---|
| On-call debugging | Named user in audit | Benign; prefer ephemeral debug containers |
| SA-initiated exec | Non-human user | Revoke SA, investigate as compromise |
| Shell parented to app | Falco proc tree | Treat as RCE; isolate + IR |

## Escalation

1. **Primary** — `@sre-oncall` in `#mm-incidents`.
2. **Security** — `@security-oncall` for any unexplained or SA-initiated shell.

## Required Prometheus labels

Diagnostics use `k8s_ns_name`, `k8s_pod_name` (Falco label names differ
from kube-state-metrics). **Requires Falco** shipping `falco_events`.

## Related runbooks

- [Privileged / Root Container Started](privileged-container-started.md) — escalation path after a shell.
- [RBAC Privilege Escalation](rbac-privilege-escalation.md) — what the shell reaches for next.
