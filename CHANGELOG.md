# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.4](https://github.com/mattermost-community/mattermost-plugin-alertmanager/compare/v1.5.0...v1.0.4) (2026-11-17)


### ⚠ BREAKING CHANGES

* the receiver list moved from the plugin config map (`alertconfigsjson`) to the plugin KV store (`alertconfigs:v1`) with no automatic migration. After upgrading, receivers must be re-created with `/alertmanager add`; the old config value is ignored and its webhooks are orphaned.

### Features

* 30-runbook catalog, in-plugin sample rules, WebhookHost UX + cross-team receiver-name fix ([b45c29b](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/b45c29b87ce60f214c8def9d04685ad07ce300f2))
* add Mattermost Operator alerts to the mattermost target, fix dead metric names ([c225db3](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/c225db3bf6b5474dcd06bdd2cf4ab0ec03c4277e))
* **add:** --private to create the destination channel as private ([64aa9b3](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/64aa9b317c64fff54551a6589cc4a4954bca494a))
* **add:** say when posts will show as the caller instead of [@alertmanagerbot](https://github.com/alertmanagerbot) ([48bdd23](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/48bdd23921e44da82c57b424c62f63770bbac861))
* admin route-tester — severity only for end-to-end + by-team scope ([0d205ad](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/0d205ad072fd9077e688be9f0e1869d67d099c06))
* AlertmanagerConfig (v1alpha1) CRD renderer ([cccd405](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/cccd405f0885525689bb9c5059c1ca8884a75a3f))
* **autocomplete:** add Prometheus Operator Alertmanager URL suggestion ([6b39aec](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/6b39aec9553175f0674fff770eab099f8371e2fb))
* channel-scoped diff with Prometheus Operator CRD auto-detect ([9ef9879](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/9ef9879c44f1c5891461733abd950ee50dd6526d))
* **commands:** add-custom for generic (non-runbook) receivers ([3d14717](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/3d147177c432662ce7abcac8ce604fe8a57b88ef))
* complete sample rules to 31 rules (all 30 runbooks) + CI validation ([93fd044](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/93fd0449432eee523c8e23293183babe99e3f85a))
* expand alert catalog to 30 runbooks with a security category ([56e6633](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/56e6633fcd27a48f0de45363cbeb9364405893fa))
* format command group, plain-word syntax, single-DM exports, channel-scoped silence expire ([bfec97f](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/bfec97fc0862929695a5d11e3486988ab1c95509))
* format command group, plain-word syntax, single-DM exports, scoped silence expire ([9c9f6f5](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/9c9f6f5ca467104b03850910f0730c4e74049ea2))
* generic (non-runbook) receivers via /alertmanager add-custom ([30f8feb](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/30f8febd2c2229b05bbde475f0bb5cb6abba0f76))
* **ha:** propagate receiver-list writes across cluster nodes ([8d3ddce](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/8d3ddce1d60875eb5cf0029fe7585611e30a58b3))
* **inventory:** recognize CRD-managed receivers as OK ([959202a](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/959202a59dcd456090052044d7d82b6b5a271578))
* mattermost target with operator alerts, per-group format rules, autocomplete fixes (includes [#70](https://github.com/mattermost-community/mattermost-plugin-alertmanager/issues/70)) ([746ab61](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/746ab61f6bc73322deafc5c948301130a8db4d96))
* **mattermost:** add WebSocketDrop to the operator pack, drop the rules it replaces ([9fde179](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/9fde1795453f0228bd7154cc325c3d8c81d74032))
* **mattermost:** mm-db-replicas and mm-postgres-pod for the Mattermost pack ([0802325](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/08023255d49ce047757e100edd514681755a7b67))
* **metrics:** add metrics-token generate/reveal slash command ([f9f445c](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/f9f445cdc0462c0ab8432887d81f1ba1c4904ba1))
* offline CRD schema validation + simpler fallback receiver ([feec9de](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/feec9de85f20b64a35bad58a21e55e7fce90b982))
* Prometheus Operator (CRD) deployment support ([c8ea602](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/c8ea6024f6d7eb5385a5c499ab5b7ee035d4d6c1))
* **rules:** format rules takes any group or runbook, with one-at-a-time hover ([480ffe6](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/480ffe676cbf88e892c8e80391f9cef7f6fced3b))
* **rules:** match Mattermost operator alerts on namespace and installation, drop mm-service ([ea1ad3d](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/ea1ad3d3917c23c5b5622813b3e4e2a4e6f8422d))
* **security:** central command registry, receiver kinds, channel-scoped silences ([787dfbe](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/787dfbe772d9cc0be19612f8f4b4157cf4a3ce85))
* **settings:** only turn on the server-wide webhook override settings when an admin opts in ([97e7235](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/97e7235fb1021414d58bf71425cf53976a16ae13))
* ship sample Prometheus rules in-plugin + trim System Console settings ([ea2f522](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/ea2f522df4b2252ffca140e3cb25e8445c666938))
* split am-url hint into three hover-able suggestions ([2c6edc0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/2c6edc0dcdb4de8adae4e6b9fd06ce32d2bcb84b))
* WebhookHost preset dropdown + richer am-url autocomplete hint ([72f7131](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/72f7131557ed3b32ad91da0f55979e28e8ac838e))
* **webhook:** name Mattermost webhooks to mirror the receiver format ([b577fc7](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/b577fc74e98bc725aed6f41b617d423eb5fcfe6d))
* wire --format=standard|crd into add and export ([53537f7](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/53537f7df3f4086a9779eaa27258a30e804a8c2d))


### Bug Fixes

* **access:** format rules needs a team admin or sysadmin ([cae0ccb](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/cae0ccb45bde8826a495fbb3b9cf6ea46ad66b05))
* add releases the config lock before the DM; DM fallback and export say what is missing ([73575d8](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/73575d89e653170a39238f9f9f69a510911624f0))
* **add-custom:** address Copilot review (auth, runbook rendering, DM, CRD) ([3f54633](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/3f546335b840114fcd3ec28ef963ffdb5be70d20))
* **add-custom:** address Copilot suppressed-review findings (C8-C12) ([71af32e](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/71af32e5f815bf3b2aa0f9648f88681e0ea850b2))
* **add-custom:** custom-only CRD group must not emit ^()$ parent matcher ([84713df](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/84713dfa5b9d717d4e63fc35daa72c06ded65a39))
* **add-custom:** valid empty routes array for custom-only CRD; harden /add auth ([1afa3a0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/1afa3a096484721e7b92289adea4ba19db4323d2))
* **add:** name which override setting is off in the config add hint ([e7eefdc](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/e7eefdcb607ad3038b042794931917290e28b9a4))
* **alertmanager:** never re-send a silence POST, stop retrying hopeless requests, keep addresses out of replies ([227d7cb](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/227d7cb4886191bc87b39f840f7cb8b916435865))
* **alertmanager:** one caller-owned deadline per call, a circuit breaker, and polling for lost silences ([eba47a8](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/eba47a8381e70b7e3b07d6301f9d9da33a1f9080))
* **autocomplete:** load channel list for add-custom, not just add ([ddb67c3](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/ddb67c3a0d0023ace06827ac9886db45ba897e36))
* **autocomplete:** one option at a time after config add, and help typeahead ([44e0f5f](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/44e0f5ffbbd56ea98944f9219a9cd05e707d0dac))
* **build:** cache manifest vars (:=) so clean can't empty the release bundle ([5f2bc6a](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/5f2bc6a2819cf383e2a9bb2f84321b2204fe0523))
* **build:** cache manifest vars once (:=) so clean can't break the release ([b64be68](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/b64be68d487affbddc8eed3ad25324605f9f5016))
* **ci:** make Grype SARIF ingestible by Code Scanning ([48369ef](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/48369ef52c21903fd964d1ae8d3a752331bb8cc9))
* **ci:** make Grype SARIF ingestible by Code Scanning ([d1c511d](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/d1c511d61917551d256653bafae93f6765eb263e))
* **config:** address review nits on RMW lock ([75f34d0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/75f34d095a0c08e38f7b1021358519627620557d))
* **config:** close the config lost-update race (atomic read-modify-write) ([f83ffa9](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/f83ffa9060b3e9f0918987b3d0ba4d3cf8f8dd28))
* **config:** keep SSRF guard detail out of slash-command validation errors ([c8c8e82](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/c8c8e827abb2effa2b9a37ef8d90b0955097c700))
* **config:** make config read-modify-write atomic to close lost-update race ([ecdb128](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/ecdb1285fd7ef659b6a3525e17c82b0df8371585))
* **deps:** build with Go 1.26.11 for the net/http, crypto/tls and crypto/x509 fixes ([87198c5](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/87198c5fb8defd39ce25d43c4c30041ba7249b46))
* **deps:** bump golang.org/x/crypto to v0.56.0 ([3e93b1b](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/3e93b1b892db15b125d949b947d825f5ea3c2f96))
* **deps:** bump golang.org/x/mod to v0.40.0 (clear HIGH CVEs blocking the v1.4.17 release) ([15a8fa3](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/15a8fa3fd74f64b394e4655671f135d1f428199e))
* **deps:** bump golang.org/x/mod to v0.40.0 to clear HIGH CVEs ([3be8759](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/3be8759aab23d34929a53f638bc5f9cf8db2026c))
* **deps:** bump grpc + x/text to clear HIGH CVEs (combines dependabot [#42](https://github.com/mattermost-community/mattermost-plugin-alertmanager/issues/42)-45) ([79ed360](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/79ed360ab258277f1d3e067a856205fdd70dbc9e))
* **deps:** bump grpc and x/text to clear HIGH CVEs; fold in dependabot updates ([4359876](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/43598764b5bfbcd1c4bcebb36cba3d5e0d8c4e51))
* **deps:** bump indirect x/net, x/crypto, klauspost/compress past grype advisories ([8b87dcc](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/8b87dccd95fe82f304acd1d399f174eeccf8c80c))
* **dm:** only ask for the routes file when it is attached, and never print a broken reload command ([8f6aa65](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/8f6aa65846b6f3f42bf122768f5ad01c16756c65))
* **errors:** sanitize every Alertmanager error shown in chat and log the raw text ([7a3b8a8](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/7a3b8a8394f81ae58f73da9fa11f9b38a9e6428f))
* **ha:** serialize peer config reload with local writes ([adba264](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/adba26467d2174123b05979b61a9615993d35f41))
* **inventory:** parse operator column-0 receiver dashes in AM config ([c1554a5](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/c1554a56e399c5cef2a0fbd9ed1f605d762a796f))
* **inventory:** send X-CSRF-Token on route-tester POST (not just XHR header) ([648e1f5](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/648e1f5eb0f09e0eae8efd593f358c4b0bb52d07))
* keep look-alike team and channel names in separate rules objects ([6388094](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/6388094d7f087eca7bcd3a3c580a7662d3b0dee2))
* **lint:** use strings.Cut in currentTargetSection (modernize) ([9eb65d5](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/9eb65d530ff95c20a74bdebde4359e07e924e279))
* make channel-scoped diff matching and SSRF hints precise ([5664b33](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/5664b3368b0b002574ad450e6d26d0ddf2f6c2ac))
* make the Alertmanager HTTP client swap race-free + close bodies on retry (4th-pass review) ([e14be77](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/e14be7782685e3a4ccd58be082c44859987837dd))
* Mattermost alert rules use the exported metric names; rotate reports a live old webhook and a failed DM ([1313f11](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/1313f113cdbbbc10c5b38213be9cc3fadd20562f))
* migrate to prometheus/alertmanager v0.33 API surface ([64dce85](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/64dce850168a54acba76512c9b52ac12e17272d8))
* PersistentVolumeFull and WebSocketDrop sample rules ([5d66c4c](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/5d66c4c89e094fb36ffbb8d1231553fde0e46980))
* **reconciler:** admin repair reports unchecked webhooks; banner dates the prune count ([c90ee8a](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/c90ee8af228144ed0bb4fbd81e696c24ab81dd39))
* **reconciler:** resume from the first check the deadline cut off ([073ef66](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/073ef669e055b1415d524b4b49435895530b1847))
* **reconciler:** resume the orphan check where the last one ran out of budget ([6691b84](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/6691b844ec23efa2932c9ea8c99fb3bde14b61c6))
* **repair:** admin repair and admin repair prune keep separate places ([39aa54e](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/39aa54eef3b13e1d6c5bfab78daa0e01d27194c5))
* **repair:** admin repair resumes where the last manual run stopped ([b442a68](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/b442a68e25f08d29d878f815f5d4cff6a552bce5))
* **rotate:** dedup group-webhook double-rotation in rotate --overdue (pre-existing, review) ([4fbc9f2](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/4fbc9f2ab6ff1bf0bc85db080147f77b161c970d))
* **rotate:** delete the old webhook only after the CAS write commits (CL-24 review) ([1ab6ff9](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/1ab6ff9a48c52685b58e8764feaaa00c4b2a3101))
* **rotate:** report all overdue group members when a group rotation fails (2nd-pass review) ([1a66f44](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/1a66f44d1a0014259668e1e4e35ea3d2c1d20ed9))
* **rules:** keep each comment with the alert it describes when filtering ([1a7addc](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/1a7addcfda258bdc19bdb218b6e282fef66b6273))
* **rules:** PersistentVolumeFull join and Bitnami registry allowlist ([7e05aa0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/7e05aa00644e4222c33c6eb9fc4bd0faf1dd104a))
* **rules:** remove MattermostLicenseExpiringSoon and its runbook ([6cbcdf8](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/6cbcdf8ba92ec2cf3ba515b37bb1eb30c73ddaa8))
* **security:** address Copilot review findings on [#57](https://github.com/mattermost-community/mattermost-plugin-alertmanager/issues/57) ([a5c1b4f](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/a5c1b4faade7c816b1e9f8b160cb65f2479fa7f8))
* **security:** fifth review pass (F1-3) — cold-start deny-all allowlist, flow-style diff redaction, repair applies sanitized list to memory ([492da4d](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/492da4dfdb73ccf83f481407ec54b22ff73c2931))
* **security:** fourth review pass (F1-4) — proxy SSRF, repair sanitize, block-scalar/proxy_url redaction ([0a9e372](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/0a9e3728d723aba51e15c0f3dcf2d49f791784a3))
* **security:** reject trailing ?/# in AM URL; sanitize on load (F-002) ([838a935](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/838a9359b001f21da88276581e8e041ceaaa83bf))
* **security:** resolve independent security review (F-001..F-007) ([615abc8](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/615abc875caa6ff1f25d3e3cf3755a52a4efcc7d))
* **security:** scrub hook ID from Client4 error text; fail-closed rollback ([2b9471c](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/2b9471c166e75907e4a0be32547cd2c66685e42b))
* **security:** second review pass — block private by default + leak/HA fixes ([9995d02](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/9995d029e3d7b8b3ca8b41bd769b8baf8eb2c7eb))
* **security:** sixth review pass (F1-5) — webhook-host SSRF/injection parity + remove-identity + info-disclosure ([fb1e17a](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/fb1e17a64f92854e96e21e4aebc3ee5af519d137))
* **security:** third review pass (F1-4) — allowlist guardrails + safe repair + diff redaction ([e867773](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/e867773235078e9ec174de8a7ce45bb028734d55))
* **settings:** apply the override setting when it is saved, not only at activation ([0d970c6](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/0d970c61b01def4db57a150f128d0242f597b1cc))
* **silence:** let pack channels silence and list the Postgres pod alerts ([3a880bf](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/3a880bf357447f425c7ae58bd616a05549e51f6c))
* **silence:** only system admins can create or expire silences ([e62e474](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/e62e474321bf6db7788694884c0d39aadcf1e784))
* team-qualify receiver names to prevent cross-team channel collisions ([ccacf30](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/ccacf303246443ef7aa923dd48702e2516a21475))
* **validate:** keep the webhook host out of webhook-test errors for team admins ([75c56a4](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/75c56a4e3d237e5d67a8ec7071ae292b98da4192))
* **validate:** pad synthetic firing alert TTL past group_wait ([3f46770](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/3f4677093948a1692b128f337d4e8456c67086cb))
* **webhook:** guard webhookDeleteError against a nil cause ([8c92d43](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/8c92d434314d3a4ddcd4c45692feff917dd843d7))


### Performance

* **ha:** broadcast peer reload off the write lock ([ae6dfe1](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/ae6dfe1eecf1c2a8ef6f91c79f9f718025e940a7))
* **inventory:** probe the page's Alertmanagers concurrently ([a8b1737](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/a8b17374b4c2a9011b03ccf0f3cc5c111c0e88fa))
* **query:** call a channel's Alertmanagers concurrently ([2f89ef0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/2f89ef02988699b4210187459dd27e47975932d5))
* **reconciler:** throttle orphan probes and YAML sweeps within a tick ([193de10](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/193de10748debdaf141a73dcdfdfb32b846de1b8))
* **silence:** create a silence on every Alertmanager concurrently ([3a24d8c](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/3a24d8cae5db9d1dcc0fbac412104b741151b016))
* **silence:** read each silence's record once when listing ([44bb6ec](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/44bb6ec7b989dd237d1fd101b409f00b0032a377))


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
* **build/render-docs:** bump goldmark to 1.8.7 ([6a61078](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/6a610782e31ee9eeb13a9d54abe730a24d62ae48))


### Documentation

* document the intentional config→KV clean break + fix stale comments (CL-19 review) ([bc66078](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/bc6607878028c90c1f276e44a0ac21b56c9d6b28))


### Chores

* release 1.0.4 ([dca70a8](https://github.com/mattermost-community/mattermost-plugin-alertmanager/commit/dca70a8b00284f503d9a65387e9953e8619610bf))

## [1.5.0](https://github.com/mattermost-community/mattermost-plugin-alertmanager/compare/v1.4.17...v1.5.0) (2026-11-03)

> **⚠️ BREAKING — command syntax.** Every `--flag` is gone. Switches are now bare words
> (`force`, `prune`, `overdue`, `end-to-end`, `simulate`, `webhook-test`) and values are
> `key=value` (`namespace=`, `webhook-host=`, `severity=`). The old `--` spellings are
> rejected with a message showing the new form. `config export`, `config diff` and `rules`
> now reply with a pointer to the `format` group. Update any saved runbooks or scripts.

> **⚠️ BREAKING — silences are system admin only.** An Alertmanager silence applies to
> every channel on that Alertmanager, so `silence create` and `silence expire` now need
> a system admin. Team admins who silenced alerts from their own channel get a denial.
> `silence list` is unchanged.

> **⚠️ BREAKING — no basic auth to Alertmanager.** The per-receiver `user`/`password`
> fields are removed. The plugin expects an Alertmanager API it can reach without
> credentials, such as an in-cluster Service URL. Entries that still carry the old keys
> load normally, and the credentials are dropped on the next write.

> **⚠️ Behavior change — webhook override settings are opt-in.** The plugin no longer
> turns on Mattermost's server-wide `EnablePostUsernameOverride` and
> `EnablePostIconOverride` at activation. Turn them on in System Console → Integrations
> → Integration Management, or enable the new `EnableIntegrationOverrides` plugin
> setting. Until then, alert posts show the name or picture of the sysadmin who ran
> `config add`, and `config add` says which. The setting takes effect when saved and
> keeps both toggles on while it is on. Installs where the plugin already turned
> them on are unaffected.

### Features

* `/alertmanager format` command group: `format export [standard|crd]` (this channel's config and rules), `format diff [standard|crd]` (Alertmanager config only, sysadmin) and `format rules`.
* `format rules <group|runbook|all> [standard|crd]` sends only one group's rules (or one runbook's) as a PrometheusRule named `mattermost-alertmanager-rules-<group>`, the same object `config add` sends. Without a group you still get all 36.
* `mattermost` target: `config add <am-url> mattermost [crd] [mm-namespace=<ns>] [mm-name=<cr>]` creates the five Mattermost runbook receivers. With `mm-namespace` it also creates one `mattermost-server` receiver for the 13 alerts the Mattermost Operator generates, with one merged PrometheusRule (16 alerts) for that install. `config remove mattermost force` removes both. The pack also includes `MattermostWebSocketDrop`, scoped to the install. `mm-db-replicas=true` adds `MattermostDBReplicaLag`, which stays off by default because Mattermost only reports replica lag once `SqlSettings.ReplicaLagSettings` holds a lag query per replica. `mm-postgres-pod=true` adds `MattermostPostgresPodNotReady` and `MattermostPostgresPodCrashLooping` for a Postgres that runs as pods in the Mattermost namespace (plugin-only, no operator counterpart). With `mm-namespace` the group's own WebSocket drop and login failure rules are left out so each signal alerts once. The pack mirrors the Mattermost Operator's draft monitoring PR; drop it if the operator ships these rules. The operator rules match `namespace` and the `installation.mattermost.com/installation` pod label instead of the scraped Service name, so they work whichever Service Prometheus scrapes. The ServiceMonitor must copy that label with `podTargetLabels`.
* Receivers carry an explicit kind (`runbook`, `custom` or `pack`). Entries saved by earlier versions keep working with no migration.
* Config and Prometheus rules arrive in one DM for `config add` and `format export`, in both standard and CRD formats.
* Channel-scoped `format diff` with Prometheus Operator CRD auto-detect.
* Autocomplete shows one option at a time after `config add` and `format rules`, `config validate` lists only the groups present in the channel, and every command accepts a trailing `help` with typeahead.
* Docs: kube-prometheus-stack control-plane alerts and the `release` label selector gotcha.

### Security

* `silence expire` only ends silences created from the calling channel with `silence create`. A team admin can no longer end another team's silence on a shared Alertmanager. Silences created before this version have no record and must be ended in Alertmanager.
* The Alertmanager runbook link in chat messages only follows `http(s)://` URLs. Any other `runbook_url` falls back to the plugin runbook, or to no link for a custom receiver.
* `silence list` shows only the silences that apply to the channel. `silence create <alert>` is limited to alerts the channel's receivers carry, and `all` and the `mattermost` group also cover the operator alerts.
* Every command is authorized in one place, before it runs. `format diff`, `silence create`, `silence expire` and the `admin` commands are system admin only, and `config`, `format export` and `format rules` verbs need a team admin.
* The image-registry allowlist entries go into generated rules only for team admins, and the blocked-address and suggested-CIDR detail of an Alertmanager connection error is shown only to system admins.
* Autocomplete requests are capped in size, and the runbook list is read once.
* Alertmanager errors in chat no longer name the resolved IP and port for anyone but system admins, and `config validate webhook-test` no longer names the webhook host for team admins. The full text goes to the plugin log.
* Dependency updates (x/net 0.61, x/crypto 0.58, otel, grpc 1.85, alertmanager 0.34.1, goldmark 1.8.7) and pinned GitHub Actions bumps. The standing `GO-2026-5932` finding stays open on purpose.

### Performance

* A channel's Alertmanagers are called concurrently, up to 8 at once, for `alerts`, `silence list`, `silence create` and the inventory page. Each call shares one 15-second budget across its retries, and an Alertmanager that fails 3 times in a row is skipped for 30 seconds.

### Bug Fixes

* The background webhook check runs every 30 minutes instead of every 5, with up to 4 checks at once and a 2-minute budget. A check that runs out of budget is picked up where it stopped, so every webhook is still checked. The expired-YAML sweep runs every 15 minutes. `admin repair` uses the same budget with its own place, so a rerun continues where the last one stopped. It says how many webhooks it did not reach in time and how many returned an error, instead of reporting none orphaned. `/alertmanager about` and the inventory page show when the last orphan check ran next to its pruned count.

* Removed the license-expiry alert and its runbook. Mattermost has no license-expiry metric, so the rule never fired.
* The Mattermost API latency, DB latency, error-rate and login-failure rules and runbooks now use the metrics Mattermost exports (`mattermost_api_time_*`, `mattermost_db_store_time_*`, `mattermost_login_logins_fail_total`). The old names matched nothing, so those alerts never fired. The docs name `MetricsSettings.Enable`, and no longer tell you to add hosts to `AllowedUntrustedInternalConnections`, which the plugin does not need.
* `config rotate all overdue` lists each receiver whose old webhook could not be deleted, instead of saying the old URLs are dead. Group and bulk rotation say when the YAML DM failed and point to `format export`.
* When the `config add` DM fails, the inline copy includes the routes, and the config lock is no longer held while the DM is sent.
* `format export` for a channel with both runbook and Mattermost operator receivers says the operator rules are left out and how to get them.
* `MattermostPostgresPodNotReady` no longer fires for a finished backup Job pod.
* `PersistentVolumeFull` join and the Bitnami registry allowlist for `UnexpectedContainerImage`, with promtool unit tests. The join also works when two kubelets report the same claim.
* The sample `MattermostWebSocketDrop` compares connection totals per namespace and job, so it fires when a replaced pod comes back with fewer connections.
* `format export crd` names each channel's PrometheusRule `mattermost-alertmanager-rules-<team>-<channel>-<hash>`. Before, look-alike names such as `ops-a`/`b` and `ops`/`a-b` produced the same object and one channel's export overwrote the other's. After you apply the new export, delete the old `mattermost-alertmanager-rules-<team>-<channel>` object, or Prometheus evaluates both and the alerts fire twice.
* SSRF hints and channel-scoped diff matching are more precise.

### Internal

* Commands run through one registry that parses, authorizes and runs each verb.
* `cmd_scaffold.go`, `cmd_lifecycle.go` and `admin_inventory.go` are split into files of a few hundred lines. `handleAdd` and the inventory page are built from small stage functions, and the inventory page markup lives in `inventory.html`.

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
