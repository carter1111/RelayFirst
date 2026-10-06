#!/usr/bin/env bash
#
# pack-platform-packages.sh — build the per-platform npm sub-packages (PKG-1).
#
# # Why per-platform packages
#
# The main package shipped every platform's binaries (~42MB, 15 of them) so every
# user downloaded all of them to run one. npm can install only the matching one when
# the dependency declares os/cpu, so this script produces one sub-package per
# platform, each carrying three binaries and the os/cpu fields npm filters on.
#
# # The platform-name mapping, which is the part that goes wrong
#
# Go and npm name platforms differently, and the difference is silent:
#
#   Go        npm       note
#   windows   win32     Go says windows, Node says win32
#   amd64     x64       Go says amd64, Node says x64
#   darwin    darwin    same
#   linux     linux     same
#   arm64     arm64     same
#
# The sub-package NAME uses npm's spelling (so the launcher is just
# @relayfirst/<process.platform>-<process.arch>, with no mapping of its own), and its
# os/cpu fields use the same. Go's spelling appears only here, in the input target.
# The mapping is in ONE function (npm_os / npm_cpu) and CI checks the result.
#
# # Usage
#
#   bash scripts/build-npm-binaries.sh      # first: fills bin/npm/
#   bash scripts/pack-platform-packages.sh  # then: fills packages/
#
# Run from the repository root.
set -euo pipefail

cd "$(dirname "$0")/.."

src="bin/npm"
out="packages"
mkdir -p "$out"

if [ ! -d "$src" ]; then
  echo "error: $src is missing — run scripts/build-npm-binaries.sh first" >&2
  exit 1
fi

# version is read from the main package so every sub-package matches it exactly:
# npm's optionalDependencies pin a version, and a mismatch means npx resolves nothing.
version="$(node -p "require('./package.json').version")"
license="$(node -p "require('./package.json').license || 'UNLICENSED'")"

commands=(relayfirst relayfirst-mcp relayfirst-dashboard)

npm_os() {
  case "$1" in
    windows) echo win32 ;;
    *) echo "$1" ;;
  esac
}

npm_cpu() {
  case "$1" in
    amd64) echo x64 ;;
    *) echo "$1" ;;
  esac
}

targets=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
)

names=()

for target in "${targets[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  os="$(npm_os "$goos")"
  cpu="$(npm_cpu "$goarch")"
  pkg="$out/$os-$cpu"
  mkdir -p "$pkg"

  ext=""
  [ "$goos" = "windows" ] && ext=".exe"

  # Copy each tool under its plain name, so the launcher only needs the package
  # directory plus the binary name — no Go spelling in the launcher.
  for cmd in "${commands[@]}"; do
    from="$src/$cmd-$goos-$goarch$ext"
    if [ ! -f "$from" ]; then
      echo "error: $from is missing — build the npm binaries first" >&2
      exit 1
    fi
    cp "$from" "$pkg/$cmd$ext"
    chmod +x "$pkg/$cmd$ext"
  done

  # PKG-2: the macOS binaries are cross-compiled and unsigned, and Gatekeeper can
  # refuse them. Saying so in the package is honest; silently shipping an unsigned
  # binary that a user cannot run is not.
  if [ "$goos" = "darwin" ]; then
    cat >"$pkg/UNSIGNED.txt" <<'NOTE'
These macOS binaries are cross-compiled and NOT code-signed or notarized.
Gatekeeper may refuse to run them. To run anyway:
  xattr -d com.apple.quarantine ./relayfirst
Signing requires an Apple Developer certificate (tracked as PKG-2).
NOTE
  fi

  # No "exports" field in the sub-package: the launcher resolves the package
  # directory by requiring its package.json, which an exports map would forbid.
  cat >"$pkg/package.json" <<JSON
{
  "name": "@relayfirst/$os-$cpu",
  "version": "$version",
  "description": "RelayFirst platform binaries for $os/$cpu",
  "os": ["$os"],
  "cpu": ["$cpu"],
  "files": ["relayfirst", "relayfirst-mcp", "relayfirst-dashboard", "UNSIGNED.txt"],
  "license": "$license"
}
JSON

  names+=("@relayfirst/$os-$cpu@$version")
  echo "  packed $pkg"
done

echo
echo "sub-packages (publish these BEFORE the main package):"
printf '  %s\n' "${names[@]}"
