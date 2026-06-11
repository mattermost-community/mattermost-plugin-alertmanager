# Certificate Expiring Soon

!!! warning "Severity: Warning"
    **Target response: file ticket within 24h.** TLS certificate expires within 14 days. Expired certs cause TLS handshake failures = total outage on the affected endpoint.

## What this alert means

```promql
probe_ssl_earliest_cert_expiry - time() < 86400 * 14
```

A monitored TLS endpoint (via blackbox exporter or cert-manager) has a cert expiring in less than 14 days. The alert intentionally fires early so there's time to renew without urgency.

Critical-severity sibling (`CertificateExpiringSoon` at <3 days) fires if renewal didn't happen.

## Quick diagnostics

Three commands to run before reading further:

```bash
# WHERE: shell with openssl (any Mac/Linux). <instance> is filled
#   in by AM at alert time with the affected hostname.
# WHAT: TLS handshake against the host, then print the cert's
#   notBefore and notAfter dates. Bypasses cert-manager entirely
#   — shows what your USERS see at handshake time.
# READ: notAfter is the hard expiry. Compare to today.
#   Past = users see browser cert warnings now, page immediately.
#   <14 days = act this week.
#   <3 days = act today.
echo | openssl s_client -servername <instance> -connect <instance>:443 2>/dev/null | openssl x509 -noout -dates
```

```bash
# WHERE: shell with kubectl context set. Only relevant if you use
#   cert-manager. Skip if certs are managed by ACM, hand-rolled,
#   or any other tool.
# WHAT: every cert-manager Certificate resource cluster-wide,
#   filtering out those with READY=True. Anything printed = not
#   currently ready (renewal stuck, validation failing, etc.).
# READ: each row shows the Certificate name + namespace + READY=False.
#   Next step is `kubectl describe certificate -n <ns> <name>` to
#   see the failure Reason in Events. Common: Let's Encrypt rate
#   limit, DNS-01 challenge failing, HTTP-01 wrong path.
kubectl get certificates -A | grep -v "True"
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: cert-manager status block for all certs cluster-wide.
#   Filters to the Status: section which has conditions + reasons.
# READ: look for `Type: Ready, Status: False` paired with Reason.
#   NoActiveOrders → renewal hasn't started, check Issuer logs
#   Issuing → renewal in flight, wait 1-2 min
#   Failed → renewal hit a hard error, check the Message field
#     for the underlying ACME response
kubectl describe certificate -A | grep -A 10 "Status:"
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning (<14d) | No | 24h | Preventive — full TLS outage if expires |
| Critical (<3d) | Yes | 4h | Imminent expiration — outage in days |

## Diagnostic steps

### 1. Which endpoint, when does it expire?
TODO — query for `probe_ssl_earliest_cert_expiry` per endpoint; see exact dates.

### 2. Is auto-renewal configured?
```bash
# If cert-manager:
kubectl get certificates -A
kubectl describe certificate -n <ns> <cert-name>
```

### 3. Why hasn't renewal happened?
```bash
# cert-manager events
kubectl get events -n <ns> --field-selector involvedObject.kind=Certificate
```

## Common causes & fixes

### A. cert-manager configured but failing
| Symptom | Fix |
|---|---|
| Cert resource exists but `READY: False` | Check cert-manager logs for the failure reason; common: ACME challenge unreachable, rate limit hit |

### B. Manual cert, renewal forgotten
| Symptom | Fix |
|---|---|
| No cert-manager resource; cert was imported as a Secret | Generate a new cert (Let's Encrypt or internal CA); update the Secret; reload ingress |

### C. ACME challenge blocked by network policy
| Symptom | Fix |
|---|---|
| cert-manager logs show ACME HTTP-01 challenge timing out | Allow inbound to `/.well-known/acme-challenge/*` for cert-manager solver pods |

## Escalation

1. **Platform on-call**.
2. **Security team** if internal CA workflow.

## Required Prometheus labels

The Quick diagnostics commands above use `<label>` placeholders that
Alertmanager fills in from each alert's labels at delivery time. For
this runbook to render copy-paste-runnable commands, your Prometheus
rule must emit:

- `instance` — the hostname being probed for TLS (e.g.,
  `api.example.com`, `mattermost.example.com`)

When a label is missing, the rendered command shows `<no value>` in
that slot — still readable, just not auto-runnable. Add the label to
your rule and reload Prometheus.

## Related runbooks

- [Ingress High 5xx](ingress-high-5xx.md) — what fires AFTER cert expires
