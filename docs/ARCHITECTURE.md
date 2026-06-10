# Architecture

This document explains why the Alertmanager plugin uses Alertmanager's
`slack_configs` notifier instead of `webhook_configs`. Written for an
architect reviewing the design decision; covers the data flow, ownership
boundaries, and trade-offs of each approach.

---

## TL;DR

| Plugin | AM block type | Plugin runtime role | Authentication |
|---|---|---|---|
| `cpanato/mattermost-plugin-alertmanager` (upstream) | `webhook_configs` | Receives every alert payload, parses, formats, posts | Plugin-managed token per receiver |
| This plugin  | `slack_configs` | None at runtime — only at bootstrap/lifecycle | Mattermost-managed hook-id (native incoming webhook) |

**The Alertmanager plugin moves alert delivery out of the plugin and into
Mattermost's native incoming-webhook system, treating the plugin as a
setup helper rather than a runtime path.** That eliminates an HTTP
receiver, an auth model, and ~40% of the codebase, at the cost of
losing interactive elements (Expire buttons) on the posted alerts.

---

## Background: Alertmanager's notifier types

Alertmanager has many built-in notifiers (`pagerduty_configs`,
`opsgenie_configs`, `email_configs`, `victorops_configs`, etc.) plus
two general-purpose ones:

- **`webhook_configs`** — POSTs Alertmanager's native JSON alert payload
  schema (`notify/webhook.Message`) to a URL of your choosing. The
  receiver decides what to do with it. Used by anyone who wants to write
  a custom integration: parse the payload, transform it, route it
  anywhere. The payload format is documented as a stable contract
  between Alertmanager and webhook consumers.

- **`slack_configs`** — POSTs Slack's incoming-webhook JSON format
  (`text`, `attachments[]` with color/title/fields/etc.) to a URL,
  with Alertmanager-side Go templates evaluated server-side. Designed
  for Slack's incoming webhook endpoint, but works with anything that
  speaks the same JSON shape — notably Mattermost's native incoming
  webhook, which is Slack-compatible by design.

Both are first-party Alertmanager features. The choice between them
determines who owns the post format.

---

## Approach 1: `webhook_configs` (cpanato design)

```
  ┌──────────────┐    POST /plugins/alertmanager/api/webhook?token=X
  │ Alertmanager │ ───────────────────────────────────────────────────▶
  └──────────────┘                                                    │
                                                                      ▼
                                        ┌─────────────────────────────────────┐
                                        │ Plugin's HTTP receiver               │
                                        │  - validates token                   │
                                        │  - parses notify/webhook.Message     │
                                        │  - dedups (HA cache)                 │
                                        │  - applies hardcoded post format     │
                                        │  - posts via p.API.CreatePost(...)   │
                                        └─────────────────────────────────────┘
                                                          │
                                                          ▼
                                                    Channel post
```

The plugin owns every step of the runtime path: auth, parsing,
formatting, posting. Mattermost's role is providing the channel and
the user-as-bot identity; everything else runs in plugin code.

### Why someone would choose this

- **Interactive elements on alert posts.** Buttons, attachment actions,
  ephemeral responses. The plugin posts via `p.API.CreatePost` so it
  has full control over `model.Post.PostAction` — useful for things
  like a "Expire silence" button on a silence-state post that calls
  back into the plugin.
- **Plugin-layer dedup.** When Alertmanager runs in HA mode, every peer
  fires every webhook. A plugin-internal dedup cache (the cpanato
  plugin had a 60s TTL cache keyed on group_key + status hash) absorbs
  the fan-out. Without the plugin in the receive path, you'd have to
  dedup somewhere upstream of Mattermost or accept N copies.
- **Format flexibility.** Plugin can render anything: custom
  attachments, threaded replies, dynamically pick channels per alert,
  rewrite tags, etc.
- **Plugin-managed auth.** Receiver tokens are minted by the plugin and
  stored in plugin settings — separate from Mattermost's webhook
  integration table. Operators who want plugin-controlled rotation get
  it.

### What it costs

- The plugin is an HTTP receiver. That's an attack surface
  (`/plugins/<id>/api/webhook` is network-reachable, sits in front of
  any post the plugin will create) that needs to be defended (token
  authentication, request parsing, deserialization). Bugs here are
  CVE-shaped.
