# Slash commands

All commands dispatch through `/alertmanager <subcommand>`. Every
read command is channel-scoped — you only see receivers bound to
the channel where you ran the command.

## Quick reference

| Subcommand | Args | Purpose | Sysadmin? |
|---|---|---|---|
| `about` | _(none)_ | Plugin build info, settings, reconciler health, links | any user |
| `add` | `<team> <channel> <am-url> [target] [on]` | Create receivers for a group set OR individual runbook slug. One shared Mattermost webhook per add invocation. DMs assembled `receivers.yml` + `routes.yml`. Trailing `on` opts in to rotation reminders. | sysadmin / team_admin |
| `alerts` | _(none)_ | Currently firing alerts, grouped by Alertmanager URL | any user |
| `config` | `<name>` | Detail card + `slack_configs` YAML for one receiver | sysadmin / team_admin |
| `docs` | `[topic]` | List or print embedded docs (alerts / architecture / configuration / development / kubernetes / requirements / rotation / slash_commands) | any user |
| `expire_silence` | `<name> <silence-id>` | Expire an active Alertmanager silence | any user |
| `export` | `[--diff-against-loaded]` | DM a fresh `receivers.yml` + `routes.yml` for this channel. With `--diff-against-loaded`, diff against AM's loaded config + schema-validate. | sysadmin / team_admin |
| `help` | _(none)_ | Slash-command reference | any user |
| `list` | _(none)_ | Receivers bound to this channel (table with Rotated column) | any user |
| `reconcile` | _(none)_ | Prune entries whose Mattermost webhook was deleted out-of-band | sysadmin |
| `remove` | `<name>` \| `<set> --force` \| `all --force` | Delete one receiver, one set, or all receivers in this channel | sysadmin / team_admin |
| `rotate` | `<name>` \| `all --overdue` | Recreate one webhook (new hook-id), or batch-rotate all overdue receivers in this channel | sysadmin / team_admin |
| `silences` | _(none)_ | Active Alertmanager silences, grouped by AM URL | any user |
| `status` | _(none)_ | Alertmanager version + uptime per backend | any user |
| `validate` | `[name\|set] [--webhook-test\|--end-to-end\|--simulate <labels>]` | Pipeline diagnostics or read-only route simulation | sysadmin / team_admin |

Sysadmin / team_admin gating applies to commands that reveal webhook
URLs (channel-bound bearer tokens) or change durable state. Pure-read
commands (`list`, `alerts`, `status`, `about`) are open to any user
in the channel.

## How receiver names work

