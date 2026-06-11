# Alertmanager Notification Failure

!!! danger "Severity: Critical"
    **Target response: 5 min.** Alertmanager can't deliver to one or more receivers. Real alerts may be firing right now and nobody is being notified.

## What this alert means

```promql
rate(alertmanager_notifications_failed_total[5m]) > 0
```

Alertmanager attempted to send to a receiver and failed. Repeated failures mean the affected receiver (chat webhook, PagerDuty, email) is down or misconfigured.

This is the meta-alert: if it's firing, your other alerting is potentially broken. Fix this before anything else.

## Quick diagnostics

Three commands to run before reading further. These cover the most
common root causes:

```bash
# Which receiver is failing?
curl -s $AM_URL/api/v2/alerts | jq '[.[].receivers[].name] | unique'
```

```bash
# Recent notify errors in AM logs
kubectl logs -n monitoring -l app=alertmanager --tail=200 | grep -i notify
```

```bash
# Hit the failing webhook directly to verify it accepts POSTs
curl -X POST -H "Content-Type: application/json" -d '{"text":"test"}' $WEBHOOK_URL
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Critical | Yes | 5 min | Alerting pipeline degraded; real issues may go unnoticed |

## Diagnostic steps

### 1. Which receiver is failing?
TODO — break failures down by `integration` label:
```promql
sum by (integration) (rate(alertmanager_notifications_failed_total[5m]))
```

### 2. AM logs for the specific failure
```bash
# If AM is in K8s
kubectl logs -n monitoring -l app=alertmanager --tail=50 | grep -i "error\|fail"
```

The log line will include the receiver name and the underlying error (HTTP status, timeout, etc.).

### 3. Hit the failing receiver manually
TODO — for a chat webhook: `curl -X POST <api_url> -d '{"text":"manual test"}'`. See what error comes back.

## Common causes & fixes

### A. Mattermost webhook deleted
| Symptom | Fix |
|---|---|
| HTTP 400 from `/hooks/<id>` with the generic "Failed to handle the payload" error | The webhook in `alertmanager.yml` was soft-deleted in Mattermost. Run `/alertmanager render <name>` to get the current valid URL; update the YAML; reload AM. |

### B. PagerDuty integration key changed
| Symptom | Fix |
|---|---|
| HTTP 401 / 403 from PagerDuty | Routing key rotated; update in `alertmanager.yml`; reload AM |

### C. Outbound network blocked
| Symptom | Fix |
|---|---|
| `connection refused` or `i/o timeout` to an external service | New egress NetworkPolicy or firewall rule; whitelist the receiver's endpoint |

### D. Receiver service degraded (chat/pager provider outage)
| Symptom | Fix |
|---|---|
| 5xx from the receiver consistently | Provider is having an outage; check their status page; wait |

## Escalation

1. **Platform on-call**.
2. If the affected receiver is paging itself (chicken-and-egg), the team's own on-call should be reached out-of-band (phone, secondary system).

## Post-incident

1. **Document any alerts that were dropped during the gap.**
2. Consider a secondary receiver for high-severity alerts as belt-and-suspenders (e.g., email AND PagerDuty AND chat).

## Related runbooks

- [Database Connectivity Loss](database-connectivity-loss.md) — when AM can't reach its own state DB
