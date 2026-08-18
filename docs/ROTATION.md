# Webhook rotation

Reminder-only rotation playbook. The plugin tracks how long ago each
receiver's Mattermost incoming webhook was last (re)created and DMs
sysadmins when a receiver is overdue. **The plugin never rotates a
webhook automatically.** Alertmanager has no write API, so even a
"safe" auto-rotation would silently break delivery until an operator
pastes the new YAML and reloads AM. Reminders are the right level of
help here; mutation is not.

## How it fits together

Three pieces, each independent:

| Piece | Where | What it does |
|---|---|---|
| `WebhookRotationDays` | System Console → Plugins → Alertmanager | Global threshold in days. `0` (default) disables the whole system. |
| `on` flag on `/alertmanager add` | Slash command | Per-channel opt-in. Without it, the receivers created by that `add` invocation are **never** flagged as overdue. |
| Background reconciler | Plugin internals | Runs every 5 min. When threshold > 0 AND a receiver has opted in AND age > threshold, DMs the calling sysadmin with the list and the exact rotate command. |

Two-tier design on purpose: sysadmin sets the policy globally, channel
team-admin opts in at the channel level. A team that doesn't want
rotation noise just doesn't pass `on` — the global threshold can't
spam them.

## Setup

### 1. Set the threshold

System Console → Plugins → Alertmanager → **Webhook rotation reminder period (days)**.

| Value | Use it when |
|---|---|
| `0` (default) | Feature off entirely. Reminders never fire. |
| `30` | Strict secret-rotation policy (FedRAMP / FISMA shops typically). Expect noise; only useful if rotation is actually happening monthly. |
| `90` | Recommended baseline. Quarterly rotation matches most internal security hygiene policies without nag fatigue. |
| `180` | Semi-annual. Reasonable for low-traffic channels. |
| `365` | Annual. Lower bar; still better than zero. |

Save the setting. Receivers already created get a one-time
migration stamp on the next reconciler cycle so existing channels
don't fire reminders immediately — the clock starts at "now" rather
than "epoch zero."

### 2. Opt receivers in

Per-channel opt-in is mandatory. The global threshold alone does
nothing without it. Pass `on` as the final arg to `/alertmanager add`:

```
/alertmanager add testing alert-slo-channel http://alertmanager:9093 compute on
```

The 6 compute receivers created by that call get
`RotationRemindersEnabled: true`. Any receivers created without `on`
are silently skipped by the reminder scheduler regardless of how
long ago they were rotated.

### 3. Wait

Reminders fire from the background reconciler, which runs every 5
minutes. Receivers don't trigger a reminder until age > threshold,
so on a 90-day setting you'll see the first DM 90 days after the
receiver was created (or last rotated).

## What the reminder looks like

One DM per channel per reconciler cycle. Multiple overdue receivers
in the same channel collapse into one DM:

```
⚠️ Webhook rotation due

The following receiver(s) in #alert-slo-channel haven't been rotated
in over 90 days:

  - `high-cpu-usage--alert-slo-channel` — last rotated 95d ago (5 days overdue)
  - `high-memory-usage--alert-slo-channel` — last rotated 93d ago (3 days overdue)

Rotate them when convenient. In #alert-slo-channel, run:

  /alertmanager rotate all --overdue

You'll receive a DM with the updated YAML. Paste it into your
alertmanager.yml and reload AM (`curl -X POST http://<am>/-/reload`).
Old URLs deactivate immediately on rotation.

