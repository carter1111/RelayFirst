#!/usr/bin/env bash
#
# build-npm-binaries.sh — cross-compile the npm-shipped binaries.
#
# # Why this exists
#
# The npm launchers (scripts/npx-relayfirst.mjs, npx-relayfirst-mcp.mjs) are
# launchers, not implementations: they hand arguments to a Go binary. For a plain
# `npx relayfirst` or an IDE MCP config to work on a machine without the Go
# toolchain, the tarball must CONTAIN a binary for the user's platform.
#
# It did not: `bin/` is gitignored, so a clean checkout published no binary and the
# launcher silently fell back to `go build`. That is a "zero-install" story that
# needs a toolchain installed, which is the opposite of zero-install.
#
# This runs from `prepublishOnly`, so the binaries are built at publish time (when
# the publisher has Go) and shipped in the tarball (where the user may not).
#
# Output: bin/npm/<name>-<os>-<arch>[.exe]
# The launchers select by process.platform / process.arch at run time.
#
# CGO_ENABLED=0 is invariant A3; it is also what makes cross-compilation trivial.
#
set -euo pipefail

cd "$(dirname "$0")/.."

out="bin/npm"
mkdir -p "$out"

# The platforms worth shipping. Windows is included because `npx` is used there too;
# the launcher appends `.exe`.
targets=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
)

# relayfirst-node is deliberately NOT built here: it is a long-lived server with a
# persistent volume, which npm is a poor fit for. Its distribution is the container
# image. The on-demand user tools ship to npm: the miner CLI, the MCP server, and the
# node's read-only dashboard (which is a client, so it is on-demand like the others).
commands=(relayfirst relayfirst-mcp relayfirst-dashboard)

export CGO_ENABLED=0
built=0

for target in "${targets[@]}"; do
  os="${target%/*}"
  arch="${target#*/}"
  ext=""
  [ "$os" = "windows" ] && ext=".exe"

  for cmd in "${commands[@]}"; do
    outfile="$out/$cmd-$os-$arch$ext"
    GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags="-s -w" -o "$outfile" "./cmd/$cmd"
    built=$((built + 1))
  done
done

echo "built $built npm binaries into $out"
ls -1 "$out"
