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
# Confirm current cert expiry on the actual endpoint
echo | openssl s_client -servername $HOST -connect $HOST:443 2>/dev/null | openssl x509 -noout -dates
```

```bash
# cert-manager: find certs that aren't Ready
kubectl get certificates -A | grep -v "True"
```

```bash
# Detail on the affected Certificate resource
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

## Related runbooks

- [Ingress High 5xx](ingress-high-5xx.md) — what fires AFTER cert expires
