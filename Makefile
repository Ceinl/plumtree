GO ?= go
GOCACHE ?= /private/tmp/plums-go-cache
PT ?= pt
PT_ARGS ?= ping
PT_DIR ?= $(CURDIR)

SSH_ADDR ?= 127.0.0.1:2222
DATABASE ?= $(CURDIR)/plumtree.db
HOST_KEY ?= $(CURDIR)/plumtree_host_key
PRODUCT_VERSION ?= dev
CONFIG ?= $(CURDIR)/config.local.json

# Public and local pt builds are generic. `make pt-local` supplies temporary
# address/token overrides matching run-server without changing user config.
PT_LDFLAGS ?= -s -w

.PHONY: help test-root run-server clear-server build-pt install-pt sqlcipher-test

help:
	@printf '%s\n' \
		'Targets:' \
		'  make test-root          Run root module tests' \
		'  make run-server         Run the native SSH/SQLite server' \
		'  make clear-server       Delete local native server state' \
		'  make build-pt           Build generic ./pt-bin' \
		'  make install-pt         Install generic pt; configure it at runtime' \
		'  make sqlcipher-test     Run the native SQLCipher qualification suite'

test-root:
	GOCACHE=$(GOCACHE) $(GO) test ./...

run-server:
	$(GO) run ./cmd/plumtree serve -config "$(CONFIG)" -storage-database-path "$(DATABASE)" -storage-ssh-identity "$(HOST_KEY)" -product-version "$(PRODUCT_VERSION)" -exposure-ssh-address "$(SSH_ADDR)"

clear-server:
	rm -f "$(DATABASE)" "$(HOST_KEY)" "$(CONFIG)" "$(CONFIG).lock"

build-pt:
	GOCACHE=$(GOCACHE) $(GO) build -trimpath -ldflags "$(PT_LDFLAGS)" -o "$(abspath $(CURDIR))/pt-bin" ./cmd/pt
	@echo "built clean pt-bin; pair it with a Plumtree server before using remote commands"

install-pt:
	GOCACHE=$(GOCACHE) $(GO) install -trimpath -ldflags "$(PT_LDFLAGS)" ./cmd/pt
	@echo "installed clean pt; pair it with a Plumtree server before using remote commands"

sqlcipher-test:
	./scripts/check-sqlcipher-target.sh "$(shell go env GOOS)/$(shell go env GOARCH)"
