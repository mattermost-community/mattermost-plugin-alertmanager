# Configuration

End-to-end setup: plugin settings, what the plugin generates, and
what your `alertmanager.yml` needs around it.

## Plugin settings

System Console → Plugins → Alertmanager exposes 5 settings. All are
optional — the plugin runs with defaults on a fresh install.

### `WebhookHost`

The host+port the plugin writes into rendered `api_url:` values.
Defaults to `ServiceSettings.SiteURL` when empty.

Set this when Alertmanager reaches Mattermost over a different
network path than end users:

| Environment | Value |
|---|---|
| Docker compose | `http://host.docker.internal:8065` |
| Kubernetes | `http://mattermost.<namespace>.svc.cluster.local:8065` |
| Bare metal / shared host | Leave empty (SiteURL works) |

Format: `<scheme>://<host>:<port>` — no trailing slash, no path. The
runbook URLs embedded in alert post templates continue using SiteURL
since those are clicked by users in browsers, not consumed by
Alertmanager.

### `WebhookRotationDays`

When non-zero, the background reconciler DMs sysadmins about
receivers whose webhook hasn't been rotated within this many days.
Default `0` = feature disabled.

Reminder-only — the plugin never auto-rotates. Per-receiver opt-in
mandatory via `on` flag on `/alertmanager add`. See
[`ROTATION.md`](ROTATION.md) for the full playbook.

Recommended values:

| Value | Use it when |
|---|---|
| `0` (default) | No rotation discipline needed |
| `90` (quarterly) | Baseline security hygiene — recommended |
| `180` (semi-annual) | Lower-traffic channels |
| `365` (annual) | Minimal viable rotation cadence |

Values below `30` produce noise unless your secret-rotation policy
actually rotates that frequently.

### `AlertManagerCABundle`

PEM-encoded CA certificates used when the plugin queries
Alertmanager's REST API (`/alertmanager status`, `alerts`,
`silences`, `expire_silence`, route simulation). Concatenate
multiple certs in one block. Leave empty to trust only system CAs.

This setting only affects the plugin's outbound calls TO
Alertmanager. The webhook URLs Alertmanager POSTs to are
Mattermost-side and use whatever TLS Mattermost is configured with.

```
-----BEGIN CERTIFICATE-----
<your CA>
-----END CERTIFICATE-----
```

### `MetricsToken`

When set, exposes Prometheus-format metrics at
`/plugins/com.mattermost.alertmanager/metrics`. Prometheus scrapes
the endpoint using this token in the `Authorization: Bearer <token>`
header. Leave empty to disable the endpoint entirely (returns 404
when unset).

Generate a random token:

```bash
openssl rand -hex 32
```

Configure Prometheus to scrape with the same token:

```yaml
scrape_configs:
  - job_name: mattermost-alertmanager-plugin
    metrics_path: /plugins/com.mattermost.alertmanager/metrics
    authorization:
      type: Bearer
      credentials: <your token>
    static_configs:
      - targets: ['<mattermost-host>:443']
```

### `AssembledYAMLTTLHours`

The bot DMs sysadmins the assembled `alertmanager-receivers.yml` +
`alertmanager-routes.yml` files after every `/alertmanager add` and
`/alertmanager rotate all --overdue`. Those files contain
channel-bound webhook URLs (bearer tokens by URL).

This setting controls how many hours the DM'd files persist before
the auto-delete janitor removes them. Default `72`. Set to `0` to
disable auto-delete (files persist forever — not recommended for
production).

### Internal: `AlertConfigsJSON`

System Console → Plugins → Alertmanager → Advanced shows
`AlertConfigsJSON` — the JSON-serialized receiver list managed by
slash commands. Don't hand-edit unless you're recovering from a bug
or batch-importing from another tool. Schema:

```json
[
  {
    "name": "high-cpu-usage--alert-slo-channel",
    "team": "testing",
    "channel": "alert-slo-channel",
    "alertManagerURL": "http://alertmanager:9093",
    "webhookID": "<MM webhook ID>",
    "webhookHostOverride": "",
    "lastRotatedAt": "2026-06-11T12:34:56Z",
    "lastReminderAt": "0001-01-01T00:00:00Z",
    "rotationRemindersEnabled": false
  }
]
```

## What the plugin generates

When you run `/alertmanager add <team> <channel> <am-url> [set] [on]`:

1. Resolves the destination channel (auto-creates if missing).
2. Mints an ephemeral PAT for the calling sysadmin, uses it to
   create one Mattermost incoming webhook per receiver in the chosen
   set, revokes the PAT.