See the rotation playbook for the full procedure.
```

Once a reminder fires for a receiver, it won't repeat for 7 days
regardless of how overdue it gets. Stops the same overdue receiver
from re-paging the same sysadmin every 5 minutes.

## Acting on the reminder

Run the suggested command from inside the affected channel:

```
/alertmanager rotate all --overdue
```

Three things happen:

1. Each overdue receiver gets a new Mattermost incoming webhook
   (new hook-id, new URL). Old URLs return 404 immediately.
2. `LastRotatedAt` is stamped, `LastReminderAt` cleared.
3. The plugin DMs you a merged YAML bundle containing the rotated
   `slack_configs:` and the `routes:` block — same shape
   `/alertmanager export` produces, scoped to just the rotated set.

Paste the YAML into `alertmanager.yml`, then reload AM:

```bash
curl -X POST http://alertmanager:9093/-/reload
```

Between rotation and AM reload, alert delivery for those receivers
is broken (AM still has the old URL, which now 404s). Keep the
window short — minutes, not hours.

## Single-receiver rotation

To rotate one receiver without batching:

```
/alertmanager rotate high-cpu-usage
```

For an **individual receiver** (created via `/alertmanager add ...
high-cpu-usage` or pre-v1.0.3): new webhook, old URL invalidated,
YAML re-rendered inline in the chat response. Paste into
`alertmanager.yml` and reload.

For a **grouped receiver** (created as part of `add ... compute`,
`add ... all`, etc.): rotating any one receiver in the group
rotates the **shared webhook used by every receiver in that group**.
The response message lists every affected receiver and DMs the
merged YAML bundle — same shape `rotate all --overdue` produces.
Example: `/alertmanager rotate high-cpu-usage` on a receiver from
a compute group rotates the webhook serving all 6 compute receivers.
This is the trade-off of webhook consolidation: rotation isolation
is per-group, not per-receiver.

## What stops a reminder

| Condition | Effect |
|---|---|
| Receiver rotated via `/alertmanager rotate <name>` | `LastRotatedAt` reset to now; clock restarts. |
| Receiver removed entirely | Reminder will never fire again for that name. |
| `RotationRemindersEnabled` set to false | The next cycle skips this receiver. Set at add time via the `on` argument; the receiver list itself lives in the plugin KV store and isn't hand-editable from System Console. |
| Global `WebhookRotationDays` set to 0 | Feature off; reconciler skips the rotation pass entirely. |

## Why no auto-rotation

The plugin owns the Mattermost side of the chain. It can mint and
destroy webhooks in Mattermost via the Client4 API. It cannot push
the new URL into Alertmanager's loaded config — Alertmanager has no
write API for that. A reload from disk is the only path, and the
file on disk is operator-owned, not plugin-owned.

An auto-rotation feature would have to either:

1. **Not touch AM's config.** Rotate the webhook, immediately break
   delivery for that receiver until the operator notices and pastes
   the new YAML. Net effect: silent outages.
2. **Reach into the operator's `alertmanager.yml`.** Out of scope —
   that file lives wherever the operator deploys AM (a config map,
   a baked image, a chef-managed file, etc.), and the plugin has no
   way to know.

Neither is acceptable. Reminders + a paste-ready YAML bundle is the
right boundary: the plugin does what it can do safely, the operator
does the part only they can do.

## Verifying the feature works

In a test environment with `WebhookRotationDays: 1` (one day for
fast iteration):

1. Add receivers with `on`:
   ```
   /alertmanager add testing test-rotation http://alertmanager:9093 compute on
   ```
2. Force one entry overdue. The receiver list lives in the plugin KV
   store (key `alertconfigs:v1`), not System Console, so there's no UI
   field to edit. In dev, write the KV value directly (decode the JSON,
   set `lastRotatedAt` on one entry to a date >1 day ago, write it
   back) using whatever KV/DB access your test stack has.

   > **A raw KV write does not refresh the running plugin.** The reconciler
   > reads the in-memory config (`getConfiguration()`), which only reloads on
   > `OnConfigurationChange`; a direct KV edit never fires that, and even the
   > plugin's own atomic writes refresh in-memory state on the writing pod only.
   > After editing KV, trigger a config reload (toggle any plugin setting in
   > System Console) or restart/redeploy the plugin — otherwise the reconciler
   > keeps using the old timestamp and simply waiting won't help.
3. Wait up to 5 minutes for the next reconciler cycle.
4. Check the bot DM channel — should have a per-channel reminder
   listing that one overdue receiver.
5. Run `/alertmanager rotate all --overdue` from the channel.
6. Confirm the DM with the merged YAML lands.
7. Next reconciler cycle should NOT re-send the reminder for that
   receiver (it's now rotated).

## Open questions

These are known gaps in v1.x:

- **Multiple sysadmins.** The reminder DM goes to the sysadmin the
  reconciler happens to pick for its ephemeral PAT minting. v1
  doesn't enumerate all sysadmins or all channel team admins.
  Possible v1.x enhancement.
- **Per-channel threshold override.** A single global threshold
  applies to every opted-in receiver. No per-channel override exists.
  Workaround: opt out specific channels by not passing `on` at add
  time.
- **Skip-rotation gestures.** No "snooze this reminder for 30 days"
  option. The 7-day repeat cadence is the only throttle.
