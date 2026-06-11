# Pod CrashLoopBackOff

!!! danger "Severity: Critical"
    **Target response: 5 min.** The pod is repeatedly starting,
    failing, and restarting. Each crash is downtime for that replica's
    capacity slice.

## What this alert means

A container in a pod has restarted more than 3 times in 15 minutes.
Kubernetes responds by inserting exponentially-increasing delay
between restart attempts (the "BackOff"). The alert fires when:

```promql
increase(kube_pod_container_status_restarts_total[15m]) > 3
```

The container is failing fast — either crashing on startup, failing
its liveness probe, or being OOMKilled. The pod IP changes each time,
so any connections are broken too.

CrashLoopBackOff is rarely the disease; it's the symptom. The
container is dying, K8s notices, restarts it, and the cycle repeats.
The diagnostic work is finding why it's dying.

## Quick diagnostics

Three commands to run before reading further:

```bash
# WHERE: shell with kubectl context set. <namespace> and <pod>
#   are filled in by AM at alert time.
# WHAT: full pod description: spec, status, events, conditions.
# READ: most useful sections, top to bottom:
#   Status: current Phase (Running, Pending, Failed)
#   Containers: each container's State (Running/Waiting/Terminated)
#     and LastState (what it was before the current state).
#     Terminated LastState with Reason=Error or OOMKilled +
#     ExitCode=N tells you why it died this time.
#   Events: chronological list of what kubelet did. Repeated
#     "Back-off restarting failed container" confirms the loop.
kubectl describe pod -n <namespace> <pod>
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: logs from the PREVIOUS container instance — the one that
#   crashed. Current container is in waiting state and hasn't
#   logged yet. --previous reads /var/log/containers/<>.log on
#   the node, which the kubelet preserves across restarts.
# READ: scan the last 50-100 lines for stack traces, panic
#   messages, "FATAL", "ERROR", "panic:" patterns. The very last
#   few lines before EOF are usually the cause — processes
#   typically log a final error line before exit.
kubectl logs -n <namespace> <pod> --previous --tail=200
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: events scoped to this specific pod, time-sorted. More
#   focused than the namespace-wide event list.
# READ: watch for:
#   BackOff → currently in backoff, will retry after the delay
#   Failed → container exited non-zero (with exit code)
#   Unhealthy → readiness or liveness probe failed
#   FailedMount → PVC didn't attach (check PVC + PV state)
#   CreateContainerError → image pull, configmap, secret, or
#     volume reference problem
kubectl get events -n <namespace> --field-selector involvedObject.name=<pod> --sort-by='.lastTimestamp'
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Critical | Yes — page on-call | 5 min | Capacity loss; pod is in/out of service rotation; request failures during restart windows |

## Diagnostic steps

### 1. Get the immediate state

```bash
kubectl get pod -n <namespace> <pod-name> -o wide
```

The `STATUS` column will show `CrashLoopBackOff`. The `RESTARTS`
column shows the count. The age of the pod vs its restart count gives
you the restart rate.

### 2. Get the kubelet's view of why it's restarting

```bash
kubectl describe pod -n <namespace> <pod-name>
```

The most useful fields:

- **`Last State`** under each container — shows the exit code and
  reason of the most recent termination (e.g., `Exit Code: 137` =
  OOMKilled, `Exit Code: 1` = application error, `Exit Code: 0` =
  clean exit which would be unusual mid-loop).
- **`Events`** at the bottom — kubelet messages about Liveness/
  Readiness probe failures, container creation failures, image pull
  errors.

### 3. Read the previous container's logs

The currently-running container hasn't finished its crash yet. The
PREVIOUS one is what crashed:

```bash
kubectl logs -n <namespace> <pod-name> --previous --tail=200
```

This is the single most valuable diagnostic command for this alert.
If the app crashes on startup, the error is here. If it crashes on a
specific request type, you'll see what request preceded the crash.

### 4. Compare config and image to a known-good version

```bash
# Most recent rollout
kubectl rollout history deployment -n <namespace> <deployment-name>

# Current image
kubectl get deployment -n <namespace> <deployment-name> \
  -o jsonpath='{.spec.template.spec.containers[0].image}'

# Recent config changes (ConfigMap, Secret)
kubectl get events -n <namespace> --sort-by='.lastTimestamp' \
  --field-selector type!=Normal | head -20
