# Development

Local build, test, and deploy workflow.

## Prerequisites

- **Go 1.26+** (pinned in `go.mod` and `build/*/go.mod`)
- **GNU Make**
- **Python 3** (used by `bundle-host` to filter `plugin.json` to one arch)
- **A Mattermost server** to deploy against — local Docker stack at `~/git/deploy-local/docker/mattermost/` works
- **An admin token** (Personal Access Token from a sysadmin user) for the deploy targets

No frontend toolchain. The plugin doesn't ship a webapp.

## Build

```bash
make server          # cross-compile the plugin binary for all 5 supported targets
make dist            # full build + bundle into dist/com.mattermost.alertmanager-<version>.tar.gz
make test            # go test -race across server/...
make                 # check-style + test + dist
```

Output is `dist/com.mattermost.alertmanager-<version>.tar.gz` — paste this into System Console → Plugins → Plugin Management → Upload Plugin, or use `make deploy-local`.

### Host-only bundle (faster dev iteration)

```bash
make dist-host         # build only host's GOOS+GOARCH (~17 MiB instead of 67 MiB)
make deploy-host-local # build + upload to http://localhost:8065
```

`plugin.json` gets filtered server-side via a Python3 inline script — `server.executables` ends up with just the host's entry. Round-trip on a modern Mac is ~5 seconds.

## Deploy

The Makefile's `deploy` target uses `pluginctl` (a small helper in `build/pluginctl/`) to upload via the Mattermost Client4 API.

### Mode 1 — HTTPS with admin token (most common)

```bash
export MM_SERVICESETTINGS_SITEURL=http://localhost:8065
export MM_ADMIN_TOKEN=...
make deploy
```

Or use the shortcut for `localhost:8065`:

```bash
export MM_ADMIN_TOKEN=...
make deploy-local        # multi-arch
make deploy-host-local   # host-arch-only (faster)
```

### Mode 2 — HTTPS with admin username + password

Falls back if `MM_ADMIN_TOKEN` is unset.

```bash
export MM_SERVICESETTINGS_SITEURL=http://localhost:8065
export MM_ADMIN_USERNAME=sysadmin
export MM_ADMIN_PASSWORD=...
make deploy
```

Prefer the token path — passwords end up in shell history.

### Mode 3 — Unix socket (mattermost-server dev mode)

```bash
export MM_LOCALSOCKETPATH=/var/tmp/mattermost_local.socket
make deploy
```

## Iterate

```bash
# edit code
make deploy-host-local
# trigger /alertmanager <something> to test
make reset    # disable + re-enable the plugin (forces OnActivate to re-run)
```

Useful surrounding targets: `make disable`, `make enable`, `make reset`, `make clean`.

## Smoke test

Once the plugin is loaded:

1. **Plugin alive:** `/alertmanager about` returns version info.
2. **Add the canonical set:** `/alertmanager add testing alerts-smoke http://localhost:9093`. Plugin creates 20 receivers, DMs you the assembled YAML files.
3. **Verify webhooks created:** Open System Console → Integrations → Incoming Webhooks → see entries named `Alertmanager: <slug>--alerts-smoke`.
4. **List receivers in the channel:** `/alertmanager list` from `~alerts-smoke`. Should show all 20 in a table.
5. **Detail card for one:** `/alertmanager config high-cpu-usage` from `~alerts-smoke`. Should show metadata + slack_configs YAML.
6. **Test the post path:**
   ```bash
   # Use any api_url from the DM'd alertmanager-receivers.yml.
   curl -X POST '<api_url>' \
     -H 'Content-Type: application/json' \
     -d '{"text": "Smoke test from curl", "channel": "alerts-smoke"}'
   ```
   Post lands in `~alerts-smoke` as `@alertmanagerbot`.
7. **Re-export:** `/alertmanager export` should produce the same receivers + routes YAML as the original add.
8. **Bulk cleanup:** `/alertmanager remove all --force` deletes all 20 receivers and their webhooks.

## Verifying persisted state

Don't use `/api/v4/config` to check plugin settings — the global config endpoint sanitizes plugin config out of its response. Use one of:

```bash
# Cleanest — mmctl bypasses the API sanitization
mmctl config get PluginSettings.Plugins.com\.mattermost\.alertmanager

# Or restart the plugin and see if state survives — the definitive test
mmctl plugin disable com.mattermost.alertmanager
mmctl plugin enable com.mattermost.alertmanager
# wait ~5s, then run /alertmanager list in any channel
```

If `/alertmanager list` still shows your entries after the disable/enable cycle, save persisted.

## Troubleshooting

### `Unable to find manifest for extracted plugin` on macOS