3. Stores each receiver in plugin config, named
   `<runbook-slug>--<team-slug>-<channel-slug>`. Stamps `LastRotatedAt = now`.
   If `on` was passed, sets `RotationRemindersEnabled = true`.
4. Renders two YAML fragments and DMs them to the calling user:
   - `alertmanager-receivers.yml` — paste under `receivers:`
   - `alertmanager-routes.yml` — paste under `route.routes:`

The plugin handles `slack_configs` blocks and the matching `routes:`
entries. Hook IDs are baked into the rendered `api_url` values; the
plugin remembers them so future `/alertmanager rotate` or
`/alertmanager remove` can find the right webhook to act on.

## What your `alertmanager.yml` needs around the plugin's output

The plugin generates the `receivers:` and the `route.routes:` block.
You provide the surrounding structure:

```yaml
# REQUIRED — global defaults
global:
  resolve_timeout: 5m         # how long after last notification before "resolved"

# REQUIRED — top-level routing
route:
  receiver: default-fallback   # catch-all for unrouted alerts (must exist below)
  group_by: ['alertname', 'cluster']
  group_wait: 30s              # delay before first notification
  group_interval: 5m           # delay between notifications for same group
  repeat_interval: 4h          # repeat if still firing (4h+ for prod)

  routes:
    # <-- PASTE FROM alertmanager-routes.yml HERE -->

# REQUIRED — receivers section
receivers:
  # REQUIRED — catch-all default referenced by `route.receiver` above.
  - name: default-fallback

  # <-- PASTE FROM alertmanager-receivers.yml HERE -->

# OPTIONAL — inhibit rules (suppress lower-severity when higher is firing)
inhibit_rules:
  - source_matchers: [severity="critical"]
    target_matchers: [severity="warning"]
    equal: ['alertname', 'cluster']
```

### Required fields

| Field | Why |
|---|---|
| `global` block | At least empty (`global: {}`) — AM parses for defaults |
| `route.receiver` | Top-level catch-all. Required even with sub-routes |
| At least one entry in `receivers:` | The receiver `route.receiver` references must exist |

### Recommended for production

| Field | Why |
|---|---|
| `route.group_by` | Without grouping you get one notification per alert per evaluation cycle = spam |
| `route.group_wait` | Lets AM coalesce a burst of related alerts into one notification |
| `route.group_interval` | Throttles updates to a still-firing group |
| `route.repeat_interval` | Controls re-notification cadence (4h+ for production; shorter for dev/testing) |

### Optional

| Field | When |
|---|---|
| `inhibit_rules` | Severity-based pairing (e.g., warning version should not fire when critical already firing) |
| `time_intervals` / `mute_time_intervals` | Scheduled silence windows (maintenance hours, weekends) |
| `templates` | External template files. Not needed — plugin bakes templates inline in each receiver's `slack_configs` |

## What your Prometheus rules need

For the plugin-generated routes to dispatch alerts to the right
receiver:

```yaml
- alert: HighCPUUsage
  expr: sum(rate(container_cpu_usage_seconds_total[5m])) by (namespace, pod) > 0.8
  for: 10m
  labels:
    severity: critical
    runbook: high-cpu-usage      # ← matches a plugin receiver's base slug
  annotations:
    summary: "Pod CPU above 80% for 10 minutes"
    description: "..."
```

The `runbook` label is what the plugin-generated routes match on.
The value must equal the receiver's base slug (e.g.,
`high-cpu-usage`, not `high-cpu-usage--alert-slo-channel`). Routes
auto-translate the base slug → suffixed receiver name.

Alerts without a `runbook` label fall through to `route.receiver`
(the default fallback).

### Required Prometheus labels per runbook

Each shipped runbook documents the labels its Quick diagnostics
section expects in a "Required Prometheus labels" footer. Most
expect at least `namespace` and `pod` (for compute, application,
storage runbooks) plus the runbook-specific label like `instance` /
`job` / `service`. Security runbooks differ — several use apiserver /
Falco labels (e.g. `k8s_ns_name`, `job`) instead. See the
corresponding `runbooks/*.md` file, or run `/alertmanager docs
requirements` for the full per-alert metric / label / tooling matrix.

If a label is missing on an incoming alert, the template falls back
to leaving the placeholder text in place — operators see
`<namespace>` rather than a substituted value. Validate with:

```
/alertmanager validate --simulate runbook=<slug> namespace=<ns> pod=<pod>
```

That walks AM's route tree against your label set without firing a
real alert.

`samples/prometheus-rules.yaml` ships a complete rule set covering
all 30 runbooks with the correct label patterns. Use it as a
starting point.

## Receiver naming convention

The plugin names every receiver `<runbook-slug>--<team-slug>-<channel-slug>`:

