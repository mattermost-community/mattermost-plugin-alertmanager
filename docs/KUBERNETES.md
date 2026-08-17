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
(e.g. `high-cpu-usage--sre-alert-slo-channel`). For Alertmanager to
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
      receiver: high-cpu-usage--sre-alert-slo-channel
      continue: true
    - matchers: [runbook="high-memory-usage"]
      receiver: high-memory-usage--sre-alert-slo-channel
      continue: true
    - matchers: [runbook="pod-crashloopbackoff"]
      receiver: pod-crashloopbackoff--sre-alert-slo-channel
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

## Prometheus Operator (AlertmanagerConfig CRD)

Everything above is the **file** method — you paste the generated block into a
hand-maintained `alertmanager.yml` (or a ConfigMap). If you run Alertmanager
via the **Prometheus Operator** (e.g. kube-prometheus-stack), you don't edit a
file: you apply an `AlertmanagerConfig` custom resource and the operator merges
it into the running config for you.

The plugin still generates the routing/receivers content the same way — this
section is how to express that content as CRDs. See
[Compatibility](COMPATIBILITY.md) for the exact API versions targeted.

### Know which CRD you're touching

Two different CRDs, and they are constantly confused:

| Kind | apiVersion | What it is |
|------|-----------|------------|
| `Alertmanager` | `monitoring.coreos.com/v1` | Deploys the Alertmanager **instance** (replicas, storage). You already have this. |
| `AlertmanagerConfig` | `monitoring.coreos.com/v1alpha1` | The **routes + receivers** — this is what replaces the plugin's file output. |

Do **not** put `monitoring.coreos.com/v1` on an `AlertmanagerConfig` — there is
no such served version; the apply will be rejected. Confirm what your cluster
serves (whichever shows `storage=true` is canonical):

```
kubectl get crd alertmanagerconfigs.monitoring.coreos.com \
  -o jsonpath='{range .spec.versions[*]}{.name}{" served="}{.served}{" storage="}{.storage}{"\n"}{end}'
```

### The webhook URL must be a Secret

Unlike the file format's inline `api_url:`, an `AlertmanagerConfig`'s
`slackConfigs.apiURL` is a **Secret reference** — you cannot inline the URL.
Create one Secret per receiver (or reuse one per group webhook), in the **same
namespace** as the `AlertmanagerConfig`. The URL is the one `/alertmanager add`
DMs you.

```
kubectl create secret generic alertmanager-webhook-sre-alert-slo-channel \
  --from-literal=url='<webhook URL from /alertmanager add>' \
  -n monitoring
```

Or declaratively (GitOps / air-gapped):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: alertmanager-webhook-sre-alert-slo-channel
  namespace: monitoring
stringData:
  url: "<webhook URL from /alertmanager add>"
```

### The AlertmanagerConfig

The operator-native equivalent of the plugin's generated `route:` + `receivers:`
(base-slug matchers, `continue: true`, one receiver per runbook-channel):

```yaml
apiVersion: monitoring.coreos.com/v1alpha1
kind: AlertmanagerConfig
metadata:
  name: mattermost-alertmanager-sre-alert-slo-channel
  namespace: monitoring
