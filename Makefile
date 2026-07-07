GO ?= $(shell command -v go 2> /dev/null)
NPM ?= $(shell command -v npm 2> /dev/null)
CURL ?= $(shell command -v curl 2> /dev/null)
MM_DEBUG ?=
MANIFEST_FILE ?= plugin.json
GOPATH ?= $(shell go env GOPATH)
GO_TEST_FLAGS ?= -race
GO_BUILD_FLAGS ?=
MM_UTILITIES_DIR ?= ../mattermost-utilities
DLV_DEBUG_PORT := 2346

export GO111MODULE=on

# You can include assets this directory into the bundle. This can be e.g. used to include profile pictures.
ASSETS_DIR ?= assets

## Define the default target (make all)
.PHONY: default
default: all

# Verify environment, and define PLUGIN_ID, PLUGIN_VERSION, HAS_SERVER and HAS_WEBAPP as needed.
include build/setup.mk
include build/legacy.mk

BUNDLE_NAME ?= $(PLUGIN_ID)-$(PLUGIN_VERSION).tar.gz

# ====================================================================================
# Build-time URL discovery
# ====================================================================================
# PLUGIN_REPO_URL resolves to "where this code lives in GitHub right now".
# Used to:
#   * Substitute __PLUGIN_REPO_URL__ in plugin.json's homepage_url,
#     support_url, and settings_schema.header (bundle time)
#   * Inject root.RepoURL via -ldflags so Go code references the same
#     resolved URL (compile time)
#   * Pass to build/render-docs so generated HTML uses the correct link
#
# Cascade (most authoritative first):
#   1. CI: $(GITHUB_SERVER_URL)/$(GITHUB_REPOSITORY) — set by GitHub Actions.
#   2. Local: parse `git remote get-url origin`, normalize SSH→HTTPS,
#      strip trailing `.git`. Correct for normal dev clones AND forks
#      (a fork's origin points at the fork, so the URL tracks).
#   3. Hardcoded fallback PLUGIN_REPO_URL_DEFAULT — catches the
#      "source tarball, no git, no CI" build case. Update this string
#      if the upstream repo is permanently moved.
#
# We DON'T fall back to plugin.json's homepage_url because that field
# now carries a __PLUGIN_REPO_URL__ placeholder itself — circular dep.
#
# PLUGIN_RELEASE_URL appends /releases/tag/v$(PLUGIN_VERSION) so a single
# placeholder substitution gives a versioned, location-correct link.
# ====================================================================================

PLUGIN_REPO_URL_DEFAULT := https://github.com/mattermost/mattermost-plugin-alertmanager

ifneq ($(GITHUB_REPOSITORY),)
PLUGIN_REPO_URL := $(GITHUB_SERVER_URL)/$(GITHUB_REPOSITORY)
else
PLUGIN_REPO_URL := $(shell git remote get-url origin 2>/dev/null | sed -e 's|^git@github.com:|https://github.com/|' -e 's|\.git$$||')
endif

ifeq ($(strip $(PLUGIN_REPO_URL)),)
PLUGIN_REPO_URL := $(PLUGIN_REPO_URL_DEFAULT)
endif

PLUGIN_RELEASE_URL := $(PLUGIN_REPO_URL)/releases/tag/v$(PLUGIN_VERSION)

# Include custom makefile, if present
ifneq ($(wildcard build/custom.mk),)
	include build/custom.mk
endif

## Checks the code style, tests, builds and bundles the plugin.
.PHONY: all
all: check-style test dist

# golangci-lint pinned to the version CI runs (.github/workflows/lint.yml).
# v1.62.0 is the last v1 release and refuses to lint projects targeting
# Go > 1.23 — go.mod says 1.26.3, so v2 is mandatory. The locally-installed
# binary always wins over a system one to avoid the "your golangci-lint was
# built with Go 1.25 < project 1.26" version-skew error.
GOLANGCI_LINT_VERSION ?= v2.12.2

## Install Go-based dev tools (golangci-lint) into build/bin.
.PHONY: install-go-tools
install-go-tools:
	@if [ ! -x "$(GOBIN)/golangci-lint" ] || ! $(GOBIN)/golangci-lint --version 2>/dev/null | grep -q "$(GOLANGCI_LINT_VERSION:v%=%)"; then \
		echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."; \
		mkdir -p $(GOBIN); \
		GOBIN=$(GOBIN) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	else \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) already installed"; \
	fi

## Runs eslint and golangci-lint
.PHONY: check-style
check-style: webapp/node_modules install-go-tools
	@echo Checking for style guide compliance

ifneq ($(HAS_WEBAPP),)
	cd webapp && npm run lint
	cd webapp && npm run check-types
endif

ifneq ($(HAS_SERVER),)
	@echo Running golangci-lint
	$(GOBIN)/golangci-lint run ./...
endif

# ldflags inject the resolved repo URL into the binary at compile time.
# Read at runtime as root.RepoURL — see main.go for the var definition
# and rationale. Lets Go code render correct GitHub links without
# re-deriving the URL on every reference.
GO_LDFLAGS := -X 'github.com/mattermost/mattermost-plugin-alertmanager.RepoURL=$(PLUGIN_REPO_URL)'

