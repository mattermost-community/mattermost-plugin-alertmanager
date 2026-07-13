# RBAC Privilege Escalation

!!! danger "Severity: critical"
    **Target response: 15m.** A high-privilege RBAC object was created or
    modified — e.g. a ClusterRoleBinding granting `cluster-admin`, or a
    binding to a service account that shouldn't have it.

## What this alert means

RBAC changes that grant broad power are rare and deliberate in a healthy
cluster. A binding to `cluster-admin`, or a new ClusterRole with wildcard
verbs, is either a reviewed platform change or an attacker escalating
after landing a foothold.

```promql
# Requires Falco (k8s audit plugin) or an audit-log → metrics pipeline.
sum by (k8s_ns_name) (
  rate(falco_events{rule=~"Create ClusterRoleBinding.*|Attach to cluster-admin.*|K8s Role.*wildcard.*"}[5m])
) > 0
```

**Dependency:** needs Kubernetes **audit logs** flowing into Falco (k8s
audit plugin) or a log→metric pipeline. Vanilla Prometheus/kube-state-metrics
cannot observe RBAC *mutations*. See Required labels.

## Quick diagnostics

```bash
# WHERE: shell with cluster-admin kubectl context.
# WHAT: list every binding that grants cluster-admin, newest first.
# READ: a binding you don't recognize — especially to a ServiceAccount or
#   a user that isn't a platform admin — is the finding. Note its subjects.
kubectl get clusterrolebindings -o json | jq -r '.items[] | select(.roleRef.name=="cluster-admin") | "\(.metadata.creationTimestamp)  \(.metadata.name)  subjects=\(.subjects)"' | sort
```

```bash
# WHERE: shell with cluster-admin context.
# WHAT: the audit-log entries for RBAC create/update in the last window.
# READ: the `user.username` that made the change. A CI/GitOps identity =
#   likely legitimate (confirm the PR). A workload SA or unknown user =
#   escalation attempt.
grep -E '"resource":"(clusterrolebindings|rolebindings|clusterroles)".*"verb":"(create|update|patch)"' /var/log/kubernetes/audit.log | tail -20
```

```bash
# WHERE: shell with cluster-admin context.
# WHAT: what a suspect subject can now do cluster-wide.
# READ: if `can-i --list` for the subject shows * on * , it has full
#   control — contain immediately.
kubectl auth can-i --list --as=system:serviceaccount:<namespace>:<suspect-sa> 2>/dev/null | head
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Critical | Yes — page on-call | 15m | Cluster-wide compromise if attacker-driven |

## Diagnostic steps

1. **Identify the object** — which binding/role, granting what, to whom (queries above).
2. **Attribute the change** — audit-log `user.username`; match against a reviewed PR/GitOps commit.
3. **Decide** — legitimate platform change (confirmed PR) vs. unexplained.
4. **Contain if unexplained** — delete the binding, revoke the subject's tokens, rotate any credentials it could have read, open IR.

## Common causes & fixes

| Symptom | Diagnosis | Fix |
|---|---|---|
| New cluster-admin binding via GitOps | Matches a merged PR | Benign; confirm reviewer |
| Binding created by a workload SA | Audit user = SA | Delete, revoke SA, IR |
| Wildcard ClusterRole appears | roleRef verbs `*` | Scope it down; investigate origin |

## Escalation

1. **Security** — `@security-oncall` in `#mm-incidents` **first** — RBAC
   escalation is a compromise indicator, not a config issue.
2. **Platform** — `@sre-oncall` to confirm whether it maps to a reviewed change.

## Required Prometheus labels

Diagnostics use `k8s_ns_name` (Falco). **Requires audit logs + Falco k8s
audit plugin** (or equivalent). Not observable from kube-state-metrics.

## Related runbooks

- [API Server Auth Failure Spike](apiserver-auth-failure-spike.md) — probing that precedes escalation.
- [Interactive Shell in Container](interactive-shell-in-container.md) — the foothold escalation follows from.
