# End-to-end smoke test

Manual procedure for verifying a fresh install can carry an
alert from Prometheus all the way to a Mattermost channel.
Run before every release.

The plugin's unit tests cover parsing, config, and helper
logic — none of them touch a real Mattermost or Alertmanager.
This procedure does.

## Prerequisites

- A Mattermost instance (single-node is fine for E2E; HA gets
  its own procedure in `HA_SMOKE_TEST.md`).
- An Alertmanager instance reachable from Mattermost. Either
  `host.docker.internal:9093` for local Docker setups, or a
  real cluster URL.
- A Prometheus instance, configured to send to the above
  Alertmanager. Doesn't need to be the same network as
  Mattermost — only Alertmanager needs to reach the MM webhook.
- A sysadmin Mattermost account.

## Procedure

### 1. Build and install

```bash
make dist
# Upload dist/com.mattermost.alertmanager-<ver>.tar.gz via
# System Console → Plugin Management → Choose File → Upload
# Then click "Enable".
```

**Pass:** plugin shows "Enabled" with no error banner.
**Fail:** check the MM server log for plugin activation
errors. Common cause: `min_server_version` mismatch.

### 2. Configure required settings

System Console → Plugins → Alertmanager:

- **WebhookHost**: set to the URL Alertmanager will use to
  reach Mattermost. `https://<mm-host>` for normal setups;
  `http://host.docker.internal:8065` for Docker-on-Mac
  development.
- **MetricsToken**: optional, set if you want to scrape
  `/metrics` from this run.
- **AlertManagerCABundle**: optional, only if AM uses a
  self-signed cert.

**Pass:** all settings save without validation errors.

### 3. Create a test channel + bind receivers

In any team, create a channel `#smoke-test`. Then in that
channel:

```
/alertmanager add <team-slug> smoke-test http://alertmanager:9093 compute
```

**Pass:** ephemeral response shows "complete: 6 created, 0
skipped, 0 failed." and the bot DM's you with
`alertmanager-receivers.yml` + `alertmanager-routes.yml`.
**Fail:** any per-receiver errors. The most common cause is
the channel-suffixed name already existing (re-running `add`
in the same channel is supposed to skip cleanly; if it fails,
file a bug).

### 4. Apply the assembled YAML to Alertmanager

Open the bot DM with the two files. Copy each one into your
`alertmanager.yml`:

- `alertmanager-receivers.yml` → paste under top-level
  `receivers:` (append, don't replace existing).
- `alertmanager-routes.yml` → paste under top-level `route:` →
  `routes:` (append).

```bash
amtool check-config alertmanager.yml
# Expected: success, no errors

curl -X POST http://alertmanager:9093/-/reload
```

**Pass:** AM reloads, `/api/v2/status` shows the new receiver
names in `config.original`.
**Fail:** YAML parse error means the plugin's renderer broke —
file a bug with the assembled output attached.

### 5. Run validate

In `#smoke-test`:

```
/alertmanager validate all
```

**Pass:** every row shows `AM reach: ✓` and
`Loaded in AM: ✓`.
**Fail:** if AM reach fails, check WebhookHost + AM URL. If
loaded-in-AM fails but AM reach passes, the YAML didn't get
applied — re-do step 4.

### 6. Run an end-to-end synthetic alert

```
/alertmanager validate high-cpu-usage--smoke-test --end-to-end
```

Watch `#smoke-test`. Within ~30 seconds you should see a
formatted alert post from the alertmanager bot. The post
should have:

- Title "[FIRING:1] HighCPUUsage" or similar
- The synthetic test's labels rendered as inline-code chips
- A runbook link pointing to the embedded runbook

**Pass:** alert posts in the channel.
**Fail:** alert never arrives. Check (in order):

1. AM `/api/v2/alerts` shows the synthetic alert with state=`active`
2. AM logs for `notify_failed_total` increment
3. MM webhook integrations page — confirm the
   `high-cpu-usage--smoke-test` hook still exists
4. AM's `slack_configs.api_url` matches the actual MM
   webhook URL (compare `/alertmanager config
   high-cpu-usage--smoke-test` output against
   `alertmanager.yml`)

### 7. Fire a real alert from Prometheus

If you have `samples/prometheus-rules.yaml` loaded in
Prometheus, force a rule to fire — easiest is to drop the
threshold to zero on `HighCPUUsage`:

```yaml
- alert: HighCPUUsage
  expr: 1  # always fires
```

Reload Prometheus. Within one evaluation interval + AM's
group_wait (~30s default), the alert arrives in `#smoke-test`.

**Pass:** real alert posts, distinct from the synthetic one.
**Fail:** Prometheus rule didn't make it to AM (check
Prometheus → Alerts page) or AM didn't route (check
`/alertmanager status` and AM's UI).

### 8. Tear down

```
/alertmanager remove all --force
```

Remove the corresponding blocks from `alertmanager.yml` per
[UNINSTALL.md](UNINSTALL.md).

**Pass:** `/alertmanager list` returns empty for the channel.

## Recording results

Append to release-notes checklist:

- MM version
- AM version
- Plugin version
- Each numbered step pass/fail
- Total wall time from step 3 (add) to step 6 (synthetic
  delivery) — typically 1-2 minutes