```

### 5. Check if it's a startup-probe vs liveness-probe issue

```bash
kubectl describe pod -n <namespace> <pod-name> | grep -A5 "Liveness:\|Startup:\|Readiness:"
```

If the container is being killed by a liveness probe, the kill timing
will be just past the probe's `failureThreshold` × `periodSeconds`.
If the startup probe has a short `failureThreshold`, slow startup
gets killed mid-init.

## Common causes & fixes

### A. Bad recent deployment

| Symptom | Diagnosis | Fix |
|---|---|---|
| Crashloop started minutes after `kubectl apply` or `helm upgrade` | `kubectl rollout history` shows revision change at the time crashes started | `kubectl rollout undo deployment -n <namespace> <name>` |

This is the single most common cause. Default response: roll back, then diagnose offline.

### B. OOMKilled (out of memory)

| Symptom | Diagnosis | Fix |
|---|---|---|
| `Last State.Reason: OOMKilled` and `Exit Code: 137` | The cgroup memory limit was hit | Raise memory limit, or fix the leak. See [High Memory Usage](high-memory-usage.md). |

### C. Missing or mis-mounted ConfigMap / Secret

| Symptom | Diagnosis | Fix |
|---|---|---|
| Pod events show `MountVolume.SetUp failed` or `CreateContainerConfigError` | A ConfigMap or Secret referenced in the manifest doesn't exist or has the wrong keys | Create/correct the missing resource. `kubectl create configmap <name> ...` or `kubectl create secret ...` |

### D. Liveness probe too aggressive

| Symptom | Diagnosis | Fix |
|---|---|---|
| App starts fine, runs briefly, gets killed by kubelet | Events show `Liveness probe failed`; logs show the app was running normally until kill | Increase `initialDelaySeconds` or `failureThreshold` on the liveness probe |

### E. Container can't reach a startup dependency

| Symptom | Diagnosis | Fix |
|---|---|---|
| Logs show DB/Redis/API connection refused; app exits non-zero | The dependency the app needs at boot is unreachable | Check the dependency's pod status; fix DNS or network policies if blocked |

### F. Image pull failure

| Symptom | Diagnosis | Fix |
|---|---|---|
| Events show `ErrImagePull` or `ImagePullBackOff` | Image tag doesn't exist in the registry, or pull credentials expired | Verify the image tag exists; refresh the pull secret if auth has expired |

### G. Volume permission mismatch

| Symptom | Diagnosis | Fix |
|---|---|---|
| Logs show "permission denied" on file write to a mounted volume | Container's user ID doesn't have write access to the volume's filesystem | Set `securityContext.fsGroup` in the pod spec; recreate the pod |

## Escalation

If unresolved within 5 minutes:

1. **Platform on-call** — `@platform-oncall` in `#mm-incidents`. PagerDuty service: `mattermost-platform`.
2. **Application team** — if logs point at app-code error rather than infra issue. PagerDuty service: `<application-team>`.
3. **Cloud team** — if image pull or registry-related. PagerDuty service: `cloud-platform`.

**Severity ladder:**

| Time elapsed | Action |
|---|---|
| 0–5 min | Primary on-call works the alert; if a recent deploy is the obvious trigger, roll back first and ask questions later |
| 5–15 min | Page secondary on-call, declare a minor incident if user-facing impact is observed |
| 15+ min | Engage incident commander; consider scaling the deployment to 0 if other replicas are healthy and the bad one is just noise |

## Post-incident

1. **File a follow-up issue** with the actual crash cause (from `--previous` logs).
2. **Update this runbook** if the cause category was novel.
3. **For OOM/leak/regression**: file a code-side regression bug.
4. **For probe misconfig**: update the workload manifest. Don't let the kill threshold be the rate-limiter on application startup.
5. **Review whether the alert threshold is right** — `> 3 restarts in 15m` is a common default, but very-slow-starting services may hit this in normal operation. Tune if it's noisy.

## Required Prometheus labels

The Quick diagnostics commands above use `<label>` placeholders that
Alertmanager fills in from each alert's labels at delivery time. For
this runbook to render copy-paste-runnable commands, your Prometheus
rule must emit:

- `namespace` — the Kubernetes namespace of the crashlooping pod
- `pod` — the specific pod that's restarting

When a label is missing, the rendered command shows `<no value>` in
that slot — still readable, just not auto-runnable. Add the label to
your rule and reload Prometheus.

## Related runbooks

- [High Memory Usage](high-memory-usage.md) — when OOMKills cause the crashloop
- [Pod Not Ready](pod-not-ready.md) — when slow-starting pods present as crashloop because liveness fires before startup
- [Database Connectivity Loss](database-connectivity-loss.md) — when DB outage takes down dependent services

## Appendix: useful PromQL queries

Top 10 most-restarting containers in the last hour:

```promql
topk(10,
  increase(kube_pod_container_status_restarts_total[1h])
)
```

OOMKill rate over the last hour:

```promql
rate(kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}[1h])
```

Time since pod's last restart — useful for finding pods that restart and recover frequently:

```promql
time() - kube_pod_container_status_last_terminated_finished_at
```
