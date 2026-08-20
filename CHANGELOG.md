# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.4](https://github.com/mattermost-community/mattermost-plugin-alertmanager/compare/v1.3.0...v1.0.4) (2026-08-20)


### ⚠ BREAKING CHANGES

* the receiver list moved from the plugin config map (`alertconfigsjson`) to the plugin KV store (`alertconfigs:v1`) with no automatic migration. After upgrading, receivers must be re-created with `/alertmanager add`; the old config value is ignored and its webhooks are orphaned.

### Features

* 30-runbook catalog, in-plugin sample rules, WebhookHost UX + cross-team receiver-name fix ([b45c29b](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/b45c29b87ce60f214c8def9d04685ad07ce300f2))
* **add:** --private to create the destination channel as private ([64aa9b3](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/64aa9b317c64fff54551a6589cc4a4954bca494a))
* admin route-tester — severity only for end-to-end + by-team scope ([0d205ad](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/0d205ad072fd9077e688be9f0e1869d67d099c06))
* AlertmanagerConfig (v1alpha1) CRD renderer ([cccd405](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/cccd405f0885525689bb9c5059c1ca8884a75a3f))
* **autocomplete:** add Prometheus Operator Alertmanager URL suggestion ([6b39aec](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/6b39aec9553175f0674fff770eab099f8371e2fb))
* **commands:** add-custom for generic (non-runbook) receivers ([3d14717](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/3d147177c432662ce7abcac8ce604fe8a57b88ef))
* complete sample rules to 31 rules (all 30 runbooks) + CI validation ([93fd044](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/93fd0449432eee523c8e23293183babe99e3f85a))
* expand alert catalog to 30 runbooks with a security category ([56e6633](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/56e6633fcd27a48f0de45363cbeb9364405893fa))
* generic (non-runbook) receivers via /alertmanager add-custom ([30f8feb](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/30f8febd2c2229b05bbde475f0bb5cb6abba0f76))
* **ha:** propagate receiver-list writes across cluster nodes ([8d3ddce](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/8d3ddce1d60875eb5cf0029fe7585611e30a58b3))
* **inventory:** recognize CRD-managed receivers as OK ([959202a](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/959202a59dcd456090052044d7d82b6b5a271578))
* **metrics:** add metrics-token generate/reveal slash command ([f9f445c](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/f9f445cdc0462c0ab8432887d81f1ba1c4904ba1))
* offline CRD schema validation + simpler fallback receiver ([feec9de](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/feec9de85f20b64a35bad58a21e55e7fce90b982))
* Prometheus Operator (CRD) deployment support ([c8ea602](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/c8ea6024f6d7eb5385a5c499ab5b7ee035d4d6c1))
* ship sample Prometheus rules in-plugin + trim System Console settings ([ea2f522](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/ea2f522df4b2252ffca140e3cb25e8445c666938))
* split am-url hint into three hover-able suggestions ([2c6edc0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/2c6edc0dcdb4de8adae4e6b9fd06ce32d2bcb84b))
* WebhookHost preset dropdown + richer am-url autocomplete hint ([72f7131](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/72f7131557ed3b32ad91da0f55979e28e8ac838e))
* **webhook:** name Mattermost webhooks to mirror the receiver format ([b577fc7](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/b577fc74e98bc725aed6f41b617d423eb5fcfe6d))
* wire --format=standard|crd into add and export ([53537f7](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/53537f7df3f4086a9779eaa27258a30e804a8c2d))


### Bug Fixes

