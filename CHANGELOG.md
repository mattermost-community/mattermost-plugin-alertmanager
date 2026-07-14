# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.1.0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/compare/v1.0.6...v1.1.0) (2026-07-14)


### Features

* expand the alert catalog to 30 runbooks with a new security category
* complete the sample Prometheus rules to 31 rules covering all 30 runbooks, and validate them in CI with `promtool check rules`
* ship the sample rules in-plugin — a browsable HTML page plus raw download — surfaced via a new `/alertmanager rules` command and a System Console link
* add a WebhookHost preset dropdown (Docker Desktop / Kubernetes / custom) and three hover-able `am-url` autocomplete suggestions on `/alertmanager add`
* admin route-tester: show the severity field only in end-to-end mode, and add a by-team scope dropdown that cascades the channel list
* trim System Console settings help text to one sentence each


### Bug Fixes

* team-qualify receiver names (`<slug>--<team>-<channel>`) so same-named channels in different teams no longer collide or misroute


## [1.0.6](https://github.com/mattermost-community/mattermost-plugin-alertmanager/compare/v1.0.5...v1.0.6) (2026-07-06)


### Dependencies

* **actions:** bump anchore/scan-action from 5.2.0 to 7.4.0 ([f3ace25](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/f3ace259f01a28ebd1afafc807a6cfac9bc5d735))
* **actions:** bump actions/upload-artifact from 4.4.3 to 7.0.1 ([35a5da5](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/35a5da5f99ef125c3991e54359d59a2c90bec862))
* **actions:** bump actions/setup-go from 5.5.0 to 6.5.0 ([8693c64](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/8693c64e9ccaaabd1fe673abea21f84c2e2b749c))
* **actions:** bump googleapis/release-please-action from 4.2.0 to 5.0.0 ([4590003](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/459000386b7e567c28cebbd557b002d55e2d3645))
* **actions:** bump the actions-minor-patch group with 5 updates ([c0035e0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/c0035e0ee80bf7d250a2718b74816d89299e7022))
* **go:** bump the go-minor-patch group with 2 updates ([70ff7f0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/70ff7f0dc2d09bfc4064e5925357b8c432eea0ff))
* bump golang.org/x/net from 0.54.0 to 0.55.0 in /build ([646f9ae](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/646f9ae0773a51e9ec9b213ab7aedfb68905a347))

## [1.0.5](https://github.com/christopherfickess/mattermost-plugin-alertmanager/compare/v1.0.4...v1.0.5) (2026-06-30)


### Bug Fixes

