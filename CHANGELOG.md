# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses semantic versioning.

---

## [Unreleased]

### Added

- S13.3d: `relayfirst session grant` / `session verify` — Session Delegation 闭环（owner 签发 + consumer 验证）
- S13.3c: `relayself session open/close/show` — 事件生产者命令
- S13.5: E2EE 端到端测试（加密回执仍可离线验签，relay 只见密文）
- S10-0: 节点身份 + verifier 二进制分离（ADR-0004）
- UX-1: `relayfirst-dashboard` v0（独立只读 TUI）
- PKG-4: dashboard 纳入 npm 打包

### Fixed

- node binary missing Tasks store (main.go) — now starts with all stores
- ADR-3: CLI framework手写修正选型表（cobra → stdlib）

---

## [v0.1.0] — YYYY-MM-DD (TBD — pending BLK-4 genesis date)

### Added

- **S1:** Receipt struct + EIP-712 signature + KAT gate + offline verifier CLI
- **S2:** Mining loop (probe/extract/compute executors + generator + anchor)
- **S3:** Global dedup ledger + scoring formula + epoch emission
- **S4:** Adversarial verification (recompute + evaluator modes + commitment)
- **S5:** Thin relay node (HTTP + SQLite + Docker + multi-relay fanout)
- **S6:** CLI UX (init/config/mine/status/receipts export)
- **S7:** On-chain anchoring (Merkle tree + RelayAnchor.sol)
- **S8:** Red team — four forgery attacks all return 0 points
- **S9-0:** Versioned foundation (A9) — cross-version matrix, frozen corpus, import-graph gates
- **S9-1..S9-12:** Full A2A protocol layer (card/session/task/message/event + WebSocket binding)
- **S10:** Node indexing + query + task forwarding + attributable verification
- **S11:** SBT integration (ERC-721 + ERC-5192, transfer interception, Merkle claim)
- **S12:** Settlement bounty registry + USDC minimal channel + MCP server
- **S13:** End-to-end encryption (X25519 + XChaCha20-Poly1305) + key hierarchy + delegation

### Engineered

- CI: 15 gates (build/vet/format/test/race/KAT/A5/frozen-corpus/benchmark/version-matrix/import-graph/merkle/solidity/npm/release-guard)
- Go 1.27.1, CGO_ENABLED=0 static binaries across 5 platforms
- Cross-language KAT (Go ↔ viem TS) byte-for-byte parity enforced
- Import-graph separation gates verified at build time

---

[unreleased]: https://github.com/<org>/relayfirst/compare/main...HEAD
[v0.1.0]: TBD
