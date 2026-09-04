GO ?= go
GOCACHE ?= /private/tmp/plums-go-cache
PT ?= pt
PT_ARGS ?= ping
PT_DIR ?= $(CURDIR)

SSH_ADDR ?= 127.0.0.1:2222
PRODUCT_VERSION ?= dev
# Shared dev server home: one config and one state dir for every worktree, so
# `make run-server` from any checkout serves the same dev server identity.
DEV_HOME ?= $(HOME)/.config/plumtree
CONFIG ?= $(DEV_HOME)/dev.json
DATABASE ?= $(DEV_HOME)/plumtree.db
HOST_KEY ?= $(DEV_HOME)/plumtree_host_key

# Public and local pt builds are generic. `make pt-local` supplies temporary
# address/token overrides matching run-server without changing user config.
PT_LDFLAGS ?= -s -w

.PHONY: help test-root run-server config-local bootstrap pair clear-server build-pt install-pt sqlcipher-test

help:
	@printf '%s\n' \
		'Targets:' \
		'  make test-root          Run root module tests' \
		             '  make run-server         Run the native SSH/SQLite server' \
		             '  make config-local       Persist local serve settings into the shared dev config' \
             '  make bootstrap          Mint a one-use first-author authority (HANDLE=$(USER))' \
             '  make pair               Pair this device (BOOTSTRAP_ID=<id> [SECRET=<phrase>])' \
		'  make clear-server       Delete local native server state' \
		'  make build-pt           Build generic ./pt-bin' \
		'  make install-pt         Install generic pt; configure it at runtime' \
		'  make sqlcipher-test     Run the native SQLCipher qualification suite'

test-root:
	GOCACHE=$(GOCACHE) $(GO) test ./...

run-server: config-local
	PLUMTREE_PRODUCT_VERSION="$(PRODUCT_VERSION)" $(GO) run ./cmd/plumtree serve --config "$(CONFIG)"

# Local serve settings live in the config file, not the serve command line:
# storage paths are relative to the config file, so they follow it into the
# shared dev home automatically. Product version stays out of the file on
# purpose: it is run-scoped version identity (flag or
# PLUMTREE_PRODUCT_VERSION), not persisted configuration. One-off overrides
# still work without flags via PLUMTREE_<FIELD> env, e.g.
# PLUMTREE_EXPOSURE_SSH_ADDRESS=:2222.
config-local:
	@mkdir -p "$(DEV_HOME)"
	$(GO) run ./cmd/plumtree config set --config "$(CONFIG)" exposure.ssh.address "$(SSH_ADDR)"

# First-author onboarding for the shared dev server. Bootstrap mints a
# one-use authority (secret shown once, 10 minute default TTL); pair consumes
# it and registers this device. With SECRET empty, pt prompts for the phrase
# instead of taking it from the command line and shell history.
HANDLE ?= $(USER)
# ID is accepted as a short alias for BOOTSTRAP_ID.
BOOTSTRAP_ID ?= $(ID)
SECRET ?=

bootstrap:
	$(GO) run ./cmd/plumtree bootstrap --config "$(CONFIG)" -handle "$(HANDLE)"

# Pairing consumes the one-use ID from `make bootstrap`:
#   make pair BOOTSTRAP_ID=<id> [SECRET=<phrase>]  (ID= works too)
# With SECRET empty, pt prompts for the phrase instead of reading shell
# history. Missing/invalid input is reported by pt itself.
pair:
	$(GO) run ./cmd/pt pair --bootstrap "$(BOOTSTRAP_ID)" --secret "$(SECRET)" --yes "$(SSH_ADDR)"

clear-server:
	rm -f "$(DATABASE)" "$(HOST_KEY)" "$(CONFIG)" "$(CONFIG).lock"

build-pt: $(PT_BIN)

PT_BIN ?= $(CURDIR)/pt-bin

$(PT_BIN):
	GOCACHE=$(GOCACHE) $(GO) build -trimpath -ldflags "$(PT_LDFLAGS)" -o "$(PT_BIN)" ./cmd/pt
	@echo "built clean pt-bin; pair it with a Plumtree server before using remote commands"

install-pt:
	GOCACHE=$(GOCACHE) $(GO) install -trimpath -ldflags "$(PT_LDFLAGS)" ./cmd/pt
	@echo "installed clean pt; pair it with a Plumtree server before using remote commands"

sqlcipher-test:
	./scripts/check-sqlcipher-target.sh "$(shell go env GOOS)/$(shell go env GOARCH)"
