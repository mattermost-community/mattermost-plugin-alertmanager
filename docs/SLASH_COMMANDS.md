# Slash commands

All commands dispatch through `/alertmanager <subcommand>`. Every
read command is channel-scoped — you only see receivers bound to
the channel where you ran the command.

## Command map

Eight top-level commands. Five of them are groups with their own subcommands. Typeahead lists every level alphabetically. Commands take plain words and `key=value` pairs, never `--flags`.

```
/alertmanager
├─ alerts [firing|status]            bare = firing
├─ silence create <ttl> <target> | list | expire <name> <id>
├─ config                            bare = list this channel's receivers
│   ├─ <name>                        detail card for one receiver
│   ├─ add | add-custom | validate
│   └─ rotate | remove
├─ format
│   ├─ export [standard|crd]         config + rules for this channel
│   ├─ diff [standard|crd]           Alertmanager config only
│   └─ rules [standard|crd]          sample Prometheus rules only
├─ admin                             sysadmin only
│   └─ repair [prune] [force] | acknowledge | allowlist | metrics-token
├─ docs
├─ about
└─ help
```

The old top-level names (`list`, `show`, `status`, `silences`, `expire_silence`,
`add`, `add-custom`, `remove`, `rotate`, `validate`, `export`, `acknowledge`,
`allowlist`, `metrics-token`, `reconcile`, `repair`) were removed and now reply
`Unknown subcommand`. `rules`, `config export` and `config diff` moved under `format` and reply with
the new command. Any `--flag` is rejected with the plain-word spelling: `--force` is `force`,
`--namespace=ops` is `namespace=ops`, `--format=crd` is `crd`.

## Getting help

Type `help` after any command. The detail grows as you go deeper: the top level is
a short list, a group lists its subcommands with examples, and a single command
shows its options, who can run it, and examples.

```
/alertmanager help                    # every top-level command, one line each
/alertmanager config help             # the subcommands of config
/alertmanager config add help         # options, permissions, examples for one command
/alertmanager help config add         # same as: config add help
```

A group typed with no subcommand (`/alertmanager admin`) or with an unknown one
shows that group's help. A trailing `help` only counts when the words before it are
a real command, so a custom receiver named `help` still works as an argument.

## Quick reference

