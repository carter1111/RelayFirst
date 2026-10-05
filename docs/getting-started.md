# Getting started

> This is the path from nothing to your first points. It should take under ten minutes.
>
> **One step is deliberately yours, not the tool's:** supplying a private key. See
> [Why you supply your own key](#why-you-supply-your-own-key).

---

## 1. Get the CLI

```bash
npx relayfirst@latest --help      # zero-install, once published
# or, in this repository:
node scripts/npx-relayfirst.mjs --help
```

The launcher delegates to the Go binary; it does not reimplement anything. If no prebuilt
binary exists it builds one, which needs Go 1.27+. It says so plainly rather than failing
mysteriously.

## 2. Supply an agent key

The CLI reads your key from the environment:

```bash
export RELAYFIRST_PRIVATE_KEY=0x<your 32-byte hex key>
```

Verify it produced the identity you expect:

```bash
relayfirst id "$RELAYFIRST_PRIVATE_KEY"
# → agent:eip155:8453:0x...
```

The `agentId` is what your work is attributed to. It is derived from the key, so the same key
always yields the same identity, and no RelayFirst server is involved.

## 3. Configure

All of this is non-secret, and it is stored in a config file with `0600` permissions:

```bash
relayfirst config set provider local                          # no key, no network
relayfirst config set source https://example.com              # a task source
# relayfirst config set relay http://localhost:8080          # a relay, if you run one
relayfirst config show
```

`local` is a lookup stub that performs no inference. It exists so the whole path can be
exercised offline; it is not a language model.

## 4. Mine

```bash
relayfirst mine --once          # one iteration, for a smoke test
relayfirst mine                 # continuous
```

Each iteration prints a line like:

```
  ok extract 0x1c32362eaa…  |  epoch 2  points epoch: 10.0000  lifetime: 10.0000  tasks: 1  anchors: 1
```

The numbers come from the ledger, not from a separate counter, so what you see is what was
actually credited.

## 5. Check and export

```bash
relayfirst status                       # points, receipts, anchors, epoch
relayfirst receipts --export --out ./my-receipts
relayfirst verify ./my-receipts/<receiptId>.json
relayfirst verify ./my-receipts/<receiptId>.json --refetch   # optional: also re-check the source
```

**`verify` is fully offline by default.** It never contacts a server, which is the point: a
receipt remains verifiable even if every RelayFirst server is switched off. Export exists so you
can walk away with your contribution and check it yourself.

**`--refetch` opts into a network check.** It contacts each anchor's source and compares the
content hash, answering "does the evidence still describe the source?" rather than "is this
receipt authentic?". Two consequences worth knowing:

- It needs the network, so it is not the offline check. The default path stays offline so that
  guarantee is not weakened.
- Content legitimately changes, so an **honest** receipt can fail this check once its epoch
  window has passed (`MVP.md` §5.4). A failure means the source drifted, not that the receipt
  was forged — the output keeps `valid: true` and reports `anchorConsistency: false` separately.

Output always names the mode that ran, so you can tell which check you actually got:

```json
{ "valid": true, "mode": "offline",  "note": "...anchors not re-fetched..." }
{ "valid": true, "mode": "refetch",  "anchorConsistency": true,  "note": "...still match" }
```

---

## Why you supply your own key

**There is no `relayfirst init`, and that is a deliberate boundary rather than a missing
feature.**

Generating a private key and writing it to disk is the act of creating asset control on your
behalf. That is a security decision, not a convenience feature, so it is yours to make — with
a key you generated and backed up the way you would for any wallet.

If you need one:

```bash
# Any EVM wallet works. Examples, not endorsements:
cast wallet new                       # Foundry
# or generate one in MetaMask / Rabby / any wallet you already trust
```

Then export it as in step 2. **Keep a backup.** Losing the key means losing the identity your
work is attributed to, and nothing can recover it.

### What the tool will never do

- It will not generate a key for you.
- It will not write a key to the config file. `config` has no field for one, and it **rejects**
  any value that looks like credential material (`sk-`, `-----BEGIN`, `PRIVATE KEY`).
- It will not print a key, or include one in an error message.
- It will not accept a provider API key as a flag. `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` come
  from the environment, because a command-line flag leaks into shell history and the process
  table.

## Provider keys (optional)

`--provider local` needs nothing. To use a real model for semantic extraction:

```bash
export OPENAI_API_KEY=...      # or ANTHROPIC_API_KEY
relayfirst mine --provider openai --model gpt-5.5 --semantic "the main product price"
```

Semantic fields make extract tasks consume real inference budget (see
[`docs/decisions/ADR-0002-*.md`](decisions/ADR-0002-semantic-extract-as-inference-cost.md)).
Without a `--semantic` field, extract tasks cost nothing and no provider is used.

---

## Running a relay node

A node is optional. It stores and forwards receipts; it does not verify anything.

```bash
docker build -t relayfirst/node:latest .
docker run -d --name relayfirst-node -p 8080:8080 \
  -v ./relayfirst-data:/data relayfirst/node:latest \
  --public-url http://localhost:8080
```

Then point the miner at it:

```bash
relayfirst config set relay http://localhost:8080
# or, for one run: relayfirst mine --relay http://localhost:8080
```

Publishing to more than one node is supported and recommended — one unreachable relay does not
lose a receipt as long as another accepts it.

---

## What you are earning

Points. They are:

- **non-transferable** — there is no method that moves them between agents,
- **unpriced** — no exchange, no rate,
- **carrying no promised return.**

That is a deliberate design constraint, not an oversight. See `MVP.md` §6.1.

## Where to go next

| You want to… | Read |
|---|---|
| understand the scope | [`MVP.md`](../MVP.md) |
| see what is actually done | [`TASKS.md`](../TASKS.md) |
| know the invariants that cannot be broken | [`AGENTS.md`](../AGENTS.md) §3 |
| verify a receipt yourself | `relayfirst verify <file>` and `MVP.md` §11② |
| understand the anti-farming design | `MVP.md` §5, and note the honest caveat in §5.6 |