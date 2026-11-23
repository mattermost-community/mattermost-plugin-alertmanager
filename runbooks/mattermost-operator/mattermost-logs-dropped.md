# Mattermost Logs Dropped

!!! warning "Severity: Warning"
    **Target response: 30 minutes.**
    The server has been dropping or blocking log records for five minutes.
    Users may not notice anything, but the next failure may leave no log
    line, and a blocked logger slows the requests that write to it.

## What this alert means

```promql
sum by (namespace, installation_mattermost_com_installation) (rate(mattermost_logging_logger_dropped_total{...}[5m])) > 0
or
sum by (namespace, installation_mattermost_com_installation) (rate(mattermost_logging_logger_blocked_total{...}[5m])) > 0
```

`{...}` is `namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"`.
Each log target (console, file, syslog, TCP, and any target in
`LogSettings.AdvancedLoggingJSON`) has a buffer. "Dropped" means a record
was thrown away because the buffer was full. "Blocked" means a caller
waited for room.

## Quick diagnostics

```promql
# WHERE: Prometheus or Grafana Explore.
# WHAT: dropped plus blocked records per second, per pod and log target.
# READ: one target (for example a syslog or TCP target) = that destination
#   is slow or unreachable. Every target = the pod is overloaded, or
#   logging is at debug level.
sum by (pod, target) (rate({__name__=~"mattermost_logging_logger_(dropped|blocked)_total", namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"}[5m]))
```

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: the log level and log targets the server runs with.
# READ: DEBUG on the console or in a file target multiplies log volume.
kubectl get deploy -n <namespace> <installation_mattermost_com_installation> -o yaml | grep -A1 -E 'MM_LOGSETTINGS_(CONSOLELEVEL|FILELEVEL|ADVANCEDLOGGINGJSON)'
```

## Diagnostic steps

### 1. Which target?

The first quick diagnostic names the target. A remote target that cannot
keep up is the most common cause.

### 2. Log level

Debug logging during an incident and never turned off is the next most
common cause. Check System Console → Environment → Logging, or the
environment variables above.

### 3. Disk, for file targets

```bash
kubectl exec -n <namespace> <pod> -c mattermost -- df -h /mattermost/logs
```

A full log volume makes the file target block.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| One remote target | Syslog or TCP endpoint slow or down | Fix the endpoint, or remove the target |
| All targets, DEBUG level | Log volume too high | Set console and file level back to INFO |
| File target blocked | Log volume full | Expand or clean the volume |
| All targets, CPU throttled | Pod overloaded | See [High CPU](mattermost-high-cpu.md) |

## Escalation

Platform on-call if logs are dropping during an active incident, since
evidence is being lost.

## Required Prometheus labels

`namespace` and `installation_mattermost_com_installation`.

## Related runbooks

- [High CPU](mattermost-high-cpu.md)
- [Persistent Volume Full](../persistent-volume-full.md)
