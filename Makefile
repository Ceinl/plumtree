GO ?= go
GOCACHE ?= /private/tmp/plums-go-cache

ADDR ?= 127.0.0.1:18080
ORIGIN ?= http://localhost:18080
DEV_TOKEN ?= local-dev
SSH_ADDR ?= 127.0.0.1:2222
SSH_HOST ?= plumtree.dev
BUILD_DEV_ROOT ?= $(abspath $(CURDIR))
STATE_DIR ?= $(HOME)/Library/Application Support/plumtree
STATE_FILE ?= $(STATE_DIR)/control-plane-state.json
KV_DIR ?= $(STATE_DIR)/kv

.PHONY: help test-control-plane run-server run-server-memory seed-server clear-server

help:
	@printf '%s\n' \
		'Targets:' \
		'  make test-control-plane Run control-plane tests' \
		'  make run-server         Run local control plane with persistent default state' \
		'  make run-server-memory  Run local control plane with in-memory state only' \
		'  make seed-server        Run local control plane with demo seed data' \
		'  make clear-server       Delete local test server state and KV data'

test-control-plane:
	cd control-plane && GOCACHE=$(GOCACHE) $(GO) test ./...

run-server:
	cd control-plane && PLUMTREE_DEV_TOKEN=$(DEV_TOKEN) $(GO) run ./cmd/control-plane \
		-addr $(ADDR) \
		-origin $(ORIGIN) \
		-dev-token $(DEV_TOKEN) \
		-build-dev-root "$(BUILD_DEV_ROOT)" \
		-ssh-addr $(SSH_ADDR) \
		-ssh-host $(SSH_HOST)

run-server-memory:
	cd control-plane && PLUMTREE_DEV_TOKEN=$(DEV_TOKEN) $(GO) run ./cmd/control-plane \
		-addr $(ADDR) \
		-origin $(ORIGIN) \
		-dev-token $(DEV_TOKEN) \
		-build-dev-root "$(BUILD_DEV_ROOT)" \
		-ssh-addr $(SSH_ADDR) \
		-ssh-host $(SSH_HOST) \
		-state-file ""

seed-server:
	cd control-plane && PLUMTREE_DEV_TOKEN=$(DEV_TOKEN) $(GO) run ./cmd/control-plane \
		-addr $(ADDR) \
		-origin $(ORIGIN) \
		-dev-token $(DEV_TOKEN) \
		-build-dev-root "$(BUILD_DEV_ROOT)" \
		-ssh-addr $(SSH_ADDR) \
		-ssh-host $(SSH_HOST) \
		-seed-demo

clear-server:
	rm -f "$(STATE_FILE)"
	rm -rf "$(KV_DIR)"
