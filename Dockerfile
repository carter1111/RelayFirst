# RelayFirst thin node — one `docker run` starts a node (S5-8, MVP.md §7.2).
#
# The acceptance criterion for S5 is that a stranger can run a node in ten
# minutes. Two decisions follow from that and are not negotiable here:
#
#   * CGO_ENABLED=0. Invariant A3 requires a static binary, so the final image can
#     be built from `scratch`-like bases with no glibc. A CGO build would tie the
#     image to a libc version and make "it works on my machine" a real risk.
#   * The node does NOT need the miner. Building only ./cmd/relayfirst-node keeps
#     the LLM providers and their credential handling out of a public server image
#     entirely — less surface, and nothing that could ever log a key.
#
# Build:
#   docker build -t relayfirst/node:latest .
# Run (MVP.md §7.2):
#   docker run -d --name relayfirst-node -p 8080:8080 \
#     -v ./relayfirst-data:/data relayfirst/node:latest \
#     --public-url https://relay.myagent.xyz

# ---------------------------------------------------------------- build stage
# The version must satisfy go.mod's `go` directive. Pinning an older toolchain here
# fails the build with "go.mod requires go >= ...", which is a confusing way to
# discover a version bump.
FROM golang:1.27-alpine AS build

WORKDIR /src

# Copy the module files first so the dependency layer is cached independently of
# the source. A source-only change then does not re-download the module graph.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 is the whole point (invariant A3). -trimpath keeps build paths out
# of the binary, so a stack trace cannot leak the builder's filesystem layout.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/relayfirst-node \
      ./cmd/relayfirst-node

# ---------------------------------------------------------------- final stage
# alpine rather than scratch: it provides a shell and CA certificates, which make
# the image debuggable and let an operator exec in to inspect the volume. The
# saving from scratch is a few MB and is not worth losing that.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget && \
    adduser -D -H -u 10001 relayfirst

# /data is the volume the operator mounts. Creating it in the image means the
# documented `docker run` works without a prior mkdir.
RUN mkdir -p /data && chown relayfirst:relayfirst /data
VOLUME ["/data"]

# Run unprivileged. A public HTTP endpoint has no business being root, and the
# node only ever writes to its own database.
USER relayfirst

WORKDIR /data

COPY --from=build /out/relayfirst-node /usr/local/bin/relayfirst-node

EXPOSE 8080

# Container-level liveness. It uses the node's own /healthz rather than a TCP
# probe, so it fails when the handler is broken rather than merely when the port
# is closed.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1

# Storage defaults to /data so the documented volume mount persists by default.
ENTRYPOINT ["relayfirst-node"]
CMD ["--listen", ":8080", "--storage", "/data/relayfirst-node.db"]
