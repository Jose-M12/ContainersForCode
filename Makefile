GO ?= go
GOCACHE ?= /tmp/cagent-go-build-cache
GOMODCACHE ?= /tmp/cagent-go-module-cache
VERSION ?= 0.1.0-alpha.1
COMMIT ?= development
BUILD_DATE ?= unknown
APP_COVERAGE_MIN ?= 10.0
PODMAN_COVERAGE_MIN ?= 70.0
# Source archives and some CI workspaces intentionally do not contain usable Git
# metadata. Disable automatic VCS stamping; version information is supplied by
# the explicit linker flags below.
GOFLAGS := -trimpath -buildvcs=false
export GOFLAGS
LDFLAGS := -s -w -X containersagents.dev/v2/internal/app.Version=$(VERSION) -X containersagents.dev/v2/internal/app.Commit=$(COMMIT) -X containersagents.dev/v2/internal/app.Date=$(BUILD_DATE)

.PHONY: all build test vet coverage check integration clean

all: check build

build:
	mkdir -p bin
	env GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) build -ldflags '$(LDFLAGS)' -o bin/cagent ./cmd/cagent

test:
	env GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) test ./...

vet:
	env GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) vet ./...

coverage:
	mkdir -p coverage
	env GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) test -coverprofile=coverage/app.out ./internal/app
	env GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) test -coverprofile=coverage/podman.out ./internal/podman
	@value=`env GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) tool cover -func=coverage/app.out | awk '/^total:/ {gsub("%", "", $$3); print $$3}'`; \
		awk -v value="$$value" -v minimum="$(APP_COVERAGE_MIN)" 'BEGIN { if (value + 0 < minimum + 0) { printf "internal/app coverage %.1f%% is below %.1f%%\n", value, minimum; exit 1 } }'
	@value=`env GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) tool cover -func=coverage/podman.out | awk '/^total:/ {gsub("%", "", $$3); print $$3}'`; \
		awk -v value="$$value" -v minimum="$(PODMAN_COVERAGE_MIN)" 'BEGIN { if (value + 0 < minimum + 0) { printf "internal/podman coverage %.1f%% is below %.1f%%\n", value, minimum; exit 1 } }'

check: test vet
	jq empty schemas/*.json profiles/builtin/*/profile.json examples/**/*.json

integration: build
	CAGENT_RUN_PODMAN_INTEGRATION=1 env GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) test -tags=integration ./tests/integration -v

clean:
	rm -rf bin coverage coverage.out
