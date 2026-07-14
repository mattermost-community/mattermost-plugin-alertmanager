# Project Guidelines — mattermost-plugin-alertmanager

Guidance for any Claude agent working in this repo. Read it before changing
code, docs, or CI. It captures invariants that are easy to break silently.

## Overview

Server-only Mattermost plugin that bridges Prometheus Alertmanager → Mattermost
via native incoming webhooks. The daily-use value: when an alert fires, the post
includes the matching runbook's first three diagnostic commands inline,
pre-filled with the failing host/pod from the alert's labels.

- **Enterprise Edition only.** Deploy/test against `mattermost-enterprise-edition`, never team-edition.
- **No webapp bundle.** `plugin.json` has a `server` block only. This rules out
  System Console `type: "custom"` settings and any React UI. Settings are the
  flat `settings_schema` — there is **no conditional show/hide** between settings.

## Architecture (key paths)

- `server/` — the Go plugin. Table-driven tests, stdlib only (no testify).
- `runbooks/*.md` — the 30 canonical SRE runbooks, embedded via `//go:embed` (`runbooks_embed.go`).
- `docs/*.md` — embedded docs, shown in chat (`/alertmanager docs`) and rendered to HTML.
- `samples/prometheus-rules.yaml` — the shipped example rules. **Single source of truth** for the in-plugin rules page.
- `build/render-docs/main.go` — renders `docs/*.md` + `runbooks/*.md` **and** `samples/prometheus-rules.yaml` → `public/*.html` at bundle time. Its own Go module.
- `public/` — **generated** (`make render-docs`, run by `make bundle`). Gitignored. Never hand-edit; edit the source + regenerate.
- `plugin.json` — manifest + System Console `settings_schema` (header/footer/settings).
- `.github/workflows/` — `lint`, `test`, `build`, `security`, `release-please`, `pages`, `release`.

## The catalog and the "keep every count in sync" rule

The catalog is **30 runbooks / 31 rules** (`ContainerOOMKilled` reuses the
`high-memory-usage` runbook, so one extra rule than runbooks). A recurring bug in
this repo has been stale hardcoded counts ("20 runbooks") left behind when the
catalog grew. **When the catalog changes, update every mirror in the same PR:**

- `plugin.json` (header, footer, `add`/`remove`/`validate` autocomplete help)
- `README.md`
- `docs/` (ALERT_CATALOG, ALERT_REQUIREMENTS, etc.) and the `runbookIndexBody` in `build/render-docs/main.go`
- `samples/prometheus-rules.yaml` (one rule per runbook) and deploy-local's copy
- Go comments that name a count (`admin_inventory.go`, `cmd_query.go`)
- `scaffoldSets` in `server/cmd_scaffold.go` (category → slugs)

**Drift guards already enforce most of this** — run `go test ./server/...`:
- `TestScaffoldMatchesRunbookFiles` — every scaffold slug has a runbook file and vice versa.
- `TestScaffoldCategoriesDocumented` — every category appears in help text + autocomplete.
- `TestRunbookPlaceholdersAllowlisted` — runbook `<label>` placeholders are real labels.
- `TestDocTopicsResolve` — every `docs` topic maps to a real embedded file.

Adding a runbook = add `runbooks/<slug>.md` + a `scaffoldSets` entry + a rule in
`samples/prometheus-rules.yaml`. The embed glob and tests handle the rest.

## Testing — required in all CI jobs

Every PR must pass **Test, Lint, Build, and Security**. Add/adjust tests with
every change; don't merge red.

- **Go:** `go test -race -count=1 ./...`. Table-driven, stdlib. New behavior gets a test.
- **Lint:** `golangci-lint` pinned at **v2.12.2** in CI. The `modernize` linter is on — e.g. prefer `strings.SplitSeq` over `strings.Split` in range loops. `gofmt` must be clean.
- **Prometheus rules — three tiers, don't confuse them:**
  1. `promtool check rules` (syntax/PromQL parse) — CI `validate-rules` job in `test.yml`, runs on `samples/prometheus-rules.yaml`.
  2. `promtool test rules` (fire/no-fire fixtures) — lives in deploy-local, **not** in this repo's CI yet.
  3. End-to-end (real AM + `/alertmanager validate --end-to-end`) — manual.
  `check rules` proves the file loads; it does **not** prove a rule fires or uses a real metric. Say so; don't overstate "validated."
