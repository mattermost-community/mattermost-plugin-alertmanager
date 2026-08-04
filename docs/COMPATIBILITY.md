# Compatibility

Which Prometheus Operator CRD API versions the plugin's Kubernetes deployment
path targets. See [Kubernetes deployment](KUBERNETES.md) for the full operator
(CRD) instructions.

> These values are the single source of truth in `server/crd_versions.go`.
> `TestCompatibilityDocMatchesConstants` fails the build if this table drifts
> from the constants — so the published table can never go stale silently.

## Current target

| Component | Target |
|-----------|--------|
| Prometheus Operator (verified against) | `v0.93.0` |
| `AlertmanagerConfig` — routes + receivers | `monitoring.coreos.com/v1alpha1` |
| `PrometheusRule` — sample alert rules | `monitoring.coreos.com/v1` |

`AlertmanagerConfig` tracks **v1alpha1** on purpose: it is the maintained
storage version. `v1beta1` has been stalled since 2022 and there is no `v1`.
`PrometheusRule` is GA at `v1`.

## Why only one version

The plugin tracks the **latest** operator CRD formatting and only that — it does
not ship per-version renderers. If a future operator release graduates
`AlertmanagerConfig` to a new apiVersion, this table, `server/crd_versions.go`,
and the vendored kubeconform schema at `build/crd-schemas/` are bumped together
in a single release, and that bump lands in the changelog. (The schema is
vendored so the `validate-crd` CI gate runs offline — see `build/crd-schemas/README.md`.)

Before applying the CRD examples, confirm what your own cluster actually serves
(whichever version shows `storage=true` is the canonical one):

```
kubectl get crd alertmanagerconfigs.monitoring.coreos.com \
  -o jsonpath='{range .spec.versions[*]}{.name}{" served="}{.served}{" storage="}{.storage}{"\n"}{end}'
```

If your operator only serves an older version than the target above, adapt the
examples accordingly — or upgrade the operator.
