# Security Policy

## Reporting a vulnerability

**Don't open a public GitHub issue for a security vulnerability.**
Public issues are indexed and crawled within minutes; that's the
opposite of what you want for a not-yet-fixed flaw.

Report via GitHub's Private Vulnerability Reporting:

1. Go to the **Security** tab of this repo.
2. Click **Report a vulnerability**.
3. Describe the issue, including:
   - Affected version(s) of the plugin
   - Affected version(s) of Mattermost (server) and Alertmanager
   - Reproduction steps or a proof-of-concept
   - The impact you've established (data exposure? privilege
     escalation? denial of service?)

You'll get an acknowledgement within 3 business days. Fix turnaround
depends on severity — critical issues with active exploitation get
same-week patches; lower-severity issues land in the next release.

## Scope

In scope:
- Anything that lets a non-sysadmin/non-team-admin user read or
  modify other channels' receiver configuration
- Bypass of the channel-scoping that limits slash command visibility
- Webhook URL exposure via the plugin's HTTP endpoints
  (`/admin/inventory`, `/metrics`, autocomplete handlers)
- Token leakage in logs, metrics, or chat output
- Plugin code that lets an attacker pivot to host the Mattermost
  server runs on (file write outside the plugin sandbox, shell
  injection via slash command args, etc.)

Out of scope:
- Misconfigured Alertmanager backends behind plugin-managed
  receivers (those are AM-side problems, not plugin problems)
- Mattermost server vulnerabilities that aren't reachable through
  this plugin's surface
- Anything requiring sysadmin access to exploit — sysadmins can
  already do anything in Mattermost
- DoS via legitimate but high-volume slash command usage
  (`/alertmanager status` against a slow AM, etc.)
- Self-XSS in slash command output rendered into your own chat
  client

## Supported versions

The plugin's `main` branch is the only supported branch. Backports
to older tagged versions are not provided.

Mattermost: the plugin's `min_server_version` in `plugin.json` is
the floor. Below that, the plugin won't load.

## Known operational considerations

These aren't vulnerabilities, but they shape what an operator
should expect:

- **Receiver configuration is stored in plain Mattermost plugin
  config**. Sysadmins (who can read all plugin config via System
  Console) can see Alertmanager basic-auth credentials if they're
  set. The plugin doesn't try to hide this — Mattermost's RBAC
  is the gate.

- **The admin inventory page (`/plugins/com.mattermost.alertmanager/admin/inventory`)
  shows webhook URLs in plaintext for sysadmins**. This is by
  design (sysadmins already have webhook visibility via the
  built-in Integrations page), but be aware that anyone with a
  sysadmin session can copy these URLs.

- **The metrics endpoint (`/plugins/com.mattermost.alertmanager/metrics`)
  uses bearer-token auth, not Mattermost session auth**.
  Configure a strong `MetricsToken` in System Console. The
  endpoint is disabled (returns 404) when the token is empty —
  the default.

- **The background reconciler mints short-lived sysadmin PATs**
  to query the webhook API (Mattermost plugin RPC doesn't expose
  webhook CRUD). The PAT is revoked immediately after each cycle.
  Audit log will show ephemeral token entries every 5 minutes
  while the plugin is active in HA.
