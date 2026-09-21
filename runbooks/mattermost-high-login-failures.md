# Mattermost High Login Failures

!!! warning "Severity: Warning"
    **Target response: 10 minutes.**
    A spike in failed logins is the earliest signal of a credential
    stuffing or brute force attack. Investigate before it escalates.

## What this alert means

The number of failed Mattermost login attempts has spiked above the
threshold in a short window. The alert fires on:

```promql
increase(mattermost_login_10sec_users_with_failed_logins[10m]) > 10
```

This counts distinct users with failed logins in rolling 10-second
windows, summed over 10 minutes. A handful of failed logins is normal
(users forget passwords). A sharp spike is not.

## Quick diagnostics

```bash
# WHERE: shell with kubectl access to the Mattermost namespace.
# WHAT: recent auth failure logs from Mattermost.
# READ: look for repeated failures from the same IP or for a burst of
#   different usernames (credential stuffing pattern). The 'client_ip'
#   field is your primary indicator.
kubectl logs -n <namespace> -l app=mattermost --since=15m \
  | grep -E "failed login|invalid credentials|Failed to authenticate" \
  | tail -40
```

```bash
# WHERE: shell or a system with access to your ingress/WAF logs.
# WHAT: top source IPs hitting the Mattermost login endpoint.
# READ: a single IP with hundreds of requests/min is brute force.
#   Multiple IPs from the same ASN or geography is credential stuffing.
#   If you have a WAF, check its dashboard for rate-limit events.
# Adapt to your ingress (nginx example):
kubectl logs -n ingress-nginx deploy/ingress-nginx-controller --since=15m \
  | grep "POST /api/v4/users/login" \
  | awk '{print $1}' | sort | uniq -c | sort -rn | head -20
```

```promql
# WHERE: Prometheus or Grafana.
# WHAT: rate of login failures over the last 30 minutes.
# READ: a flat baseline with a sudden spike = attack or user lockout
#   event (e.g., a sync that changed all passwords). A gradual climb
#   = misconfigured SSO or a slow credential sweep.
rate(mattermost_login_10sec_users_with_failed_logins[5m])
```

## Diagnostic steps

### 1. Distinguish attack from operational cause

Operational causes (not attacks):
- LDAP/AD password sync that changed passwords and invalidated sessions
- SSO misconfiguration after an IdP change
- Service accounts with stale passwords hitting the API

Attack indicators:
- Failures from IPs you don't recognize
- Failures distributed across many usernames you don't own
- Failures concentrated on a few usernames (targeted account takeover)

```bash
# Check for LDAP sync errors that may have caused mass logout:
kubectl logs -n <namespace> -l app=mattermost --since=1h \
  | grep -i "ldap\|sync\|authentication" | tail -30
```

### 2. Check for affected accounts

```bash
# Mattermost CLI: list users with locked-out or recently failed accounts
# (adapt to your deployment — mmctl or direct DB query)
mmctl user list --page 0 --per-page 200 \
  | grep -i "fail\|lock\|inactive"
```

### 3. Block or rate-limit the source

If you've confirmed an attack, the fastest mitigation is at the ingress
layer, not Mattermost itself:

```bash
# Nginx ingress: add a rate limit annotation to the Mattermost ingress
# or block the source IP at the WAF/firewall.
# Example: block a specific CIDR at the cluster network policy level.
kubectl apply -f - <<'EOF'
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: block-attacker
  namespace: <namespace>
spec:
  podSelector:
    matchLabels:
      app: mattermost
  ingress:
  - from:
    - ipBlock:
        cidr: 0.0.0.0/0
        except:
        - <attacker-cidr>
EOF
```

### 4. Enable or tighten MFA

If an account may have been compromised, force MFA enrollment:
```bash
mmctl user convert --mfa-required <username>
```

## Common causes & fixes

### A. Credential stuffing (automated attack)

| Symptom | Fix |
|---------|-----|
| Many IPs, many usernames, all failures | Block at WAF/ingress, enable rate limiting, enable MFA |

### B. Brute force (targeted)

| Symptom | Fix |
|---------|-----|
| Few IPs, one or two usernames | Block source IPs, lock the targeted accounts, enable MFA |

### C. LDAP/SSO misconfiguration

| Symptom | Fix |
|---------|-----|
| Failures started after an IdP change | Roll back the IdP change or re-sync credentials |

### D. Service account with stale password

| Symptom | Fix |
|---------|-----|
| Same username failing repeatedly, not a human account | Rotate the service account password |

## Escalation

1. **Security team** — if you cannot rule out a breach. Provide login
   failure logs, source IPs, and any affected usernames.
2. **Platform on-call** — `@platform-oncall` if the volume is causing
   performance impact on the Mattermost cluster.

## Required Prometheus labels

The `mattermost_login_10sec_users_with_failed_logins` metric is a gauge
exported by Mattermost when metrics are enabled. No additional labels
are needed for this alert.

## Related runbooks

- [Mattermost High Error Rate](mattermost-high-error-rate.md)
