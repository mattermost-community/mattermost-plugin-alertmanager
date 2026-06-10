# Mattermost Alertmanager Plugin

Sets up Prometheus Alertmanager → Mattermost alert delivery via native
incoming webhooks. The plugin creates the Mattermost webhook (owned by
the `@alertmanagerbot` identity) and generates the templated
`slack_configs` YAML block for `alertmanager.yml`. No per-config tokens
to manage — authentication is whatever Mattermost's native incoming
webhook system uses (the long random hook-id in the URL).

**Status:** v1.0 in development. This is the federal-org rewrite of the
upstream [cpanato/mattermost-plugin-alertmanager](https://github.com/cpanato/mattermost-plugin-alertmanager)
with the architecture redesigned around `slack_configs` + native webhooks
rather than a plugin-owned webhook receiver.

## Quick start

Once installed and enabled (sysadmin):

```
/alertmanager add sre-prod-critical sre alerts-sre-prod-critical http://alertmanager.monitoring.svc.cluster.local:9093
```

The plugin will create the Mattermost incoming webhook, register the
receiver, and print the Alertmanager YAML block ready to paste into
`alertmanager.yml`. After the paste + Alertmanager reload, alerts post
into `~alerts-sre-prod-critical` formatted by the `standard` template.

Pick a different template by appending its name as the last arg:

```
/alertmanager add sre-oncall sre alerts-oncall http://alertmanager:9093 runbook
```

Available templates: `standard`, `rich`, `minimal`, `runbook`. List with
`/alertmanager templates`.

## Slash commands

Run `/alertmanager help` once installed. Full reference comes with the
v1.0 docs work (task #38).

## Building

```bash
make dist            # multi-arch tarball (~80 MiB)
make dist-host       # current host's arch only (~17 MiB, for dev iter)
make deploy-host-local  # build + upload to localhost:8065
```

Requires `MM_ADMIN_TOKEN` env var for deploy targets.

## License

Apache 2.0.