* **add-custom:** address Copilot review (auth, runbook rendering, DM, CRD) ([3f54633](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/3f546335b840114fcd3ec28ef963ffdb5be70d20))
* **add-custom:** address Copilot suppressed-review findings (C8-C12) ([71af32e](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/71af32e5f815bf3b2aa0f9648f88681e0ea850b2))
* **add-custom:** custom-only CRD group must not emit ^()$ parent matcher ([84713df](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/84713dfa5b9d717d4e63fc35daa72c06ded65a39))
* **add-custom:** valid empty routes array for custom-only CRD; harden /add auth ([1afa3a0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/1afa3a096484721e7b92289adea4ba19db4323d2))
* **autocomplete:** load channel list for add-custom, not just add ([ddb67c3](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/ddb67c3a0d0023ace06827ac9886db45ba897e36))
* **build:** cache manifest vars (:=) so clean can't empty the release bundle ([5f2bc6a](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/5f2bc6a2819cf383e2a9bb2f84321b2204fe0523))
* **build:** cache manifest vars once (:=) so clean can't break the release ([b64be68](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/b64be68d487affbddc8eed3ad25324605f9f5016))
* **ci:** make Grype SARIF ingestible by Code Scanning ([48369ef](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/48369ef52c21903fd964d1ae8d3a752331bb8cc9))
* **ci:** make Grype SARIF ingestible by Code Scanning ([d1c511d](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/d1c511d61917551d256653bafae93f6765eb263e))
* **config:** address review nits on RMW lock ([75f34d0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/75f34d095a0c08e38f7b1021358519627620557d))
* **config:** close the config lost-update race (atomic read-modify-write) ([f83ffa9](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/f83ffa9060b3e9f0918987b3d0ba4d3cf8f8dd28))
* **config:** make config read-modify-write atomic to close lost-update race ([ecdb128](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/ecdb1285fd7ef659b6a3525e17c82b0df8371585))
* **deps:** bump golang.org/x/mod to v0.40.0 (clear HIGH CVEs blocking the v1.4.17 release) ([15a8fa3](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/15a8fa3fd74f64b394e4655671f135d1f428199e))
* **deps:** bump golang.org/x/mod to v0.40.0 to clear HIGH CVEs ([3be8759](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/3be8759aab23d34929a53f638bc5f9cf8db2026c))
* **deps:** bump grpc + x/text to clear HIGH CVEs (combines dependabot [#42](https://github.com/mattermost-community/mattermost-plugin-alertmanager/issues/42)-45) ([79ed360](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/79ed360ab258277f1d3e067a856205fdd70dbc9e))
* **deps:** bump grpc and x/text to clear HIGH CVEs; fold in dependabot updates ([4359876](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/43598764b5bfbcd1c4bcebb36cba3d5e0d8c4e51))
* **ha:** serialize peer config reload with local writes ([adba264](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/adba26467d2174123b05979b61a9615993d35f41))
* **inventory:** parse operator column-0 receiver dashes in AM config ([c1554a5](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/c1554a56e399c5cef2a0fbd9ed1f605d762a796f))
* **inventory:** send X-CSRF-Token on route-tester POST (not just XHR header) ([648e1f5](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/648e1f5eb0f09e0eae8efd593f358c4b0bb52d07))
* **lint:** use strings.Cut in currentTargetSection (modernize) ([9eb65d5](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/9eb65d530ff95c20a74bdebde4359e07e924e279))
* make the Alertmanager HTTP client swap race-free + close bodies on retry (4th-pass review) ([e14be77](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/e14be7782685e3a4ccd58be082c44859987837dd))
* migrate to prometheus/alertmanager v0.33 API surface ([64dce85](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/64dce850168a54acba76512c9b52ac12e17272d8))
* **rotate:** dedup group-webhook double-rotation in rotate --overdue (pre-existing, review) ([4fbc9f2](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/4fbc9f2ab6ff1bf0bc85db080147f77b161c970d))
* **rotate:** delete the old webhook only after the CAS write commits (CL-24 review) ([1ab6ff9](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/1ab6ff9a48c52685b58e8764feaaa00c4b2a3101))
* **rotate:** report all overdue group members when a group rotation fails (2nd-pass review) ([1a66f44](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/1a66f44d1a0014259668e1e4e35ea3d2c1d20ed9))
* **security:** address Copilot review findings on [#57](https://github.com/mattermost-community/mattermost-plugin-alertmanager/issues/57) ([a5c1b4f](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/a5c1b4faade7c816b1e9f8b160cb65f2479fa7f8))
* **security:** fifth review pass (F1-3) — cold-start deny-all allowlist, flow-style diff redaction, repair applies sanitized list to memory ([492da4d](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/492da4dfdb73ccf83f481407ec54b22ff73c2931))
* **security:** fourth review pass (F1-4) — proxy SSRF, repair sanitize, block-scalar/proxy_url redaction ([0a9e372](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/0a9e3728d723aba51e15c0f3dcf2d49f791784a3))
* **security:** reject trailing ?/# in AM URL; sanitize on load (F-002) ([838a935](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/838a9359b001f21da88276581e8e041ceaaa83bf))
* **security:** resolve independent security review (F-001..F-007) ([615abc8](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/615abc875caa6ff1f25d3e3cf3755a52a4efcc7d))
* **security:** scrub hook ID from Client4 error text; fail-closed rollback ([2b9471c](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/2b9471c166e75907e4a0be32547cd2c66685e42b))
* **security:** second review pass — block private by default + leak/HA fixes ([9995d02](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/9995d029e3d7b8b3ca8b41bd769b8baf8eb2c7eb))
* **security:** sixth review pass (F1-5) — webhook-host SSRF/injection parity + remove-identity + info-disclosure ([fb1e17a](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/fb1e17a64f92854e96e21e4aebc3ee5af519d137))
* **security:** third review pass (F1-4) — allowlist guardrails + safe repair + diff redaction ([e867773](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/e867773235078e9ec174de8a7ce45bb028734d55))
* team-qualify receiver names to prevent cross-team channel collisions ([ccacf30](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/ccacf303246443ef7aa923dd48702e2516a21475))
* **validate:** pad synthetic firing alert TTL past group_wait ([3f46770](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/3f4677093948a1692b128f337d4e8456c67086cb))
* **webhook:** guard webhookDeleteError against a nil cause ([8c92d43](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/8c92d434314d3a4ddcd4c45692feff917dd843d7))


### Performance

* **ha:** broadcast peer reload off the write lock ([ae6dfe1](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/ae6dfe1eecf1c2a8ef6f91c79f9f718025e940a7))


### Dependencies

* **actions:** Bump actions/checkout from 4.2.2 to 7.0.0 ([9f6772f](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/9f6772fe3b807358f069fef6fa05a076d049beed))
* **actions:** bump actions/configure-pages from 5.0.0 to 6.0.0 ([907bf65](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/907bf65cff6f0b68a9cd5502991ffa30eb78ac47))
* **actions:** Bump actions/deploy-pages from 4.0.5 to 5.0.0 ([b15de40](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/b15de40681a20bd842ae4de3639e0afc0b24bac2))
* **actions:** bump actions/setup-go from 5.5.0 to 6.5.0 ([8693c64](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/8693c64e9ccaaabd1fe673abea21f84c2e2b749c))
* **actions:** bump actions/setup-go from 6.5.0 to 7.0.0 ([342f5df](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/342f5df72c603e17cb887ec03e926189158df6b4))
* **actions:** bump actions/upload-artifact from 4.4.3 to 7.0.1 ([35a5da5](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/35a5da5f99ef125c3991e54359d59a2c90bec862))
* **actions:** bump actions/upload-pages-artifact from 3.0.1 to 5.0.0 ([26c5e86](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/26c5e86585e2888f5cbebeaa1be9641a8d7c3c80))
* **actions:** bump anchore/scan-action from 5.2.0 to 7.4.0 ([f3ace25](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/f3ace259f01a28ebd1afafc807a6cfac9bc5d735))
* **actions:** bump golangci/golangci-lint-action ([2f14ec5](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/2f14ec5829bf9e29eaa1d47de07495ad8f6b41e7))
* **actions:** bump googleapis/release-please-action from 4.2.0 to 5.0.0 ([4590003](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/459000386b7e567c28cebbd557b002d55e2d3645))
* **actions:** bump softprops/action-gh-release from 2.3.2 to 3.0.1 ([32e18a6](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/32e18a6b3602b28714046c0f1bcf06bd1727d188))
* **actions:** bump the actions-minor-patch group across 1 directory with 4 updates ([766a456](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/766a456d51c42db186283889433161bfbcf0096a))
* **actions:** bump the actions-minor-patch group across 1 directory with 5 updates ([ee6c9a8](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/ee6c9a85e3362ebae1d264ec6ad7d4dc4f65bc92))
* **actions:** bump the actions-minor-patch group with 4 updates ([59b103c](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/59b103ca41dae84d6dee6b2cdb0e099df2561bae))
* **actions:** Bump the actions-minor-patch group with 4 updates ([d312f7c](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/d312f7c54e945baf7699447366edfb92ac3fbff4))
* **actions:** bump the actions-minor-patch group with 5 updates ([c0035e0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/c0035e0ee80bf7d250a2718b74816d89299e7022))
* **build/render-docs:** bump github.com/yuin/goldmark ([d6a5cda](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/d6a5cda8f952c2d2608a7ef12d26e0424d76db7a))
* **build/render-docs:** Bump github.com/yuin/goldmark ([beb0e7f](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/beb0e7f6b90a3aca95f20a4e05a56c8b780593f5))
* **build/render-docs:** Bump github.com/yuin/goldmark ([94a0c1b](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/94a0c1b3454f4497381b4183dd258c08f4e393c7))


### Documentation

* document the intentional config→KV clean break + fix stale comments (CL-19 review) ([bc66078](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/bc6607878028c90c1f276e44a0ac21b56c9d6b28))


### Chores

* release 1.0.4 ([dca70a8](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/dca70a8b00284f503d9a65387e9953e8619610bf))

## [1.4.17](https://github.com/mattermost-community/mattermost-plugin-alertmanager/compare/v1.2.0...v1.4.17) (2026-08-20)

Rolls up the `add-custom` feature (previously drafted as 1.3.0, never tagged) plus a
broad round of hardening and reliability work. Versions 1.3.x–1.4.16 were internal
development bumps and were never released as tags.

> **⚠️ BREAKING — action required for an in-cluster Alertmanager.** The plugin now
> restricts which network destinations it will contact for Alertmanager: **non-public
> (private / in-cluster) addresses must be explicitly allowlisted**. If your
> Alertmanager is reachable only on a private or in-cluster IP (e.g.
> `alertmanager.monitoring.svc.cluster.local`), set the new **`AlertManagerAllowedCIDRs`**
> setting (System Console → Plugins → Alertmanager) to its network — the Service
> ClusterIP as a `/32`, or your Kubernetes Service CIDR — **before or right after
> upgrading**, or Alertmanager calls (`alerts` / `status` / `validate` / inventory) will
> be refused as an un-allowlisted destination. A publicly-routable Alertmanager needs
> no change.

### Features

* `/alertmanager add-custom <team> <channel> <am-url> <name>` — create a generic (non-runbook) receiver named `<name>--<team>-<channel>` with its own webhook, for custom alerts that don't map to a shipped runbook. No `runbook=` route is generated; `/alertmanager export` includes a commented matcher stub you wire manually. See `/alertmanager docs configuration`.
* `--private` on `/alertmanager add` / `add-custom` — create the destination channel as private when it doesn't already exist.
* `/alertmanager metrics-token generate|reveal` — manage the bearer token for the Prometheus `/metrics` endpoint from chat.
* High availability: receiver-list changes propagate across cluster nodes, so `list` / `export` / scope checks stay consistent on every node.
* `/alertmanager repair` (system admin) — rebuild the receiver list if its stored value ever becomes unreadable.
* Autocomplete: suggest the Prometheus Operator Alertmanager URL; load the channel list for `add-custom`.
* Mattermost incoming webhooks are named to mirror the receiver format (`<base>--<team>-<channel>`) for easy identification in System Console.

### Hardening & reliability

* A broad set of internal hardening and robustness improvements across input handling, configuration storage, outbound network calls, authorization scoping, and multi-node consistency. The new `AlertManagerAllowedCIDRs` setting (see the migration note above) is the only operator-visible change.

### Bug Fixes

* `rotate`: report all overdue members when a shared-webhook group rotation fails; dedup group double-rotation in `rotate --overdue`.
* `validate`: pad the synthetic firing alert's TTL past `group_wait` so the end-to-end delivery check works.
* `inventory`: parse operator column-0 receiver dashes in the Alertmanager config.
* Alertmanager HTTP client swap made race-free; response bodies closed on retry.

## [1.2.0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/compare/v1.1.0...v1.2.0) (2026-08-12)


### Features

* Prometheus Operator (CRD) deployment support: `--format=crd` on `/alertmanager add` and `/alertmanager export` emits an `AlertmanagerConfig` (v1alpha1) + Secret to `kubectl apply`, with `--namespace=` (default `monitoring`). Generated CRDs are schema-validated offline in CI via kubeconform. See `/alertmanager docs kubernetes`.
* recognize CRD-managed receivers as healthy in the inventory and `/alertmanager validate`: receiver-name matching now strips the Prometheus Operator's `<namespace>/<config>/` prefix, so operator-managed receivers show `OK · via operator` instead of "Not in AM YAML"


### Bug Fixes

* validate the user-supplied `--namespace` on `add`/`export` — reject non-RFC-1123 values before they reach generated manifests
* bump grpc and golang.org/x/text to clear HIGH CVEs


### Dependencies

* group Dependabot security updates into one PR per run; bump the go-minor-patch, goldmark, and GitHub Actions groups

## [1.1.0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/compare/v1.0.6...v1.1.0) (2026-07-14)


### Features

* expand the alert catalog to 30 runbooks with a new security category
* complete the sample Prometheus rules to 31 rules covering all 30 runbooks, and validate them in CI with `promtool check rules`
* ship the sample rules in-plugin — a browsable HTML page plus raw download — surfaced via a new `/alertmanager rules` command and a System Console link
* add a WebhookHost preset dropdown (Docker Desktop / Kubernetes / custom) and three hover-able `am-url` autocomplete suggestions on `/alertmanager add`
* admin route-tester: show the severity field only in end-to-end mode, and add a by-team scope dropdown that cascades the channel list
* trim System Console settings help text to one sentence each


### Bug Fixes

* team-qualify receiver names (`<slug>--<team>-<channel>`) so same-named channels in different teams no longer collide or misroute


## [1.0.6](https://github.com/mattermost-community/mattermost-plugin-alertmanager/compare/v1.0.5...v1.0.6) (2026-07-06)


### Dependencies

* **actions:** bump anchore/scan-action from 5.2.0 to 7.4.0 ([f3ace25](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/f3ace259f01a28ebd1afafc807a6cfac9bc5d735))
* **actions:** bump actions/upload-artifact from 4.4.3 to 7.0.1 ([35a5da5](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/35a5da5f99ef125c3991e54359d59a2c90bec862))
* **actions:** bump actions/setup-go from 5.5.0 to 6.5.0 ([8693c64](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/8693c64e9ccaaabd1fe673abea21f84c2e2b749c))
* **actions:** bump googleapis/release-please-action from 4.2.0 to 5.0.0 ([4590003](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/459000386b7e567c28cebbd557b002d55e2d3645))
* **actions:** bump the actions-minor-patch group with 5 updates ([c0035e0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/c0035e0ee80bf7d250a2718b74816d89299e7022))
* **go:** bump the go-minor-patch group with 2 updates ([70ff7f0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/70ff7f0dc2d09bfc4064e5925357b8c432eea0ff))
* bump golang.org/x/net from 0.54.0 to 0.55.0 in /build ([646f9ae](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/646f9ae0773a51e9ec9b213ab7aedfb68905a347))

## [1.0.5](https://github.com/christopherfickess/mattermost-plugin-alertmanager/compare/v1.0.4...v1.0.5) (2026-06-30)


### Bug Fixes

* migrate to prometheus/alertmanager v0.33 API surface ([64dce85](https://github.com/christopherfickess/mattermost-plugin-alertmanager/commit/64dce850168a54acba76512c9b52ac12e17272d8))


### Dependencies

* **actions:** bump golangci/golangci-lint-action ([2f14ec5](https://github.com/christopherfickess/mattermost-plugin-alertmanager/commit/2f14ec5829bf9e29eaa1d47de07495ad8f6b41e7))
* **actions:** bump softprops/action-gh-release from 2.3.2 to 3.0.1 ([32e18a6](https://github.com/christopherfickess/mattermost-plugin-alertmanager/commit/32e18a6b3602b28714046c0f1bcf06bd1727d188))
* **actions:** bump the actions-minor-patch group across 1 directory with 4 updates ([766a456](https://github.com/christopherfickess/mattermost-plugin-alertmanager/commit/766a456d51c42db186283889433161bfbcf0096a))

## [1.0.4](https://github.com/christopherfickess/mattermost-plugin-alertmanager/compare/v1.0.3...v1.0.4) (2026-06-30)


### Chores

* release 1.0.4 ([dca70a8](https://github.com/christopherfickess/mattermost-plugin-alertmanager/commit/dca70a8b00284f503d9a65387e9953e8619610bf))

## [Unreleased]

## [1.0.3] - 2026-06-12

Webhook consolidation. `/alertmanager add` now creates **one shared
Mattermost webhook per group** instead of one per receiver. A
`/alertmanager add ... compute` invocation that previously minted 6
Mattermost webhooks now mints 1, with all 6 receivers' `slack_configs`
blocks pointing at the same `api_url`. Each receiver keeps its own
runbook-specific text template — only the webhook URL is shared.

Same security posture: a leaked webhook URL grants post-as-bot
permission to the channel, which is identical whether the channel is
served by 1 or 20 webhooks. The blast radius doesn't change; the
secret count drops 20x in the worst case.

### Added

- **`--severity` flag on `/alertmanager validate --end-to-end`.**
  Controls which severity the synthetic alert is fired at. Accepted
  values: `warning` (default), `critical`, `info`, or `all`.
  `--severity=all` fires four synthetic alerts per receiver
  (warning + critical + info + resolved) so an operator can visually
  verify every render path — sidebar color mapping, the new
  `[SEVERITY:AlertName]` title shape, and the `[✓ RESOLVED:]`
  variant — in one command. The resolved alert is fired with
  `endsAt` in the past so AM immediately routes the resolved
  notification path.
- **`all` option in the `/admin/inventory` severity dropdown.**
  Same multi-fire behavior as the slash command; pick `all` when
  running end-to-end mode to verify the visual matrix from the
  admin form.
- **Individual-slug add path.** `/alertmanager add <team> <channel>
  <am-url> high-cpu-usage` now works — creates one receiver + one
  dedicated webhook for that runbook. Previously the `[target]` arg
  only accepted category set names; now it also accepts any runbook
  slug. Webhook display name follows `Alertmanager: <slug>--<channel>`.
- **Group webhook semantics.** Category-set adds (`compute`,
  `database`, etc.) and `all` create a single Mattermost webhook
  named `Alertmanager: <group>--<channel>`. Every receiver in the
  group's `slack_configs` block uses the same `api_url`.
- **`GroupName` field on `alertConfig`.** Persists the unit (set
  keyword or runbook slug) the receiver was created under. Drives
  the refcount-aware webhook lifecycle.

### Changed

- **Alert post title format rewritten.** Old:
  `[FIRING:1] HighCPUUsage (namespace=billing, pod=api-7d9-2xfgs)`.
  New:
  `[WARNING:HighCPUUsage] (namespace=billing, pod=api-7d9-2xfgs)`.
  Severity now leads the title instead of the AM state — SRE eyes
  scan severity first at 3am. Resolved alerts render as
  `[✓ RESOLVED:HighCPUUsage]`. Mixed-severity groups fall back to
  `[ALERT:HighCPUUsage]`. Firing count appears in parens after the
  bracket only when greater than 1 (single-alert groups are the
  common case and `(1 firing)` is noise).
- **Remove is now refcount-aware.** `/alertmanager remove <name>`
  deletes the receiver entry, then deletes the underlying webhook
  only if no other receiver still references it. Group webhook
  survives partial removal; fully orphaned webhooks get cleaned up.
- **Rotate rotates the SHARED webhook.** `/alertmanager rotate
  <grouped-receiver>` rotates the webhook used by every receiver in
  that group. Response message lists every affected receiver and
  (for multi-receiver groups) DMs the merged YAML bundle, same
  shape as `/alertmanager rotate all --overdue`. Legacy receivers
  (pre-v1.0.3, empty `GroupName`) keep per-receiver rotation
  semantics for backwards compatibility.
- **Reconciler dedups webhook probes.** Orphan-detection cycles
  call `GetIncomingWebhook` once per unique webhookID instead of
  once per receiver. Reduces API call rate from N (receivers) to
  K (distinct webhooks), where K ≤ N.
- `parseAlertConfigs` validation relaxed: receivers may share a
  `WebhookID` provided they also share `Team + Channel + GroupName`.
  Mismatched ownership (different groups claiming the same hookID)
  remains a hard reject.

### Migration

Existing v1.0.0–v1.0.2 receivers stay on per-receiver webhooks —
no automatic consolidation. Mixed model: an upgraded install runs
old per-receiver and new shared-webhook channels side by side
without alert-delivery interruption. To migrate one channel:

```
/alertmanager remove all --force
/alertmanager add <team> <channel> <am-url> <target>
```

Paste the new YAML into `alertmanager.yml`, reload AM.

## [1.0.2] - 2026-06-11

Route-simulation and admin-form release. Closes the "validate, don't
just generate" reviewer wedge — operators can now confirm a Prometheus
rule's labels actually route to the expected receiver before they cost
an incident.

### Added

- `/alertmanager validate --simulate <labels>` walks Alertmanager's
  loaded route tree against a supplied label set and reports which
  receiver(s) an alert with those labels would dispatch to. Mirrors
  `amtool config routes test`. Read-only — no synthetic alert fired,
  safe to run repeatedly. Uses
  `prometheus/alertmanager/dispatch.NewRoute` directly so the
  simulation matches AM's runtime behavior exactly.
- Bare `/alertmanager validate --simulate` (no labels) prints a
  preset list of runbook-slug starter expressions — one
  copy-pasteable `--simulate runbook=<slug>` per shipped runbook —
  for discoverability.
- Route tester form on the `/admin/inventory` page. Three modes:
  - **Simulate** — read-only route walk against the AM's loaded config
  - **Webhook test** — POST a hardcoded test payload directly to each
    target receiver's webhook (tests Mattermost side only)
  - **End-to-end** — fire a synthetic alert through AM, AM templates
    and delivers via real `slack_configs` (tests the full chain)
- Cascading dropdowns on the route tester form: Mode → Type →
  Target → Channel → Severity. Type dropdown filters Target options
  (group names vs. individual runbook slugs); Channel dropdown
  filters to channels that actually host at least one matching
  receiver. Computed server-side, applied via client-side JS at page
  load and on dropdown change.
- `/alertmanager list` now shows a Rotated column with human age
  (`today`, `yesterday`, `N days ago`, `never`). Overdue receivers
  (opted-in via `on`, past the global threshold) get a `⚠️` prefix.
- Severity-driven attachment sidebar color in alert posts: warning
  yellow, critical red, info blue, resolved green.

### Changed

- `samples/prometheus-rules.yaml` rewritten so all 20 alert rules
  emit the labels each runbook's "Required Prometheus labels"
  footer expects. Compute rules switched from node-level to
  container-level metrics for `namespace` and `pod` coverage.
  Application rules add `namespace` alongside `service` / `app`.
  Persistent-volume rule joins `kubelet_volume_stats_*` with
  `kube_pod_spec_volumes_persistentvolumeclaims_info` to surface
  `pod`. Inline comments document where a metric doesn't carry a
  label natively (relabel hints for `blackbox_exporter`,
  `metric_relabel_configs` for app metrics, kube-state-metrics joins
  for deployment app labels).
- `README.md` rewritten to lead with the runbook-at-fire-time worked
  example. Two-minute setup pushed down a section; the headline is
  the daily-use value, not the YAML plumbing.
- `plugin.json` description rewritten to match the new README
  positioning.
- `CONTRIBUTING.md` adds an "Adding a new runbook" walkthrough that
  references `runbooks/TEMPLATE.md` and documents the
  WHERE / WHAT / READ convention every Quick diagnostics block
  must follow.

### Fixed

- Inverse drift detection on the inventory page (added in 1.0.1)
  surfaces correctly when AM has a receiver that the plugin doesn't
  track. Receiver-list extraction now correctly skips route entries
  and `slack_configs` sub-blocks during regex parse.

## [1.0.1] - 2026-06-11

Reviewer-feedback release. Five distinct asks closed plus several
bug fixes uncovered during smoke testing.

### Added

- **Webhook rotation reminders.** New `WebhookRotationDays` System
  Console setting (default `0` = off). When set, the background
  reconciler DMs sysadmins when an opted-in receiver hasn't been
  rotated within the threshold. Per-receiver opt-in via trailing
  `on` arg to `/alertmanager add`. 7-day repeat cadence per
  receiver. No auto-rotation by design — Alertmanager has no write
  API, so the plugin reminds but never applies. See
  [`docs/ROTATION.md`](docs/ROTATION.md) for the playbook.
- `/alertmanager rotate all --overdue` rotates only receivers past
  the threshold in the calling channel, DMs the merged updated YAML
  as one paste-ready bundle.
- **Inverse drift section** on `/admin/inventory`. Receivers loaded
  in AM that have no matching plugin entry surface as their own
  orange "AM-only receivers" section. Catches hand-edits of
  `alertmanager.yml` plus post-rotation gaps where AM YAML wasn't
  updated.
- **Schema validation in `export --diff-against-loaded`.** Merged
  YAML runs through `prometheus/alertmanager/config.Load` — the
  same parser Alertmanager uses at reload time. Surfaces
  undefined-receiver references, malformed matchers, and route tree
  errors before the operator pastes.
- **Required Prometheus labels** section in 15 of the 20 shipped
  runbooks. Each runbook now documents the labels it expects on
  incoming alerts so the inline diagnostics block has valid
  placeholders to substitute. The 5 runbooks that don't use
  placeholder substitution are skipped.
- `runbooks/TEMPLATE.md` documents the Required Labels convention
  for new contributors.
- WHERE / WHAT / READ rewrite of every Quick diagnostics section
  across all 19 runbooks that have one. Each fenced code block
  carries:
  - **WHERE** — exact tool and context (`shell with kubectl context
    set`, `Grafana → Explore (Prometheus data source)`, `psql to
    primary`, etc.)
  - **WHAT** — command effect plus surrounding theory
  - **READ** — concrete value interpretation and next action

### Security

- **Redacted other-channels' secrets** in
  `export --diff-against-loaded` output. `api_url`, `password`,
  `service_key`, `routing_key`, `integration_url`, `auth_token`,
  `bearer_token`, `webhook_url`, `url`, and `secret` values in
  CONTEXT lines from receivers not owned by the calling channel
  are masked. Own-channel additions (the operator needs them to
  paste) stay un-redacted. Addition lines (plus-sign prefix) are
  never redacted regardless of channel ownership.
- Validation runs on the un-redacted in-memory merge so YAML
  validation stays reliable even when the displayed diff is
  redacted.

### Changed

- Reconciler cycle now runs orphan pruning AND rotation reminder
  check in the same scheduled job. One leader-elected goroutine
  handles both — no second background goroutine introduced.

## [1.0.0] - 2026-06-10

Initial release. Bridges Prometheus Alertmanager to Mattermost via
native incoming webhooks, with 20 canonical SRE runbook receivers
spanning compute, application, database, storage, networking, and
observability categories.

### Added

- Slash commands (all alphabetized, all channel-scoped where it
  makes sense):
  - `/alertmanager add <team> <channel> <am-url> [set]` — bulk-create
    receivers for a named runbook set (`all`, `application`,
    `compute`, `database`, `networking`, `observability`, `storage`)
  - `/alertmanager remove <name|set|all> [--force]` — delete a
    receiver, a runbook set, or every receiver in the channel
  - `/alertmanager rotate <name>` — delete + recreate the webhook
    with a new hook-id
  - `/alertmanager reconcile` — manual orphan prune (also runs
    automatically every 5 minutes)
  - `/alertmanager list` — receivers bound to the current channel
  - `/alertmanager config <name>` — detail card + slack_configs YAML
  - `/alertmanager export` — DM the assembled receivers.yml +
    routes.yml for the channel
  - `/alertmanager validate [name|set] [--webhook-test] [--end-to-end]` —
    pipeline diagnostics (AM reach, receiver-loaded-in-AM check,
    optional webhook test post, optional synthetic alert delivery)
  - `/alertmanager alerts` / `silences` / `status` — Alertmanager
    API queries, output grouped by Alertmanager URL (one section
    per backend, not per receiver)
  - `/alertmanager expire_silence <name> <silence-id>`
  - `/alertmanager docs [topic]` — embedded documentation
  - `/alertmanager about` — version, settings, receiver counts,
    reconciler health, jump-off links
  - `/alertmanager help`

- HTTP endpoints (sysadmin-only, served from the plugin's ServeHTTP):
  - `/admin/inventory` — sortable cross-channel inventory page with
    AM reachability + per-receiver loaded-in-AM badges, search,
    group-by-channel / group-by-team, CSV export
  - `/metrics` — Prometheus-format scrape endpoint, bearer-token
    auth (404 when token unset)

- Background reconciler that prunes receivers whose Mattermost
  webhook was deleted out-of-band. Uses `pluginapi/cluster.Schedule`
  for leader election across HA Mattermost pods — only one pod
  reconciles at a time. Mints + revokes a short-lived sysadmin PAT
  per cycle since plugin RPC doesn't expose webhook CRUD.

- Channel-suffix receiver naming (`<slug>--<channel>`) so the same
  runbook can fan out to multiple channels without collisions.

- Multi-cluster support via per-receiver `WebhookHostOverride`
  (`/alertmanager add --webhook-host=<url>`) — one Mattermost
  serving multiple Alertmanagers reachable via different network
  paths.

- Self-signed Alertmanager certificate support via
  `AlertManagerCABundle` System Console setting.

- Auto-delete janitor for DM'd YAML attachments — `AssembledYAMLTTLHours`
  setting controls retention (0 = disabled).

- Embedded runbooks rendered to static HTML at bundle time
  (`build/render-docs`).

- `samples/prometheus-rules.yaml` — alert rules covering all 20
  runbooks; emits the `runbook: <slug>` label that the plugin's
  routes block matches on.

- Sysadmin and channel-team-admin permission tiers (no
  custom-role machinery).

- Audit logging for privileged operations (add, remove, rotate,
  validate).

### Security

- Webhook URLs and basic-auth credentials never echoed in chat
  output. The detail-card view shows username but masks password.
- Metrics endpoint disabled by default; enabling requires setting
  a token.
- Channel-scoping enforced across all slash commands — a user in
  `#web-alerts` cannot enumerate or retrieve receiver YAML for
  `#db-alerts` via slash command.

[Unreleased]: https://github.com/mattermost/mattermost-plugin-alertmanager/compare/v1.0.3...HEAD
[1.0.3]: https://github.com/mattermost/mattermost-plugin-alertmanager/compare/v1.0.2...v1.0.3
[1.0.2]: https://github.com/mattermost/mattermost-plugin-alertmanager/compare/v1.0.1...v1.0.2
[1.0.1]: https://github.com/mattermost/mattermost-plugin-alertmanager/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/mattermost/mattermost-plugin-alertmanager/releases/tag/v1.0.0
