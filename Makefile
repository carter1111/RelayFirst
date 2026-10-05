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

.PHONY: help build test vet fmt race dev-node dev-dashboard release docker clean

help:
	@echo "make build         — CGO_ENABLED=0 go build ./..."
	@echo "make test          — the full CI gate (scripts/ci.sh)"
	@echo "make vet           — go vet ./..."
	@echo "make fmt           — gofmt the tree"
	@echo "make race          — go test -race ./..."
	@echo "make dev-node      — run a local node (defaults: :8080, ./relayfirst-node.db)"
	@echo "make dev-dashboard — watch the local node (defaults: http://localhost:8080)"
	@echo "make release       — RELEASE build (arms the genesis guard; refuses if provisional)"
	@echo "make docker        — build the node image"

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
#
# The flags are omitted on purpose: the node's defaults are :8080 and
# ./relayfirst-node.db, so `relayfirst-node` alone is a complete command. Passing
# them again would suggest the defaults are not the defaults.
dev-node:
	$(GO) run ./cmd/relayfirst-node

# Watch that node, in another terminal. Override the address with
# URL=... if the node is elsewhere, e.g. `make dev-dashboard URL=http://host:9000`.
dev-dashboard:
	$(GO) run ./cmd/relayfirst-dashboard $(if $(URL),--url $(URL),)

# The release path. See scripts/build-release.sh for why the tag matters and why
# it smoke-runs the binary before handing it over.
release:
	bash scripts/build-release.sh

docker:
	docker build -t relayfirst/node:latest .

clean:
	rm -rf bin/release