| Command | Args | Purpose | Who can run it |
|---|---|---|---|
| `alerts` | `[firing]` | Firing alerts whose `runbook=` label matches a receiver in this channel. | any user |
| `alerts status` | _(none)_ | Alertmanager version + uptime per backend | any user |
| `silence create` | `<TTL> <alert\|group\|all>` | Mute matching alerts in Alertmanager. TTL is required: `30m`, or whole hours `1h`–`72h`. An alert name or a group can use the full range. `all` is every runbook in this channel, is capped at `1h`, and requires system_admin. The silence lives in Alertmanager, so other channels that receive the same alert go quiet too. A custom receiver has no `runbook` label, so a group or `all` does not match it. `silence <TTL> <target>` without `create` also works. | alert or group: team_admin. `all`: sysadmin |
| `silence list` | _(none)_ | Active Alertmanager silences, grouped by AM URL | any user |
| `silence expire` | `<name> <silence-id>` | Expire a silence created from this channel with `silence create` | team admin |
| `config` / `config list` | _(none)_ | Receivers bound to this channel (table with Rotated column) | any user |
| `config <name>` | `<name>` | Detail card for one receiver. Not offered in typeahead, because Mattermost cannot mix subcommands and free text at one level. | team_admin or sysadmin |
| `config add` | `<am-url> [target] [standard\|crd] [on] [namespace=]` | Create receivers in the channel you run it in, for a group set OR individual runbook slug. One shared Mattermost webhook per add invocation. DMs assembled `receivers.yml` + `routes.yml` (`standard`, the default), or an `AlertmanagerConfig` v1alpha1 + Secret (`crd`; `namespace=` default `monitoring`). The rules for the runbooks in that add are attached to the same DM. Trailing `on` opts in to rotation reminders. The channel must already exist, and DMs and group messages are rejected. | team_admin of this channel's team, or sysadmin |
| `config add-custom` | `<am-url> <name> [standard\|crd] [on] [namespace=<ns>] [webhook-host=<url>]` | In the channel you run it in, create ONE generic (non-runbook) receiver named `<name>--<team>-<channel>` with its own webhook. `standard` (default) DMs plain `alertmanager.yml`; `crd` DMs Prometheus Operator objects; `on` opts into rotation reminders. No `runbook=` route is generated — you wire the matcher manually (the DM includes a commented stub). For custom alerts that don't map to a shipped runbook. | sysadmin / team_admin |
| `config validate` | `[all\|group\|name] [webhook-test\|end-to-end\|simulate <labels>]` | Pipeline diagnostics or read-only route simulation | sysadmin / team_admin |
| `format export` | `[standard\|crd] [namespace=<ns>]` | DMs every receiver and route in this channel, with the Prometheus rules for the runbooks those receivers cover attached to the same message. `standard` (default): `alertmanager.yml` pieces plus a plain rules file. `crd`: AlertmanagerConfig + Secret plus a PrometheusRule named `mattermost-alertmanager-rules-<team>-<channel>` (`namespace=` defaults to `monitoring`). | team_admin or sysadmin |
| `format diff` | `[standard\|crd]` | Alertmanager config only, no rules. Compares this channel to Alertmanager's loaded config. With no format, detects standard or CRD from the loaded config. | sysadmin |
| `format rules` | `[standard\|crd] [namespace=<ns>]` | Rules only, all 37, not limited to this channel. `standard` (default): links to view or download the plain rules file. `crd`: DMs a ready-to-apply `PrometheusRule` (`monitoring.coreos.com/v1`). | any user |
| `config rotate` | `<name>` \| `all overdue` | Recreate one webhook (new hook-id), or batch-rotate all overdue receivers in this channel | sysadmin / team_admin |
| `config remove` | `<name>` \| `<set> force` \| `all force` | Delete one receiver, one set, or all receivers in this channel | sysadmin / team_admin |
| `admin repair` | `[prune] [force]` | Bare is read-only: checks the receiver-list KV store and lists receivers whose webhook was deleted out of band. `prune` removes those receivers (the background job does the same every 5 minutes). `force` rewrites a corrupt KV blob from the plugin's last-known in-memory list. `force` is recovery-only. | sysadmin |
| `admin acknowledge` | `[add\|remove <name>]` | Hide a receiver from the orange AM-only inventory warning. Bare lists the hidden names. Use this when GitOps or another tool created the receiver on purpose. Alerts that receiver sends still fire. | sysadmin |
| `admin allowlist` | `[add\|remove <prefix>]` | Extra registry prefixes for `UnexpectedContainerImage`. Bare lists the shipped prefixes and yours. `add` and `remove` change the list baked into the next `/alertmanager format rules crd` file. The plugin does not patch a running PrometheusRule. | sysadmin |
| `admin metrics-token` | `[generate]` | Prometheus `/metrics` bearer token: bare shows status + endpoint; `generate` mints a new token (rotating the old one), reveals it once, and prints a ready-to-paste `scrape_config`. The `MetricsToken` setting is `secret:true` and can't be read back from System Console — regenerate here if lost. | sysadmin |
| `docs` | _(none)_ | Links to the documentation site, runbook library, and sample Prometheus rules. A topic argument is deprecated and only links to that page. | any user |
| `about` | _(none)_ | Plugin build info, this channel's receiver count, links (any user). Org-wide counts, reconciler health, and configured-setting presence are shown to sysadmins only. | any user |
| `help` | _(none)_ | Slash-command reference | any user |

Sysadmin / team_admin gating applies to commands that reveal webhook
URLs (channel-bound bearer tokens) or change durable state. Pure-read
commands (`config list`, `alerts`, `alerts status`, `about`) are open to any user
in the channel.

## What `silence` matches

`/alertmanager silence create` writes a silence in Alertmanager. It does not
change `slack_configs`.

* An **alert** (`HighCPUUsage`) matches `alertname` exactly. A team
  admin can set the TTL from `30m` through `72h`.
