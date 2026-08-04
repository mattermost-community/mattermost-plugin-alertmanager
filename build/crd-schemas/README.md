# Vendored CRD JSON schemas

kubeconform schemas for the Prometheus Operator CRDs the plugin generates
(`/alertmanager export|add --format=crd`). **Vendored on purpose** so the
`validate-crd` CI job runs fully offline — air-gapped / closed environments have
no access to the upstream schema catalog at run time.

## Files

- `alertmanagerconfig_v1alpha1.json` — the `AlertmanagerConfig` v1alpha1 schema.

Source: [datreeio/CRDs-catalog](https://github.com/datreeio/CRDs-catalog)
(`monitoring.coreos.com/alertmanagerconfig_v1alpha1.json`), Apache-2.0.

## Refresh (only when the targeted operator version changes)

We track **one** CRD apiVersion at a time — always the latest release's — and
manage only that (see `docs/COMPATIBILITY.md` and `server/crd_versions.go`).
When you bump the target, pull the matching schema into this directory:

```
curl -fsSL \
  https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/monitoring.coreos.com/alertmanagerconfig_v1alpha1.json \
  -o build/crd-schemas/alertmanagerconfig_v1alpha1.json
```

Then bump `server/crd_versions.go` + `docs/COMPATIBILITY.md` in the same change
so the schema, the emitted apiVersion, and the docs all move together. If the
apiVersion itself changes (e.g. a future graduation), rename the file to match
kubeconform's `{{.ResourceKind}}_{{.ResourceAPIVersion}}.json` pattern.
