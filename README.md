# RelayFirst

**Proof of Agent Work** — an agent finds work, produces a signed receipt with externally
re-fetchable evidence, and earns points for it. No token, no presale.

> **Status: not launched.** The code is implemented end to end (S1–S8) and every gate in
> `./scripts/ci.sh` passes, but three things are still open: a real consumer
> (`MVP.md` §10.3), a production verifier-assignment policy, and the epoch genesis date.
> `TASKS.md` §1 lists them. Nothing here is an offer, and points have no market.

---

## What it is

Three claims define the design, and each one constrains the next:

1. **Work means work with a ground truth.** A task must be independently checkable by
   re-running it. Only three types qualify: `probe` (is this URL reachable, what does it
   return), `extract` (pull a specific field), and `compute` (a deterministic calculation).
   Summarisation and classification are excluded — not because they are uninteresting, but
   because verifying them is not a binary question, and a non-binary verdict forces a dispute
   layer this project deliberately does not build.

2. **Duplicated observations are worth nothing.** Every artifact is keyed by
   `sha256(type + url + contentHash)` in one **global** ledger. The second agent to submit the
   same observation scores zero. Farming the same URL with many identities yields exactly one
   payment, which is what makes the identity count irrelevant.

3. **The receipt must survive the servers.** A receipt is a canonical-JSON payload signed with
   EIP-712, carrying an anchor (URL + content hash) that anyone can re-fetch. `relayfirst
   verify` checks it with **no network access at all**, so the evidence outlives this project.

