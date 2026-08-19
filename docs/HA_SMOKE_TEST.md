# HA Mattermost smoke test

Manual procedure for verifying the plugin's leader-elected
background reconciler behaves correctly in a multi-pod
Mattermost deployment.

Run this before every release that touches `server/reconciler.go`,
`server/yaml_janitor.go`, or anything in the cluster-leader path.

The reconciler cycle handles three things in one leader-elected
goroutine: orphan pruning, the YAML auto-delete janitor, and (when
`WebhookRotationDays > 0`) the rotation reminder pass. All three
need single-leader semantics — duplicate execution either spams
admins or races on `saveConfigs`. The procedure below covers all
three implicitly: anything that breaks the leader election breaks
all three.

## Why this needs verification

The reconciler is the only piece of the plugin that's
intentionally non-deterministic across pods. It uses
`pluginapi/cluster.Schedule` with a named KV mutex to elect a
single leader per scheduling cycle. The contract is "only one
pod runs `runBackgroundReconcile` at a time, even with N pods
active." If that contract breaks, two failure modes appear:

1. **Multiple pods run concurrently** — duplicate audit log
   entries every 5 minutes, duplicate webhook API calls, and
   races on `saveConfigs` write paths.
2. **No pod runs** — silent failure where automatic orphan
   pruning stops working. Symptoms: `/alertmanager about`
   shows "Reconciler: never run since plugin start" indefinitely.

Unit tests can't catch either of these — they're deployment
behavior.

## Prerequisites

- A Mattermost cluster with ≥2 active server pods. Kubernetes
  with the official Helm chart is the canonical setup;
  `replicaCount: 2` minimum.
- `kubectl` access to the namespace.
- The plugin installed and active (`make dist` → upload via
  System Console).
- At least one receiver configured via `/alertmanager add` so
  there's something for the reconciler to walk.

## Procedure

### A. Confirm only one pod runs each cycle

```bash
# Stream logs from all MM pods, filter for reconciler output
kubectl logs -n mattermost -l app=mattermost --tail=0 -f \
  | grep -E 'reconciler:|YAML janitor:'
```

Wait one reconciler cycle (≤5 min). You should see:

- Exactly one `reconciler: pruned ...` OR `reconciler: cycle
  completed (no orphans)` log line per cycle (we don't currently
  log the no-op case; absence of the prune line + a present
  `recordReconcileRun` is also valid evidence — check
  `/alertmanager about`).
- The pod name in `kubectl logs` output is the leader for that
  cycle. It may rotate across cycles, but it should never be
  two pod names within the same 5-minute window.

**Pass:** one leader per cycle.
**Fail:** two pods both logging reconciler activity within the
same minute. File a bug.

### B. Verify failover when the leader dies

```bash
# Identify the current leader by grepping for the last
# 'reconciler: pruned' or 'YAML janitor:' log line:
LEADER_POD=$(kubectl logs -n mattermost -l app=mattermost --tail=2000 \
  | grep -B1 'reconciler:' | tail -2 | head -1 | awk -F'/' '{print $1}')

# Kill the leader pod
kubectl delete pod -n mattermost "$LEADER_POD"

# Within the next reconciler cycle (≤5 min), another pod must
# pick up. Watch the logs:
kubectl logs -n mattermost -l app=mattermost --tail=0 -f \
  | grep -E 'reconciler:|YAML janitor:'
```

**Pass:** a different pod logs reconciler activity within
one cycle of the kill.
**Fail:** no pod logs reconciler activity for >10 minutes after
the kill. The KV mutex didn't release on pod death — file a bug
against `pluginapi/cluster.Schedule` upstream and surface as a
plugin-level workaround.

### C. Verify ephemeral PAT cleanup

Each reconciler cycle mints a short-lived PAT for a sysadmin to
call Client4.GetIncomingWebhook. The PAT must be revoked at
end-of-cycle — leaving it active is a credential leak.

```bash
# After a reconciler cycle, list non-revoked PATs owned by an
# active sysadmin via the MM API:
curl -H "Authorization: Bearer $ADMIN_PAT" \
  https://<mm-host>/api/v4/users/<sysadmin-id>/tokens \
  | jq '[.[] | select(.description | contains("alertmanager"))]'
```

**Pass:** empty list. PATs created during the last cycle are
gone.
**Fail:** any "alertmanager-reconciler" PATs surviving past
the cycle. The deferred `revokeUserAccessToken` call didn't
fire — file a bug.

### D. Verify `/alertmanager reconcile` (manual trigger) doesn't
fight the background job

While the background reconciler is running (mid-cycle), a
sysadmin runs `/alertmanager reconcile` from any channel.

**Pass:** the manual command completes (success or "no orphans"
message), audit log shows two reconcile entries (one background,
one manual). No duplicate prune events for the same receiver.
**Fail:** manual command hangs, or two pods race on
`saveConfigs` and one of them returns an error.

### E. Verify receiver-list changes propagate across nodes

The receiver list lives in the plugin KV store. A KV write does NOT
fire `OnConfigurationChange` on peer nodes, so the plugin broadcasts a
`reload_alertconfigs` cluster event after every committed write; peers
reload from KV in `OnPluginClusterEvent`. This confirms a change made
via one pod is visible on the others.

```bash
# Find two distinct pods.
kubectl get pods -n mattermost -l app=mattermost -o name

# 1. Run `/alertmanager add testing prop-check <am-url>` from the app
#    (the request lands on whichever pod the LB picks).
# 2. Immediately hit the admin inventory / `/alertmanager list` a few
#    times so the LB routes you to different pods (or curl each pod
#    directly). Every pod must show the new receiver right away.
# 3. Then `/alertmanager remove prop-check` and repeat — it must
#    disappear on every pod without waiting for a reconciler cycle.
```

**Pass:** the add/remove is visible on all pods within a second or two
(cluster-event latency), not only after the ≤5-min reconciler cycle.
**Fail:** a pod keeps showing the old list until the next reconciler
cycle or a config-settings toggle — the broadcast isn't reaching peers;
check for `could not broadcast receiver-list reload` warnings.

## Recording results

Append to the project's release-notes checklist before tagging
a release that touches the reconciler. Include:

- Pod count tested
- Mattermost version
- Plugin version
- A/B/C/D/E pass/fail
- Any unexpected log lines
