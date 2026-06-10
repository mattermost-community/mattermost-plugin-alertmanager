# Slash commands

All commands dispatch through `/alertmanager <subcommand>`. Every read command is channel-scoped — you only see receivers bound to the channel where you ran the command.

## Quick reference

| Subcommand | Args | Purpose | Sysadmin? |
|---|---|---|---|
| `add` | `<team> <channel> <am-url> [set]` | Create receivers for a runbook set, DM assembled `receivers.yml` | yes |
| `remove` | `<name>` \| `<set> --force` \| `all --force` | Delete one receiver, one set, or all receivers in this channel | yes |
| `rotate` | `<name>` | Recreate the underlying Mattermost webhook with a new hook-id | yes |
| `reconcile` | _(none)_ | Prune entries whose Mattermost webhook was deleted out-of-band | yes |
| `export` | _(none)_ | DM yourself a fresh `receivers.yml` for all receivers in this channel | yes |
| `list` | _(none)_ | Table of receivers bound to this channel | any user |
| `config` | `<name>` | Detail card + `slack_configs` YAML for one receiver | yes |
| `alerts` / `silences` / `status` | _(none)_ | Query Alertmanager APIs for receivers in this channel | any user |
| `expire_silence` | `<name> <silence-id>` | Expire an active AM silence on the named receiver | any user |
| `docs` | `[topic]` | List embedded docs or print one | any user |
| `help` / `about` | _(none)_ | Self-explanatory | any user |

System_admin gates apply to mutations (`add`, `remove`, `rotate`, `reconcile`, `export`, `config`) — these reveal webhook URLs (channel-bound bearer tokens) or change durable state.

## How receiver names work

The plugin names receivers `<runbook-slug>--<channel-slug>`, e.g. `high-cpu-usage--alert-slo-channel`. The `--` separator is the boundary between the runbook (what the alert is about) and the channel (where it's delivered).

When invoking commands from chat, you can use either form:

```
/alertmanager config high-cpu-usage--alert-slo-channel    # full name
/alertmanager config high-cpu-usage                       # short form
```

The short form resolves to a receiver bound to the current channel. If the same runbook is bound to multiple channels (fan-out scenarios), the short form picks the one bound to *this* channel — disambiguates without you specifying.

## Why channel-suffixed names

The same runbook can be subscribed by multiple channels (e.g., `high-cpu-usage` delivered to both a team channel AND an oncall channel for fan-out). Alertmanager requires each `receiver:` in `alertmanager.yml` to have a unique name, so the channel suffix is what makes them distinguishable in the AM config:

```yaml
routes:
  - matchers: [runbook="high-cpu-usage"]
    receiver: high-cpu-usage--alert-slo-channel
    continue: true                               # fan-out
  - matchers: [runbook="high-cpu-usage"]
    receiver: high-cpu-usage--oncall-critical
```

To set this up: run `/alertmanager add` once per destination channel. The plugin auto-suffixes the receiver names; you wire the AM routes with `continue: true` for the fan-out.

## Worked examples

### First-time setup — all 20 canonical receivers in one channel

```
/alertmanager add testing alert-slo-channel http://host.docker.internal:9093
```

Creates every embedded runbook (the `all` set — 20 receivers) bound to `~alert-slo-channel`. The summary lands in the channel; the assembled `alertmanager-receivers.yml` lands in your DM with `@alertmanagerbot`.

To create just one category:

```
/alertmanager add testing alert-slo-channel http://host.docker.internal:9093 database
```

Sets: `all` (default, every embedded runbook), `compute`, `application`, `database`, `storage`, `networking`, `observability`.

### Fan out an alert to a second channel

Already have `~alert-slo-channel` receiving the `compute` set. Want CPU alerts to also go to `~oncall-critical`:

```
/alertmanager add testing oncall-critical http://host.docker.internal:9093 compute
```

Same set, second channel → plugin creates `high-cpu-usage--oncall-critical` (and friends), DMs you the new YAML. Append the YAML to your `alertmanager.yml`'s `receivers:` block, add the AM routes with `continue: true` for fan-out, reload AM.

### See what's bound to this channel

```
/alertmanager list
```

Prints a table — receiver name, team, channel, Alertmanager URL. No webhook URLs. Safe to run with non-admins watching.

### Show one receiver's YAML

```
/alertmanager config high-cpu-usage
```

(Short form — resolves to `high-cpu-usage--<current-channel>`.)

Output: metadata card (team, channel, AM URL, webhook ID, runbook default URL), the `slack_configs` YAML block, the AM reload command, and quick-action links to rotate/remove.

### Rotate a webhook URL

If a webhook URL leaked (someone pasted it publicly), regenerate it without touching anything else:

```
/alertmanager rotate high-cpu-usage
```

Plugin creates a new Mattermost webhook in the same channel, deletes the old one, updates the stored hook-id, re-renders the YAML with the new URL. Update `alertmanager.yml` and reload AM — old URL returns 404 from Mattermost immediately.

### Update existing receivers after a plugin upgrade

Plugin templates can change between releases. To re-render every receiver in this channel with the latest template without rotating webhooks:

```
/alertmanager export
```

DMs you a freshly-rendered `receivers.yml`. Replace the `receivers:` block in your `alertmanager.yml` with the file contents. Hook IDs are preserved — your existing api_url values stay valid.

### Bulk cleanup

Three patterns, all channel-scoped:

```
/alertmanager remove <name>                  # one receiver, no --force needed
/alertmanager remove compute --force         # one set in this channel (6 receivers)
/alertmanager remove all --force             # every receiver in this channel
```

For set or `all` targets, run without `--force` first to see a dry-run preview of what would be deleted, then re-run with `--force` to confirm. Single-receiver remove doesn't require `--force` because the name is explicit (low blast radius).

The `remove` autocomplete dropdown shows `all` plus the six category sets (compute, application, database, storage, networking, observability) — pick one or type a receiver name freely.

### Find orphaned receivers

If you deleted a webhook via System Console manually, the plugin's config entry becomes an orphan. The reconciler runs every 5 minutes to prune these automatically, but to trigger immediately:

```
/alertmanager reconcile
```

Reports how many orphans (if any) it removed.

