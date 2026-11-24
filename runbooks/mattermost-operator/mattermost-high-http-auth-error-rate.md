# Mattermost High HTTP Auth Error Rate

!!! warning "Severity: Warning"
    **Target response: 15 minutes.**
    More than 20% of this installation's API requests are answered 401 or
    403, with at least one such request a second, for ten minutes. Someone
    is replaying tokens or probing permissions, or a client, integration or
    SSO proxy is sending credentials the server rejects.

## What this alert means

```promql
(
  sum by (namespace, installation_mattermost_com_installation) (rate(mattermost_api_time_count{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>", status_code=~"401|403"}[5m]))
  /
  sum by (namespace, installation_mattermost_com_installation) (rate(mattermost_api_time_count{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"}[5m]))
) > 0.2
and
sum by (namespace, installation_mattermost_com_installation) (rate(mattermost_api_time_count{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>", status_code=~"401|403"}[5m])) > 1
```

The share and the floor are `mm-auth-error-percent` and `mm-auth-min-rps`
on `config add` or `format rules`. Render the rules again after changing
them.

401 means the request had no valid session or token. 403 means the user
is known but not allowed. Neither reaches the login endpoint, so
[Login Failure Spike](mattermost-login-failure-spike.md) does not see this
traffic. A short burst of expired sessions after a restart is normal and
clears inside the 10 minutes. WebSocket upgrades are not in the
denominator.

## Quick diagnostics

```promql
# WHERE: Prometheus or Grafana Explore.
# WHAT: the API handlers answering 401 or 403 for this installation.
# READ: one handler with most of it = one client or integration calling an
#   endpoint it may not use. Many handlers with 401 = sessions or tokens
#   rejected everywhere: replayed tokens, a revoked bot token still in use,
#   or an SSO proxy that stopped passing its header.
topk(10, sum by (handler, status_code) (rate(mattermost_api_time_count{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>", status_code=~"401|403"}[5m])))
```

```promql
# WHERE: Prometheus or Grafana Explore.
# WHAT: 401 and 403 per second by pod.
# READ: even across pods = the clients are spread over the load balancer,
#   the usual case. One pod far ahead = sticky sessions pinning one client,
#   or that pod lost something the others have (config, clock, plugin).
sum by (pod, status_code) (rate(mattermost_api_time_count{namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>", status_code=~"401|403"}[5m]))
```

## Diagnostic steps

### 1. One client or everyone?

The metric carries no client address. Take the handlers from the first
quick diagnostic to your ingress or load balancer access logs and count
401 and 403 by client IP there. A few IPs or one handler points at a
single client, bot or integration. Many IPs across many handlers points at
something shared: the ingress, an SSO proxy, or a session change.

### 2. Did something change?

- A restart or upgrade that invalidated sessions shows as 401 that falls
  away within minutes as clients log in again.
- A revoked personal access token or bot token still configured in an
  integration shows as steady 401 on that integration's handlers.
- A permission or scheme change shows as 403 on the endpoints it touched.

### 3. Attack: contain it

- Block the source IPs at the ingress or WAF.
- Revoke the sessions or tokens in use from those IPs (System Console,
  Users, or `mmctl user` and `mmctl token`).
- Review the audit log for any call from the same IPs that succeeded.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| 401 from few IPs on many handlers | Stolen or replayed session tokens | Block IPs; revoke the sessions; reset the affected users |
| 401 on one integration's handlers | Revoked or expired bot or personal access token | Give the integration a new token |
| 403 from one user or bot on admin endpoints | Permission probing, or a script run with too few rights | Confirm with the owner; block or fix the role |
| 401 everywhere after an ingress or proxy change | SSO or auth header no longer forwarded | Restore the header or proxy config |
| 401 spike that clears in minutes after a restart | Expired sessions on clients reconnecting | None; the 10m `for` usually hides it |

## Escalation

1. **Security on-call** for suspected token replay or probing, with the
   source IPs and handlers.
2. **Integration owner** when one integration's token is rejected.

## Required Prometheus labels

`namespace` and `installation_mattermost_com_installation`.

## Related runbooks

- [Login Failure Spike](mattermost-login-failure-spike.md)
- [High HTTP Error Rate](mattermost-high-http-error-rate.md)
- [API Server Auth Failure Spike](../apiserver-auth-failure-spike.md)