- **Run promtool locally** without the stack: `docker run --rm --entrypoint promtool -v "$PWD":/w -w /w prom/prometheus:v3.5.0 check rules /w/samples/prometheus-rules.yaml` (the image entrypoint is `/bin/prometheus`, hence the override).
- **Regenerate docs after editing** `docs/`, `runbooks/`, or `samples/`: `make render-docs`.

## Security — what's covered, and what must stay covered

- **Grype SBOM scan** runs in `security.yml` and uploads SARIF to Code Scanning. Code Scanning rejects empty `artifactLocation.uri`; a jq step anchors it (to `go.sum` / `webapp/package-lock.json`). If you touch that workflow, keep the anchoring or the Security job breaks silently.
- **Standing finding `GO-2026-5932` is intentionally left OPEN** for a security review. Do **not** suppress, dismiss, or add an ignore rule for it.
- **No automated CVE gate yet** — target policy is block on CRITICAL/HIGH. When recommending or bumping an image/chart, include the scan command (`trivy image <ref>` or `grype <ref>`) and flag known HIGH/CRITICAL. This is an open hardening item.
- **Version policy:** exact pins everywhere, never `latest`, never floating ranges. Verify a version live before recommending it (training data is stale). Current pins in use: `prom/prometheus:v3.5.0`, `prom/alertmanager:v0.30.1`, `golangci-lint v2.12.2`.
- **Secrets:** webhook URLs are channel-bound bearer tokens. `AssembledYAMLTTLHours` auto-deletes the DM'd YAML; `WebhookRotationDays` drives rotation reminders. Don't log webhook URLs.
- **Dev stacks** (`docker-compose.dev.yml`) bind `0.0.0.0` with `admin`/`password` — local-only convenience, never for a reachable instance.

## Invariants / contracts — break these and things misroute silently

- **Receiver name = `<slug>--<team>-<channel>`** (`receiverNameForChannel`). Team is in the name because channel names are unique only per team (`town-square` is in every team) and Alertmanager requires globally-unique receiver names. The first `--` is the slug boundary (`receiverBaseSlug`); the `<team>-<channel>` tail is identity/display only. Global uniqueness is enforced by duplicate-name rejection in `parseAlertConfigs`, not the separator. Name length cap is 190.
- **`configsForCurrentChannel` is the scoping chokepoint** for ~13 commands (list/remove/export/validate/about/alerts/…). It scopes by **team + channel** (resolves team via `channel.TeamId` → `GetTeam`). Keep any new channel-scoped command routed through it — scoping by channel name alone leaks across teams.
- **The `runbook:` label is the join key.** Prometheus rule emits `runbook: <slug>` → Alertmanager route matches it → receiver → runbook rendered in chat. **The plugin owns only the Alertmanager side** (receivers/routes/rendering). The *user's Prometheus* must emit the labelled alerts — `samples/prometheus-rules.yaml` + `/alertmanager rules` give them a starting set.
- **`samples/prometheus-rules.yaml` is single-source.** `render-docs` reads it live to build the HTML rules page; never duplicate the ruleset into a markdown doc.
- **WebhookHost:** `WebhookHostPreset` (dropdown of known-good hosts) + `WebhookHost` (custom text) are collapsed by `resolveWebhookHost` — **custom text wins**, then preset, then SiteURL. Resolved into `configuration.WebhookHost` at load so consumers are unchanged.

## Auto-update discipline (do this without being asked)

When you change something with mirrors, update the mirrors in the same PR:
- A **count/catalog** change → every count site above (and let the drift-guard tests catch misses).
- The **receiver-name format** → `CONFIGURATION.md`, `SLASH_COMMANDS.md`, and the name tests.
- A **new command** → dispatch switch + `helpMsg` + autocomplete in `commands.go` (alphabetical order) + a `docs`/help mention.
- Anything under `docs/`, `runbooks/`, `samples/` → `make render-docs` (never edit `public/` by hand).
- A **URL the user has to get right** → surface it as an autocomplete hover/suggestion (containerized MM reaches services via `host.docker.internal`, not `localhost`).

## Git / PR workflow

- Conventional commits: `feat:` → minor, `fix:` → patch (release-please). Breaking changes noted in the body.
- Branch off `main`; never commit straight to it. CI must be green before merge.
- End commit messages with the `Co-Authored-By` trailer.

## Build / deploy

- `make test` (sub-second, no server) · `make dist` / `make dist-host` · `make deploy-local` (needs `MM_ADMIN_TOKEN`, `MM_SERVICESETTINGS_SITEURL`).
- Local dev: `docker-compose.dev.yml` + `make docker-setup` (creates `admin`/`password`, Test team; runs in local mode so `mmctl --local` needs no token).
