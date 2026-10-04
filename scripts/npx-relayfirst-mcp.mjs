// npm launcher for the RelayFirst MCP server (S13-4, MVP.md §9.2).
//
// # Why this is a launcher and not an implementation
//
// MCP is a protocol, and an agent-facing server is where the ecosystem is most tempted to write a
// second implementation "just for the tools". Doing that would put a second copy of the relay's
// semantics in TypeScript, and the two would drift the way the EIP-712 encoder would have (invariant
// A4). So this file does one thing: find or build the Go binary and hand it stdio.
//
// # Why stdio and not HTTP
//
// MCP over stdio is what an IDE launches from a config block, which is the whole distribution story
// for this server: one line, no port, no daemon. An HTTP variant would need a port, a lifecycle, and
// a way for the agent to discover it — all of which the stdio transport gets for free.
//
// # The security property the binary carries
//
// `relayfirst-mcp` links no signing code, so it cannot sign anything even if a prompt injection asks
// it to. That is enforced by the CI import-graph gate, not by this launcher and not by convention. See
// cmd/relayfirst-mcp/main.go.
//
// Usage:
//   npx relayfirst-mcp                 # talks to http://localhost:8080
//   RELAYFIRST_RELAY=https://node npx relayfirst-mcp

import { existsSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "..");

const candidates = [
  join(repoRoot, "bin", "relayfirst-mcp"),
  join(repoRoot, "relayfirst-mcp"),
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
  const out = join(repoRoot, "bin", "relayfirst-mcp");
  const built = spawnSync("go", ["build", "-o", out, "./cmd/relayfirst-mcp"], {
    cwd: repoRoot,
    stdio: "inherit",
  });
  if (built.status !== 0) {
    console.error("relayfirst-mcp: go build failed");
    process.exit(built.status ?? 1);
  }
  return out;
}

function main() {
  let binary = findPrebuilt();
  if (!binary) {
    if (!haveGo()) {
      // Failing loudly rather than downloading a binary: a silent download failure is
      // indistinguishable from a protocol error, and an MCP server that exits mysteriously is
      // exactly the debugging experience this project avoids elsewhere.
      console.error(
        [
          "relayfirst-mcp: no prebuilt binary found and the Go toolchain is not available.",
          "",
          "Either:",
          "  - install Go 1.27+ (https://go.dev/dl/), or",
          "  - build the binary yourself: go build -o bin/relayfirst-mcp ./cmd/relayfirst-mcp",
        ].join("\n"),
      );
      process.exit(1);
    }
    binary = buildBinary();
  }

  // stdio is inherited so the MCP client speaks to the Go process directly. Nothing is parsed here:
  // a launcher that interpreted the protocol would be a second implementation of it.
  const run = spawnSync(binary, process.argv.slice(2), {
    cwd: process.cwd(),
    stdio: "inherit",
    env: process.env,
  });
  process.exit(run.status ?? 1);
}

main();