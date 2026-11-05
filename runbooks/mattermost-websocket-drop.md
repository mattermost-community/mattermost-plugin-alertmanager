# Mattermost WebSocket Drop

!!! warning "Severity: Warning"
    **Target response: 10 minutes.**
    WebSocket connections carry real-time messages, typing indicators,
    and channel updates. A large drop means users stop receiving live
    updates and fall back to polling — a significant UX regression.

## What this alert means

The number of active Mattermost WebSocket connections dropped by more than
50% in a 10-minute window. The alert fires on:

```promql
mattermost_http_websockets_total
  < (mattermost_http_websockets_total offset 10m) * 0.5
```

This detects relative drops, which makes it scale-agnostic: whether you
have 100 or 10,000 active connections, a 50% drop is always an anomaly.

A drop this large typically means:
- A Mattermost pod restarted (all its connections dropped)
- A network disruption between clients and the server
- A load balancer timeout that isn't forwarding WebSocket upgrades correctly

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: pod restarts in the last 30 minutes.
# READ: RESTARTS > 0 in the last 30 minutes = the pod dropped all
#   its WebSocket connections when it restarted. If multiple pods
#   restarted, check the reason — OOM, failed readiness probe, etc.
kubectl get pods -n <namespace> -l app=mattermost \
  -o custom-columns="NAME:.metadata.name,RESTARTS:.status.containerStatuses[0].restartCount,STATUS:.status.phase"
```

```bash
# WHERE: shell with kubectl access.
# WHAT: last 50 lines of Mattermost server logs from the last pod restart.
# READ: OOM kill, panic, or connection refused = pod crash. A graceful
#   shutdown (SIGTERM + drain) produces "Graceful Shutdown" messages.
#   An abrupt crash produces nothing useful — check the previous container's
#   logs instead.
kubectl logs -n <namespace> -l app=mattermost --previous --tail=50 2>/dev/null \
  || kubectl logs -n <namespace> -l app=mattermost --since=15m | tail -50
```

```bash
# WHERE: shell with kubectl access.
# WHAT: check if the load balancer WebSocket timeout is long enough.
# READ: WebSocket connections are long-lived. If the LB idle timeout
#   is shorter than the Mattermost websocket ping interval, LB will
#   silently close connections. Default MM websocket ping: 60s.
#   Your LB idle timeout must be > 60s (300s recommended).
kubectl get ingress -n <namespace> -o yaml \
  | grep -E "timeout|proxy-read|proxy-send"
```

## Diagnostic steps

### 1. Check for pod restarts

```bash
kubectl describe pod -n <namespace> -l app=mattermost \
  | grep -E "Restart Count|Reason|Exit Code|OOMKilled"
```

If a pod OOMKilled, see [High Memory Usage](high-memory-usage.md). If the
pod restarted for another reason, check its logs before the restart.

### 2. Verify WebSocket proxy configuration

For nginx ingress, WebSocket upgrades require specific annotations:

```yaml
nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
nginx.ingress.kubernetes.io/proxy-send-timeout: "3600"
nginx.ingress.kubernetes.io/configuration-snippet: |
  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection "Upgrade";
```

If these are missing or set too low, the LB will drop WebSocket
connections silently after the timeout.

### 3. Test WebSocket connectivity directly

```bash
# Install websocat if available, then test the WebSocket endpoint:
websocat "ws://<mattermost-host>/api/v4/websocket" \
  -H "Authorization: Bearer <personal-access-token>"
```

A successful upgrade returns `{"event":"hello",...}`. A failure returns
an HTTP error or immediately closes the connection.

### 4. Check Mattermost connection limits

```bash
# Mattermost config: ServiceSettings.MaximumWebsocketMessagesPerSecond
# and ServiceSettings.AllowedUntrustedInternalConnections
kubectl get cm -n <namespace> mattermost-config -o yaml \
  | grep -A2 "MaximumWebsocket\|WebsocketURL"
```

## Common causes & fixes

### A. Pod OOM kill or crash

| Symptom | Fix |
|---------|-----|
| `OOMKilled` in pod describe | Increase memory limits, see High Memory Usage runbook |
| Non-zero restart count + no OOM | Check pod logs before restart |

### B. Load balancer timeout too short

| Symptom | Fix |
|---------|-----|
| Connections drop on a schedule (e.g., every 60s) | Increase LB idle timeout to 3600s |

### C. Network disruption

| Symptom | Fix |
|---------|-----|
| Multiple services lost connectivity simultaneously | Check node network, CSP, cloud provider status |

### D. Mattermost rolling restart (planned)

If a deployment rollout is in progress, WebSocket connection counts will
drop and recover as pods cycle. Check:

```bash
kubectl rollout status deployment -n <namespace> mattermost
```

This is expected behavior — alert can be silenced during planned maintenance.

## Escalation

1. **Platform on-call** — `@platform-oncall` if connections don't recover
   within 15 minutes after the root cause is addressed.
2. **Network team** — if you suspect a cloud/datacenter network issue.

## Required Prometheus labels

`mattermost_http_websockets_total` is exported as a gauge by Mattermost
when metrics are enabled. No additional labels are required.

## Related runbooks

- [High Memory Usage](high-memory-usage.md)
- [Pod CrashLoopBackOff](pod-crashloopbackoff.md)
