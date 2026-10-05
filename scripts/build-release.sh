#!/usr/bin/env bash
#
# build-release.sh — produce a RELAYFIRST RELEASE build, or refuse to.
#
# # Why this exists
#
# A release build differs from a development one in exactly one way that matters
# here: it carries the `mainnet` build tag, which arms the genesis guard in
# internal/epoch. Without this script nothing built with that tag, so the guard
# could never fire — the guard was decorative, and a release assembled by hand
# with a plain `go build` would have bypassed it.
#
# # The genesis is inside a receipt's signed payload
#
# So a release shipped with the development placeholder would sign receipts under
# a genesis nobody agreed to, and those receipts cannot be re-signed afterwards.
# This script therefore does not merely BUILD with the tag; it RUNS the result and
# refuses to hand over a binary that carries the placeholder.
#
# Once the launch date is pinned (see docs/notes/blk-4-genesis-decision.md), the
# smoke run succeeds and this script produces the release binaries.
#
# Usage:
#   scripts/build-release.sh [output-dir]     # default: bin/release
#
set -euo pipefail

cd "$(dirname "$0")/.."

out="${1:-bin/release}"
mkdir -p "$out"

# -trimpath keeps build paths out of the binary (a stack trace should not leak the
# builder's layout). -s -w strips symbols. CGO_ENABLED=0 is invariant A3.
export CGO_ENABLED=0
ldflags="-s -w"
tags="mainnet"

echo "==> building release binaries (-tags $tags) into $out"

# The node and MCP server do not read the epoch clock, so the tag is harmless to
# them; building them here keeps one place that names every released binary.
for cmd in relayfirst relayfirst-node relayfirst-verifier relayfirst-mcp; do
  go build -trimpath -ldflags="$ldflags" -tags "$tags" -o "$out/$cmd" "./cmd/$cmd"
  echo "    built $out/$cmd"
done

# The smoke run is the point. `relayfirst version` reaches internal/epoch's init()
# before main, so a provisional genesis makes it panic; a pinned one makes it print
# and exit 0. Cross-compiled artifacts cannot be run here, so this check is only
# meaningful for a host build — which is what the script produces by default.
echo "==> smoke-running $out/relayfirst to confirm the genesis guard passes"
if ! "$out/relayfirst" version >/dev/null 2>&1; then
  reason="$("$out/relayfirst" version 2>&1 || true)"
  {
    echo
    echo "REFUSING TO RELEASE: the built binary will not start."
    echo "  $reason"
    echo
    echo "This is the genesis guard (internal/epoch). Pin GenesisValue to the public"
    echo "launch date (00:00 UTC) before releasing; see docs/notes/blk-4-genesis-decision.md."
  } >&2
  exit 1
fi

echo
echo "release artifacts in $out:"
ls -1 "$out"
