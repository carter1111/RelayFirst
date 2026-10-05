// npm launcher for the RelayFirst CLI (S6-7, MVP.md §9.2).
//
// # Why this is a launcher and not a reimplementation
//
// MVP.md §9.2 picks `npx` as the entry point because it is zero-install and is the
// path crypto users already know. But the CLI itself is Go, and reimplementing
// signing in JavaScript would duplicate the EIP-712 encoder — which is exactly the
// cross-language drift that the KAT corpus exists to catch (invariant A4) and that
// nobody should be maintaining twice.
//
// So this file does one thing: select a prebuilt binary for the user's platform and
// hand it the arguments. Delegating means there is one implementation of the
// protocol, and the KAT corpus remains the only thing that has to stay in sync.
//
// # Where the platform binary comes from
//
// The tarball ships one binary per supported platform (bin/npm/<name>-<os>-<arch>),
// built by scripts/build-npm-binaries.sh at publish time. Selection is by
// process.platform / process.arch, so a user with no Go toolchain runs a real
// binary.
//
// # Why it still falls back to `go` when no binary matches
//
// The fallback is last, not first. It used to be the ONLY path (bin/ was gitignored,
// so nothing shipped), which made "zero-install" silently require a toolchain. Now a
// missing platform binary is a clear message naming the platform, and the Go build
// is an explicit secondary path for a source checkout.
//
// Usage:
//   npx relayfirst mine --source https://example.com
//   node scripts/npx-relayfirst.mjs --help

import { existsSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "..");

const NAME = "relayfirst";
const EXE = process.platform === "win32" ? ".exe" : "";
// process.arch is "x64" on Intel/AMD and "arm64" on Apple silicon and ARM servers;
// Go's GOARCH names differ only for x64 -> amd64.
const GOARCH = process.arch === "x64" ? "amd64" : process.arch;
const platformBinary = join(repoRoot, "bin", "npm", `${NAME}-${process.platform}-${GOARCH}${EXE}`);

// A source checkout may have bin/<name> from `go build -o bin/<name>`; prefer the
// platform-specific tarball binary, then that.
const candidates = [
  platformBinary,
  join(repoRoot, "bin", NAME),
  join(repoRoot, NAME),
];

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
  const args = process.argv.slice(2);

  let binary = findPrebuilt();
  if (!binary) {
    if (!haveGo()) {
      // Name the platform so the message is actionable. A silent download failure
      // would be indistinguishable from a protocol error, so there is no download.
      console.error(
        [
          `${NAME}: no binary for this platform (${process.platform}/${GOARCH}) and the Go toolchain is not available.`,
          "",
          "Either:",
          `  - install Go 1.27+ (https://go.dev/dl/) and re-run, or`,
          `  - build the binary yourself: go build -o bin/${NAME} ./cmd/${NAME}`,
          "",
          "A node operator does not need this launcher:",
          "  docker run -d -p 8080:8080 relayfirst/node:latest",
        ].join("\n"),
      );
      process.exit(1);
    }
    binary = buildBinary();
  }

  const run = spawnSync(binary, args, { cwd: process.cwd(), stdio: "inherit" });
  process.exit(run.status ?? 1);
}

main();