* A **group** (`compute`, `database`, `mattermost`, and the other
  scaffold sets) matches the `runbook` labels of that group's
  receivers in this channel. Same TTL range as one alert. `compute`
  does not include `database`. `all` is not a group here: the scaffold
  map also has an `all` key, and the command treats the word `all` as
  the channel-wide mute instead.
* **`all`** matches every runbook receiver in this channel. The TTL
  cannot be longer than `1h` (`30m` or `1h`). Only a system admin can
  run it, because it quiets every channel that receives those alerts
  on the same Alertmanager.
* A **custom receiver** (`add-custom`) has no `runbook` label. A group
  silence and `all` both skip it, so that alert keeps firing. Mute it
  by typing its alert name. This is the uncommon path: a custom
  receiver exists because the alert does not belong to a shipped
  runbook.

## How receiver names work

The plugin names receivers `<runbook-slug>--<team-slug>-<channel-slug>`,
e.g. `high-cpu-usage--sre-alert-slo-channel`. The `--` after the
runbook slug is the boundary between the runbook (what the alert is
about) and where it's delivered (team + channel). Team is included
because channel names repeat across teams (`town-square` is in every
team), so it's needed to keep receiver names globally unique.

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

To set this up: run `/alertmanager config add` once in each destination
channel. The plugin auto-suffixes the receiver names and emits
routes with `continue: true` so AM doesn't stop at the first match.

## Worked examples

### First-time setup — all 36 canonical receivers in one channel

Create the channel if it doesn't exist, then run this in it:

```
/alertmanager config add http://alertmanager:9093
```

Creates every embedded runbook (the `all` set — 36 receivers) bound
to `~alert-slo-channel` **behind one shared Mattermost webhook**.
The summary lands in the channel; the assembled
`alertmanager-receivers.yml` + `alertmanager-routes.yml` land in
your DM with `@alertmanagerbot`. Each receiver's `slack_configs`
block still carries its own runbook-specific text template — only
the `api_url` is shared.

To create just one category:

```
/alertmanager config add http://alertmanager:9093 database
```

Available group sets: `all` (default), `application`, `compute`,
`database`, `mattermost`, `networking`, `observability`, `security`,
`storage`. Each set add creates one shared webhook named
`<set>--<team>-<channel>`, mirroring the receiver-name format so the
System Console webhook list lines up with the receiver names (and the
team segment disambiguates channels that repeat across teams).

### Add one specific runbook (individual)

For a single receiver with its own dedicated webhook:

```
/alertmanager config add http://alertmanager:9093 high-cpu-usage
```

Creates one receiver named `high-cpu-usage--testing-alert-slo-channel` and
one Mattermost webhook with the identical name
`high-cpu-usage--testing-alert-slo-channel` (individual adds make the webhook
name match the receiver name exactly). Any runbook slug works — see
the Runbook library link from `/alertmanager docs` for the full list.

If you try to add an individual slug that's already part of an
existing group in this channel, the add is skipped with a clear
"already exists" message. Remove the receiver first
(`/alertmanager config remove high-cpu-usage`) if you want to detach it
from the group and re-add individually.

### Custom (non-runbook) receiver

For an alert that has no shipped runbook — a bespoke rule you wrote —
create a generic receiver with a name you choose:

```
/alertmanager config add-custom http://alertmanager:9093 slo-burn-rate
```

Creates one receiver named `slo-burn-rate--testing-sre-alerts`
and its own webhook. Because there's no runbook, the plugin does **not**
generate a `runbook=` route — the post is the raw alert, and **you must
wire the route yourself**. Run `/alertmanager format export` and you'll get the
receiver block plus a commented stub to fill in:

```yaml
# Custom (non-runbook) receivers — created via /alertmanager config add-custom.
#  - matchers: [ <your labels, e.g. alertname="ErrorBudgetBurn", severity="critical"> ]
#    receiver: slo-burn-rate--testing-sre-alerts
#    continue: true
```

Uncomment it, set the matcher(s) to whatever labels your Prometheus rule
emits, and paste under `route.routes:` in your `alertmanager.yml`. See
[Custom (non-runbook) receivers](CONFIGURATION.md) in the configuration
doc for the full walkthrough.

The name must be lowercase `[a-z0-9_-]`, contain no `--`, and not collide
with a runbook slug or a category set name (`all`, `compute`, …).