## Builds the server for Linux (amd64 + arm64). Mattermost servers run on
## Linux — including every docker dev environment — so the darwin/windows
## plugin binaries were never actually loaded and only tripled the build
## time (5 cross-compiles -> 2). If you must run the MM *server* natively
## on macOS/Windows, add those GOOS/GOARCH lines back here and to
## plugin.json's executables.
.PHONY: server
server:
ifneq ($(HAS_SERVER),)
	mkdir -p server/dist;
ifeq ($(MM_DEBUG),)
	cd server && env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -trimpath -o dist/plugin-linux-amd64;
	cd server && env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -trimpath -o dist/plugin-linux-arm64;
else
	$(info DEBUG mode is on; to disable, unset MM_DEBUG)

	cd server && env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -trimpath -gcflags "all=-N -l" -o dist/plugin-linux-amd64;
	cd server && env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -trimpath -gcflags "all=-N -l" -o dist/plugin-linux-arm64;
endif
endif

## Ensures NPM dependencies are installed without having to run this all the time.
webapp/node_modules: $(wildcard webapp/package.json)
ifneq ($(HAS_WEBAPP),)
	cd webapp && $(NPM) install
	touch $@
endif

## Builds the webapp, if it exists.
.PHONY: webapp
webapp: webapp/node_modules
ifneq ($(HAS_WEBAPP),)
ifeq ($(MM_DEBUG),)
	cd webapp && $(NPM) run build;
else
	cd webapp && $(NPM) run debug;
endif
endif

## Renders docs/*.md → public/help/*.html for the in-System-Console docs link.
## Re-runs every bundle so HTML stays in sync with the source markdown.
## Passes PLUGIN_REPO_URL so the rendered "View on GitHub" link tracks
## the build-time repo location (CI env / git remote / fallback default).
.PHONY: render-docs
render-docs:
	cd build/render-docs && PLUGIN_REPO_URL=$(PLUGIN_REPO_URL) $(GO) run main.go

## Generates a tar bundle of the plugin for install.
##
## The plugin.json copy step substitutes three placeholders at bundle time:
##   __PLUGIN_VERSION__     → $(PLUGIN_VERSION) (from build/setup.mk parse)
##   __PLUGIN_REPO_URL__    → $(PLUGIN_REPO_URL) (CI env → git remote → default)
##   __PLUGIN_RELEASE_URL__ → $(PLUGIN_RELEASE_URL) (REPO_URL + /releases/tag/vN)
## Used in homepage_url, support_url, and settings_schema.header so MM's
## System Console renders the correct location regardless of repo moves.
## sed delimiter is `|` instead of `/` because URLs contain slashes.
## Placeholder strings are all distinct (no substring overlap), so the
## sed order doesn't matter.
.PHONY: bundle
bundle: render-docs
	rm -rf dist/
	mkdir -p dist/$(PLUGIN_ID)
	sed -e 's|__PLUGIN_RELEASE_URL__|$(PLUGIN_RELEASE_URL)|g' \
	    -e 's|__PLUGIN_REPO_URL__|$(PLUGIN_REPO_URL)|g' \
	    -e 's|__PLUGIN_VERSION__|$(PLUGIN_VERSION)|g' \
	    $(MANIFEST_FILE) > dist/$(PLUGIN_ID)/$(MANIFEST_FILE)
	@# Fail the build if any placeholder survived substitution. A bundle
	@# with raw __PLUGIN_*__ tokens ships broken URLs to System Console
	@# (renders literally, 404s). This turns that silent, user-facing
	@# failure into a hard build error. Also catches a NEW placeholder
	@# added to plugin.json without a matching sed rule above.
	@if grep -qE '__PLUGIN_[A-Z_]+__' dist/$(PLUGIN_ID)/$(MANIFEST_FILE); then \
		echo "ERROR: unsubstituted placeholder(s) in bundled plugin.json:"; \
		grep -oE '__PLUGIN_[A-Z_]+__' dist/$(PLUGIN_ID)/$(MANIFEST_FILE) | sort -u | sed 's/^/  /'; \
		echo "Add a matching sed rule to the 'bundle' target in the Makefile."; \
		exit 1; \
	fi
	cp README.md dist/$(PLUGIN_ID)/
	cp LICENSE dist/$(PLUGIN_ID)/
ifneq ($(wildcard $(ASSETS_DIR)/.),)
	cp -r $(ASSETS_DIR) dist/$(PLUGIN_ID)/
endif
ifneq ($(wildcard docs/.),)
	cp -r docs dist/$(PLUGIN_ID)/
endif
	@# Run-time check, not parse-time. render-docs creates public/help/
	@# on first build, so a parse-time wildcard would miss it on fresh
	@# clones. We always want to ship whatever public/ render-docs
	@# produced, regardless of whether HAS_PUBLIC was set at parse time.
	@if [ -d public ]; then cp -r public dist/$(PLUGIN_ID)/ ; fi
ifneq ($(HAS_SERVER),)
	mkdir -p dist/$(PLUGIN_ID)/server
	cp -r server/dist dist/$(PLUGIN_ID)/server/
endif
ifneq ($(HAS_WEBAPP),)
	mkdir -p dist/$(PLUGIN_ID)/webapp
	cp -r webapp/dist dist/$(PLUGIN_ID)/webapp/
