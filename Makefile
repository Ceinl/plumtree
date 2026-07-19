GO ?= go
GOCACHE ?= /private/tmp/plums-go-cache

ADDR ?= 127.0.0.1:18080
ORIGIN ?= http://localhost:18080
DEV_TOKEN ?= local-dev
SSH_ADDR ?= 127.0.0.1:2222
SSH_HOST ?= plumtree.dev
SESSION_TIMEOUT ?= 0
SSH_IDLE_TIMEOUT ?= -1
BUILD_DEV_ROOT ?= $(abspath $(CURDIR))
STATE_DIR ?= $(HOME)/Library/Application Support/plumtree
STATE_FILE ?= $(STATE_DIR)/control-plane-state.json
KV_DIR ?= $(STATE_DIR)/kv

# pt build config. The server URL and deploy token are baked into the binary so
# a baked pt runs `pt deploy` with no env/flags. They are build config, not
# secrets — the token is recoverable from the binary, so keep it narrowly scoped.
# pt is `package main`, so the linker symbol prefix is `main`, not the import path.
PT_ADDR ?= $(ORIGIN)
PT_DEV_TOKEN ?= $(DEV_TOKEN)
PT_LDFLAGS = -s -w -X main.defaultServerURL=$(PT_ADDR) -X main.defaultDevToken=$(PT_DEV_TOKEN)

.PHONY: help test-control-plane run-server run-server-memory seed-server clear-server build-pt install-pt

help:
	@printf '%s\n' \
		'Targets:' \
		'  make test-control-plane Run control-plane tests' \
		'  make run-server         Run local control plane with persistent default state' \
		'  make run-server-memory  Run local control plane with in-memory state only' \
		'  make seed-server        Run local control plane with demo seed data' \
		'  make clear-server       Delete local test server state and KV data' \
		'  make build-pt           Build pt with server URL + token baked in (./pt)' \
		'  make install-pt         go install pt with server URL + token baked in'

test-control-plane:
	cd control-plane && GOCACHE=$(GOCACHE) $(GO) test ./...

run-server:
	cd control-plane && PLUMTREE_DEV_TOKEN=$(DEV_TOKEN) $(GO) run ./cmd/control-plane \
		-addr $(ADDR) \
		-origin $(ORIGIN) \
		-dev-token $(DEV_TOKEN) \
		-build-dev-root "$(BUILD_DEV_ROOT)" \
		-ssh-addr $(SSH_ADDR) \
		-ssh-host $(SSH_HOST) \
		-session-timeout $(SESSION_TIMEOUT) \
		-ssh-idle-timeout $(SSH_IDLE_TIMEOUT)

run-server-memory:
	cd control-plane && PLUMTREE_DEV_TOKEN=$(DEV_TOKEN) $(GO) run ./cmd/control-plane \
		-addr $(ADDR) \
		-origin $(ORIGIN) \
		-dev-token $(DEV_TOKEN) \
		-build-dev-root "$(BUILD_DEV_ROOT)" \
		-ssh-addr $(SSH_ADDR) \
		-ssh-host $(SSH_HOST) \
		-session-timeout $(SESSION_TIMEOUT) \
		-ssh-idle-timeout $(SSH_IDLE_TIMEOUT) \
		-state-file ""

seed-server:
	cd control-plane && PLUMTREE_DEV_TOKEN=$(DEV_TOKEN) $(GO) run ./cmd/control-plane \
		-addr $(ADDR) \
		-origin $(ORIGIN) \
		-dev-token $(DEV_TOKEN) \
		-build-dev-root "$(BUILD_DEV_ROOT)" \
		-ssh-addr $(SSH_ADDR) \
		-ssh-host $(SSH_HOST) \
		-session-timeout $(SESSION_TIMEOUT) \
		-ssh-idle-timeout $(SSH_IDLE_TIMEOUT) \
		-seed-demo

clear-server:
	rm -f "$(STATE_FILE)"
	rm -rf "$(KV_DIR)"

build-pt:
	cd pt && GOCACHE=$(GOCACHE) $(GO) build -trimpath -ldflags "$(PT_LDFLAGS)" -o "$(abspath $(CURDIR))/pt-bin" .
	@echo "built pt-bin (server=$(PT_ADDR))"

install-pt:
	cd pt && GOCACHE=$(GOCACHE) $(GO) install -trimpath -ldflags "$(PT_LDFLAGS)" .
	@echo "installed pt (server=$(PT_ADDR))"
