# Ensure that go is installed. Note that this is independent of whether or not a server is being
# built, since the build script itself uses go.
ifeq ($(GO),)
    $(error "go is not available: see https://golang.org/doc/install")
endif

# Ensure that the build tools are compiled. Go's caching makes this quick.
$(shell cd build/manifest && $(GO) build -o ../bin/manifest)

# Ensure that the deployment tools are compiled. Go's caching makes this quick.
$(shell cd build/pluginctl && $(GO) build -o ../bin/pluginctl)

# Plugin metadata from the manifest.
#
# These use := (simply-expanded, resolved ONCE at parse time) rather than ?=
# (recursively-expanded, re-runs `build/bin/manifest ...` on every reference).
# The lazy ?= form was a latent bug: `make release` chains `clean` (which
# deletes build/bin/) before the build, so a later $(PLUGIN_ID)/$(BUNDLE_NAME)
# reference re-ran a now-missing tool and expanded to empty -> empty bundle
# name -> "tar: refusing to create an empty archive". Caching here (before
# clean runs) keeps clean free to delete build/bin/ as normal.
#
# $(or ...) preserves the ?= override: an env var or command-line PLUGIN_ID=...
# still wins; otherwise resolve from the manifest.

# Extract the plugin id from the manifest.
PLUGIN_ID := $(or $(PLUGIN_ID),$(shell build/bin/manifest id))
ifeq ($(PLUGIN_ID),)
    $(error "Cannot parse id from $(MANIFEST_FILE)")
endif

# Extract the plugin version from the manifest.
PLUGIN_VERSION := $(or $(PLUGIN_VERSION),$(shell build/bin/manifest version))
ifeq ($(PLUGIN_VERSION),)
    $(error "Cannot parse version from $(MANIFEST_FILE)")
endif

# Determine if a server is defined in the manifest.
HAS_SERVER := $(or $(HAS_SERVER),$(shell build/bin/manifest has_server))

# Determine if a webapp is defined in the manifest.
HAS_WEBAPP := $(or $(HAS_WEBAPP),$(shell build/bin/manifest has_webapp))

# Determine if a /public folder is in use
HAS_PUBLIC ?= $(wildcard public/.)

# Determine if the mattermost-utilities repo is present
HAS_MM_UTILITIES ?= $(wildcard $(MM_UTILITIES_DIR)/.)

# Store the current path for later use
PWD ?= $(shell pwd)

# Ensure that npm (and thus node) is installed.
ifneq ($(HAS_WEBAPP),)
ifeq ($(NPM),)
    $(error "npm is not available: see https://www.npmjs.com/get-npm")
endif
endif
