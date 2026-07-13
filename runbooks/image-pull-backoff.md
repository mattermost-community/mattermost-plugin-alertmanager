# Pod ImagePullBackOff

!!! danger "Severity: critical"
    **Target response: 15m.** A pod can't pull its container image —
    it's stuck `ImagePullBackOff`/`ErrImagePull` and will never start.
    A rollout is effectively down.

## What this alert means

Kubernetes couldn't fetch the image: wrong tag, private registry with no
/expired pull secret, registry outage, or rate limiting. Unlike a
crashloop (image runs then dies), the container never starts at all.

```promql
kube_pod_container_status_waiting_reason{reason=~"ImagePullBackOff|ErrImagePull"} == 1
```

If this is a new deploy, the rollout is blocked; if it's a scale-up, you
can't add capacity.

## Quick diagnostics

```bash
# WHERE: shell with kubectl context set.
# WHAT: the pull error message straight from the pod events.
# READ: "not found" = bad tag/name. "unauthorized"/"denied" = pull secret
#   missing/expired. "toomanyrequests" = registry rate limit (Docker Hub).
kubectl describe pod -n <namespace> <pod> | grep -A3 -iE "failed|pull|back-off"
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: the image the pod wants + whether an imagePullSecret is attached.
# READ: no imagePullSecrets on a private-registry image = the cause. Wrong
#   registry host or tag in the image string = a bad manifest.
kubectl get pod -n <namespace> <pod> -o jsonpath='image={.spec.containers[0].image}{"\n"}pullSecrets={.spec.imagePullSecrets}{"\n"}'
```

```bash
# WHERE: shell with kubectl context set.
# WHAT: confirm the pull secret exists and is the right type in this ns.
# READ: missing secret, or type != kubernetes.io/dockerconfigjson = fix
#   the secret. Present + correct = suspect registry outage / rate limit.
kubectl get secret -n <namespace> -o custom-columns=NAME:.metadata.name,TYPE:.type | grep -i docker
```

## Severity & urgency

| Severity | Pager? | Target response | Business impact |
|---|---|---|---|
| Critical | Yes — page on-call | 15m | Rollout blocked / can't scale |

## Common causes & fixes

| Symptom | Diagnosis | Fix |
|---|---|---|
| "not found" | `kubectl describe` event | Fix the image tag in the manifest |
| "unauthorized" | no/expired pull secret | Recreate `imagePullSecret`, attach to SA |
| "toomanyrequests" | Docker Hub rate limit | Authenticate pulls / mirror through your registry |

## Escalation

1. **Primary** — `@sre-oncall` in `#mm-incidents`.
2. **Registry owner** — if the registry itself is down/unreachable.

## Required Prometheus labels

Diagnostics use `namespace`, `pod`. From `kube_pod_container_status_waiting_reason`
(kube-state-metrics).

## Related runbooks

- [Deployment Replicas Unavailable](deployment-replicas-unavailable.md) — the deployment-level symptom.
- [Unexpected Container Image](unexpected-container-image.md) — if the "wrong" image is also unapproved.
