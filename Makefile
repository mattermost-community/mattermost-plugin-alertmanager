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

# Include custom makefile, if present
ifneq ($(wildcard build/custom.mk),)
	include build/custom.mk
endif

## Checks the code style, tests, builds and bundles the plugin.
.PHONY: all
all: check-style test dist

## Runs eslint and golangci-lint
.PHONY: check-style
check-style: webapp/node_modules
	@echo Checking for style guide compliance

ifneq ($(HAS_WEBAPP),)
	cd webapp && npm run lint
	cd webapp && npm run check-types
endif

ifneq ($(HAS_SERVER),)
	@if ! [ -x "$$(command -v golangci-lint)" ]; then \
		echo "golangci-lint is not installed. Please see https://github.com/golangci/golangci-lint#install for installation instructions."; \
		exit 1; \
	fi; \

	@echo Running golangci-lint
	golangci-lint run ./...
endif

## Builds the server, if it exists, for all supported architectures.
.PHONY: server
server:
ifneq ($(HAS_SERVER),)
	mkdir -p server/dist;
ifeq ($(MM_DEBUG),)
	cd server && env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) -trimpath -o dist/plugin-linux-amd64;
	cd server && env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(GO_BUILD_FLAGS) -trimpath -o dist/plugin-linux-arm64;
	cd server && env CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) -trimpath -o dist/plugin-darwin-amd64;
	cd server && env CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build $(GO_BUILD_FLAGS) -trimpath -o dist/plugin-darwin-arm64;
	cd server && env CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) -trimpath -o dist/plugin-windows-amd64.exe;
else
	$(info DEBUG mode is on; to disable, unset MM_DEBUG)

	cd server && env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) -trimpath -gcflags "all=-N -l" -o dist/plugin-linux-amd64;
	cd server && env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build $(GO_BUILD_FLAGS) -trimpath -gcflags "all=-N -l" -o dist/plugin-linux-arm64;
	cd server && env CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) -trimpath -gcflags "all=-N -l" -o dist/plugin-darwin-amd64;
	cd server && env CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build $(GO_BUILD_FLAGS) -trimpath -gcflags "all=-N -l" -o dist/plugin-darwin-arm64;
	cd server && env CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build $(GO_BUILD_FLAGS) -trimpath -gcflags "all=-N -l" -o dist/plugin-windows-amd64.exe;
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
.PHONY: render-docs
render-docs:
	cd build/render-docs && $(GO) run main.go

## Generates a tar bundle of the plugin for install.
.PHONY: bundle
bundle: render-docs
	rm -rf dist/
	mkdir -p dist/$(PLUGIN_ID)
	cp $(MANIFEST_FILE) dist/$(PLUGIN_ID)/
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
	cd server && env CGO_ENABLED=0 GOOS=$(HOST_GOOS) GOARCH=$(HOST_GOARCH) $(GO) build $(GO_BUILD_FLAGS) -trimpath -o dist/plugin-$(HOST_ARCH_KEY)$(if $(filter windows,$(HOST_GOOS)),.exe,)
endif

## Bundles only the host arch's binary, with a plugin.json filtered to
## match. python3 is used to filter the JSON — present by default on
## macOS and on most Linux dev hosts.
.PHONY: bundle-host
bundle-host: render-docs
	rm -rf dist/
	mkdir -p dist/$(PLUGIN_ID)
	python3 -c "import json; \
m = json.load(open('plugin.json')); \
k = '$(HOST_ARCH_KEY)'; \
m['server']['executables'] = {k: m['server']['executables'][k]}; \
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

# Help documentation à la https://marmelab.com/blog/2016/02/29/auto-documented-makefile.html
help:
	@cat Makefile build/*.mk | grep -v '\.PHONY' |  grep -v '\help:' | grep -B1 -E '^[a-zA-Z0-9_.-]+:.*' | sed -e "s/:.*//" | sed -e "s/^## //" |  grep -v '\-\-' | sed '1!G;h;$$!d' | awk 'NR%2{printf "\033[36m%-30s\033[0m",$$0;next;}1' | sort
