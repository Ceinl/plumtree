GO ?= go
GOCACHE ?= /private/tmp/plums-go-cache
PT ?= pt
PT_ARGS ?= ping
PT_DIR ?= $(CURDIR)

ADDR ?= 127.0.0.1:18080
ORIGIN ?= http://localhost:18080
DEV_TOKEN ?= local-dev
SSH_ADDR ?= 127.0.0.1:2222
# A standard-port all-in-one server can override SSH_ADDR=<app-ip>:22, but only
# after administrator SSH is proven on a different address or port.
SESSION_TIMEOUT ?= 0
SSH_IDLE_TIMEOUT ?= -1s
BUILD_DEV_ROOT ?= $(abspath $(CURDIR))
STATE_DIR ?= $(HOME)/Library/Application Support/plumtree
STATE_FILE ?= $(STATE_DIR)/control-plane-state.json
KV_DIR ?= $(STATE_DIR)/kv

# Public and local pt builds are generic. `make pt-local` supplies temporary
# address/token overrides matching run-server without changing user config.
PT_LDFLAGS ?= -s -w

.PHONY: help test-control-plane pt-local run-server run-server-memory seed-server clear-server build-pt install-pt sqlcipher-test

help:
	@printf '%s\n' \
		'Targets:' \
		'  make test-control-plane Run control-plane tests' \
		'  make pt-local           Run pt against run-server (PT_ARGS=ping)' \
		'  make run-server         Run local control plane with persistent default state' \
		'  make run-server-memory  Run local control plane with in-memory state only' \
		'  make seed-server        Run local control plane with demo seed data' \
		'  make clear-server       Delete local test server state and KV data' \
		'  make build-pt           Build generic ./pt-bin' \
		'  make install-pt         Install generic pt; configure it at runtime' \
		'  make sqlcipher-test     Run the native SQLCipher qualification suite'

test-control-plane:
	GOCACHE=$(GOCACHE) $(GO) test ./internal/server/controlrole

pt-local:
	@printf 'pt local endpoint: %s\n' "$(ORIGIN)"
	@cd "$(PT_DIR)" && PLUMTREE_SERVER_URL="$(ORIGIN)" PLUMTREE_DEV_TOKEN="$(DEV_TOKEN)" "$(PT)" $(PT_ARGS)

run-server:
	PLUMTREE_DEV_TOKEN=$(DEV_TOKEN) $(GO) run ./cmd/plumtree \
		-addr $(ADDR) \
		-origin $(ORIGIN) \
		-dev-token $(DEV_TOKEN) \
		-build-dev-root "$(BUILD_DEV_ROOT)" \
		-ssh-addr $(SSH_ADDR) \
		-session-timeout $(SESSION_TIMEOUT) \
		-ssh-idle-timeout $(SSH_IDLE_TIMEOUT)

run-server-memory:
	PLUMTREE_DEV_TOKEN=$(DEV_TOKEN) $(GO) run ./cmd/plumtree \
		-addr $(ADDR) \
		-origin $(ORIGIN) \
		-dev-token $(DEV_TOKEN) \
		-build-dev-root "$(BUILD_DEV_ROOT)" \
		-ssh-addr $(SSH_ADDR) \
		-session-timeout $(SESSION_TIMEOUT) \
		-ssh-idle-timeout $(SSH_IDLE_TIMEOUT) \
		-state-file ""

seed-server:
	PLUMTREE_DEV_TOKEN=$(DEV_TOKEN) $(GO) run ./cmd/plumtree \
		-addr $(ADDR) \
		-origin $(ORIGIN) \
		-dev-token $(DEV_TOKEN) \
		-build-dev-root "$(BUILD_DEV_ROOT)" \
		-ssh-addr $(SSH_ADDR) \
		-session-timeout $(SESSION_TIMEOUT) \
		-ssh-idle-timeout $(SSH_IDLE_TIMEOUT) \
		-seed-demo

clear-server:
	rm -f "$(STATE_FILE)"
	rm -rf "$(KV_DIR)"

build-pt:
	GOCACHE=$(GOCACHE) $(GO) build -trimpath -ldflags "$(PT_LDFLAGS)" -o "$(abspath $(CURDIR))/pt-bin" ./cmd/pt
	@echo "built generic pt-bin; run 'pt-bin --add-server URL ALIAS' and enter the token"

install-pt:
	GOCACHE=$(GOCACHE) $(GO) install -trimpath -ldflags "$(PT_LDFLAGS)" ./cmd/pt
	@echo "installed generic pt; run 'pt --add-server URL ALIAS' and enter the token"

sqlcipher-test:
	./scripts/check-sqlcipher-target.sh "$(shell go env GOOS)/$(shell go env GOARCH)"