```
high-cpu-usage--sre-alert-slo-channel
high-cpu-usage--sre-oncall-critical
database-connectivity-loss--platform-dba-team-channel
```

The team slug is part of the name because channel names are unique
only *per team* — `town-square` exists in every team. Without it,
the same runbook delivered to same-named channels in different teams
would collide (Alertmanager requires globally-unique receiver names).

The `--` after the runbook slug is the parse boundary — runbook
filenames never contain doubles, so the runbook slug is always
recoverable. The `<team>-<channel>` tail uses a single hyphen and is
identity/display only (team and channel are also stored as separate
fields); global uniqueness is enforced by the plugin rejecting
duplicate names on save, not by the separator.

## Fan-out: one alert, multiple channels

To deliver the same runbook to multiple channels (e.g., CPU alerts
to both team channel AND oncall pager), run `/alertmanager add`
once per destination:

```
/alertmanager add testing alert-slo-channel http://alertmanager:9093 compute
/alertmanager add testing oncall-critical http://alertmanager:9093 compute
```

The plugin creates 6 receivers in each channel, all
channel-suffixed:

- `high-cpu-usage--alert-slo-channel`
- `high-cpu-usage--oncall-critical`
- (and the rest of the compute set, in both)

`/alertmanager export` (or the next `/alertmanager add` DM)
produces a routes block like:

```yaml
routes:
  - matchers: [runbook="high-cpu-usage"]
    receiver: high-cpu-usage--alert-slo-channel
    continue: true                               # keep evaluating after match
  - matchers: [runbook="high-cpu-usage"]
    receiver: high-cpu-usage--oncall-critical
    continue: true
```

`continue: true` on every plugin-generated route makes AM keep
evaluating routes after a match, so the alert hits both receivers
and both channels get the post. The plugin sets this
unconditionally — each runbook's matcher is unique, so `continue`
only changes behavior in the fan-out case, where it fixes the
otherwise-dead second route.

## Channel auto-creation

The destination channel doesn't have to exist before you run
`/alertmanager add`. If missing, the plugin creates it as an open
channel under the named team. The bot user (`@alertmanagerbot`) is
the channel creator. The plugin doesn't add anyone else to the
channel — that's a Mattermost admin concern after creation.

## Multiple Alertmanagers

The plugin supports multiple AM backends as independent
registrations:

```
/alertmanager add testing alerts-east http://alertmanager.east.example.com:9093 compute
/alertmanager add testing alerts-west http://alertmanager.west.example.com:9093 compute
```

Each gets its own channel binding. `/alertmanager status` (when
invoked in either channel) queries the AM URL bound to that
channel's receivers. Independent error handling — failure on one
doesn't affect the other.

For one Mattermost serving multiple Alertmanagers reachable via
different network paths (e.g., one MM serving K8s clusters in
different VPCs), use the per-receiver host override:

```
/alertmanager add testing alerts-east http://am.east:9093 compute --webhook-host=http://mattermost.east.svc:8065
/alertmanager add testing alerts-west http://am.west:9093 compute --webhook-host=http://mattermost.west.svc:8065
```

The override takes precedence over the global `WebhookHost` setting
at YAML render time for those specific receivers.

### HA Alertmanager (peers of the same logical instance)

Every peer fires its configured webhooks independently for the same
alert group. The Mattermost incoming webhook receiver doesn't
deduplicate by default, so identical posts may appear if multiple
peers send simultaneously.

Mitigations from least to most invasive:

- Set `external_labels` identical across peers so they group the
  same way at the AM side and one peer becomes the primary notifier
- Front Mattermost with a deduplication proxy (out of scope for
  this plugin)
- Configure only one peer to send webhooks (loses redundancy)

## Validating your configuration

Three commands cover the common verification paths:

```
# Cheap, read-only — checks AM reachable + receivers loaded in AM
/alertmanager validate

# Route simulation — walks AM's loaded route tree against a label set,
# reports which receivers would catch the alert. No alert fired.
/alertmanager validate --simulate runbook=high-cpu-usage severity=critical

# Diff your channel's assembled YAML against what AM has loaded,
# with schema validation via prometheus/alertmanager/config.Load
/alertmanager export --diff-against-loaded
```

The `--diff-against-loaded` mode catches undefined-receiver
references, malformed matchers, and route tree errors before the
operator pastes new YAML and reloads AM.

The admin inventory page at
`/plugins/com.mattermost.alertmanager/admin/inventory` provides the
same simulation as a GUI form (Mode / Type / Target / Channel /
Severity dropdowns) plus the org-wide inverse-drift view (receivers
in AM that the plugin doesn't track).