spec:
  route:
    receiver: mattermost-fallback            # in-CR fallback; grafted under AM's main route
    groupBy: [alertname, cluster]
    groupWait: 30s
    groupInterval: 5m
    repeatInterval: 4h
    routes:
      # one per receiver — matcher keys on the BASE slug, continue:true for fan-out
      - matchers: [{name: runbook, value: high-cpu-usage, matchType: "="}]
        receiver: high-cpu-usage--sre-alert-slo-channel
        continue: true
      - matchers: [{name: runbook, value: high-memory-usage, matchType: "="}]
        receiver: high-memory-usage--sre-alert-slo-channel
        continue: true
      # ... one route per receiver
  receivers:
    - name: high-cpu-usage--sre-alert-slo-channel
      slackConfigs:
        - apiURL: {name: alertmanager-webhook-sre-alert-slo-channel, key: url}
          channel: "#alert-slo-channel"
          sendResolved: true
          username: alertmanagerbot
          iconURL: https://<your-mm-host>/plugins/com.mattermost.alertmanager/public/alertmanager-logo.png
          # NOTE: simplified for readability. What the plugin actually GENERATES
          # wraps every attacker-influenceable label/annotation in a sanitizer —
          # in the title, e.g. `{{ .CommonLabels.alertname | reReplaceAll "[\x60\r\n()<>]" "" | printf "%.256s" }}`,
          # and the same for label values in the text body — stripping
          # markdown/shell-breakout chars so a hostile alert can't inject links or
          # code spans into the post. Prefer `/alertmanager add --format=crd` over
          # hand-authoring so you get the hardened templates.
          title: '[{{ .Status | toUpper }}:{{ .CommonLabels.alertname }}]'
          text: |-
            {{ range .Alerts -}}
            **Alert:** {{ .Labels.alertname }}{{ if .Labels.severity }} - {{ .Labels.severity }}{{ end }}
            {{ end -}}
    - name: mattermost-fallback
      slackConfigs:
        - apiURL: {name: alertmanager-webhook-fallback, key: url}
          channel: "#alerts"
          sendResolved: true
```

Field renames vs the file format (same meaning, camelCased, secret-backed URL):

| File (`alertmanager.yml`) | CRD (`AlertmanagerConfig`) |
|---------------------------|----------------------------|
| `slack_configs` | `slackConfigs` |
| `api_url: https://…` | `apiURL: {name, key}` (Secret ref, mandatory) |
| `send_resolved` | `sendResolved` |
| `matchers: [runbook="…"]` | `matchers: [{name, value, matchType}]` |

### Operator gotchas

- **Namespace matcher injection.** By default the operator adds a `namespace`
  matcher to every route (`alertmanagerConfigMatcherStrategy: OnNamespace`), so
  an `AlertmanagerConfig` only matches alerts whose `namespace` label equals its
  own namespace. If your alerts originate elsewhere, deploy the CR in that
  namespace or set the matcher strategy to `None`.
- **Route grafting.** Your `spec.route` is grafted as a child under the
  operator's top-level route — it is not the global route. The true catch-all
  fallback lives in the main Alertmanager config, not here.
- **Selector.** Your `Alertmanager` resource must select this CR via
  `alertmanagerConfigSelector` (plus `alertmanagerConfigNamespaceSelector` for
  cross-namespace).
- **Object names are team-qualified.** Generated `metadata.name` values (Secret,
  AlertmanagerConfig, fallback receiver) include the Mattermost **team** as well
  as the channel — e.g. `mattermost-alertmanager-<team>-<channel>` — because
  channel names are unique only per team. Without the team, two teams both
  exporting their `~alerts` channel into the same namespace would produce
  byte-identical object names, and `kubectl apply` would silently merge them
  (one team's Secret/route overwriting the other's).
  - **Migration:** if you applied manifests from a build that named objects by
    channel only, the new names differ, so `kubectl apply` **creates** the new
    objects and leaves the old ones orphaned — alerts can double-deliver until you
    remove the stragglers. Re-export, apply, then delete the old channel-only
    objects, e.g. `kubectl -n monitoring delete alertmanagerconfig
    mattermost-alertmanager-<channel>` and the matching
    `secret alertmanager-webhook-<channel>`.

### Alerting rules as a PrometheusRule

The file-format rules in `samples/prometheus-rules.yaml` become a
`PrometheusRule` (this one **is** GA, `monitoring.coreos.com/v1`) — identical
`groups:` content, wrapped:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: mattermost-alertmanager-rules
  namespace: monitoring
  labels:
    release: kube-prometheus-stack     # must match your Prometheus's ruleSelector
spec:
  groups:
    # exact contents of samples/prometheus-rules.yaml (do not hand-copy —
    # take it from that single source; `/alertmanager rules` prints it)
```

The only hard requirement for the plugin to work is that your rules emit
`runbook: <slug>` — see [Required: route alerts to the right
receiver](#required-route-alerts-to-the-right-receiver) above.

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
