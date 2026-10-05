# RelayFirst — build entry points.
#
# # Why a Makefile at all, when the project is standard-library-only
#
# Not for the build system: `go build ./...` needs no help. It is for the two
# entry points that are NOT obvious and that acceptance criteria depend on:
#
#   make test     — the full gate (scripts/ci.sh)
#   make release  — a build that carries the `mainnet` tag, so the genesis guard
#                   in internal/epoch is armed and a placeholder cannot ship
#
# Getting `release` wrong by hand (a plain `go build`, no tag) would silently skip
# the guard. A named target is harder to get wrong than a remembered flag.
#
# CGO_ENABLED=0 everywhere: invariant A3, a static single binary.

GO ?= go
CGO_ENABLED ?= 0
export CGO_ENABLED

.PHONY: help build test vet fmt race dev-node release docker clean

help:
	@echo "make build    — CGO_ENABLED=0 go build ./..."
	@echo "make test     — the full CI gate (scripts/ci.sh)"
	@echo "make vet      — go vet ./..."
	@echo "make fmt      — gofmt the tree"
	@echo "make race     — go test -race ./..."
	@echo "make dev-node — run a local node on :8080"
	@echo "make release  — RELEASE build (arms the genesis guard; refuses if provisional)"
	@echo "make docker   — build the node image"

build:
	$(GO) build ./...

# The gate. It is the real one (scripts/ci.sh), not a subset: a `make test` that
# ran less than CI would let a local pass diverge from a CI failure.
test:
	bash scripts/ci.sh

vet:
	$(GO) vet ./...

fmt:
	gofmt -w cmd internal

race:
	$(GO) test -race ./...

# The documented 10-minute path (criterion ③): one command starts a node.
dev-node:
	$(GO) run ./cmd/relayfirst-node --listen :8080 --storage ./relayfirst-node.db

# The release path. See scripts/build-release.sh for why the tag matters and why
# it smoke-runs the binary before handing it over.
release:
	bash scripts/build-release.sh

docker:
	docker build -t relayfirst/node:latest .

clean:
	rm -rf bin/release
