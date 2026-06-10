# Uninstalling the Alertmanager plugin

Uninstalling cleanly is a four-step process. Skipping any step
leaves the system in a half-state that's hard to debug later.

## 1. Delete the plugin's receivers from every channel

In each Mattermost channel that has receivers, run:

```
/alertmanager remove all --force
```

This deletes the Mattermost incoming webhooks the plugin created
and removes the matching entries from plugin config. Use
`/alertmanager list` first to see what's there.

If you have receivers across many channels and don't want to
visit each one, a sysadmin can iterate the System Console →
Plugins → Alertmanager → `AlertConfigsJSON` field and clear
it manually, then remove the matching webhooks from
**System Console → Integrations → Incoming Webhooks**.

## 2. Strip the plugin's blocks from `alertmanager.yml`

The plugin renders receiver and route blocks into your
`alertmanager.yml` via `/alertmanager export` and `/alertmanager
add`. Those blocks live on the AM side and won't be removed by
uninstalling the plugin.

Remove:

- Every `slack_configs:` block whose `api_url:` points at the
  Mattermost webhook URL pattern (typically
  `https://<your-mattermost>/hooks/<id>`).
- Every route under the top-level `route:` block whose `matchers:`
  contains `runbook=<slug>` and whose `receiver:` was created
  by the plugin.

```bash
# Validate the result before reloading AM
amtool check-config alertmanager.yml

# Reload AM (HUP signal or POST /-/reload depending on your setup)
curl -X POST http://alertmanager:9093/-/reload
```

Confirm no receivers reference plugin-managed webhooks:

```bash
grep -E 'api_url.*mattermost|matchers.*runbook=' alertmanager.yml
# Expected: no output
```

## 3. (Optional) Remove the corresponding Prometheus rules

If you used `samples/prometheus-rules.yaml` (or any rule file that
emits `runbook: <slug>` labels) and you're done with the plugin's
runbooks entirely, remove the rule file from Prometheus's
`rule_files` glob and reload Prometheus.

If you want to keep the alerts firing but route them somewhere
other than Mattermost (PagerDuty, OpsGenie, plain email), leave
the rules alone and replace the corresponding receivers in
`alertmanager.yml` with non-plugin variants.

## 4. Uninstall the plugin

**System Console → Plugin Management** → Alertmanager → **Disable**,
then **Remove**. Mattermost cleans up the plugin's KV store and
the bot user it created.

## Verifying a clean uninstall

```bash
# AM side: no receivers reference plugin webhooks
grep -c 'mattermost' alertmanager.yml
# Expected: 0

# MM side: no incoming webhooks owned by the alertmanager bot
# (admin SQL or via the Integrations page)
```

If `/alertmanager list` returns "command not found" or similar,
the plugin is gone. If it returns the old help text, the plugin
is still installed — re-check step 4.

## What stays behind

- **Audit log entries.** Mattermost's audit log retains the
  plugin's recorded events; that's intentional, the log is
  immutable.
- **PAT mint/revoke entries.** The reconciler's ephemeral token
  events stay in the audit log. Useful for the next
  security review.
- **Alertmanager silences.** If you silenced any alerts while
  the plugin was active, the silences remain in AM until they
  expire naturally. Use the AM UI or `amtool` to expire them
  early if needed.