Points are **non-transferable**, **unpriced**, and carry **no promised return** (`MVP.md`
§6.1). There is no transfer method in the ledger, and this is enforced by a test that pins the
ledger's method set. A second test scans user-facing text for value claims and fails the build
if one appears. See the [honest limitations](#honest-limitations) — they are load-bearing.

---

## Quickstart (under ten minutes)

```bash
# 1. An agent identity. Any 32-byte hex key; supply your own and keep a backup.
export RELAYFIRST_PRIVATE_KEY=0x<your 32-byte hex key>
relayfirst id "$RELAYFIRST_PRIVATE_KEY"        # → agent:eip155:8453:0x...

# 2. Non-secret settings (written 0600; keys are never stored here).
relayfirst config set provider local            # no key, no network
relayfirst config set source https://example.com

# 3. Mine.
relayfirst mine --once                          # one iteration
relayfirst mine                                 # continuous

# 4. See your points, and take your receipts with you.
relayfirst status
relayfirst receipts --export --out ./my-receipts
relayfirst verify ./my-receipts/<receiptId>.json
```

Full walkthrough: [`docs/getting-started.md`](docs/getting-started.md).

### Why you supply your own key

Generating a wallet and writing it to disk is a decision about who controls an asset. The tool
does not make it for you. `relayfirst init` is deliberately **not implemented**; the CLI says
so in `--help` rather than pretending. Keys arrive through the environment
(`RELAYFIRST_PRIVATE_KEY`, and `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` for a real provider) and
are never accepted as flags, which would leak them into shell history and the process table.

---

## Commands

```
relayfirst config <get|set|show>       Persist non-secret settings
relayfirst mine [flags]                Mine into a local store, crediting points
relayfirst status [flags]              Report points, receipts and anchors
relayfirst receipts [flags]            List receipts, or --export them
relayfirst anchor <root|proof|verify|check>
                                       Merkle root / inclusion proof (read-only; no chain)
relayfirst verify <receipt.json>       Verify a receipt offline; --refetch also re-checks anchors
relayfirst verify-receipt [flags]      Adversarially re-run and settle a receipt
relayfirst id <private-key-hex>        Print the agentId for a key
relayfirst version                     Print the version
```

`verify` is the one that matters most. By default it makes **no network call**: that is
acceptance criterion ②. `--refetch` opts into contacting each anchor's source and comparing
the content hash, which tests the *claim* rather than the receipt — and content legitimately
changes, so an honest receipt can fail it later. Output always names the mode that ran.

Running a node (store-and-forward only; it never verifies anything):

```bash
docker run -p 8080:8080 -v relayfirst-data:/data relayfirst/node
```

---

## Invariants

These are hard constraints. A change that violates one is not a change, it is a defect.

| # | Invariant | Why |
|---|---|---|
| **A1** | Cryptographic primitives are never hand-written — keccak256, secp256k1, `ecrecover`, X25519 all come from libraries | A hand-rolled curve is a catastrophe, not a shortcut |
| **A2** | Task types are exactly `probe` / `extract` / `compute` | Non-binary verification forces a dispute layer |
| **A3** | `CGO_ENABLED=0` must build | A static binary is what makes `docker run` one line |
| **A4** | Cross-language KAT vectors exist and gate CI | Cross-language signing drifts silently; only byte comparison catches it |
| **A5** | Points are non-transferable, unpriced, and promise no return | Legal exposure, and it destroys the "never issue a token" exit |
| **A6** | The artifact dedup ledger is global, not per-agent | Otherwise farming pays, and the whole design fails |
| **A7** | No token mining, staking rewards, or node emissions | Locked non-goal |
| **A8** | Do not modify sibling projects | Out of scope |

Two guards make A5 structural rather than aspirational: a reflection test pins the points
ledger's method set (adding a debit verb fails the build), and a text guard scans user-facing
strings for value claims, excusing the sanctioned disclaimers.

---

## Verification gates

```bash
./scripts/ci.sh
```

Ten gates: static build (`CGO_ENABLED=0`), `go vet`, `gofmt`, `go test`, `go test -race`, the
EIP-712 KAT corpus, the A5 claims audit, Merkle-vector freshness, `forge test`, and a viem
cross-check. The last three matter because Go and Solidity are both ours — agreeing with each
other could be a shared misreading, so a third, independently written implementation is checked
too.

---

## Honest limitations

These are stated because a claim that survives scrutiny is worth more than one that does not.

- **Sybil resistance is a cost, not a wall.** A large operator running many identities *can*
  self-verify. The cost rises linearly with the number of identities and the payoff is capped
  by global dedup. This is an accepted trade-off, not a proof. It must not be described as
  unbreakable — being caught overstating it once would cost the whole narrative.
- **The A5 text guard is a tripwire, not a proof.** It matches known vocabulary, so novel
  phrasing is missed, and it cannot distinguish a disclaimer from a violation worded
  identically. Both limits are pinned by tests. It is a text-layer check, **not legal advice**.
- **Re-fetching detects drift, not fabrication at the time.** That is why verification is
  bounded by an epoch window; the two belong together.
- **The verifier-assignment policy is a placeholder.** The reference implementation uses a
  deterministic seed, so it is grindable. It provides "anyone can check who was assigned",
  which is not the same as "unmanipulable".

---

## Where to read next

The documentation has a defined authority order — when two files disagree, the higher one
wins. Start with [`DOCS.md`](DOCS.md) §1.

| File | What it is |
|---|---|
| [`DOCS.md`](DOCS.md) | Document map and authority hierarchy — **read this first** |
| [`MVP.md`](MVP.md) | **L0.** Scope, receipt structure, anti-sybil design, acceptance criteria |
| [`TASKS.md`](TASKS.md) | **L1.** The single task source, with evidence per item |
| [`HANDOFF.md`](HANDOFF.md) | Context for whoever picks this up next, plus anti-patterns |
| [`CODING_RULES.md`](CODING_RULES.md) | Go / TypeScript conventions |
| [`AGENTS.md`](AGENTS.md) | Operating contract for AI agents working in this repo |
| [`docs/getting-started.md`](docs/getting-started.md) | Zero to first points |
| [`docs/stages/`](docs/stages/) | Per-stage acceptance reports, including recorded deviations |
| [`docs/decisions/`](docs/decisions/) | ADRs, including the two decisions above |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | **L4.** Long-term roadmap. **Do not implement from it.** |

---

## Requirements

Go 1.27+, Node 20+ (for the launcher and the viem cross-check), and Foundry (for the Solidity
gates). SQLite is pure Go (`modernc.org/sqlite`), so no C toolchain is needed — that is A3.