# Configuration

End-to-end setup: plugin settings, what the plugin generates, and what your `alertmanager.yml` needs around it.

## Plugin settings

System Console → Plugins → Alertmanager:

| Setting | Required? | Notes |
|---|---|---|
| `WebhookHost` | When AM can't reach Mattermost on `SiteURL` | The host+port the plugin writes into `api_url:` values when rendering YAML. For Docker compose: `http://host.docker.internal:8065`. For Kubernetes: cluster-internal MM service URL (e.g. `http://mattermost.mattermost.svc.cluster.local:8065`). Bare-metal where AM and MM share a host: leave empty. |

That's the only plugin setting. The `AlertConfigsJSON` field in System Console → Plugins → Alertmanager → Advanced is the JSON-serialized receiver list — managed by slash commands, not hand-edited.

## What the plugin generates

When you run `/alertmanager add <team> <channel> <am-url> [set]`:

1. Resolves the destination channel (auto-creates if missing)
2. Mints an ephemeral PAT for the calling sysadmin, uses it to create one Mattermost incoming webhook per receiver in the chosen set, revokes the PAT
3. Stores each receiver in plugin config, named `<runbook-slug>--<channel-slug>`
4. Renders two YAML fragments and DMs them to the calling user:
   - `alertmanager-receivers.yml` — paste under `receivers:`
   - `alertmanager-routes.yml` — paste under `route.routes:`

The plugin handles `slack_configs` blocks and the matching `routes:` entries. Hook IDs are baked into the rendered `api_url` values; the plugin remembers them so future `/alertmanager rotate` or `/alertmanager remove` can find the right webhook to act on.

## What your `alertmanager.yml` needs around the plugin's output

The plugin generates the `receivers:` and the `route.routes:` block. You provide the surrounding structure:

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
  # Can be a no-op or any delivery target you want for unrouted alerts.
  - name: default-fallback
    # optional - configure as you like, or leave empty for no-op

  # <-- PASTE FROM alertmanager-receivers.yml HERE -->

# OPTIONAL — inhibit rules (suppress lower-severity when higher is firing)
inhibit_rules:
  - source_matchers: [severity="critical"]
    target_matchers: [severity="warning"]
    equal: ['alertname', 'cluster']
```

### Required fields

These must exist for AM to start at all:

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
| `inhibit_rules` | When you have severity-based pairing (e.g., warning version of an alert should not fire when critical is already firing) |
| `time_intervals` / `mute_time_intervals` | Scheduled silence windows (maintenance hours, weekends, etc.) |
| `templates` | External template files. Not needed — plugin bakes templates inline in each receiver's `slack_configs`. |

## What your Prometheus rules need

For the plugin-generated routes to dispatch alerts to the right receiver:

```yaml
- alert: HighCPUUsage
  expr: avg(rate(node_cpu_seconds_total[5m])) > 0.8
  for: 10m
  labels:
    severity: critical
    runbook: high-cpu-usage      # ← matches a plugin receiver's base slug
  annotations:
    summary: "Node CPU above 80% for 10 minutes"
    description: "..."
    # runbook_url optional — plugin template falls back to its
    # in-plugin runbook page if not set
```

The `runbook` label is what the plugin-generated routes match on. The value must equal the receiver's base slug (e.g., `high-cpu-usage`, not `high-cpu-usage--alert-slo-channel`). Routes auto-translate the base slug → suffixed receiver name.

Alerts without a `runbook` label fall through to `route.receiver` (the default fallback).

## Receiver naming convention

The plugin names every receiver `<runbook-slug>--<channel-slug>`:

```
high-cpu-usage--alert-slo-channel
high-cpu-usage--oncall-critical
database-connectivity-loss--dba-team-channel
```

The `--` separator is unambiguous because neither side contains it (Mattermost channel slugs and runbook filenames use single hyphens, never doubles).

The suffix guarantees uniqueness when the same runbook is delivered to multiple channels. Routes are auto-generated to fan out alerts to all receivers that share a base slug — see fan-out below.

## Fan-out: one alert, multiple channels

To deliver the same runbook to multiple channels (e.g., CPU alerts to both team channel AND oncall pager), run `/alertmanager add` once per destination:

```
/alertmanager add testing alert-slo-channel http://host.docker.internal:9093 compute
/alertmanager add testing oncall-critical http://host.docker.internal:9093 compute
```

The plugin creates 6 receivers in each channel, all channel-suffixed:
- `high-cpu-usage--alert-slo-channel`
- `high-cpu-usage--oncall-critical`
- (and the rest of the compute set, in both)

`/alertmanager export` (or the next `/alertmanager add` DM) produces a routes block like:

```yaml
routes:
  - matchers: [runbook="high-cpu-usage"]
    receiver: high-cpu-usage--alert-slo-channel
    continue: true                               # keep evaluating after match
  - matchers: [runbook="high-cpu-usage"]
    receiver: high-cpu-usage--oncall-critical
```

`continue: true` makes AM keep evaluating routes after a match, so the alert hits both receivers and both channels get the post.

## Channel auto-creation

The destination channel doesn't have to exist before you run `/alertmanager add`. If missing, the plugin creates it as an open channel under the named team. The bot user (`@alertmanagerbot`) is the channel creator. The plugin doesn't add anyone else to the channel — that's a Mattermost admin concern after creation.

## Multiple Alertmanagers

The plugin supports multiple AM backends as independent registrations:

```
/alertmanager add testing alerts-east http://alertmanager.east.example.com:9093 compute
/alertmanager add testing alerts-west http://alertmanager.west.example.com:9093 compute
```

Each gets its own channel binding. `/alertmanager status` (when invoked in either channel) queries the AM URL bound to that channel's receivers. Independent error handling — failure on one doesn't affect the other.

### HA Alertmanager (peers of the same logical instance)

Every peer fires its configured webhooks independently for the same alert group. The Mattermost incoming webhook receiver doesn't dedup by default, so identical posts may appear if multiple peers send simultaneously.

Mitigations from least to most invasive:

- Set `external_labels` identical across peers so they group the same way at the AM side and one peer becomes the primary notifier
- Front Mattermost with a deduplication proxy (out of scope for this plugin)
- Configure only one peer to send webhooks (loses redundancy)