The plugin names receivers `<runbook-slug>--<channel-slug>`, e.g.
`high-cpu-usage--alert-slo-channel`. The `--` separator is the
boundary between the runbook (what the alert is about) and the
channel (where it's delivered).

When invoking commands from chat, you can use either form:

```
/alertmanager config high-cpu-usage--alert-slo-channel    # full name
/alertmanager config high-cpu-usage                       # short form
```

The short form resolves to a receiver bound to the current channel.
If the same runbook is bound to multiple channels (fan-out
scenarios), the short form picks the one bound to *this* channel —
disambiguates without you specifying.

## Why channel-suffixed names

The same runbook can be subscribed by multiple channels (e.g.,
`high-cpu-usage` delivered to both a team channel AND an oncall
channel for fan-out). Alertmanager requires each `receiver:` in
`alertmanager.yml` to have a unique name, so the channel suffix is
what makes them distinguishable in the AM config:

```yaml
routes:
  - matchers: [runbook="high-cpu-usage"]
    receiver: high-cpu-usage--alert-slo-channel
    continue: true                               # fan-out
  - matchers: [runbook="high-cpu-usage"]
    receiver: high-cpu-usage--oncall-critical
    continue: true
```

To set this up: run `/alertmanager add` once per destination
channel. The plugin auto-suffixes the receiver names and emits
routes with `continue: true` so AM doesn't stop at the first match.

## Worked examples

### First-time setup — all 20 canonical receivers in one channel

```
/alertmanager add testing alert-slo-channel http://alertmanager:9093
```

Creates every embedded runbook (the `all` set — 20 receivers) bound
to `~alert-slo-channel` **behind one shared Mattermost webhook**.
The summary lands in the channel; the assembled
`alertmanager-receivers.yml` + `alertmanager-routes.yml` land in
your DM with `@alertmanagerbot`. Each receiver's `slack_configs`
block still carries its own runbook-specific text template — only
the `api_url` is shared.

To create just one category:

```
/alertmanager add testing alert-slo-channel http://alertmanager:9093 database
```

Available group sets: `all` (default), `application`, `compute`,
`database`, `networking`, `observability`, `security`, `storage`. Each
set add creates one shared webhook named `Alertmanager: <set>--<channel>`.

### Add one specific runbook (individual)

For a single receiver with its own dedicated webhook:

```
/alertmanager add testing alert-slo-channel http://alertmanager:9093 high-cpu-usage
```

Creates one receiver named `high-cpu-usage--alert-slo-channel` and
one Mattermost webhook named `Alertmanager: high-cpu-usage--alert-slo-channel`.
Any runbook slug works — see `/alertmanager docs` for the full list.

If you try to add an individual slug that's already part of an
existing group in this channel, the add is skipped with a clear
"already exists" message. Remove the receiver first
(`/alertmanager remove high-cpu-usage`) if you want to detach it
from the group and re-add individually.

### Set up with rotation reminders

Pass `on` as the final argument to opt the receivers in to the
rotation reminder system:

```
/alertmanager add testing alert-slo-channel http://alertmanager:9093 compute on
```

Two-tier opt-in: the global threshold lives at System Console →
Plugins → Alertmanager → `WebhookRotationDays`. Without that
threshold set to a non-zero value, the `on` flag is inert. Without
`on`, the global threshold doesn't apply to these specific
receivers. Both required. See [`ROTATION.md`](ROTATION.md).

### Fan out an alert to a second channel

Already have `~alert-slo-channel` receiving the `compute` set. Want
CPU alerts to also go to `~oncall-critical`:

```
/alertmanager add testing oncall-critical http://alertmanager:9093 compute
```

Same set, second channel → plugin creates `high-cpu-usage--oncall-critical`
(and friends), DMs you the new YAML. Append to your `alertmanager.yml`'s
`receivers:` block, add the AM routes with `continue: true` for
fan-out, reload AM.

### Multi-cluster — same Mattermost, different AMs

Use `--webhook-host=<url>` for per-receiver override:

```
/alertmanager add testing east-alerts http://am.east:9093 compute --webhook-host=http://mattermost.east.svc:8065
/alertmanager add testing west-alerts http://am.west:9093 compute --webhook-host=http://mattermost.west.svc:8065
```

The override takes precedence over the global `WebhookHost` setting
for those specific receivers. Useful when one Mattermost serves
multiple Alertmanagers reachable via different network paths.

### See what's bound to this channel

```
/alertmanager list
```

Prints a table — receiver name, team, channel, Alertmanager URL,
Rotated age. No webhook URLs. Safe to run with non-admins watching.

The Rotated column shows `today`, `yesterday`, `N days ago`, or
`never`. Receivers opted-in to rotation reminders that are past the
global threshold get a `⚠️` prefix.

### Show one receiver's YAML

```
/alertmanager config high-cpu-usage
```

(Short form — resolves to `high-cpu-usage--<current-channel>`.)

Output: metadata card (team, channel, AM URL, webhook ID, runbook
default URL), the `slack_configs` YAML block, the AM reload command,
and quick-action links to rotate / remove.

### Validate the pipeline

Three modes covering increasingly intrusive checks:

```
# Cheap, no side effects — checks AM reachable + receivers loaded in AM
/alertmanager validate

# Read-only route simulation — walks AM's loaded route tree against
# supplied labels, reports which receivers would catch the alert
/alertmanager validate --simulate runbook=high-cpu-usage severity=critical

# Side-effect: POSTs a visible test message to each webhook
/alertmanager validate --webhook-test

# Side-effect: fires a synthetic alert through AM, watch the channel
/alertmanager validate --end-to-end

# Combine set filter + flag
/alertmanager validate compute --end-to-end
```

The `--simulate` mode is the answer to "would my Prometheus rule's
labels actually route to the receiver I expect?" Empty `--simulate`
(no labels supplied) prints a preset list of runbook-slug starter
expressions for copy-paste discoverability.

### Test every severity at once

`--severity=<value>` controls which severity the `--end-to-end` flag
fires. Values: `warning` (default), `critical`, `info`, or `all`.

```
# Fire one synthetic at critical severity instead of the default warning
/alertmanager validate high-cpu-usage --end-to-end --severity=critical

# Fire four synthetics per receiver: warning + critical + info + resolved
# Use this to visually verify every render path in one shot —
# sidebar colors (yellow/red/blue/green), the new [SEVERITY:Alertname]
# title format, and the [✓ RESOLVED:...] resolved variant.
/alertmanager validate high-cpu-usage --end-to-end --severity=all
```

`--severity=all` is per-receiver. A whole-channel test
(`/alertmanager validate all --end-to-end --severity=all`) fires
4 × N alerts where N is the number of receivers — scope to one or
two receivers when running a visual smoke test, not the full set.

The `--severity` flag has no effect without `--end-to-end` (the
read-only modes don't fire alerts).

### Rotate a webhook URL

Single receiver (individual or legacy pre-v1.0.3):

```
/alertmanager rotate high-cpu-usage
```

Plugin creates a new Mattermost webhook in the same channel, deletes
the old one, updates the stored hook-id, re-renders the YAML inline.
Update `alertmanager.yml` and reload AM — old URL returns 404 from
Mattermost immediately.

**Group-webhook rotation (v1.0.3+):** if the receiver belongs to a
group, rotating it rotates the **shared webhook** — every receiver
in that group gets the new URL. The response message lists every
affected receiver and DMs you the merged YAML bundle for paste-once
update. Example: `/alertmanager rotate high-cpu-usage` on a receiver
created via `add ... compute` rotates the webhook for all 6 compute
receivers.

Batch all overdue (only opted-in receivers past the global
threshold):

```
/alertmanager rotate all --overdue
```

DMs you a merged YAML bundle with just the rotated receivers'
`slack_configs` + matching `routes:` block. Paste once, reload AM
once. See [`ROTATION.md`](ROTATION.md).

### Update existing receivers after a plugin upgrade

Plugin templates can change between releases. To re-render every
receiver in this channel with the latest template without rotating
webhooks:

```
/alertmanager export
```

DMs you a freshly-rendered `receivers.yml` + `routes.yml`. Replace
the corresponding blocks in your `alertmanager.yml` with the file
contents. Hook IDs are preserved — existing api_url values stay
valid.

### Diff your YAML against what AM has loaded

Catches drift between what the plugin would produce and what AM
actually has in memory right now:

```
/alertmanager export --diff-against-loaded
```

Output includes:
- Side-by-side diff between AM's loaded config and the plugin's
  current channel-scoped output
- Schema validation result from running the merged config through
  `prometheus/alertmanager/config.Load` — the same parser AM uses
  at reload time

Catches undefined-receiver references, malformed matchers, and
route-tree errors before the operator pastes new YAML and triggers
a reload that AM would reject.

Secrets in other channels' receivers are masked in the diff display
(operator-context info only; their secrets are not yours to see).
Own-channel additions stay un-redacted — the operator needs them to
paste.

### Bulk cleanup

Three patterns, all channel-scoped:

```
/alertmanager remove <name>                  # one receiver, no --force needed
/alertmanager remove compute --force         # one set in this channel
/alertmanager remove all --force             # every receiver in this channel
```

For set or `all` targets, run without `--force` first to see a
dry-run preview of what would be deleted, then re-run with `--force`
to confirm. Single-receiver remove doesn't require `--force` because
the name is explicit (low blast radius).

The `remove` autocomplete dropdown shows `all` plus the seven category
sets (compute, application, database, storage, networking,
observability, security) — pick one or type a receiver name freely.

### Find orphaned receivers

If you deleted a webhook via System Console manually, the plugin's
config entry becomes an orphan. The reconciler runs every 5 minutes
to prune these automatically, but to trigger immediately:

```
/alertmanager reconcile
```

Reports how many orphans (if any) it removed.

### Embedded docs

```
/alertmanager docs
/alertmanager docs configuration
/alertmanager docs rotation
```

Available topics: `alerts`, `architecture`, `configuration`,
`development`, `kubernetes`, `requirements`, `rotation`,
`slash_commands`. Content is rendered
inline in chat — for the full version, hit
`/plugins/com.mattermost.alertmanager/public/help/home.html` in a
browser.

### Plugin info

```
/alertmanager about
```

Reports plugin version, configured settings (with sensitive values
masked), receiver counts across all channels, reconciler health
(last run / last pruned count), and quick links to the inventory
page, docs, and runbook library.
