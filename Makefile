BINARY  := filex
PASSWD_BINARY := filex-passwd
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_FLAGS := -ldflags "-X main.Version=$(VERSION)"
INSTALL_DIR ?= /usr/local/bin

# How long each Go fuzz target runs during `make test` / `make fuzz`.
# Override from the CLI (e.g. `make test FUZZTIME=20s`) for longer hunts.
FUZZTIME ?= 2s

.PHONY: all build test fuzz check install install-openrc clean docker lint sample-config sample-config-auth openapi openapi-check

all: build

build:
	mkdir -p bin
	go build $(BUILD_FLAGS) -o bin/$(BINARY) ./cmd/filex
	go build -o bin/$(PASSWD_BINARY) ./cmd/filex-passwd

test:
	go test ./...
	$(MAKE) fuzz

# Fuzz targets discovered in internal/handler/*_fuzz_test.go; the go tool
# refuses `-fuzz=.` when it matches more than one target, so each runs for
# FUZZTIME in its own `go test` invocation.
FUZZ_TARGETS := $(shell grep -hoE '^func (Fuzz[[:alnum:]_]+)' internal/handler/*_fuzz_test.go | awk '{print $$2}')

# fuzz runs every fuzz target for FUZZTIME each, catching mutations-API
# regressions, panics and jail escapes beyond the unit corpus.
fuzz:
	@test -n "$(FUZZ_TARGETS)" || { echo "no fuzz targets found in internal/handler"; exit 1; }
	@for target in $(FUZZ_TARGETS); do \
		echo "==> fuzzing $$target"; \
		go test -run '^$$' -fuzz="^$${target}$$" -fuzztime=$(FUZZTIME) ./internal/handler || exit 1; \
	done

# check is the full pre-commit gate: formatting, vet, build, tests, the
# embedded JS bundle and doc freshness. CI runs the same targets.
check: lint openapi-check
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }
	go build ./...
	go test ./...
	node --check web/static/app.js

install: build
	install -Dm755 bin/$(BINARY) $(INSTALL_DIR)/$(BINARY)
	install -Dm755 bin/$(PASSWD_BINARY) $(INSTALL_DIR)/$(PASSWD_BINARY)

install-openrc:
	install -Dm755 init/filex.openrc /etc/init.d/filex
	install -d -m 0755 /var/log/filex

clean:
	rm -rf bin/

docker:
	docker build --build-arg VERSION=$(VERSION) -t filex:latest .

lint:
	go vet ./...

# Generate a sample config (no-auth mode). An existing target file is backed
# up to <path>.bak.<timestamp> before it is overwritten.
sample-config: build
	./bin/$(BINARY) -sample-config config.sample.yaml

# Generate a sample config with a login-required user jail. The bundled
# filex-passwd tool must be used to set a real password_hash before running.
# An existing target file is backed up before it is overwritten.
sample-config-auth: build
	./bin/$(BINARY) -sample-config-auth config.sample.yaml

# Regenerate api/openapi.yaml from the route catalog in internal/handler
# (see tools/gen-openapi). Always run this after adding/changing an endpoint.
openapi:
	go run ./tools/gen-openapi

# Verify the committed api/openapi.yaml is in sync with the route catalog.
openapi-check:
	@tmp=$$(mktemp); \
	go run ./tools/gen-openapi -out "$$tmp" >/dev/null && cmp -s "$$tmp" api/openapi.yaml; \
	rc=$$?; rm -f "$$tmp"; \
	if [ $$rc -ne 0 ]; then \
		echo "api/openapi.yaml is stale - run 'make openapi'"; \
		exit 1; \
	fi; \
	echo "api/openapi.yaml is up to date"
