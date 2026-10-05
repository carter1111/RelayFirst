# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.x     | :white_check_mark:  |

**Only the latest release tag receives security patches.** No backports to pre-release versions.

---

## Reporting a Vulnerability

**DO NOT open a public issue for security vulnerabilities.**

Please report via one of these channels:

1. **GitHub Security Advisories**: Use the "Report a vulnerability" button on this repo's [Security tab](https://github.com/<org>/relayfirst/security/advisories). This is the preferred method — it creates a private discussion thread.

2. **Email**: Send details to `security@relayfirst.dev` (if registered) or contact via the project maintainer's GitHub profile email.

### What to Include

```text
- Type of vulnerability (e.g., signature bypass, key leakage, replay attack)
- Proof-of-concept (minimal reproducer, no production data)
- Affected components and version tags
- Severity assessment (CVSS if known)
```

### Timeline

```text
① Acknowledgement:     48 hours
② Assessment + fix plan:   1 week
③ Patch release:         2 weeks
④ Public disclosure:      After patch lands
```

We will **not** disclose vulnerabilities until a fix is available — so we can focus on fixing rather than mitigating.

---

## Cryptographic Conventions (A1–A4)

This project enforces strict rules around cryptography, which are the most likely sources of security issues:

| Rule | Invariant | Enforcement |
|------|-----------|-------------|
| **Never hand-roll crypto** | A1: keccak256 / secp256k1 / ecrecover / X25519 use only mature libraries | CI import-graph gates + code review |
| **KAT vectors must pass** | A4: TS (viem) ↔ Go byte-for-byte parity on EIP-712 hashing | Gate #4 in CI |
| **Static build** | A3: CGO_ENABLED=0 always builds | Gate #1 in CI |
| **Points are non-transferable** | A5: No transfer interface exists | Structural gate + compliance audit |
| **Global dedup ledger** | A6: artifactKey global namespace, not per-agent | Gate #11 import-graph + unit tests |

### Cryptographic Libraries in Use

| Primitive | Library | License | Category |
|-----------|---------|---------|----------|
| `keccak256` | `golang.org/x/crypto/sha3` | BSD-3 | ① (never hand-roll) |
| `secp256k1` sign/verify | `github.com/decred/dcrd/dcrec/secp256k1/v4` | MIT | ① |
| `ecrecover` | `decred/dcrd` or `go-ethereum/crypto` | MIT / LGPL-3.0 | ① |
| X25519 / XChaCha20-Poly1305 | `golang.org/x/crypto` | BSD-3 | ① |
| OpenZeppelin contracts | Vendored v5.7.0 | MIT | Solidity stdlib |

---

## Known Attack Surfaces

These are documented in [`MVP.md` §5.7](MVP.md) and [`HANDOFF.md`](HANDOFF.md) §5. They are **not** vulnerabilities — they are acknowledged trade-offs:

| Attack | Status | Mitigation |
|--------|--------|------------|
| N agents submitting same URL | ✅ Blocked | Global dedup → novelty = 0 |
| Forged anchor contentHash | ✅ Blocked | Re-fetch fails |
| Generate-type task farming | ✅ Blocked | Only probe/extract/compute accepted |
| Subscription-based inflation | ⚠️ Partial | BLK-1 verification; paid behavior is desired |
| Rush-order (front-running) | ❌ Not blocked | First finder is valid by design |
| Whale sybil self-verification | ⚠️ Partial | Linear cost increase + ratio caps (MVP §5.6) |

**Do NOT present sybil defense as bulletproof.** It is an acceptable trade-off, not a guarantee.

---

## Incident Response

If a critical vulnerability is discovered:

1. **Immediately patch** on a `hotfix/*` branch from `main`
2. Add regression test that fails without the fix
3. Release `vX.Y.Z+1` (patch bump)
4. Notify community via existing channels
5. Update this page with remediation steps

### Post-Incident Review

After any incident, write a `docs/notes/post-mortem-YYYY-MM-DD.md` covering:

- What happened
- How it was detected
- What was fixed
- What preventive controls should be added (new CI gate? architectural change?)
- Action items (with assignee and deadline)

---

## Questions About This Policy

Open an [issue](https://github.com/<org>/relayfirst/issues) with label `question`.
