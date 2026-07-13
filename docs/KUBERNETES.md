# Kubernetes deployment

Notes for running this plugin on a Mattermost server deployed in Kubernetes (HA, multi-pod, behind an Ingress). Reading order: required settings first, then network topology, then HA correctness.

## TL;DR

Three things to set up beyond the standard plugin install:

1. **`WebhookHost` plugin setting** → set to the cluster-internal Mattermost Service URL (e.g. `http://mattermost.mattermost.svc.cluster.local:8065`).
2. **`runbook` label on every Prometheus rule** → matches a receiver name in `alertmanager.yml`.
3. **Sub-routes in `alertmanager.yml`** → one per receiver, matching on the `runbook` label.

Everything else (channel creation, webhook lifecycle, the embedded runbook pages) works the same as a single-node install.

## Why `WebhookHost` matters

The plugin renders two URLs into your `alertmanager.yml`:

- `api_url:` — the address Alertmanager POSTs notifications to. Used **inside the cluster**.
- Runbook URL inside the `text:` template — clicked by users in chat. Used **from a browser, through the Ingress**.

Without `WebhookHost`, both URLs come from `ServiceSettings.SiteURL` (the public-facing URL). In K8s that means Alertmanager would POST to `https://mm.example.com` — leaving the cluster through egress, hitting the LB, and routing back through the Ingress controller. That works but is:

