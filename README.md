# Mattermost Alertmanager Plugin

**When `PostgresReplicationLagHigh` fires into your channel at 3am,
the runbook's first three diagnostic commands appear inline with the
alert — pre-filled with the failing host and pod, copy-paste ready.**

That's the daily-use value. The plugin also helps you wire up the
Prometheus → Alertmanager → Mattermost pipeline in the first place,
but the runbook-at-fire-time experience is why your on-call wants it.

## What an alert looks like in chat

```
[WARNING:HighCPUUsage] (namespace=billing, pod=api-7d9-2xfgs)

**Alert:** HighCPUUsage - warning
**Description:** Sustained high CPU may indicate runaway processes...

**Details:**
  • alertname: HighCPUUsage
  • namespace: billing
  • pod: api-7d9-2xfgs
  • severity: warning

**Runbook:** https://mm.example.com/plugins/com.mattermost.alertmanager/public/runbooks/high-cpu-usage.html

**Quick diagnostics:**

1.
```bash
# WHERE: shell with kubectl context set to the affected cluster.
# WHAT: top 20 pods cluster-wide by CPU, sorted descending.
# READ: if the alert's pod is at the top with millicores in the
#   thousands, that confirms the source...
kubectl top pods -A --sort-by=cpu | head -20
```

2.
```promql
# WHERE: Grafana → Explore (Prometheus data source) or Prometheus /graph.
# WHAT: CFS throttle time per container per 5 minutes, as a rate.
# READ: 0 = no throttling, healthy. >0.1 means the container is being
#   throttled 10%+ of the time...
rate(container_cpu_cfs_throttled_seconds_total[5m])
```

3.
```bash
# WHERE: shell with kubectl context set.
# WHAT: last 5 rollouts of the deployment in billing.
# READ: if REVISION N was created in the last ~30 minutes and the
#   CPU alert started after, you've found the cause...
kubectl rollout history deployment -n billing --limit 5
```
```

The diagnostic content above is parsed from the embedded runbook file
[`runbooks/high-cpu-usage.md`](runbooks/high-cpu-usage.md) at YAML-render
time. Placeholders like `<namespace>` and `<pod>` are substituted by
Alertmanager at alert delivery time using the alert's actual labels.
Each of the 20 shipped runbooks (compute / application / database /
storage / networking / observability) follows the same WHERE / WHAT /
READ structure.

## Two-minute setup

1. **Install the plugin.** Upload the tarball from `dist/` via System
   Console → Plugin Management → Choose File → Enable.

2. **Wire one channel.** In the channel you want alerts to land:

   ```
   /alertmanager add ops alerts http://alertmanager:9093 all
   ```

   The plugin creates 20 Mattermost incoming webhooks (one per
   runbook) bound to this channel and DMs you the assembled
   `receivers.yml` + `routes.yml`.

3. **Paste into `alertmanager.yml`, reload Alertmanager.**

   ```bash
   curl -X POST http://alertmanager:9093/-/reload
   ```

4. **Verify end-to-end.** Fire a synthetic alert through Alertmanager:

   ```
   /alertmanager validate high-cpu-usage --end-to-end
   ```

   Watch the channel for delivery. Once you see the alert post with
   inline diagnostics, you're done.

## Beyond setup

| Command | Use it when |
|---|---|
| `/alertmanager validate --simulate runbook=high-cpu-usage` | Confirm a Prometheus rule's labels will actually route to the expected receiver (no synthetic alert fired) |
| `/alertmanager list` | See receivers bound to the current channel |
| `/alertmanager config <name>` | Detail card + slack_configs YAML for one receiver |
| `/alertmanager export [--diff-against-loaded]` | Re-render the channel's YAML, or diff against AM's loaded config with schema validation |
| `/alertmanager rotate <name>` | Rotate a webhook (new hook-id, paste new YAML, reload AM) |
| `/alertmanager rotate all --overdue` | Rotate every receiver past the configured threshold |
| `/alertmanager about` | Plugin info, settings, reconciler health |

Run `/alertmanager help` for the full reference. The
`/plugins/com.mattermost.alertmanager/admin/inventory` page (sysadmin
only) provides an org-wide view including AM reachability badges,
inverse drift detection (receivers in AM that the plugin doesn't
track), and a route simulator form.

## Key features for daily use

- **Inline diagnostics** — every alert post includes 3 runbook-specific
  commands with WHERE / WHAT / READ context. Pre-filled with failing
  host, pod, namespace via per-alert label substitution.
- **Route simulation** — `validate --simulate` walks AM's loaded route
  tree against a label set you supply, no synthetic alert needed. Find
  dead routes and unexpected dispatches before they cost you an
  incident.
- **Schema validation in diffs** — `export --diff-against-loaded` runs
  the merged config through Alertmanager's own parser. Catches
  undefined-receiver references and malformed matchers before you
  paste.
- **Webhook rotation reminders** — set `WebhookRotationDays` in System
  Console, opt receivers in with `/alertmanager add ... on`. Sysadmins
  get DM'd when a receiver hasn't been rotated within the window. See
  [`docs/ROTATION.md`](docs/ROTATION.md).
- **Drift detection** — inventory page flags receivers in AM YAML that
  the plugin doesn't track (hand-edits + post-rotation gaps).

## Architecture (one paragraph)

The plugin creates Mattermost incoming webhooks owned by the
`@alertmanagerbot` user. Each is referenced from `alertmanager.yml`
as the `api_url` of a `slack_configs` block. At alert delivery time,
Alertmanager templates the post body — including the inlined Quick
diagnostics from the runbook — and POSTs to the Mattermost webhook
URL. The plugin doesn't intercept alert payloads at runtime; it
only owns the YAML generation, webhook lifecycle, and the inventory
page. The 20 runbook markdown files ship embedded in the plugin
binary via Go's `embed.FS`.

For deeper context: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
explains why `slack_configs` rather than a plugin-owned receiver,
[`docs/KUBERNETES.md`](docs/KUBERNETES.md) covers HA deployment, and
[`docs/CONFIGURATION.md`](docs/CONFIGURATION.md) walks the plugin
settings.

## Building from source

```bash
make dist          # cross-compile all platforms + assemble tarball
make dist-host     # current host's arch only (faster for dev iter)
make deploy-local  # build + upload to localhost:8065
```

Requires export `MM_ADMIN_TOKEN` and `MM_SERVICESETTINGS_SITEURL` for deploy targets.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for build/test conventions,
runbook contribution guide, and the WHERE / WHAT / READ template that
every Quick diagnostics block follows.

Security disclosures: [`SECURITY.md`](SECURITY.md).
