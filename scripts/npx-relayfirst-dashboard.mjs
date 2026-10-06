// npm launcher for the RelayFirst node dashboard (UX-1).
//
// # Why this is a launcher and not an implementation
//
// The dashboard is Go (bubbletea), and reimplementing it in JavaScript would be a
// second terminal UI to keep in step for no gain. So this finds a prebuilt binary for
// the platform and hands it the arguments, exactly like the CLI and MCP launchers.
//
// # Where the platform binary comes from
//
// scripts/build-npm-binaries.sh cross-compiles it into bin/npm/<name>-<os>-<arch> at
// publish time, so `npx relayfirst-dashboard` works on a machine without the Go
// toolchain.
//
// # Usage
//
//   relayfirst-node                 # in one terminal
//   npx relayfirst-dashboard        # in another; defaults to http://localhost:8080
//   RELAYFIRST_RELAY=https://node npx relayfirst-dashboard

import { existsSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";
import { spawnSync } from "node:child_process";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "..");
const require = createRequire(import.meta.url);

const NAME = "relayfirst-dashboard";
const EXE = process.platform === "win32" ? ".exe" : "";
const GOARCH = process.arch === "x64" ? "amd64" : process.arch;
const platformBinary = join(repoRoot, "bin", "npm", `${NAME}-${process.platform}-${GOARCH}${EXE}`);

// The per-platform npm sub-package (PKG-1); its name uses npm's own platform
// spelling, so no Go<->npm mapping is needed here.
const subPackage = findSubPackage();

function findSubPackage() {
  const rel = `@relayfirst/${process.platform}-${process.arch}/package.json`;
  try {
    const manifest = require.resolve(rel);
    return join(dirname(manifest), `${NAME}${EXE}`);
  } catch {
    return null;
  }
}

const candidates = [
  platformBinary,
  subPackage,
  join(repoRoot, "bin", NAME),
  join(repoRoot, NAME),
].filter(Boolean);

function findPrebuilt() {
  for (const p of candidates) {
    if (existsSync(p) && statSync(p).isFile()) return p;
  }
  return null;
}

function haveGo() {
  const probe = spawnSync("go", ["version"], { stdio: "ignore" });
  return probe.status === 0;
}

function buildBinary() {
  const out = join(repoRoot, "bin", NAME);
  const built = spawnSync("go", ["build", "-o", out, `./cmd/${NAME}`], {
    cwd: repoRoot,
    stdio: "inherit",
  });
  if (built.status !== 0) {
    console.error(`${NAME}: go build failed`);
    process.exit(built.status ?? 1);
  }
  return out;
}

function main() {
  let binary = findPrebuilt();
  if (!binary) {
    if (!haveGo()) {
      console.error(
        [
          `${NAME}: no binary for this platform (${process.platform}/${GOARCH}) and the Go toolchain is not available.`,
          "",
          "Either:",
          `  - install Go 1.27+ (https://go.dev/dl/), or`,
          `  - build the binary yourself: go build -o bin/${NAME} ./cmd/${NAME}`,
        ].join("\n"),
      );
      process.exit(1);
    }
    binary = buildBinary();
  }

  // stdio is inherited so the TUI gets the real terminal. A launcher that read the
  // terminal itself could not render the dashboard.
  const run = spawnSync(binary, process.argv.slice(2), {
    cwd: process.cwd(),
    stdio: "inherit",
    env: process.env,
  });
  process.exit(run.status ?? 1);
}

main();
