# Request Rate Anomaly

!!! warning "Severity: Warning"
    **Target response: 15 min.** Request rate is significantly higher or lower than baseline. Spike may overwhelm; drop may indicate upstream failure.

## What this alert means

The current request rate deviates from the rolling baseline by more than a threshold (e.g., 3 standard deviations or a fixed percentage). The alert can fire for two distinct conditions:

- **Spike** — traffic surge. Capacity stress, possible DDoS, or a viral event.
- **Drop** — traffic loss. Upstream is unable to send, or DNS / LB / authn is broken upstream.

```promql
# Example: traffic > 2x the 1-hour rolling baseline
rate(http_requests_total[5m]) > 2 * avg_over_time(rate(http_requests_total[5m])[1h:5m])
```

Drops can be more dangerous than spikes — a sudden drop to 10% normal traffic often means something on the path is failing silently (CDN, DNS, upstream auth), and downstream alerting may not fire if your monitoring only looks at "errors per request" ratios.

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning (spike) | No | 15 min | Capacity stress; latency degradation if not scaled |
| Warning (drop) | No | 15 min | Possible silent loss of inbound traffic |

## Diagnostic steps

### 1. Confirm direction and magnitude
TODO — open Prometheus, plot request rate over 24h to see the spike/drop context.

### 2. Per-endpoint breakdown
```promql
topk(10, sum by (route) (rate(http_requests_total[5m])))
```
Is the change concentrated on one endpoint or spread?

### 3. Source check
TODO — depending on your ingress, query for client IP distribution. Is it from many sources (legitimate spike) or one source (possible attack)?

### 4. For drops: walk upstream
TODO — CDN logs, LB logs, DNS resolution check. Is the traffic reaching your edge but not your services?

## Common causes & fixes

### A. Legitimate traffic spike (campaign, viral event)
| Symptom | Diagnosis | Fix |
|---|---|---|
| Broad source distribution; user-facing endpoints affected | Marketing or product launch coincides with the time | Scale out: `kubectl scale deployment ...`. Verify HPA is actually engaging. |

### B. Synthetic / load-test traffic
| Symptom | Diagnosis | Fix |
|---|---|---|
| Single source IP, suspicious user-agent | A team is running a load test without coordination | Identify and stop the source; consider rate-limiting at the LB |

### C. DDoS or scraping attack
| Symptom | Diagnosis | Fix |
|---|---|---|
| Many source IPs targeting specific endpoints (often /login or /api/*) | Coordinated traffic | Engage your network team to block at the LB / WAF. Rate-limit. Cloudflare-style edge protection if available. |

### D. Traffic drop — DNS/upstream failure
| Symptom | Diagnosis | Fix |
|---|---|---|
| Rate dropped to ~0; downstream services have no inbound | TODO — depends on your edge topology | TODO |

### E. Traffic drop — CDN/cache change
| Symptom | Diagnosis | Fix |
|---|---|---|
| Traffic dropped to a non-zero floor; cache hit rate climbed | A cache layer is absorbing what used to hit the origin | Verify cache config didn't change inappropriately |

## Escalation

1. Service owning team's on-call.
2. **Network team** if DDoS-shaped.
3. **CDN/Edge team** if cache or routing involved.

## Post-incident

1. Capacity review if the spike caused real strain.
2. Improve detection — should you have an alert on *direction* (spike vs drop), with different responses?

## Related runbooks

- [High HTTP 5xx Error Rate](high-http-error-rate.md) — when traffic spike turns into errors
- [High API Latency](high-api-latency.md) — when traffic spike causes slowness
- [Ingress High 5xx](ingress-high-5xx.md) — when LB struggles under spike
