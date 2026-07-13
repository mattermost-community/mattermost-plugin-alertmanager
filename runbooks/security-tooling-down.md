# Security Tooling Down

!!! danger "Severity: critical"
    **Target response: 15m.** A security sensor (Falco, Kyverno, or the
    audit pipeline) stopped reporting. Your runtime/policy/audit-based
    alerts are now blind — threats can occur without firing anything.

## What this alert means

Several security alerts depend on tooling that emits its own metrics:
Falco (`interactive-shell`, `rbac-escalation`), Kyverno/Gatekeeper
(`privileged-container`). If that tooling's scrape target goes down, those
detections silently stop — the absence of alerts starts meaning nothing.

```promql
up{job=~"falco|kyverno.*|gatekeeper.*"} == 0
```

This is the meta-alert that watches the watchers. Without it, a Falco
crash looks exactly like "all clear."

## Quick diagnostics

```bash
# WHERE: shell with kubectl context set.
# WHAT: state of the security tooling pods (Falco daemonset, Kyverno).
# READ: CrashLoopBackOff / not Ready = the sensor is down. Fewer Falco
#   pods than nodes = some nodes are unmonitored (per-node blind spots).
kubectl get pods -n falco; kubectl get pods -n kyverno 2>/dev/null
```

```promql
# WHERE: Grafana → Explore or Prometheus /graph.
# WHAT: which security scrape targets are down, by job/instance.
# READ: the job label tells you which sensor (falco vs kyverno). instance
#   tells you which node/replica. All instances of a job down = full blind
#   spot for that detection class.
up{job=~"falco|kyverno.*|gatekeeper.*"} == 0
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: recent logs from the down sensor to see why it stopped.
# READ: OOMKilled = raise limits. Config/parse error = a bad rule/policy
#   push. Crash on start = version/kernel mismatch (Falco eBPF).
kubectl logs -n falco -l app.kubernetes.io/name=falco --tail=40 --previous 2>/dev/null
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Critical | Yes — page on-call | 15m | Security detections dark; blind to intrusions |

## Common causes & fixes

| Symptom | Diagnosis | Fix |
|---|---|---|
| Falco pods OOMKilled | logs / OOM events | Raise memory limits; check ruleset size |
| Kyverno not Ready | pod status | Restart; check webhook cert validity |
| Bad rule/policy push | parse error in logs | Roll back the offending rule/policy |
| Fewer Falco pods than nodes | daemonset desired vs ready | Fix node taints/scheduling for the DS |

## Escalation

1. **Security** — `@security-oncall` in `#mm-incidents` — you are operating
   without detections until this is restored; treat as a security incident
   if the outage overlaps any suspicious activity.
2. **Platform** — `@sre-oncall` to restore the workload.

## Required Prometheus labels

Diagnostics use `job`, `instance` from `up`. Requires the security tooling
(Falco/Kyverno) to expose Prometheus scrape targets.

## Related runbooks

- [Interactive Shell in Container](interactive-shell-in-container.md) — dark while Falco is down.
- [RBAC Privilege Escalation](rbac-privilege-escalation.md) — dark while the audit pipeline is down.
- [Privileged / Root Container Started](privileged-container-started.md) — dark while Kyverno is down.
