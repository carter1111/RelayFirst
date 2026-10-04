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
// So this file does one thing: find or build the Go binary and hand it the
// arguments. Delegating means there is one implementation of the protocol, and the
// KAT corpus remains the only thing that has to stay in sync across languages.
//
// # Why it fails loudly when Go is missing
//
// It could fall back to a prebuilt binary download, and that would be a worse
// experience to debug: a silent download failure looks identical to a protocol
// error. A first run that needs the toolchain is a clear, fixable message; a
// mystery failure later is not.
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

// Prefer a prebuilt binary when one is present, so an installed copy does not need
// the toolchain.
const candidates = [
  join(repoRoot, "bin", "relayfirst"),
  join(repoRoot, "relayfirst"),
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
  const out = join(repoRoot, "bin", "relayfirst");
  const built = spawnSync(
    "go",
    ["build", "-o", out, "./cmd/relayfirst"],
    { cwd: repoRoot, stdio: "inherit" },
  );
  if (built.status !== 0) {
    console.error("relayfirst: go build failed");
    process.exit(built.status ?? 1);
  }
  return out;
}

function main() {
  const args = process.argv.slice(2);

  let binary = findPrebuilt();
  if (!binary) {
    if (!haveGo()) {
      console.error(
        [
          "relayfirst: no prebuilt binary found and the Go toolchain is not available.",
          "",
          "Either:",
          "  - install Go 1.27+ (https://go.dev/dl/), or",
          "  - build the binary yourself: go build -o bin/relayfirst ./cmd/relayfirst",
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