### Set up with rotation reminders

Pass `on` as the final argument to opt the receivers in to the
rotation reminder system:

```
/alertmanager config add http://alertmanager:9093 compute on
```

Two-tier opt-in: the global threshold lives at System Console →
Plugins → Alertmanager → `WebhookRotationDays`. Without that
threshold set to a non-zero value, the `on` flag is inert. Without
`on`, the global threshold doesn't apply to these specific
receivers. Both required. See [`ROTATION.md`](ROTATION.md).

### Fan out an alert to a second channel

Already have `~alert-slo-channel` receiving the `compute` set. Want
CPU alerts to also go to `~oncall-critical`? Switch to that channel and run:

```
/alertmanager config add http://alertmanager:9093 compute
```

Same set, second channel → plugin creates `high-cpu-usage--oncall-critical`
(and friends), DMs you the new YAML. Append to your `alertmanager.yml`'s
`receivers:` block, add the AM routes with `continue: true` for
fan-out, reload AM.

### Multi-cluster — same Mattermost, different AMs

Use `webhook-host=<url>` for per-receiver override. Run each command in its own channel (`~east-alerts`, `~west-alerts`):

```
/alertmanager config add http://am.east:9093 compute webhook-host=http://mattermost.east.svc:8065
/alertmanager config add http://am.west:9093 compute webhook-host=http://mattermost.west.svc:8065
```

The override takes precedence over the global `WebhookHost` setting
for those specific receivers. Useful when one Mattermost serves
multiple Alertmanagers reachable via different network paths.

### See what's bound to this channel

```
/alertmanager config list
```

Prints a table — receiver name, team, channel, Alertmanager URL,
Rotated age. No webhook URLs. Safe to run with non-admins watching.

The Rotated column shows `today`, `yesterday`, `N days ago`, or
`never`. Receivers opted-in to rotation reminders that are past the
global threshold get a `⚠️` prefix.

### Inspect a receiver

```
/alertmanager config high-cpu-usage
```

(Short form — resolves to `high-cpu-usage--<current-channel>`.)

Output: metadata card (team, channel, AM URL, webhook ID, runbook
default URL), the `slack_configs` YAML block, the AM reload command,
and quick-action links to rotate / remove.

Run without a name to see the full channel receiver list:

```
/alertmanager config
```

### Validate the pipeline

Three modes covering increasingly intrusive checks:

```
# Cheap, no side effects — checks AM reachable + receivers loaded in AM
/alertmanager config validate

# Read-only route simulation — walks AM's loaded route tree against
# supplied labels, reports which receivers would catch the alert
/alertmanager config validate simulate runbook=high-cpu-usage severity=critical

# Side-effect: POSTs a visible test message to each webhook
/alertmanager config validate webhook-test

# Side-effect: fires a synthetic alert through AM, watch the channel
/alertmanager config validate end-to-end

# Combine a group filter with a mode
/alertmanager config validate compute end-to-end
```

The typeahead after `validate` lists `all` plus only the groups that have a
receiver in the channel you are in. The `simulate` mode is the answer to "would my Prometheus rule's
labels actually route to the receiver I expect?" Empty `simulate`
(no labels supplied) prints a preset list of runbook-slug starter
expressions for copy-paste discoverability.

### Test every severity at once

`severity=<value>` controls which severity `end-to-end` fires. It is
typed by hand and does not appear in typeahead. Values: `warning` (default), `critical`, `info`, or `all`.

```
# Fire one synthetic at critical severity instead of the default warning
/alertmanager config validate high-cpu-usage end-to-end severity=critical

# Fire four synthetics per receiver: warning + critical + info + resolved
# Use this to visually verify every render path in one shot —
# sidebar colors (yellow/red/blue/green), the new [SEVERITY:Alertname]
# title format, and the [✓ RESOLVED:...] resolved variant.
/alertmanager config validate high-cpu-usage end-to-end severity=all
```

`severity=all` is per-receiver. A whole-channel test
(`/alertmanager config validate all end-to-end severity=all`) fires
4 × N alerts where N is the number of receivers — scope to one or
two receivers when running a visual smoke test, not the full set.