endif
	# COPYFILE_DISABLE=1 prevents macOS BSD tar from emitting AppleDouble
	# (._*) metadata files for every entry. Those files end up as extra
	# top-level entries in the tarball, which breaks Mattermost's
	# "exactly one top-level directory → recurse" extraction heuristic and
	# produces a misleading "Unable to find manifest for extracted plugin"
	# upload error. --exclude is a belt-and-suspenders for tar versions that
	# don't honor COPYFILE_DISABLE.
	cd dist && COPYFILE_DISABLE=1 tar --exclude='._*' --exclude='.DS_Store' -cvzf $(BUNDLE_NAME) $(PLUGIN_ID)

	@echo plugin built at: dist/$(BUNDLE_NAME)

## Builds and bundles the plugin.
.PHONY: dist
dist:	server webapp bundle
	rm -rf dist/alertmanager

## Builds and installs the plugin to a server.
## Requires one of these connection modes (see docs/DEVELOPMENT.md for details):
##   - MM_LOCALSOCKETPATH set                     (unix socket, mattermost-server dev mode)
##   - MM_SERVICESETTINGS_SITEURL + MM_ADMIN_TOKEN (HTTPS, admin token)
##   - MM_SERVICESETTINGS_SITEURL + MM_ADMIN_USERNAME + MM_ADMIN_PASSWORD (HTTPS, password)
.PHONY: deploy
deploy: dist
	./build/bin/pluginctl deploy $(PLUGIN_ID) dist/$(BUNDLE_NAME)

## Convenience: deploy to a Mattermost instance at http://localhost:8065.
## Requires MM_ADMIN_TOKEN to be set in the calling environment.
.PHONY: deploy-local
deploy-local: dist
	MM_SERVICESETTINGS_SITEURL=http://localhost:8065 ./build/bin/pluginctl deploy $(PLUGIN_ID) dist/$(BUNDLE_NAME)

# ---------------------------------------------------------------------------
# Per-host (single-arch) targets — for fast dev iteration.
# Produces a ~17MB tarball instead of the 85MB all-archs bundle by
# building only the host's GOOS+GOARCH and stripping plugin.json's
# server.executables to match.
#
# Use the regular `make dist` for releases; use `make dist-host` while
# iterating against your own dev Mattermost.
# ---------------------------------------------------------------------------

HOST_GOOS := $(shell $(GO) env GOOS)
HOST_GOARCH := $(shell $(GO) env GOARCH)
HOST_ARCH_KEY := $(HOST_GOOS)-$(HOST_GOARCH)
HOST_BUNDLE_NAME := $(PLUGIN_ID)-$(PLUGIN_VERSION)-$(HOST_ARCH_KEY).tar.gz