* migrate to prometheus/alertmanager v0.33 API surface ([64dce85](https://github.com/christopherfickess/mattermost-plugin-alertmanager/commit/64dce850168a54acba76512c9b52ac12e17272d8))


### Dependencies

* **actions:** bump golangci/golangci-lint-action ([2f14ec5](https://github.com/christopherfickess/mattermost-plugin-alertmanager/commit/2f14ec5829bf9e29eaa1d47de07495ad8f6b41e7))
* **actions:** bump softprops/action-gh-release from 2.3.2 to 3.0.1 ([32e18a6](https://github.com/christopherfickess/mattermost-plugin-alertmanager/commit/32e18a6b3602b28714046c0f1bcf06bd1727d188))
* **actions:** bump the actions-minor-patch group across 1 directory with 4 updates ([766a456](https://github.com/christopherfickess/mattermost-plugin-alertmanager/commit/766a456d51c42db186283889433161bfbcf0096a))

## [1.0.4](https://github.com/christopherfickess/mattermost-plugin-alertmanager/compare/v1.0.3...v1.0.4) (2026-06-30)


### Chores

* release 1.0.4 ([dca70a8](https://github.com/christopherfickess/mattermost-plugin-alertmanager/commit/dca70a8b00284f503d9a65387e9953e8619610bf))

## [Unreleased]

## [1.0.3] - 2026-06-12

Webhook consolidation. `/alertmanager add` now creates **one shared
Mattermost webhook per group** instead of one per receiver. A
`/alertmanager add ... compute` invocation that previously minted 6
Mattermost webhooks now mints 1, with all 6 receivers' `slack_configs`
blocks pointing at the same `api_url`. Each receiver keeps its own
runbook-specific text template — only the webhook URL is shared.

Same security posture: a leaked webhook URL grants post-as-bot
permission to the channel, which is identical whether the channel is
served by 1 or 20 webhooks. The blast radius doesn't change; the
secret count drops 20x in the worst case.

### Added

- **`--severity` flag on `/alertmanager validate --end-to-end`.**
  Controls which severity the synthetic alert is fired at. Accepted
  values: `warning` (default), `critical`, `info`, or `all`.
  `--severity=all` fires four synthetic alerts per receiver
  (warning + critical + info + resolved) so an operator can visually
  verify every render path — sidebar color mapping, the new
  `[SEVERITY:AlertName]` title shape, and the `[✓ RESOLVED:]`
  variant — in one command. The resolved alert is fired with
  `endsAt` in the past so AM immediately routes the resolved
  notification path.
- **`all` option in the `/admin/inventory` severity dropdown.**
  Same multi-fire behavior as the slash command; pick `all` when
  running end-to-end mode to verify the visual matrix from the
  admin form.
- **Individual-slug add path.** `/alertmanager add <team> <channel>
  <am-url> high-cpu-usage` now works — creates one receiver + one
  dedicated webhook for that runbook. Previously the `[target]` arg
  only accepted category set names; now it also accepts any runbook
  slug. Webhook display name follows `Alertmanager: <slug>--<channel>`.
- **Group webhook semantics.** Category-set adds (`compute`,
  `database`, etc.) and `all` create a single Mattermost webhook
  named `Alertmanager: <group>--<channel>`. Every receiver in the
  group's `slack_configs` block uses the same `api_url`.
- **`GroupName` field on `alertConfig`.** Persists the unit (set
  keyword or runbook slug) the receiver was created under. Drives
  the refcount-aware webhook lifecycle.

### Changed

- **Alert post title format rewritten.** Old:
  `[FIRING:1] HighCPUUsage (namespace=billing, pod=api-7d9-2xfgs)`.
  New:
  `[WARNING:HighCPUUsage] (namespace=billing, pod=api-7d9-2xfgs)`.
  Severity now leads the title instead of the AM state — SRE eyes
  scan severity first at 3am. Resolved alerts render as
  `[✓ RESOLVED:HighCPUUsage]`. Mixed-severity groups fall back to
  `[ALERT:HighCPUUsage]`. Firing count appears in parens after the
  bracket only when greater than 1 (single-alert groups are the
  common case and `(1 firing)` is noise).
- **Remove is now refcount-aware.** `/alertmanager remove <name>`
  deletes the receiver entry, then deletes the underlying webhook
  only if no other receiver still references it. Group webhook
  survives partial removal; fully orphaned webhooks get cleaned up.
- **Rotate rotates the SHARED webhook.** `/alertmanager rotate
  <grouped-receiver>` rotates the webhook used by every receiver in
  that group. Response message lists every affected receiver and
  (for multi-receiver groups) DMs the merged YAML bundle, same
  shape as `/alertmanager rotate all --overdue`. Legacy receivers
  (pre-v1.0.3, empty `GroupName`) keep per-receiver rotation
  semantics for backwards compatibility.
- **Reconciler dedups webhook probes.** Orphan-detection cycles
  call `GetIncomingWebhook` once per unique webhookID instead of
  once per receiver. Reduces API call rate from N (receivers) to
  K (distinct webhooks), where K ≤ N.
- `parseAlertConfigs` validation relaxed: receivers may share a
  `WebhookID` provided they also share `Team + Channel + GroupName`.
  Mismatched ownership (different groups claiming the same hookID)
  remains a hard reject.

### Migration

Existing v1.0.0–v1.0.2 receivers stay on per-receiver webhooks —
no automatic consolidation. Mixed model: an upgraded install runs
old per-receiver and new shared-webhook channels side by side
without alert-delivery interruption. To migrate one channel:

```
/alertmanager remove all --force
/alertmanager add <team> <channel> <am-url> <target>
```

Paste the new YAML into `alertmanager.yml`, reload AM.

## [1.0.2] - 2026-06-11

Route-simulation and admin-form release. Closes the "validate, don't
just generate" reviewer wedge — operators can now confirm a Prometheus
rule's labels actually route to the expected receiver before they cost
an incident.

### Added

- `/alertmanager validate --simulate <labels>` walks Alertmanager's
  loaded route tree against a supplied label set and reports which
  receiver(s) an alert with those labels would dispatch to. Mirrors
  `amtool config routes test`. Read-only — no synthetic alert fired,
  safe to run repeatedly. Uses
  `prometheus/alertmanager/dispatch.NewRoute` directly so the
  simulation matches AM's runtime behavior exactly.
- Bare `/alertmanager validate --simulate` (no labels) prints a
  preset list of runbook-slug starter expressions — one
  copy-pasteable `--simulate runbook=<slug>` per shipped runbook —
  for discoverability.
- Route tester form on the `/admin/inventory` page. Three modes:
  - **Simulate** — read-only route walk against the AM's loaded config
  - **Webhook test** — POST a hardcoded test payload directly to each
    target receiver's webhook (tests Mattermost side only)
  - **End-to-end** — fire a synthetic alert through AM, AM templates
    and delivers via real `slack_configs` (tests the full chain)
- Cascading dropdowns on the route tester form: Mode → Type →
  Target → Channel → Severity. Type dropdown filters Target options
  (group names vs. individual runbook slugs); Channel dropdown
  filters to channels that actually host at least one matching
  receiver. Computed server-side, applied via client-side JS at page
  load and on dropdown change.
- `/alertmanager list` now shows a Rotated column with human age
  (`today`, `yesterday`, `N days ago`, `never`). Overdue receivers
  (opted-in via `on`, past the global threshold) get a `⚠️` prefix.
- Severity-driven attachment sidebar color in alert posts: warning
  yellow, critical red, info blue, resolved green.

### Changed

- `samples/prometheus-rules.yaml` rewritten so all 20 alert rules
  emit the labels each runbook's "Required Prometheus labels"
  footer expects. Compute rules switched from node-level to
  container-level metrics for `namespace` and `pod` coverage.
  Application rules add `namespace` alongside `service` / `app`.
  Persistent-volume rule joins `kubelet_volume_stats_*` with
  `kube_pod_spec_volumes_persistentvolumeclaims_info` to surface
  `pod`. Inline comments document where a metric doesn't carry a
  label natively (relabel hints for `blackbox_exporter`,
  `metric_relabel_configs` for app metrics, kube-state-metrics joins
  for deployment app labels).
- `README.md` rewritten to lead with the runbook-at-fire-time worked
  example. Two-minute setup pushed down a section; the headline is
  the daily-use value, not the YAML plumbing.
- `plugin.json` description rewritten to match the new README
  positioning.
- `CONTRIBUTING.md` adds an "Adding a new runbook" walkthrough that
  references `runbooks/TEMPLATE.md` and documents the
  WHERE / WHAT / READ convention every Quick diagnostics block
  must follow.

### Fixed

- Inverse drift detection on the inventory page (added in 1.0.1)
  surfaces correctly when AM has a receiver that the plugin doesn't
  track. Receiver-list extraction now correctly skips route entries
  and `slack_configs` sub-blocks during regex parse.

## [1.0.1] - 2026-06-11

Reviewer-feedback release. Five distinct asks closed plus several
bug fixes uncovered during smoke testing.

### Added

- **Webhook rotation reminders.** New `WebhookRotationDays` System
  Console setting (default `0` = off). When set, the background
  reconciler DMs sysadmins when an opted-in receiver hasn't been
  rotated within the threshold. Per-receiver opt-in via trailing
  `on` arg to `/alertmanager add`. 7-day repeat cadence per
  receiver. No auto-rotation by design — Alertmanager has no write
  API, so the plugin reminds but never applies. See
  [`docs/ROTATION.md`](docs/ROTATION.md) for the playbook.
- `/alertmanager rotate all --overdue` rotates only receivers past
  the threshold in the calling channel, DMs the merged updated YAML
  as one paste-ready bundle.
- **Inverse drift section** on `/admin/inventory`. Receivers loaded
  in AM that have no matching plugin entry surface as their own
  orange "AM-only receivers" section. Catches hand-edits of
  `alertmanager.yml` plus post-rotation gaps where AM YAML wasn't
  updated.
- **Schema validation in `export --diff-against-loaded`.** Merged
  YAML runs through `prometheus/alertmanager/config.Load` — the
  same parser Alertmanager uses at reload time. Surfaces
  undefined-receiver references, malformed matchers, and route tree
  errors before the operator pastes.
- **Required Prometheus labels** section in 15 of the 20 shipped
  runbooks. Each runbook now documents the labels it expects on
  incoming alerts so the inline diagnostics block has valid
  placeholders to substitute. The 5 runbooks that don't use
  placeholder substitution are skipped.
- `runbooks/TEMPLATE.md` documents the Required Labels convention
  for new contributors.
- WHERE / WHAT / READ rewrite of every Quick diagnostics section
  across all 19 runbooks that have one. Each fenced code block
  carries:
  - **WHERE** — exact tool and context (`shell with kubectl context
    set`, `Grafana → Explore (Prometheus data source)`, `psql to
    primary`, etc.)
  - **WHAT** — command effect plus surrounding theory
  - **READ** — concrete value interpretation and next action

### Security

- **Redacted other-channels' secrets** in
  `export --diff-against-loaded` output. `api_url`, `password`,
  `service_key`, `routing_key`, `integration_url`, `auth_token`,
  `bearer_token`, `webhook_url`, `url`, and `secret` values in
  CONTEXT lines from receivers not owned by the calling channel
  are masked. Own-channel additions (the operator needs them to
  paste) stay un-redacted. Addition lines (plus-sign prefix) are
  never redacted regardless of channel ownership.
- Validation runs on the un-redacted in-memory merge so YAML
  validation stays reliable even when the displayed diff is
  redacted.

### Changed

- Reconciler cycle now runs orphan pruning AND rotation reminder
  check in the same scheduled job. One leader-elected goroutine
  handles both — no second background goroutine introduced.

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

[Unreleased]: https://github.com/mattermost/mattermost-plugin-alertmanager/compare/v1.0.3...HEAD
[1.0.3]: https://github.com/mattermost/mattermost-plugin-alertmanager/compare/v1.0.2...v1.0.3
[1.0.2]: https://github.com/mattermost/mattermost-plugin-alertmanager/compare/v1.0.1...v1.0.2
[1.0.1]: https://github.com/mattermost/mattermost-plugin-alertmanager/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/mattermost/mattermost-plugin-alertmanager/releases/tag/v1.0.0