- Slow (egress + LB + ingress hop for in-cluster traffic)
- Often blocked by NetworkPolicy (pods aren't supposed to reach the public Ingress)
- Wasteful of LB capacity

With `WebhookHost` set to the cluster-internal Service URL:

| URL in rendered YAML | Comes from | Resolves to |
|---|---|---|
| `api_url:` | `WebhookHost` | `mattermost.mattermost.svc.cluster.local` (in-cluster) |
| Runbook URL in `text:` | `SiteURL` (always) | `mm.example.com` (Ingress) |

Set it in System Console → Plugins → Alertmanager → Webhook host override. Format: `<scheme>://<host>:<port>` (no trailing slash, no path).

## Required: route alerts to the right receiver

The plugin creates **one receiver per runbook slug, channel-suffixed**
(e.g. `high-cpu-usage--alert-slo-channel`). For Alertmanager to
actually route alerts to those receivers — instead of dumping
everything on the fallback — your routing tree needs sub-routes that
match labels.

The simplest pattern: set a `runbook` label on every Prometheus rule
that matches a receiver's base slug. Example:

```yaml
# Prometheus rule
- alert: HighCPUUsage
  expr: sum(rate(container_cpu_usage_seconds_total[5m])) by (namespace, pod) > 0.8
  for: 10m
  labels:
    severity: critical
    runbook: high-cpu-usage      # ← matches plugin receiver's BASE slug
    # namespace + pod auto-populated by Prometheus via the metric's labels
  annotations:
    summary: "Pod CPU > 80% for 10 minutes"
```

Then in `alertmanager.yml` — **but you don't write this block by hand.**
`/alertmanager add` generates it for you and DMs it as
`alertmanager-routes.yml`. The generated block looks like:

```yaml
route:
  receiver: default-fallback         # catch-all for unlabeled alerts (you provide)
  group_by: ['alertname', 'cluster']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h                # production value, not the 5m dev default

  routes:
    # ↓ PASTE FROM alertmanager-routes.yml HERE
    - matchers: [runbook="high-cpu-usage"]
      receiver: high-cpu-usage--alert-slo-channel
      continue: true
    - matchers: [runbook="high-memory-usage"]
      receiver: high-memory-usage--alert-slo-channel
      continue: true
    - matchers: [runbook="pod-crashloopbackoff"]
      receiver: pod-crashloopbackoff--alert-slo-channel
      continue: true
    # ... one route per receiver, 30 total for the standard set
```

`continue: true` on every plugin-generated route is what makes fan-out
work (same `runbook` slug routed to multiple channels via separate
`/alertmanager add` calls). Without it, AM stops at the first match
and the second channel never gets the alert.

The matcher always keys on the **base slug** (no `--channel` suffix)
because the same runbook label fans out across all channels
subscribed to it. The receiver name carries the suffix so AM's
receiver list stays unique.

This pattern keeps the alertname-to-runbook coupling **in the
Prometheus rule** (next to the alert definition), not split across
two files. Multiple alertnames can share one runbook (e.g.,
`NodeCPUSpike` and `K8sContainerCPUHigh` both set
`runbook: high-cpu-usage`).

## HA / multi-pod considerations

The plugin is HA-aware where it counts:

| Concern | How the plugin handles it |
|---|---|
| Plugin config storage | `SavePluginConfig` writes to the MM database — all pods see the same state |
| Bot user creation | `EnsureBot` is idempotent — pods race but only one bot exists |
| Slash command handlers | Stateless — any pod can serve any command |
| Background reconciler | Uses `pluginapi/cluster.Schedule` — only the cluster-elected leader runs the periodic webhook check |
| Webhook URL generation | Deterministic from hook-id + WebhookHost/SiteURL — same output from any pod |

The reconciler leader election uses a KV mutex under the key `alertmanager-orphan-reconciler`. If for some reason cluster scheduling fails to register, the plugin logs a warning and disables automatic pruning — manual `/alertmanager reconcile` continues to work from any pod.

## Network topology cheat sheet

```
                    user's browser
                          |
                          | HTTPS via Ingress
                          v
              +-----------------------+
              | Ingress controller    |
              +-----------------------+
                          |
                          v
              +-----------------------+      cluster DNS
              | mattermost Service    | <----+
              +-----------------------+      |
                  | | |  (3 pods)            |
                  v v v                      |
              +-------+ +-------+ +-------+  |
              |  pod  | |  pod  | |  pod  |  |
              |  MM   | |  MM   | |  MM   |  |
              | plug  | | plug  | | plug  |  |
              +-------+ +-------+ +-------+  |
                                             |
              +-----------------------+      |
              | alertmanager Service  |------+
              +-----------------------+
                  | |   (2 pods, typical)
                  v v
              +-------+ +-------+
              |  AM   | |  AM   |
              +-------+ +-------+
```

The plugin (running inside each MM pod):
- Routes its own API calls to `http://localhost:<ListenAddress>` — never leaves the pod
- Renders `api_url:` using `WebhookHost` → AM uses cluster DNS to reach the MM Service (load-balances across pods)
- Renders runbook URLs using `SiteURL` → users reach pages via the Ingress

## Required Kubernetes resources

You provide; the plugin doesn't manage:

- **Mattermost Deployment + Service** (any standard MM Helm chart)
- **Alertmanager StatefulSet + Service** (any standard AM Helm chart or operator)
- **Prometheus StatefulSet** (with rules referencing your `runbook` labels)
- **Ingress** for the Mattermost Service (user traffic)
- **NetworkPolicy allowing AM → MM** on the MM Service port (8065 by default). If your default-deny NetworkPolicy blocks pod-to-pod traffic, add an explicit allow rule.

Optional but useful:

- **PrometheusRule CRs** managed by Helm or kustomize — keeps the `runbook` label and alert definition together
- **Service monitor** for Alertmanager itself, with the `prometheus-scrape-target-down` and `alertmanager-notification-failure` receivers wired up so AM's own failure modes get surfaced

## NetworkPolicy example

If your cluster has default-deny pod-to-pod traffic:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-alertmanager-to-mattermost
  namespace: mattermost
spec:
  podSelector:
    matchLabels:
      app: mattermost
  policyTypes: [Ingress]
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              name: monitoring          # whatever AM's namespace is labeled
          podSelector:
            matchLabels:
              app: alertmanager
      ports:
        - protocol: TCP
          port: 8065
```

Match the labels to whatever your MM Helm chart uses for its pods.

## Verifying the K8s setup

After deploying:

1. **Set `WebhookHost`** in System Console → Plugins → Alertmanager.
2. **Run `/alertmanager add testing alerts-sre http://alertmanager.monitoring.svc.cluster.local:9093`** from a Mattermost channel. The rendered `api_url:` should now use your cluster-internal MM service URL.
3. **Reload Alertmanager** after pasting the YAML — `kubectl exec -it alertmanager-0 -- /bin/sh -c "killall -HUP alertmanager"` or via the Operator's reconciliation.
4. **Fire a synthetic alert** (e.g., `up == 0` for a scrape target you deliberately broke). Verify:
   - Post lands in the intended channel
   - Runbook link uses the public Ingress URL (clickable from a browser)
   - The receiver name in the post's source matches what your `runbook` label was set to
5. **Confirm HA**: scale Mattermost to 3 replicas, wait 5 minutes, grep MM logs for `reconciler:` messages — only one pod should be emitting them per cycle.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| AM logs `connection refused` posting to api_url | `WebhookHost` not set, or set to a host AM can't resolve |
| Runbook URL in post 404s when clicked | `SiteURL` not configured correctly; user can't reach the plugin's `/public/runbooks/` path via Ingress |
| Multiple pods logging `reconciler: pruned ...` per cycle | Cluster-mutex registration failed; fall back to manual `/alertmanager reconcile` until next plugin restart |
| `/alertmanager add` hangs on channel creation | NetworkPolicy blocking plugin → MM internal API; check `localhost:8065` is reachable from the MM pod itself |
| Alerts firing but never reach Mattermost | Routing tree has no sub-route matching the `runbook` label — alerts land on the fallback receiver only |