- The plugin owns the post format. Changes require a plugin rebuild +
  redeploy. The cpanato plugin had a four-year-open `Custom Templates`
  issue (#19) precisely because the format was hardcoded and adding
  flexibility required substantial new code.
- Plugin-managed tokens are an ops burden: rotation, distribution to
  Alertmanager config, secret leakage prevention. Mattermost has no
  visibility into them — they live in plugin settings under a custom
  schema.
- The plugin has to be running and healthy for any alerts to arrive.
  A plugin crash takes alerts down even if Mattermost itself is fine.

---

## Approach 2: `slack_configs` (federal design)

```
  ┌──────────────┐    POST <site>/hooks/<hook-id>
  │ Alertmanager │ ───────────────────────────────────────▶
  └──────────────┘    (Slack-format JSON, with                │
                       text/title rendered by AM templates)   ▼
                                       ┌─────────────────────────────────────┐
                                       │ Mattermost native webhook receiver   │
                                       │  - looks up hook-id → channel        │
                                       │  - parses Slack-format JSON          │
                                       │  - posts to channel as override user │
                                       └─────────────────────────────────────┘
                                                         │
                                                         ▼
                                                   Channel post


  ┌──────────┐      /alertmanager add <name> ...
  │ Sysadmin │ ─────────────────────────────────────▶
  └──────────┘                                       │
                                                     ▼
                            ┌────────────────────────────────────────────┐
                            │ Plugin (bootstrap/lifecycle only)           │
                            │  - mints ephemeral PAT for the sysadmin     │
                            │  - calls Client4.CreateIncomingWebhook       │
                            │  - revokes PAT                              │
                            │  - persists receiver name → hook-id mapping │
                            │  - renders slack_configs YAML for paste     │
                            └────────────────────────────────────────────┘
```

The plugin is only involved at **setup time** — when you run `/alertmanager
add`, it creates the Mattermost webhook and generates the YAML you paste
into `alertmanager.yml`. After that, the plugin is out of the runtime
path. Alerts go Alertmanager → Mattermost-native-webhook → channel,
with Mattermost handling auth and post creation.

### Why we chose this

- **Smaller attack surface.** No plugin-owned HTTP receiver. The
  network-facing path is Mattermost's own `/hooks/<id>` endpoint, which
  is hardened code that ships with the platform and is audited as part
  of Mattermost's normal release process.
- **No plugin-managed tokens.** Authentication is the hook-id Mattermost
  embeds in the URL — same shared-secret-in-URL pattern Slack uses for
  its own incoming webhooks. Rotation is a one-command plugin operation
  that deletes and recreates the Mattermost webhook.
- **Format flexibility via Alertmanager's own template language.** The
  `slack_configs.text` and `title` fields are Go templates that
  Alertmanager evaluates server-side, giving the same templating power
  to every receiver (not gated by a plugin update). We ship four
  curated default templates; admins can also write their own if they
  edit the rendered YAML directly.
- **Operational visibility.** The webhooks the plugin creates appear in
  System Console → Integrations → Incoming Webhooks under their human
  names (`Alertmanager: sre-prod-critical`). Sysadmins can audit them
  with the same tooling they use for every other integration.
- **Plugin crashes don't take alerts down.** Alerts flow through
  Mattermost's native receiver. The plugin can be disabled or
  malfunctioning and alert delivery still works — only the slash
  commands stop. That's a strict reliability improvement for the
  primary use case.
- **Code mass.** ~40% less code than the cpanato plugin (no receiver,
  no payload parser, no per-config token machinery, no dedup cache).
  Less to maintain, less to test, less to break.

### What it costs

- **No interactive elements on alert posts.** Alertmanager's
  `slack_configs` payload doesn't include `model.PostAction` because
  Slack's incoming webhook format doesn't have an equivalent concept.
  Mattermost's webhook receiver doesn't add them. So an "Expire
  Silence" button on a silence post isn't possible — users have to run
  `/alertmanager expire_silence <name> <silence-id>` instead. This is
  the meaningful UX regression vs cpanato.
- **No plugin-layer dedup.** HA Alertmanager peers will fan out posts.
  Mitigation lives on the Alertmanager side: matching `external_labels`
  across peers, careful inhibit_rules, etc. The plugin can't help here
  because it's not in the receive path.
- **Less control over post shape.** The four built-in templates cover
  most cases, but they're constrained by what `slack_configs` supports.
  Things like threading replies under an initial alert post, or
  cross-channel routing based on alert content, aren't possible without
  Alertmanager doing it — and Alertmanager has very limited support for
  that kind of dynamic routing.

---

## Comparison summary

| Dimension | `webhook_configs` (cpanato) | `slack_configs` (federal) |
|---|---|---|
| Plugin runtime path | Receives, parses, formats, posts | None at runtime |
| Setup time path | Configure receiver token in plugin settings + AM YAML | `/alertmanager add` creates webhook + prints YAML |
| Authentication | Plugin-managed token in URL | Mattermost-managed hook-id in URL |
| Post format owner | Plugin code (hardcoded, change requires rebuild) | Alertmanager template (per-receiver, GitOps-friendly) |
| Interactive buttons on alert posts | Yes (`model.PostAction`) | No (slack_configs has no equivalent) |
| HA Alertmanager dedup | In-plugin TTL cache | Must happen Alertmanager-side |
| Attack surface | Plugin HTTP receiver + Mattermost | Mattermost only |
| Visibility in System Console | Custom plugin settings field with tokens | Standard Incoming Webhooks list |
| Plugin disabled = alerts down? | Yes | No — only slash commands stop |
| Lines of code | Higher | ~40% less |

---

## When `webhook_configs` is the right choice

We don't claim `slack_configs` is universally better. Pick
`webhook_configs` when:

- You need interactive elements on alert posts (buttons, expandable
  attachments tied to plugin callbacks)
- You're integrating Alertmanager with a system Mattermost doesn't speak
  natively (e.g., posting to a custom ticketing-tool channel that
  requires non-standard payloads)
- You want plugin-layer policy on every alert (dedup, suppression,
  enrichment, conditional routing)
- You're already running the cpanato plugin and the migration cost
  isn't worth it

For a Mattermost-native deployment where alerts are read-only
notifications that lead users into Mattermost-side actions (running
slash commands, opening runbooks, etc.) — which is the common case in
SRE/oncall workflows — `slack_configs` is the lower-overhead choice.

---

## Migration cost between the two

Each direction has a one-time cost:

**cpanato → federal:** re-run `/alertmanager add` for each receiver,
update `alertmanager.yml` to swap `webhook_configs:` blocks for
`slack_configs:` blocks. Webhook URLs change; tokens are gone. Routing
rules in the `route:` tree can keep the same `receiver:` names if you
preserve them. Time: ~5 min per receiver.

**federal → cpanato:** the reverse. Install cpanato, set up plugin
config with per-receiver tokens, update `alertmanager.yml` to swap
`slack_configs:` for `webhook_configs:`. Time: ~5 min per receiver
plus token generation/rotation overhead.

Neither path requires Alertmanager downtime — the receiver swap can
be staged: add new receiver, route alerts to both temporarily, observe,
remove old.

---

## Why this matters for org-wide deployments

In a single-team setup, either approach works. The difference shows up
at organizational scale:

- **Audit trail.** Native webhooks appear in System Console's standard
  audit log. Plugin-managed tokens often don't. For compliance contexts
  (FedRAMP, FISMA, internal audit) the native path has more existing
  scaffolding.
- **Onboarding.** A new sysadmin can look at the Incoming Webhooks list
  in System Console and see what's wired up. They don't need to know
  the plugin exists to understand alert delivery. The plugin's role
  becomes "the thing that creates these webhooks for you" rather than
  "the thing in the middle of all alert traffic."
- **Disaster recovery.** If the plugin database state is lost (corruption,
  bad migration, plugin uninstall+reinstall) but Mattermost itself is
  fine, the webhooks remain — alerts keep flowing. With cpanato, lost
  plugin state means lost receiver configs and dead webhooks until
  rebuilt.
- **Permission model.** Native webhook permissions live in Mattermost's
  role system. Operators can grant fine-grained access to
  manage_incoming_webhooks without granting plugin admin. With
  cpanato, "edit plugin settings" was the only access lever, and it
  was all-or-nothing.

---

## Open questions for v1.x

The federal design has known gaps we're tracking:

- **Custom user-supplied templates.** v1.0 ships four built-in templates;
  arbitrary user templates open security concerns (template injection,
  info disclosure via overly-permissive label exposure) that deserve
  thought before opening up.
- **HA dedup as an Alertmanager-side recipe.** We don't ship a plugin
  for this anymore; the answer is "configure your Alertmanager peers
  correctly." Worth a documented playbook.
- **Interactive expire-silence flow.** v1.0 falls back to `/alertmanager
  expire_silence <name> <silence-id>` typed by hand. v1.1 may bring
  back something button-shaped via Mattermost's interactive dialogs,
  but it'll be triggered from `/alertmanager silences` rather than
  inline on alert posts — those are owned by Mattermost's webhook
  pipeline now, not the plugin.
