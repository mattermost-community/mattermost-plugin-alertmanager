# Mattermost Login Failure Spike

!!! warning "Severity: Warning"
    **Target response: 15 minutes.**
    Most login attempts on this installation are failing, with at least
    20 failures in fifteen minutes. That is either an attack (password
    spraying, credential stuffing) or a broken login path that locks real
    users out.

## What this alert means

```promql
(
  sum by (namespace, installation_mattermost_com_installation) (rate(mattermost_login_logins_fail_total{...}[15m]))
  /
  sum by (namespace, installation_mattermost_com_installation) (
    rate(mattermost_login_logins_fail_total{...}[15m]) + rate(mattermost_login_logins_total{...}[15m])
  )
) > 0.5
and
sum by (namespace, installation_mattermost_com_installation) (increase(mattermost_login_logins_fail_total{...}[15m])) > 20
```

`{...}` is `namespace="<namespace>", installation_mattermost_com_installation="<installation_mattermost_com_installation>"`.
`mattermost_login_logins_total` counts successes only, so the denominator
adds the two. The ratio and the floor are `loginFailurePercent` and
`loginMinFailures` under `prometheusRule.thresholds` on the Mattermost CR.

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: failed-login log lines from every pod, counted by client IP.
# READ: a handful of IPs with hundreds of failures = an attack from those
#   addresses. Failures spread across many IPs and users = the login path
#   is broken (LDAP, SAML or an IdP outage) or a distributed spray.
kubectl logs -n <namespace> -l installation.mattermost.com/installation=<installation_mattermost_com_installation> -c mattermost --since=15m \
  | grep -iE 'login|authenticat' | grep -oE '"ip_addr":"[^"]*"' | sort | uniq -c | sort -rn | head -15
```

```promql
# WHERE: Prometheus or Grafana Explore.
# WHAT: successful logins over the last few hours.
# READ: successes near zero while failures climb = nobody can log in, so
#   treat it as an outage. Successes normal = most users are fine and the
#   failures come from someone else.
sum(rate(mattermost_login_logins_total{namespace="<namespace>"}[5m]))
```

## Diagnostic steps

### 1. Attack or outage?

Use both quick diagnostics. Few IPs and normal successes means attack.
Successes falling means outage.

### 2. Outage: check the identity provider

```bash
kubectl logs -n <namespace> -l installation.mattermost.com/installation=<installation_mattermost_com_installation> -c mattermost --since=15m \
  | grep -iE 'ldap|saml|oauth|openid' | grep -E '"level":"(error|warn)"' | tail -20
```

LDAP bind failures, an expired SAML certificate, or an OpenID provider
outage break every login of that type at once.

### 3. Attack: contain it

- Block the source IPs at the ingress or WAF.
- Check that `ServiceSettings.MaximumLoginAttempts` is set (default 10), so
  accounts lock after repeated failures.
- Review the audit log for any account that logged in successfully from
  the same IPs.

## Common causes & fixes

| Symptom | Cause | Fix |
|---|---|---|
| Few IPs, many users | Password spraying | Block IPs; enforce MFA |
| Few IPs, one user | Brute force on one account | Account lockout handles it; notify the user |
| All LDAP users fail | LDAP server or bind account | Fix `LdapSettings`, check the bind password |
| All SAML users fail | Expired IdP or SP certificate | Rotate the certificate in `SamlSettings` |
| Failures after password policy change | Users with old passwords on mobile | Communicate; failures fade on their own |

## Escalation

1. **Security on-call** for a suspected attack, with the source IPs.
2. **Identity team** for LDAP, SAML or OpenID failures.

## Required Prometheus labels

`namespace` and `installation_mattermost_com_installation`.

## Related runbooks

- [Mattermost High Login Failures](../mattermost-high-login-failures.md)
- [API Server Auth Failure Spike](../apiserver-auth-failure-spike.md)
