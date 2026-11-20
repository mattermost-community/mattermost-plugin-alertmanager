# Mattermost Server Down

!!! danger "Severity: Critical"
    **Target response: 5 minutes.**
    Prometheus cannot scrape one Mattermost pod. Either the pod is not
    serving, or its metrics endpoint is off. If every pod is affected,
    users cannot reach Mattermost.

## What this alert means

One scrape target of this installation has answered `up == 0` for five
minutes:

```promql
up{namespace="<namespace>", installation_mattermost_com_installation="<installation>"} == 0
```

The target is the pod's metrics port (8067, `/metrics`). The alert carries
the `pod` label, so it names the pod that failed. It does not mean every
pod is down. When every pod is gone there is no `up` series at all, and
this alert goes quiet; [Pod Crash Looping](mattermost-pod-crash-looping.md)
covers that case.

A pod can be serving users and still fire this alert: `/metrics` is only
served with `MetricsSettings.Enable` on and a valid Enterprise license.

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: every pod of this installation, with readiness and restarts.
# READ: READY 0/1 or a high RESTARTS count on the alerting pod means the
#   server itself is down; see the Pod Crash Looping runbook. All pods
#   1/1 and Running means the server is up and only metrics are failing;
#   the runbook's step 2 checks why.
kubectl get pods -n <namespace> -l installation.mattermost.com/installation=<installation_mattermost_com_installation> -o wide
```

```bash
# WHERE: shell with kubectl access.
# WHAT: ask the alerting pod for its metrics directly, bypassing Prometheus.
# READ: HTTP 200 with mattermost_ lines = metrics work, so the problem is
#   between Prometheus and the pod (ServiceMonitor, NetworkPolicy).
#   404 or connection refused = metrics are off or unlicensed.
kubectl port-forward -n <namespace> pod/<pod> 18067:8067 &
sleep 2; curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18067/metrics; kill %1
```

## Diagnostic steps

### 1. Is the pod serving?

```bash
kubectl describe pod -n <namespace> <pod> | sed -n '/Conditions:/,/Events:/p'
kubectl logs -n <namespace> <pod> -c mattermost --since=10m | tail -50
```

A failing readiness probe or a recent restart puts this alert on the same
footing as a crash. Work it as [Pod Crash Looping](mattermost-pod-crash-looping.md).

### 2. Are metrics enabled and licensed?

```bash
kubectl get deploy -n <namespace> <installation> -o yaml | grep -A1 -E 'MM_METRICSSETTINGS_(ENABLE|LISTENADDRESS)'
kubectl logs -n <namespace> <pod> -c mattermost | grep -iE 'metrics|license' | tail -20
```

`MetricsSettings.Enable` must be true and `ListenAddress` must be `:8067`.
If the Deployment sets neither variable, the values come from the database
config: check System Console → Environment → Performance Monitoring. An
expired or missing license turns `/metrics` off without stopping the server.

### 3. Can Prometheus reach the pod?

Open Prometheus → Status → Targets and find the pod. The error column says
whether the scrape timed out, was refused, or got a 404.

```bash
kubectl get servicemonitor -n <namespace> <installation> -o yaml | grep -A6 endpoints
kubectl get networkpolicy -n <namespace>
```

The ServiceMonitor must point at the `metrics` port of the
`<installation>-metrics` Service. A NetworkPolicy that only admits ingress
traffic on 8065 blocks the scrape.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| Pod not Ready, restarts climbing | Server is crashing | [Pod Crash Looping](mattermost-pod-crash-looping.md) |
| Pod Ready, port-forward gets 404 | Metrics disabled | Set `MetricsSettings.Enable` (operator: `spec.monitoring`) |
| Pod Ready, logs mention the license | License expired or missing | Renew and reapply the license secret |
| Port-forward gets 200, target still down | NetworkPolicy or ServiceMonitor port | Allow the Prometheus namespace on 8067; fix the endpoint port |
| Alert during a rollout only | Old pod terminating | Wait for the rollout; silence during planned maintenance |

## Escalation

1. **Platform on-call** if more than one pod is down, or users report errors.
2. **Mattermost support** with the pod logs if the server will not start
   and the cause is not in the logs.

## Required Prometheus labels

`namespace`, `pod`, and `installation_mattermost_com_installation`. The
ServiceMonitor copies the last one from the pod label with `podTargetLabels`.

## Related runbooks

- [Pod Crash Looping](mattermost-pod-crash-looping.md)
- [Prometheus Scrape Target Down](../prometheus-scrape-target-down.md)
