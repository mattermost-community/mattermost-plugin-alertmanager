# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-06-10

Initial release. Bridges Prometheus Alertmanager to Mattermost via
native incoming webhooks, with 20 canonical SRE runbook receivers
spanning compute, application, database, storage, networking, and
observability categories.

### Added

- Slash commands (all alphabetized, all channel-scoped where it
  makes sense):
  - `/alertmanager add <team> <channel> <am-url> [set]` — bulk-create
    receivers for a named runbook set (`all`, `application`,
    `compute`, `database`, `networking`, `observability`, `storage`)
  - `/alertmanager remove <name|set|all> [--force]` — delete a
    receiver, a runbook set, or every receiver in the channel
  - `/alertmanager rotate <name>` — delete + recreate the webhook
    with a new hook-id
  - `/alertmanager reconcile` — manual orphan prune (also runs
    automatically every 5 minutes)
  - `/alertmanager list` — receivers bound to the current channel
  - `/alertmanager config <name>` — detail card + slack_configs YAML
  - `/alertmanager export` — DM the assembled receivers.yml +
    routes.yml for the channel
  - `/alertmanager validate [name|set] [--webhook-test] [--end-to-end]` —
    pipeline diagnostics (AM reach, receiver-loaded-in-AM check,
    optional webhook test post, optional synthetic alert delivery)
  - `/alertmanager alerts` / `silences` / `status` — Alertmanager
    API queries, output grouped by Alertmanager URL (one section
    per backend, not per receiver)
  - `/alertmanager expire_silence <name> <silence-id>`
  - `/alertmanager docs [topic]` — embedded documentation
  - `/alertmanager about` — version, settings, receiver counts,
    reconciler health, jump-off links
  - `/alertmanager help`

- HTTP endpoints (sysadmin-only, served from the plugin's ServeHTTP):
  - `/admin/inventory` — sortable cross-channel inventory page with
    AM reachability + per-receiver loaded-in-AM badges, search,
    group-by-channel / group-by-team, CSV export
  - `/metrics` — Prometheus-format scrape endpoint, bearer-token
    auth (404 when token unset)

- Background reconciler that prunes receivers whose Mattermost
  webhook was deleted out-of-band. Uses `pluginapi/cluster.Schedule`
  for leader election across HA Mattermost pods — only one pod
  reconciles at a time. Mints + revokes a short-lived sysadmin PAT
  per cycle since plugin RPC doesn't expose webhook CRUD.

- Channel-suffix receiver naming (`<slug>--<channel>`) so the same
  runbook can fan out to multiple channels without collisions.

- Multi-cluster support via per-receiver `WebhookHostOverride`
  (`/alertmanager add --webhook-host=<url>`) — one Mattermost
  serving multiple Alertmanagers reachable via different network
  paths.

- Self-signed Alertmanager certificate support via
  `AlertManagerCABundle` System Console setting.

- Auto-delete janitor for DM'd YAML attachments — `AssembledYAMLTTLHours`
  setting controls retention (0 = disabled).

- Embedded runbooks rendered to static HTML at bundle time
  (`build/render-docs`).

- `samples/prometheus-rules.yaml` — alert rules covering all 20
  runbooks; emits the `runbook: <slug>` label that the plugin's
  routes block matches on.

- Sysadmin and channel-team-admin permission tiers (no
  custom-role machinery).

- Audit logging for privileged operations (add, remove, rotate,
  validate).

### Security

- Webhook URLs and basic-auth credentials never echoed in chat
  output. The detail-card view shows username but masks password.
- Metrics endpoint disabled by default; enabling requires setting
  a token.
- Channel-scoping enforced across all slash commands — a user in
  `#web-alerts` cannot enumerate or retrieve receiver YAML for
  `#db-alerts` via slash command.

[Unreleased]: https://github.com/christopherfickess/mattermost-plugin-alertmanager/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/christopherfickess/mattermost-plugin-alertmanager/releases/tag/v1.0.0
