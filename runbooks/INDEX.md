# Runbook Library

20 runbooks covering the most common SRE alert categories. Each
follows the same structure (see [TEMPLATE.md](TEMPLATE.md)) so the
on-call experience is consistent across alert types.

## Compute & containers

| Runbook | When it fires | Severity |
|---|---|---|
| [High CPU Usage](high-cpu-usage.md) | Container >80% CPU for 10+ min | warning → critical at 95% |
| [High Memory Usage](high-memory-usage.md) | Container >85% memory limit, or OOMKilled | warning → critical at 95% |
| [Pod CrashLoopBackOff](pod-crashloopbackoff.md) | Container restart count climbing fast | critical |
| [Pod Not Ready](pod-not-ready.md) | Readiness probe failing 5+ min | warning → critical if majority of replicas |
| [Deployment Replicas Unavailable](deployment-replicas-unavailable.md) | Available replicas < spec replicas for 5+ min | critical |
| [Node Not Ready](node-not-ready.md) | K8s node `Ready=False` or pressure conditions | critical |

## Application

| Runbook | When it fires | Severity |
|---|---|---|
| [High HTTP 5xx Error Rate](high-http-error-rate.md) | 5xx > 5% of requests over 10 min | critical |
| [High API Latency](high-api-latency.md) | p95 latency above SLO threshold for 10 min | warning |
| [Service Endpoint Down](service-endpoint-down.md) | Prometheus probe failing on a known endpoint | critical |
| [Request Rate Anomaly](request-rate-anomaly.md) | Sudden spike or drop in request rate | warning |

## Database

| Runbook | When it fires | Severity |
|---|---|---|
| [Database Connectivity Loss](database-connectivity-loss.md) | App can't reach DB; zero active connections | critical |
| [Database Replication Lag](database-replication-lag.md) | Replica lag > N seconds for 5+ min | warning → critical at higher thresholds |
| [Database High Latency](database-high-latency.md) | p95 query time > N ms sustained | warning |

## Storage

| Runbook | When it fires | Severity |
|---|---|---|
| [Persistent Volume Full](persistent-volume-full.md) | PV usage > 85% | warning → critical at 95% |
| [Disk Fill Rate High](disk-fill-rate-high.md) | Projected to fill in < 24h based on growth | warning |

## Networking

| Runbook | When it fires | Severity |
|---|---|---|
| [Ingress High 5xx](ingress-high-5xx.md) | Load balancer / ingress 5xx rate spike | critical |
| [Certificate Expiring Soon](certificate-expiring-soon.md) | TLS cert expires in < 14 days | warning |
| [DNS Resolution Failure](dns-resolution-failure.md) | Cluster DNS lookup failures | critical |

## Observability

| Runbook | When it fires | Severity |
|---|---|---|
| [Prometheus Scrape Target Down](prometheus-scrape-target-down.md) | `up == 0` for a target for 5+ min | warning |
| [Alertmanager Notification Failure](alertmanager-notification-failure.md) | AM delivery dead-letter rate climbing | critical |

## How these get referenced

Every Prometheus rule in your alert config should set its
`runbook_url` annotation to the corresponding page in this library:

```yaml
- alert: HighCPUUsage
  annotations:
    runbook_url: "http://localhost:8065/plugins/com.mattermost.alertmanager/public/runbooks/high-cpu-usage.html"
```

Replace `http://localhost:8065` with your Mattermost SiteURL for
production deployments. The path after `/public/runbooks/` matches the
markdown filename (without `.md`, with `.html` suffix).

When alerts fire, Alertmanager renders the URL into the chat post (via
the `slack_configs.text` template's `{{ .Annotations.runbook_url }}`
reference). On-call clicks the URL → opens the rendered runbook page.

## Conventions for new runbooks

When adding a 21st runbook:

1. Start from `TEMPLATE.md`. Copy it to `runbooks/<kebab-case-slug>.md`.
2. Fill in every section. Don't leave placeholder text — better to
   write "TODO: confirm with team X" than leave `<placeholder>` text
   that's read mid-incident.
3. Use real commands. `kubectl get pods -n actual-namespace`, not
   `kubectl get pods -n NAMESPACE`. The on-call wants to copy-paste.
4. Cause categories should be ordered by frequency — most common
   first. Recent-deploy regression and traffic spikes usually take
   slots A and B.
5. Cross-link related runbooks. The "Related runbooks" section is
   underused but is the single best way to short-circuit the wrong
   diagnosis path.
6. Add an entry to this INDEX.md under the right category.
7. Rebuild the plugin so the HTML site picks up the new page.

## When to write a runbook vs. inline the procedure

If the alert's response procedure fits in 3-5 lines, you don't need a
runbook — put it directly in the `annotations.description` field of
the Prometheus rule. The full runbook treatment is for procedures
where:

- Diagnosis involves multiple steps with branching decisions
- The cause has multiple known categories worth distinguishing
- Escalation paths differ based on what the on-call finds
- Mid-incident reference saves time vs reading the description in chat

The 20 listed here all meet that bar.