`severity=` has no effect without `end-to-end` (the
read-only modes don't fire alerts).

### Rotate a webhook URL

Single receiver (individual or legacy pre-v1.0.3):

```
/alertmanager config rotate high-cpu-usage
```

Plugin creates a new Mattermost webhook in the same channel, deletes
the old one, updates the stored hook-id, re-renders the YAML inline.
Update `alertmanager.yml` and reload AM — old URL returns 404 from
Mattermost immediately.

**Group-webhook rotation (v1.0.3+):** if the receiver belongs to a
group, rotating it rotates the **shared webhook** — every receiver
in that group gets the new URL. The response message lists every
affected receiver and DMs you the merged YAML bundle for paste-once
update. Example: `/alertmanager config rotate high-cpu-usage` on a receiver
created via `add ... compute` rotates the webhook for all 9 compute
receivers.

Batch all overdue (only opted-in receivers past the global
threshold):

```
/alertmanager config rotate all overdue
```

DMs you a merged YAML bundle with just the rotated receivers'
`slack_configs` + matching `routes:` block. Paste once, reload AM
once. See [`ROTATION.md`](ROTATION.md).

### Export this channel's config

Re-render every receiver in this channel with the latest template
without rotating webhooks:

```
/alertmanager format export
```

DMs you a freshly-rendered `receivers.yml` + `routes.yml`. Replace
the corresponding blocks in your `alertmanager.yml` with the file
contents. Hook IDs are preserved — existing api_url values stay
valid.

For Prometheus Operator deployments:

```
/alertmanager format export crd namespace=monitoring
```

### Diff your YAML against what AM has loaded

Catches drift between what the plugin would produce and what AM
actually has in memory right now:

```
/alertmanager format diff
```

Output includes:
- A channel-scoped diff: context lines show what AM currently has
  for **this channel only**, `+` lines show what would be added.
  Other channels' receivers are never shown — no noise, no redaction
  gymnastics.
- Schema validation result from running the merged config through
  `prometheus/alertmanager/config.Load` — the same parser AM uses
  at reload time.

**Prometheus Operator auto-detect:** if AM's loaded config shows
this channel's receivers under CRD-namespaced names
(`<namespace>/mattermost-alertmanager-<hash>/<receiver-name>`), the
diff automatically switches to CRD mode — "already loaded" detection
matches on the short receiver name rather than the full namespaced
path. To skip detection, add a format: `format diff crd` compares against the
namespaced form, `format diff standard` against plain receiver names. Use `/alertmanager format export crd` to get the
`AlertmanagerConfig` YAML to apply via `kubectl apply`.

### Bulk cleanup

Three patterns, all channel-scoped:

```
/alertmanager config remove <name>                  # one receiver, no force needed
/alertmanager config remove compute force         # one set in this channel
/alertmanager config remove all force             # every receiver in this channel
```

For set or `all` targets, run without `force` first to see a
dry-run preview of what would be deleted, then re-run with `force`
to confirm. Single-receiver remove doesn't require `force` because
the name is explicit (low blast radius).

The `remove` autocomplete dropdown shows `all` plus the eight category
sets (compute, application, database, mattermost, storage, networking,
observability, security) — pick one or type a receiver name freely.

### Find orphaned receivers

If you deleted a webhook via System Console manually, the plugin's
config entry becomes an orphan. The reconciler runs every 5 minutes
to prune these automatically, but to trigger immediately:

```
/alertmanager admin repair prune
```

Reports how many orphans (if any) it removed.

### Documentation site

```
/alertmanager docs
```

Replies with links to the documentation site, the runbook library, and the
sample Prometheus rules. Open the site and use the sidebar to browse
topics. The same site lives at
`/plugins/com.mattermost.alertmanager/public/help/home.html`.

`/alertmanager docs <topic>` (for example `docs kubernetes`) is deprecated.
It no longer prints the document in chat. It links to that page and the
site home.

### Plugin info

```
/alertmanager about
```

Reports plugin version, configured settings (with sensitive values
masked), receiver counts across all channels, reconciler health
(last run / last pruned count), and quick links to the inventory
page, docs, and runbook library.