**Cause:** macOS BSD `tar` emits AppleDouble files (`._*`) for every entry. The top-level `._<plugin-id>` confuses Mattermost's "exactly one top-level directory → recurse" extraction heuristic.

**Fix:** the Makefile sets `COPYFILE_DISABLE=1` and adds `--exclude='._*'` on the bundle's tar invocation specifically to suppress this. If you have an old tarball built before that fix, rebuild with `make clean dist`.

### `Permission denied` on `CreateUserAccessToken`

The plugin mints an ephemeral PAT for the calling sysadmin to authenticate Client4 calls (the only API path Mattermost exposes for webhook CRUD). If your install has Personal Access Tokens globally disabled at System Console → Integrations → Integration Management → Enable Personal Access Tokens, the plugin can't mint the PAT and `/alertmanager add` will fail.

**Fix:** enable PATs in System Console. They're enabled by default in fresh installs.

### Posts show as the calling admin, not `@alertmanagerbot`

**Cause:** the System Console toggles for username/icon override are off. The plugin auto-enables them at activation, but if your install has `config.json` mounted read-only (typical in some Helm charts), `SaveConfig` silently fails.

**Fix:** Manually set both to true at System Console → Integrations → Integration Management:

- `Enable Integrations to override usernames`
- `Enable Integrations to override profile picture icons`

The webhook already has the bot username and icon URL stored on its database row; these toggles control whether Mattermost honors them at post time. If they're stuck at false despite the plugin's attempt to flip them, check MM logs for `"could not enable integration override settings"` to see the underlying error.

### `context deadline exceeded` on `/alertmanager status` / `alerts` / `silences`

**Cause:** the receiver's `alertManagerURL` is `http://localhost:9093` (or `127.0.0.1`). Inside the Mattermost container, `localhost` resolves to the Mattermost container itself, not the host or sibling Docker containers. So the plugin's outbound HTTP call to Alertmanager goes nowhere.

The same issue applies in reverse: the `slack_configs.api_url` value in your `alertmanager.yml` must use `host.docker.internal:8065` (Docker) or the cluster-internal MM service URL (K8s) to reach Mattermost from inside the Alertmanager container.

**Fix for the api_url side:** set the `WebhookHost` plugin setting (System Console → Plugins → Alertmanager) to the cross-container or cluster-internal MM URL, then re-run `/alertmanager export` to get fresh YAML with corrected URLs. Paste into alertmanager.yml, reload AM.

**Fix for the alertManagerURL side:** re-add the receiver with the correct AM URL:

```
/alertmanager remove all --force
/alertmanager add <team> <channel> http://host.docker.internal:9093 [set]
```

If you're running Mattermost directly on the host (not in Docker/K8s), `localhost` is fine for both sides.

### `connection refused` on `make deploy` against localhost:8065

Either Mattermost isn't running, or it's bound to a port other than 8065. Verify:

```bash
curl -sS http://127.0.0.1:8065/api/v4/system/ping
# expect: {"status":"OK"}
```

Use `127.0.0.1` not `localhost` in `MM_SERVICESETTINGS_SITEURL` — macOS often resolves `localhost` to IPv6 first, and Docker Desktop binds IPv4 only.

### `404` on plugin public files that exist on disk

**Cause:** Go's `http.ServeFile` (which Mattermost calls under the hood) auto-redirects any URL ending in `/index.html` to `./`, and Mattermost's plugin public file handler 404s URLs ending in `/`. So a file named `index.html` is unreachable via the plugin's public route.

**Fix:** the renderer at `build/render-docs/main.go` emits the landing page as `home.html` instead. Do not rename it back to `index.html` without changing the underlying behavior.

### Inspecting the plugin install dir without `docker exec`

Recent Mattermost container images strip userland utilities — `ls`, `cat`, etc. don't exist in the container. Two workarounds:

```bash
# docker cp to copy a directory to your host
docker cp mattermost-mattermost-1:/mattermost/plugins/com.mattermost.alertmanager/ /tmp/alertmanager-install

# busybox sidecar against the same volume
docker run --rm -v mattermost_mm-plugins:/p busybox ls -la /p/com.mattermost.alertmanager/
```

## Embedded docs

The contents of this `docs/` directory are embedded into the plugin binary via `go:embed` (see `docs_embed.go` at the repo root). `/alertmanager docs <topic>` serves the embedded content rather than reading from disk, so the docs in the binary always match the binary's behavior.

When editing docs:

1. Update the relevant `.md` file in `docs/`.
2. Rebuild: `make server` (the embed runs at compile time).
3. Re-render the HTML: `make render-docs` (renders to `public/help/*.html`).
4. The next deploy ships both forms.

Don't add files outside the `docs/` directory — the embed directive pattern is `docs/*.md`.
