# Unexpected Container Image

!!! danger "Severity: warning"
    **Target response: 30m.** A pod is running an image from outside the
    approved registry allowlist — possible supply-chain compromise, a
    mis-tagged deploy, or a workload that bypassed CI.

## What this alert means

Every image your workloads run should come from a known registry. This
alert fires when `kube_pod_container_info` reports an image whose
reference does not match the allowlist regex. Tighten the regex to your
real registries before enabling.

```promql
# Any running container whose image is NOT from an approved registry.
count by (namespace, pod, container, image) (
  kube_pod_container_info{image!~"^(mattermost/|registry\\.mattermost\\.com/|ghcr\\.io/mattermost/).*"}
) > 0
```

An image from an unexpected registry means code you didn't vet is
running with your service accounts and network access — the classic
foothold for lateral movement or data exfil.

## Quick diagnostics

```bash
# WHERE: shell with kubectl context set to the affected cluster.
# WHAT: show the exact image reference (with digest) the pod is running.
# READ: compare the registry host against your allowlist. A public
#   registry (docker.io/library/*, random ghcr users) on a prod workload
#   is the red flag. Note the digest for the image-provenance check below.
kubectl get pod -n <namespace> <pod> -o jsonpath='{range .spec.containers[*]}{.name}{" -> "}{.image}{"\n"}{end}'
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: who created this pod and when — owner ref + creation timestamp.
# READ: a Deployment/Job you recognize + a recent rollout = probably a
#   mis-tagged deploy. No owner, or an unfamiliar creator = investigate
#   as a rogue workload immediately.
kubectl get pod -n <namespace> <pod> -o jsonpath='{.metadata.ownerReferences}{"\n"}{.metadata.creationTimestamp}{"\n"}'
```

```bash
# WHERE: shell with cosign installed (if you sign images).
# WHAT: verify the image signature against your public key.
# READ: "no matching signatures" on a supposedly-internal image means it
#   was NOT built by your pipeline. Treat as compromise until disproven.
cosign verify --key cosign.pub $(kubectl get pod -n <namespace> <pod> -o jsonpath='{.spec.containers[0].image}')
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Warning | No — chat only | 30m | Unvetted code running with cluster access |

Escalate to **critical/page** if the image is on a production namespace,
signature verification fails, or the pod has no recognizable owner.

## Diagnostic steps

1. **Confirm it's still running** — `kubectl get pod -n <namespace> <pod>`.
   If gone, capture the image ref from logs/audit before it's GC'd.
2. **Identify the source** — owner ref → Deployment/Job → who applied it
   (`kubectl get <owner> -o yaml`, check `kubectl.kubernetes.io/last-applied` + GitOps history).
3. **Classify** — mis-tag (image *almost* matches, wrong host) vs. rogue
   (unknown workload, public registry, failed signature).
4. **Contain if rogue** — cordon nothing yet; scale the owning workload to
   0 or delete the pod, revoke its service-account token, snapshot for IR.

## Common causes & fixes

| Symptom | Diagnosis | Fix |
|---|---|---|
| Image host is a typo of yours | Compare to allowlist | Fix the deploy manifest / CI tag |
| Public registry on prod | `image` starts `docker.io/` | Re-pull through your registry; add admission policy |
| Unknown workload, no owner | No ownerReferences | Contain, revoke SA, open IR ticket |

## Escalation

1. **Primary** — `@sre-oncall` in `#mm-incidents`.
2. **Security** — `@security-oncall` immediately if signature fails or the
   workload is unrecognized. This is a potential compromise, not a config bug.

## Required Prometheus labels

Diagnostics use: `namespace`, `pod`, `container`. Provided by
`kube_pod_container_info` (kube-state-metrics).

## Related runbooks

- [Privileged Container Started](privileged-container-started.md) — often co-fires on a rogue workload.
- [RBAC Privilege Escalation](rbac-privilege-escalation.md) — the next step an attacker takes after landing a pod.