## Builds the server binary for the host's OS/arch only.
.PHONY: server-host
server-host:
ifneq ($(HAS_SERVER),)
	mkdir -p server/dist
	cd server && env CGO_ENABLED=0 GOOS=$(HOST_GOOS) GOARCH=$(HOST_GOARCH) $(GO) build $(GO_BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -trimpath -o dist/plugin-$(HOST_ARCH_KEY)$(if $(filter windows,$(HOST_GOOS)),.exe,)
endif

## Bundles only the host arch's binary, with a plugin.json filtered to
## match. python3 is used to filter the JSON — present by default on
## macOS and on most Linux dev hosts.
.PHONY: bundle-host
bundle-host: render-docs
	rm -rf dist/
	mkdir -p dist/$(PLUGIN_ID)
	# Same three placeholder substitutions as `make bundle`, applied to
	# every string value in the manifest dict so homepage_url, support_url,
	# and the nested settings_schema.header all get rewritten.
	python3 -c "import json; \
m = json.load(open('plugin.json')); \
k = '$(HOST_ARCH_KEY)'; \
m['server']['executables'] = {k: m['server']['executables'][k]}; \
sub = lambda s: s.replace('__PLUGIN_VERSION__', m['version']).replace('__PLUGIN_REPO_URL__', '$(PLUGIN_REPO_URL)').replace('__PLUGIN_RELEASE_URL__', '$(PLUGIN_RELEASE_URL)'); \
m['homepage_url'] = sub(m['homepage_url']); \
m['support_url'] = sub(m['support_url']); \
m['settings_schema']['header'] = sub(m['settings_schema']['header']); \
json.dump(m, open('dist/$(PLUGIN_ID)/plugin.json', 'w'), indent=2)"
	cp README.md dist/$(PLUGIN_ID)/
	cp LICENSE dist/$(PLUGIN_ID)/
ifneq ($(wildcard $(ASSETS_DIR)/.),)
	cp -r $(ASSETS_DIR) dist/$(PLUGIN_ID)/
endif
ifneq ($(wildcard docs/.),)
	cp -r docs dist/$(PLUGIN_ID)/
endif
ifneq ($(wildcard public/.),)
	cp -r public dist/$(PLUGIN_ID)/
endif
	mkdir -p dist/$(PLUGIN_ID)/server/dist
	cp server/dist/plugin-$(HOST_ARCH_KEY)$(if $(filter windows,$(HOST_GOOS)),.exe,) dist/$(PLUGIN_ID)/server/dist/
	cd dist && COPYFILE_DISABLE=1 tar --exclude='._*' --exclude='.DS_Store' -czf $(HOST_BUNDLE_NAME) $(PLUGIN_ID)
	@echo plugin built at: dist/$(HOST_BUNDLE_NAME)

## Build + bundle for the host arch only.
.PHONY: dist-host
dist-host: server-host bundle-host
	rm -rf dist/$(PLUGIN_ID)

## Build, bundle, and deploy the host-arch-only tarball. Fast iteration loop.
.PHONY: deploy-host
deploy-host: dist-host
	./build/bin/pluginctl deploy $(PLUGIN_ID) dist/$(HOST_BUNDLE_NAME)

## Like deploy-host but defaults SITEURL to http://localhost:8065.
.PHONY: deploy-host-local
deploy-host-local: dist-host
	MM_SERVICESETTINGS_SITEURL=http://localhost:8065 ./build/bin/pluginctl deploy $(PLUGIN_ID) dist/$(HOST_BUNDLE_NAME)

## Builds and installs the plugin to a server, updating the webapp automatically when changed.
.PHONY: watch
watch: server bundle
ifeq ($(MM_DEBUG),)
	cd webapp && $(NPM) run build:watch
else
	cd webapp && $(NPM) run debug:watch
endif

## Installs a previous built plugin with updated webpack assets to a server.
.PHONY: deploy-from-watch
deploy-from-watch: bundle
	./build/bin/pluginctl deploy $(PLUGIN_ID) dist/$(BUNDLE_NAME)

## Setup dlv for attaching, identifying the plugin PID for other targets.
.PHONY: setup-attach
setup-attach:
	$(eval PLUGIN_PID := $(shell ps aux | grep "plugins/${PLUGIN_ID}" | grep -v "grep" | awk -F " " '{print $$2}'))
	$(eval NUM_PID := $(shell echo -n ${PLUGIN_PID} | wc -w))

	@if [ ${NUM_PID} -gt 2 ]; then \
		echo "** There is more than 1 plugin process running. Run 'make kill reset' to restart just one."; \
		exit 1; \
	fi

## Check if setup-attach succeeded.
.PHONY: check-attach
check-attach:
	@if [ -z ${PLUGIN_PID} ]; then \
		echo "Could not find plugin PID; the plugin is not running. Exiting."; \
		exit 1; \
	else \
		echo "Located Plugin running with PID: ${PLUGIN_PID}"; \
	fi

## Attach dlv to an existing plugin instance.
.PHONY: attach
attach: setup-attach check-attach
	dlv attach ${PLUGIN_PID}

## Attach dlv to an existing plugin instance, exposing a headless instance on $DLV_DEBUG_PORT.
.PHONY: attach-headless
attach-headless: setup-attach check-attach
	dlv attach ${PLUGIN_PID} --listen :$(DLV_DEBUG_PORT) --headless=true --api-version=2 --accept-multiclient

## Detach dlv from an existing plugin instance, if previously attached.
.PHONY: detach
detach: setup-attach
	@DELVE_PID=$(shell ps aux | grep "dlv attach ${PLUGIN_PID}" | grep -v "grep" | awk -F " " '{print $$2}') && \
	if [ "$$DELVE_PID" -gt 0 ] > /dev/null 2>&1 ; then \
		echo "Located existing delve process running with PID: $$DELVE_PID. Killing." ; \
		kill -9 $$DELVE_PID ; \
	fi

## Runs any lints and unit tests defined for the server and webapp, if they exist.
.PHONY: test
test: webapp/node_modules
ifneq ($(HAS_SERVER),)
	$(GO) test -v $(GO_TEST_FLAGS) ./server/...
endif
ifneq ($(HAS_WEBAPP),)
	cd webapp && $(NPM) run test;
endif
ifneq ($(wildcard ./build/sync/plan/.),)
	cd ./build/sync && $(GO) test -v $(GO_TEST_FLAGS) ./...
endif

## Creates a coverage report for the server code.
.PHONY: coverage
coverage: webapp/node_modules
ifneq ($(HAS_SERVER),)
	$(GO) test $(GO_TEST_FLAGS) -coverprofile=server/coverage.txt ./server/...
	$(GO) tool cover -html=server/coverage.txt
endif

## Extract strings for translation from the source code.
.PHONY: i18n-extract
i18n-extract:
ifneq ($(HAS_WEBAPP),)
ifeq ($(HAS_MM_UTILITIES),)
	@echo "You must clone github.com/mattermost/mattermost-utilities repo in .. to use this command"
else
	cd $(MM_UTILITIES_DIR) && npm install && npm run babel && node mmjstool/build/index.js i18n extract-webapp --webapp-dir $(PWD)/webapp
endif
endif

## Disable the plugin.
.PHONY: disable
disable: detach
	./build/bin/pluginctl disable $(PLUGIN_ID)

## Enable the plugin.
.PHONY: enable
enable:
	./build/bin/pluginctl enable $(PLUGIN_ID)

## Reset the plugin, effectively disabling and re-enabling it on the server.
.PHONY: reset
reset: detach
	./build/bin/pluginctl reset $(PLUGIN_ID)

## Kill all instances of the plugin, detaching any existing dlv instance.
.PHONY: kill
kill: detach
	$(eval PLUGIN_PID := $(shell ps aux | grep "plugins/${PLUGIN_ID}" | grep -v "grep" | awk -F " " '{print $$2}'))

	@for PID in ${PLUGIN_PID}; do \
		echo "Killing plugin pid $$PID"; \
		kill -9 $$PID; \
	done; \

## Clean removes all build artifacts.
.PHONY: clean
clean:
	rm -fr dist/
ifneq ($(HAS_SERVER),)
	rm -fr server/coverage.txt
	rm -fr server/dist
endif
ifneq ($(HAS_WEBAPP),)
	rm -fr webapp/junit.xml
	rm -fr webapp/dist
	rm -fr webapp/node_modules
endif
	rm -fr build/bin/

## Sync directory with a starter template
sync:
ifndef STARTERTEMPLATE_PATH
	@echo STARTERTEMPLATE_PATH is not set.
	@echo Set STARTERTEMPLATE_PATH to a local clone of https://github.com/mattermost/mattermost-plugin-starter-template and retry.
	@exit 1
endif
	cd ${STARTERTEMPLATE_PATH} && go run ./build/sync/main.go ./build/sync/plan.yml $(PWD)

# ====================================================================================
# Release pipeline (security-gated)
# ====================================================================================
# `make release` chains: clean -> all (style+test+dist) -> sbom-audit ->
# codeql-analyze -> security-gate -> release-bundle (re-pack with SBOMs +
# SARIF) -> virus-scan -> release-sign (optional) -> release-checksum.
# Operators tag a release with `make release-tag` once `make release`
# succeeds, then push the tag — release.yml workflow re-runs the same
# pipeline and publishes the GitHub Release.
# ====================================================================================

GOBIN ?= $(PWD)/build/bin

## Pre-release checks: git status and changelog validation.
.PHONY: release-check
release-check:
	@echo "Running pre-release checks..."
	@if [ -n "$$(git status --porcelain -- . ':!webapp/package-lock.json')" ]; then \
		echo "ERROR: Working directory has uncommitted changes."; \
		echo "Commit or stash before building a release."; \
		git status --short -- . ':!webapp/package-lock.json'; \
		exit 1; \
	fi
	@if [ ! -f CHANGELOG.md ]; then \
		echo "ERROR: CHANGELOG.md not found."; \
		exit 1; \
	fi
	@if ! grep -q "## \[Unreleased\]" CHANGELOG.md && ! grep -q "## \[$(PLUGIN_VERSION)\]" CHANGELOG.md; then \
		echo "WARNING: CHANGELOG.md may not be updated for version $(PLUGIN_VERSION)."; \
	fi
	@echo "Pre-release checks passed."

## Generate SHA256 checksum for the release bundle.
.PHONY: release-checksum
release-checksum:
	@echo "Generating SHA256 checksum..."
	@cd dist && shasum -a 256 $(BUNDLE_NAME) > $(BUNDLE_NAME).sha256
	@echo "Checksum: $$(cat dist/$(BUNDLE_NAME).sha256)"

## Re-package the release tarball with SBOMs + CodeQL SARIF reports embedded.
## Runs after `make all` produced dist/$(BUNDLE_NAME) so we already have the
## staged dist/$(PLUGIN_ID) tree to extend.
.PHONY: release-bundle
release-bundle:
	@echo "Including SBOMs and security reports in release bundle..."
	@# The original `make bundle` deletes dist/$(PLUGIN_ID) after tarring.
	@# Re-create it from the tarball so we can add sbom/ and security/ alongside.
	@if [ ! -d dist/$(PLUGIN_ID) ]; then \
		cd dist && tar -xzf $(BUNDLE_NAME); \
	fi
	@if [ -d dist/sbom ]; then \
		cp -r dist/sbom dist/$(PLUGIN_ID)/; \
		echo "SBOMs included in bundle"; \
	else \
		echo "WARNING: No SBOMs found to include"; \
	fi
	@mkdir -p dist/$(PLUGIN_ID)/security
	@if [ -f dist/codeql-go.sarif ]; then \
		cp dist/codeql-go.sarif dist/$(PLUGIN_ID)/security/; \
		echo "Go CodeQL results included"; \
	fi
	@rm -f dist/$(BUNDLE_NAME)
	@if [ "$$(uname)" = "Darwin" ]; then \
		cd dist && COPYFILE_DISABLE=1 tar --disable-copyfile --exclude='._*' --exclude='.DS_Store' -czf $(BUNDLE_NAME) $(PLUGIN_ID); \
	else \
		cd dist && tar -czf $(BUNDLE_NAME) $(PLUGIN_ID); \
	fi
	@rm -rf dist/$(PLUGIN_ID)

## Sign the plugin bundle with GPG. Requires PLUGIN_SIGNING_KEY (key ID).
## Skips silently if PLUGIN_SIGNING_KEY is unset — local builds don't need
## a signature. CI is expected to set it from a repo secret.
.PHONY: release-sign
release-sign:
	@if [ -n "$(PLUGIN_SIGNING_KEY)" ]; then \
		echo "Signing plugin bundle with GPG key $(PLUGIN_SIGNING_KEY)..."; \
		gpg -u $(PLUGIN_SIGNING_KEY) --verbose --personal-digest-preferences SHA256 --detach-sign dist/$(BUNDLE_NAME); \
		echo "Signature: dist/$(BUNDLE_NAME).sig"; \
	else \
		echo "PLUGIN_SIGNING_KEY not set, skipping signing."; \
	fi

## Create an annotated git tag for the release version.
.PHONY: release-tag
release-tag:
	@echo "Creating git tag v$(PLUGIN_VERSION)..."
	@if git rev-parse "v$(PLUGIN_VERSION)" >/dev/null 2>&1; then \
		echo "ERROR: Tag v$(PLUGIN_VERSION) already exists."; \
		exit 1; \
	fi
	git tag -a "v$(PLUGIN_VERSION)" -m "Release v$(PLUGIN_VERSION)"
	@echo "Tag v$(PLUGIN_VERSION) created. Push with: git push origin v$(PLUGIN_VERSION)"

## Full release build: clean, checks, style, tests, build, SBOM audit, CodeQL,
## security gate, bundle with SBOMs/SARIF, virus scan, sign, checksum.
.PHONY: release
release: release-check clean all sbom-audit codeql-analyze security-gate release-bundle virus-scan release-sign release-checksum
	@echo ""
	@echo "=========================================="
	@echo "Release build complete!"
	@echo "Bundle:   dist/$(BUNDLE_NAME)"
	@echo "Checksum: dist/$(BUNDLE_NAME).sha256"
	@if [ -f dist/$(BUNDLE_NAME).sig ]; then echo "Signature: dist/$(BUNDLE_NAME).sig"; fi
	@echo "SBOMs included in bundle"
	@echo ""
	@echo "To tag this release: make release-tag"
	@echo "=========================================="

# ====================================================================================
# SBOM & Vulnerability Scanning
# ====================================================================================
# CycloneDX-gomod produces a CycloneDX JSON SBOM from the Go module graph;
# Grype scans the SBOM against the OSS vulnerability databases (NVD, GHSA,
# OS package advisories). `--fail-on high` blocks the build at HIGH and
# CRITICAL severity — MEDIUM/LOW are surfaced but non-blocking.
# ====================================================================================

## Install SBOM generation tools.
.PHONY: install-sbom-tools
install-sbom-tools:
	@echo "Installing SBOM generation tools..."
	GOBIN=$(GOBIN) $(GO) install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest

## Install Grype vulnerability scanner into build/bin via `go install`.
## Cross-platform: works on Linux, macOS, and Windows wherever Go is
## installed (which is required to build the plugin anyway). Avoids
## anchore's install.sh — that script's writability probe fails on
## macOS arm64 with "ERROR: You must be root" even when -b points at a
## user-writable directory, and using `go install` keeps the toolchain
## identical across local dev, Linux build runners, and Windows hosts.
## Trade-off: `grype --version` reports "[not provided]" because we
## don't set goreleaser's ldflags — purely cosmetic, scanner works.
.PHONY: install-grype
install-grype:
	@if [ ! -x "$(GOBIN)/grype" ]; then \
		echo "Installing Grype..."; \
		mkdir -p $(GOBIN); \
		GOBIN=$(GOBIN) $(GO) install github.com/anchore/grype/cmd/grype@latest; \
	else \
		echo "Grype already installed"; \
	fi

## Generate CycloneDX JSON SBOM for the Go server module.
.PHONY: sbom
sbom: install-sbom-tools
	@mkdir -p dist/sbom
ifneq ($(HAS_SERVER),)
	@echo "Generating Go SBOM..."
	$(GOBIN)/cyclonedx-gomod mod -json -output dist/sbom/server-sbom.json
endif
	@echo "SBOMs generated in dist/sbom/"
	@ls -la dist/sbom/

## Scan generated SBOMs for vulnerabilities. Fails on HIGH or CRITICAL.
.PHONY: sbom-scan
sbom-scan: install-grype
	@if [ ! -d dist/sbom ]; then \
		echo "No SBOMs found. Run 'make sbom' first."; \
		exit 1; \
	fi
ifneq ($(HAS_SERVER),)
	@echo "Scanning Go dependencies for vulnerabilities..."
	$(GOBIN)/grype sbom:dist/sbom/server-sbom.json --output table --fail-on high
endif

## Generate SBOMs and scan for vulnerabilities.
.PHONY: sbom-audit
sbom-audit: sbom sbom-scan

# ====================================================================================
# CodeQL Static Analysis
# ====================================================================================
# CodeQL parses the source into a relational DB and runs the standard
# go-queries pack against it. SARIF output lands in dist/ for upload to
# GitHub code scanning + inclusion in the release bundle.
# ====================================================================================

CODEQL_VERSION ?= 2.20.1
CODEQL_DIR := $(PWD)/build/codeql
CODEQL := $(CODEQL_DIR)/codeql/codeql
CODEQL_DB_DIR := $(PWD)/build/codeql-db

## Install CodeQL CLI bundle (cached after first install).
.PHONY: install-codeql
install-codeql:
	@if [ ! -f "$(CODEQL)" ]; then \
		echo "Installing CodeQL CLI v$(CODEQL_VERSION)..."; \
		mkdir -p $(CODEQL_DIR); \
		if [ "$$(uname)" = "Darwin" ]; then \
			CODEQL_PLATFORM="osx64"; \
		else \
			CODEQL_PLATFORM="linux64"; \
		fi; \
		curl -sSL "https://github.com/github/codeql-action/releases/download/codeql-bundle-v$(CODEQL_VERSION)/codeql-bundle-$$CODEQL_PLATFORM.tar.gz" | tar -xz -C $(CODEQL_DIR); \
		echo "CodeQL CLI installed"; \
	else \
		echo "CodeQL CLI already installed"; \
	fi

## Run CodeQL analysis on the Go server code; writes SARIF to dist/.
## Indexes from the repo root so root-package files (main.go,
## docs_embed.go, runbooks_embed.go) are analyzed alongside server/.
## Earlier --source-root=server omitted ~10 files because they import
## packages that live outside the source-root.
.PHONY: codeql-go
codeql-go: install-codeql
ifneq ($(HAS_SERVER),)
	@mkdir -p dist
	@echo "Running CodeQL analysis on Go code..."
	@rm -rf $(CODEQL_DB_DIR)/go
	@mkdir -p $(CODEQL_DB_DIR)/go
	$(CODEQL) database create $(CODEQL_DB_DIR)/go --language=go --overwrite
	$(CODEQL) database analyze $(CODEQL_DB_DIR)/go --format=sarif-latest --output=dist/codeql-go.sarif -- codeql/go-queries
	@echo "Go CodeQL results: dist/codeql-go.sarif"
endif

## Run all CodeQL analyses.
.PHONY: codeql-analyze
codeql-analyze: codeql-go
	@echo "CodeQL analysis complete. Results in dist/codeql-*.sarif"

## Fail the build if any CodeQL SARIF report contains an error-level result.
## CodeQL maps "error" to its high/critical severity rules.
.PHONY: security-gate
security-gate:
	@echo "Checking security scan results for critical/high issues..."
	@failed=0; \
	for sarif in dist/codeql-*.sarif; do \
		[ -f "$$sarif" ] || continue; \
		count=$$(python3 -c "import json,sys;data=json.load(open(sys.argv[1]));print(sum(1 for run in data.get('runs',[]) for result in run.get('results',[]) if result.get('level')=='error'))" "$$sarif"); \
		if [ "$$count" -gt 0 ]; then \
			echo "ERROR: $$sarif contains $$count critical/high severity issue(s)."; \
			failed=1; \
		else \
			echo "OK: $$sarif has no critical/high severity issues."; \
		fi; \
	done; \
	if [ "$$failed" -eq 1 ]; then \
		echo ""; \
		echo "Security gate FAILED: Critical or high severity issues found."; \
		echo "Review the SARIF files in dist/ for details."; \
		exit 1; \
	fi
	@echo "Security gate passed."

# ====================================================================================
# Virus Scanning
# ====================================================================================
# ClamAV scans the dist/ tree (which contains the staged bundle + tarball)
# before we ship anything. Catches the supply-chain-poisoning class of
# issue where a dependency drops a malicious binary into vendored output.
# ====================================================================================

## Install ClamAV antivirus scanner (idempotent).
.PHONY: install-clamav
install-clamav:
	@if ! command -v clamscan >/dev/null 2>&1; then \
		echo "Installing ClamAV..."; \
		if [ "$$(uname)" = "Darwin" ]; then \
			brew install clamav; \
		else \
			sudo apt-get update && sudo apt-get install -y clamav; \
		fi; \
	else \
		echo "ClamAV already installed"; \
	fi
	@echo "Updating virus definitions..."
	@if [ "$$(uname)" = "Darwin" ]; then \
		if [ ! -f /opt/homebrew/etc/clamav/freshclam.conf ] && [ -f /opt/homebrew/etc/clamav/freshclam.conf.sample ]; then \
			cp /opt/homebrew/etc/clamav/freshclam.conf.sample /opt/homebrew/etc/clamav/freshclam.conf; \
			sed -i '' 's/^Example/#Example/' /opt/homebrew/etc/clamav/freshclam.conf; \
		fi; \
	else \
		sudo systemctl stop clamav-freshclam 2>/dev/null || true; \
	fi
	@sudo freshclam || freshclam || true

## Scan dist/ for viruses (fails if any detected).
.PHONY: virus-scan
virus-scan: install-clamav
	@if [ ! -d dist ]; then \
		echo "No dist/ directory found. Run 'make dist' first."; \
		exit 1; \
	fi
	@echo "Scanning release artifacts for viruses..."
	clamscan --recursive --infected --alert-broken dist/
	@echo "Virus scan passed."

# ====================================================================================
# Docker Development Environment
# ====================================================================================
# Spins up Mattermost Enterprise Edition + Postgres locally for plugin
# iteration. `make docker-setup` is the one-shot bootstrap (start +
# create admin user + create Test team). `make docker-deploy` builds the
# plugin and uploads it via mmctl --local.
# ====================================================================================

DOCKER_COMPOSE := docker compose -f docker-compose.dev.yml
MM_PORT ?= 8065

## Start Mattermost and PostgreSQL containers.
.PHONY: docker-start
docker-start:
	@echo "Starting Mattermost Enterprise Edition..."
	@mkdir -p docker/mattermost/{config,data,logs,plugins,client-plugins}
	@mkdir -p docker/postgres-data
	@$(DOCKER_COMPOSE) up -d

## Stop containers (preserves data).
.PHONY: docker-stop
docker-stop:
	@$(DOCKER_COMPOSE) stop

## Stop and remove containers.
.PHONY: docker-down
docker-down:
	@$(DOCKER_COMPOSE) down

## Remove containers and all data.
.PHONY: docker-clean
docker-clean:
	@$(DOCKER_COMPOSE) down -v
	@rm -rf docker/postgres-data docker/mattermost
	@echo "Containers and data removed"

## Kill orphaned Docker containers on the MM port.
.PHONY: docker-kill-orphans
docker-kill-orphans:
	@project=$$(docker ps --filter "publish=$(MM_PORT)" \
		--format '{{.Label "com.docker.compose.project"}}' | head -1); \
	if [ -z "$$project" ]; then \
		echo "No containers found on port $(MM_PORT)"; \
	else \
		echo "Stopping compose project: $$project"; \
		docker compose -p $$project down -v; \
		echo "Project $$project removed"; \
	fi

## Tail Mattermost container logs.
.PHONY: docker-logs
docker-logs: docker-check
	@$(DOCKER_COMPOSE) logs -f mattermost

## First-time setup: start containers and create admin user + Test team.
.PHONY: docker-setup
docker-setup: docker-start
	@echo "Waiting for Mattermost to be ready..."
	@until curl -sf http://localhost:$(MM_PORT)/api/v4/system/ping >/dev/null 2>&1; do \
		sleep 2; \
		echo "Waiting..."; \
	done
	@echo "Creating admin user..."
	@$(DOCKER_COMPOSE) exec -T mattermost mmctl --local user create \
		--email admin@example.com \
		--username admin \
		--password 'password' \
		--system-admin 2>/dev/null || echo "Admin user already exists"
	@echo "Creating default team..."
	@$(DOCKER_COMPOSE) exec -T mattermost mmctl --local team create \
		--name test \
		--display-name "Test" 2>/dev/null || echo "Team 'Test' already exists"
	@echo "Adding admin to Test team..."
	@$(DOCKER_COMPOSE) exec -T mattermost mmctl --local team users add test admin 2>/dev/null || echo "Admin already in Test team"
	@echo ""
	@echo "=========================================="
	@echo "Mattermost ready at http://localhost:$(MM_PORT)"
	@echo "Login: admin / password"
	@echo "Team: Test"
	@echo "=========================================="

## Check if Mattermost container is running.
.PHONY: docker-check
docker-check:
	@if ! $(DOCKER_COMPOSE) ps --status running 2>/dev/null | grep -q mattermost; then \
		echo "Error: Mattermost container is not running."; \
		echo "Run 'make docker-setup' first to start the environment."; \
		exit 1; \
	fi

## Build and deploy plugin to Docker Mattermost.
.PHONY: docker-deploy
docker-deploy: docker-check dist
	@echo "Deploying plugin to Docker Mattermost..."
	@$(DOCKER_COMPOSE) cp dist/$(BUNDLE_NAME) mattermost:/tmp/$(BUNDLE_NAME)
	@$(DOCKER_COMPOSE) exec -T mattermost mmctl --local plugin add /tmp/$(BUNDLE_NAME) --force
	@$(DOCKER_COMPOSE) exec -T mattermost mmctl --local plugin enable $(PLUGIN_ID)
	@echo "Plugin $(PLUGIN_ID) deployed and enabled"

## Disable then re-enable the plugin in Docker.
.PHONY: docker-reset
docker-reset: docker-check
	@$(DOCKER_COMPOSE) exec -T mattermost mmctl --local plugin disable $(PLUGIN_ID)
	@$(DOCKER_COMPOSE) exec -T mattermost mmctl --local plugin enable $(PLUGIN_ID)
	@echo "Plugin $(PLUGIN_ID) reset"

## List installed plugins in Docker.
.PHONY: docker-plugin-list
docker-plugin-list: docker-check
	@$(DOCKER_COMPOSE) exec -T mattermost mmctl --local plugin list

## Nuke everything: containers, data, build artifacts.
.PHONY: nuke
nuke: docker-kill-orphans
	@echo "Nuking everything..."
	@$(DOCKER_COMPOSE) down -v 2>/dev/null || true
	@rm -rf docker/postgres-data docker/mattermost
	@rm -fr dist/
	@rm -fr server/coverage.txt server/dist
	@rm -fr build/bin/ build/codeql/ build/codeql-db/
	@echo "Everything removed. Run 'make docker-setup' to start fresh."

# Help documentation à la https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html
help:
	@cat Makefile build/*.mk | grep -v '\.PHONY' |  grep -v '\help:' | grep -B1 -E '^[a-zA-Z0-9_.-]+:.*' | sed -e "s/:.*//" | sed -e "s/^## //" |  grep -v '\-\-' | sed '1!G;h;$$!d' | awk 'NR%2{printf "\033[36m%-30s\033[0m",$$0;next;}1' | sort
