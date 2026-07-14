# Contributing

Thanks for picking up the codebase. This plugin bridges Prometheus
Alertmanager to Mattermost via native incoming webhooks — no novel
networking, no shared secrets, just YAML rendering and slash commands.
The contribution surface is small on purpose.

## Repo layout

```
.
├── server/                Go plugin code (server-side)
│   ├── alertmanager/      Thin wrapper around AM REST API
│   ├── cmd_*.go           One file per /alertmanager subcommand
│   ├── http.go            ServeHTTP — admin inventory + metrics + autocomplete
│   ├── reconciler.go      Cluster-leader background job
│   └── *_test.go          Unit tests
├── docs/                  Markdown sources (rendered to HTML at bundle time)
├── runbooks/              The 30 canonical SRE runbook docs
├── samples/               Sample Prometheus rules for the 30 runbooks
├── public/                Static assets bundled into the plugin tarball
└── build/                 Build tooling (manifest, render-docs)
```

## Local development

You need Go 1.26+ and `make`.

```bash
# Run the unit test suite (race detector on)
go test -race -count=1 ./...

# Lint (golangci-lint v2.12.2 minimum)
golangci-lint run --timeout=5m

# Build the plugin tarball — output at dist/com.mattermost.alertmanager-<ver>.tar.gz
make dist
```

For a feedback loop with a running Mattermost, use `make deploy` with
`MM_SERVICESETTINGS_SITEURL`, `MM_ADMIN_USERNAME`, `MM_ADMIN_PASSWORD`
set. See `build/setup.mk` for the full env-var set.

## Adding a new runbook

Each of the 20 receivers maps 1:1 to a Markdown file in `runbooks/`.
To add a 21st:

1. Copy [`runbooks/TEMPLATE.md`](runbooks/TEMPLATE.md) to
   `runbooks/<slug>.md`. The slug must be lowercase kebab-case and
   match the receiver name you want the plugin to use.
2. Fill in the template. The `## Quick diagnostics` section is the
   load-bearing one — the plugin extracts the first three fenced
   code blocks and inlines them into every alert post. Each code
   block must follow the **WHERE / WHAT / READ** convention:
   - **WHERE** — exact tool and context (`shell with kubectl context
     set`, `Grafana → Explore (Prometheus data source)`, `psql to
     primary`, etc.). No ambiguity.
   - **WHAT** — what the command does and the surrounding theory.
     Why this query? What state does it surface?
   - **READ** — concrete value interpretation. "0 = no throttling.
     >0.1 = throttled 10%+ of the time, indicates CPU contention."
     Plus the suggested next action if the value is bad.
   Use the placeholder tokens `<namespace>`, `<pod>`, `<instance>`,
   `<job>`, etc. where labels should be substituted at delivery
   time — the plugin's renderer fills them from the alert's labels.
3. Add the slug to the appropriate category in
   `server/cmd_scaffold.go` (the `scaffoldSets` map).
4. Add a corresponding Prometheus alert rule to
   `samples/prometheus-rules.yaml`. Set the rule's `runbook:` label
   to the slug — that's how Alertmanager routes the alert. The
   rule's emitted labels must cover what the runbook's diagnostic
   commands reference (declared in the runbook's "Required labels"
   footer).
5. Run `go test ./...` and `make dist` to confirm the embedded
   runbook bundles and the new receiver is picked up by
   `/alertmanager add`.

## Commit conventions

Subject line: imperative, ≤72 chars. Body wrap ~72 chars per line.
Example:

```
Add database-query-timeout runbook to compute set

Captures the case where a connection pool exhausts before a slow
query finishes. Distinct from database-high-latency, which is
about steady-state p95 drift.
```

Use `Co-Authored-By:` trailers when the work was paired.

## PRs

Open a PR against `main`. CI runs three jobs — Test, Lint, Build —
all of which are required by branch protection. Don't merge until
they're green.

Keep PRs focused. A new runbook is one PR. A refactor across the
template rendering is a different PR. Smaller is easier to review.

## Code style

- **No comments restating what the code does.** Comments explain
  WHY a non-obvious choice was made — see existing files for the
  pattern.
- **No backwards-compatibility shims.** This plugin is pre-1.x in
  spirit even though the version says 1.0.0. If something gets
  renamed or removed, change the code and move on; don't leave
  deprecated aliases behind.
- **Channel-scoping is load-bearing.** Slash commands filter what
  the user can see to receivers bound to the current channel.
  Don't add unscoped variants without explicit reason.
- **Sysadmin and team-admin are the only privilege tiers.**
  Everything else is open to channel members.

## Reporting bugs

For non-security bugs: open a GitHub issue with reproduction steps,
plugin version, Mattermost version, and Alertmanager version.

For security issues: see [SECURITY.md](SECURITY.md) — don't open
public issues for vulnerabilities.
