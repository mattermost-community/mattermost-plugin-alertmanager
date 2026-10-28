# Mattermost License Expiring Soon

!!! warning "Severity: Warning"
    **Target response: within 1 business day.**
    An expired Mattermost Enterprise license disables enterprise features
    (LDAP, SAML, guest accounts, compliance exports, and more) for all
    users. This is a planned, avoidable outage.

## What this alert means

The Mattermost Enterprise license will expire in fewer than 30 days. The
alert fires on:

```promql
(mattermost_license_expire_date - time()) / 86400 < 30
```

`mattermost_license_expire_date` is a gauge containing the Unix timestamp
of the license expiry. Dividing by 86400 converts seconds to days.

The value of `$value` in the alert is the number of days remaining.

## Quick diagnostics

```bash
# WHERE: shell with access to Mattermost (mmctl or System Console).
# WHAT: current license details including expiry date.
# READ: confirm the expiry date and the licensed SKU. If the license
#   shows as 'None' or 'Team Edition', a previously valid license has
#   already expired.
mmctl license info
```

```bash
# WHERE: Mattermost System Console.
# Navigate: System Console → Edition and License → License Key
# WHAT: full license details including seat count, expiry, and features.
# READ: compare the seat count to current active users — if you're near
#   the seat cap, a renewal may need a capacity increase.
```

```promql
# WHERE: Prometheus or Grafana.
# WHAT: exact days remaining until license expiry.
(mattermost_license_expire_date - time()) / 86400
```

## Renewal steps

### 1. Confirm the license expiry date

```bash
mmctl license info | grep -iE "expire|expiry|valid"
```

### 2. Contact Mattermost for renewal

- **Existing customers:** contact your Mattermost account manager or
  renew through the customer portal at `customers.mattermost.com`.
- **Via support:** open a ticket at `support.mattermost.com` with your
  current license details and the desired renewal period.
- **Self-serve (some SKUs):** `mattermost.com/pricing` → log in with
  your customer account.

Timeline: allow at least 3–5 business days for license delivery.
Enterprise agreements may require longer lead time.

### 3. Apply the new license

Once you have the new `.mattermost-license` file:

**Via System Console:**
1. System Console → Edition and License → License Key → Upload License Key
2. Click Save — no restart required.

**Via CLI (if UI is inaccessible):**
```bash
mmctl license upload /path/to/mattermost-license
```

**Via environment variable (for automated deployments):**
```bash
MM_SERVICESETTINGS_LICENSEFILELOCATION=/etc/mattermost/mattermost-license
```

### 4. Verify the new license is active

```bash
mmctl license info
# Confirm the new expiry date appears.
```

Then verify the Prometheus metric updated:
```promql
(mattermost_license_expire_date - time()) / 86400
```

## What happens when a license expires

On expiry, Mattermost enters Team Edition mode:

| Feature | Status after expiry |
|---------|---------------------|
| LDAP / Active Directory | **Disabled** — users can't log in via LDAP |
| SAML SSO | **Disabled** — SSO users can't log in |
| Guest Accounts | **Disabled** |
| Data retention policies | **Disabled** |
| Compliance exports | **Disabled** |
| Custom branding | **Disabled** |
| AD Group sync | **Disabled** |
| High Availability (HA) | **Disabled** — cluster may degrade |

Regular username/password users are unaffected. The server keeps running —
features are gated, not the server itself.

## Escalation

1. **Procurement / finance** — to initiate the renewal purchase.
2. **Mattermost account manager** — for enterprise renewal negotiation.
3. **Platform on-call** — `@platform-oncall` only if the license has
   already expired and you're seeing feature loss.

## Required Prometheus labels

`mattermost_license_expire_date` is a gauge exported by Mattermost
Enterprise Edition when metrics are enabled. It is NOT present on
Team Edition. If the metric is absent (`absent(mattermost_license_expire_date)`)
the server is running Team Edition or metrics are disabled.

Enable metrics: `ServiceSettings.EnableMetrics: true` in `config.json`.
