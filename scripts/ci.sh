#!/usr/bin/env bash
#
# RelayFirst — pre-commit / CI gate.
#
# Mirrors CODING_RULES.md §9. All of these must pass before a change is
# considered landable. Run from the repository root:
#
#   ./scripts/ci.sh
#
# The KAT step is the reason this script exists: cross-language EIP-712 hashing
# drifts silently, and only a byte-for-byte comparison catches it
# (CODING_RULES.md §4, invariant A4).

set -euo pipefail

cd "$(dirname "$0")/.."

# ---------------------------------------------------------------------------
# A note on `set -e` and `pipefail`, because this script was bitten three times.
#
# `pipefail` makes ANY stage's failure fail the whole pipeline, not just the last. Combined with
# `set -e`, that means a command substitution whose pipeline matches nothing EXITS THE SCRIPT from
# inside an assignment — silently, with no FAIL line, because the failure happens at a `var=$(...)`
# rather than at a gate.
#
# Measured: an empty testdata/eip712-vectors.json made `count=$(grep ... | wc -l | tr -d ' ')` exit 1
# and the whole run produced no output at all. That is worse than a wrong answer, because it looks
# like nothing happened.
#
# So every piped command substitution in this file ends in `|| true` and guards the empty case.
# If you add one, do the same. The alternative — dropping `pipefail` — would hide real pipeline
# failures (a `go test` that fails but whose output is piped to `tail` would look like a pass), so
# the discipline is the right trade.
# ---------------------------------------------------------------------------

# Homebrew-installed Go is not always on PATH in non-interactive shells.
if ! command -v go >/dev/null 2>&1 && [ -x /opt/homebrew/bin/go ]; then
  export PATH="/opt/homebrew/bin:$PATH"
fi

pass() { printf '  \033[32mPASS\033[0m %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m %s\n' "$1"; }
step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }

failures=0
run() {
  local name="$1"; shift
  if "$@" >/tmp/rf-ci-step.log 2>&1; then
    pass "$name"
  else
    fail "$name"
    sed 's/^/      /' /tmp/rf-ci-step.log | tail -30
    failures=$((failures + 1))
  fi
}

step "Go toolchain"
go version

step "Static build (invariant A3: no CGO)"
run "CGO_ENABLED=0 go build ./..." env CGO_ENABLED=0 go build ./...

step "Static analysis"
run "go vet ./..." env CGO_ENABLED=0 go vet ./...

step "Formatting"
if [ -z "$(gofmt -l . 2>/dev/null)" ]; then
  pass "gofmt clean"
else
  fail "gofmt found unformatted files:"
  gofmt -l . | sed 's/^/      /'
  failures=$((failures + 1))
fi

step "Tests (includes the cross-language KAT gate, invariant A4)"
run "go test ./..." env CGO_ENABLED=0 go test ./...

# Concurrency gate.
#
# The project has genuinely concurrent code: mutex-guarded ledgers, a mining runner loop, a
# relay HTTP server, and a verifier that settles commitments. `go test` alone does not exercise
# any of it under the race detector, so a data race could ship while every functional test
# passed — and a race in a ledger is the kind of bug that corrupts a balance once in a thousand
# runs and is then impossible to reproduce.
#
# # Why CGO_ENABLED=1 here, when invariant A3 forbids it elsewhere
#
# A3 requires the *shipping binary* to build with CGO_ENABLED=0 so the node is a static binary
# behind one `docker run`. The race detector is test-time instrumentation and needs cgo; it does
# not affect the binary that ships. So this gate deliberately differs from the build gate, and
# the build gate above still enforces A3.
#
# It skips rather than fails when a race-capable toolchain is unavailable, because that is an
# environment fact rather than a defect in this repository — the same reasoning as the Solidity
# and viem gates.
step "Race detector (skipped when a race-capable toolchain is unavailable)"
race_out=$(CGO_ENABLED=1 go test -race ./... 2>&1) && race_status=0 || race_status=$?
if [ "$race_status" -eq 0 ]; then
  pass "go test -race ./..."
elif printf '%s' "$race_out" | grep -qiE 'requires cgo|not supported|cgo: .*not|race.*disabled'; then
  printf '  \033[33mSKIP\033[0m the race detector needs a cgo-capable toolchain on this platform\n'
else
  fail "go test -race ./... found a problem:"
  printf '%s\n' "$race_out" | sed 's/^/      /' | tail -40
  failures=$((failures + 1))
fi

step "KAT corpus freshness"
if [ -f testdata/eip712-vectors.json ]; then
  # `|| true` because `pipefail` makes ANY stage's failure fail the whole pipeline, so a grep that
  # matches nothing would exit the SCRIPT from inside this assignment — silently, with no FAIL line.
  # Measured: an empty vectors file produced exit 1 and no output at all.
  count=$(grep -o '"name"' testdata/eip712-vectors.json | wc -l | tr -d ' ' || true)
  count=${count:-0}
  pass "testdata/eip712-vectors.json present ($count vectors)"
else
  fail "testdata/eip712-vectors.json missing — run: npm run gen:kat"
  failures=$((failures + 1))
fi

# The A5 claims gate (S8-9).
#
# Invariant A5 says points are non-transferable, unpriced, and carry no promised return, and
# that this must be written into the UI and docs. The structural half of A5 is already pinned by
# a reflection test on the points ledger's method set. This gate covers the half no structural
# test can reach: the words. A CLI that printed "your points are worth $5" would violate A5
# while every other test still passed.
#
# It exits non-zero when user-facing text needs review, so it is a real gate rather than a
# report. The guard is a tripwire over known vocabulary, not a proof — see the package comment
# in internal/compliance for its stated limits.
step "A5 points-claims compliance (invariant A5: no value, price, return, or transferability claims)"
run "compliance audit" env CGO_ENABLED=0 go run internal/devtools/compliance_audit.go

# The backward-compatibility gate (S9-0g, invariant A9 §① and §⑥).
#
# A9 promises that a receipt stays verifiable forever, because acceptance
# criterion ② requires it after every server is switched off. That promise breaks
# silently: a refactor changes how the signed payload is rebuilt, every unit test
# still passes (they all sign and verify inside the same build), and the only
# casualty is the ability to verify a receipt signed by an older version.
#
# The corpus under testdata/receipts/ is the tripwire. It is append-only: adding a
# schema major means adding a new v<major> directory, never editing an existing
# one. This step fails if the corpus is missing or if any frozen receipt no longer
# verifies, so the guarantee is enforced rather than asserted.
step "Frozen receipt corpus (invariant A9: historical receipts must stay verifiable)"
if [ ! -d testdata/receipts ]; then
  fail "testdata/receipts/ missing — run: go run internal/devtools/gen_frozen_receipts.go"
  failures=$((failures + 1))
else
  run "go test ./internal/receipt/ -run FrozenCorpus" env CGO_ENABLED=0 go test ./internal/receipt/ -run FrozenCorpus -count=1
fi

# The cross-version compatibility matrix (S9-0g: G2 + G3 + G4).
#
# The ingest benchmark gate (Phase 1).
#
# # Why this gate exists
#
# RelayFirst had never measured its own ingest throughput, and the WuKongIM note was corrected
# (b6ee030) for having borrowed a number from another project. The correction left a real gap: the
# cost of single-connection, row-at-a-time SQLite was KNOWN to be unquantified. A measurement that
# nobody re-runs would leave the same gap, so the number is gated.
#
# # Why the threshold is 20% and why that is defensible
#
# Measured repeatability on the baseline machine (Apple M4, go1.27.1, modernc.org/sqlite v1.60.1)
# over four runs: 10782, 11578, 11124, 10885 writes/s. That is a spread of about 7%, so run-to-run
# noise alone can move the number by more than a few percent.
#
# A threshold BELOW the observed noise would fail for its own jitter and would be switched off,
# which is worse than no gate. 20% is comfortably above the ~7% noise floor while still catching a
# real algorithmic regression: the Phase 2 change is expected to move throughput by a multiple, not
# by percent, so the gate's job is to catch "something got much slower", not to police small drift.
#
# # Why a missing baseline is a FAILURE rather than a skip
#
# The baseline is committed, so its absence means someone deleted it — and a silently skipped gate
# is how a regression ships. It fails with the command to regenerate.
step "Ingest benchmark regression (write path; baseline in testdata/ingest-baseline.json)"
if [ ! -f testdata/ingest-baseline.json ]; then
  fail "testdata/ingest-baseline.json is missing — regenerate with: go run internal/devtools/ingest_bench.go -update"
  failures=$((failures + 1))
else
  bench_out=$(CGO_ENABLED=0 go run internal/devtools/ingest_bench.go 2>&1) && bench_status=0 || bench_status=$?
  if [ "$bench_status" -ne 0 ]; then
    fail "the ingest benchmark did not run:"
    printf '%s\n' "$bench_out" | sed 's/^/      /' | tail -20
    failures=$((failures + 1))
  else
    # The benchmark prints its own comparison; this parses the delta it computed so the gate and the
    # tool cannot disagree about what the number means.
    #
    # The `|| true` is load-bearing. Under `set -euo pipefail`, a pipeline whose last command matches
    # nothing exits nonzero, and a command substitution that does so KILLS THE SCRIPT — silently, with
    # no FAIL line, because the failure happens inside an assignment rather than at a gate. Measured:
    # deleting batchSize from the baseline made this whole step exit 1 with no output at all, which is
    # the least debuggable outcome available.
    bench_delta=$(printf '%s\n' "$bench_out" | grep -E '^change ' | grep -oE '[-+][0-9.]+' | head -1 || true)
    if [ -z "$bench_delta" ]; then
      # No delta line means the workload or the machine differed, and the tool said so rather than
      # printing a misleading figure. That is not a regression, so it is reported and not failed.
      # `|| true` for the same reason as above.
      printf '  \033[33mSKIP\033[0m no comparable baseline (workload or machine differs)\n'
      printf '%s\n' "$bench_out" | grep -E '^baseline|^change|differs' | sed 's/^/      /' || true
    else
      # Compare with awk rather than bc, which is not present everywhere.
      #
      # The `+0` forces a numeric context. Without it awk compares an empty or
      # malformed string against -20 in a way that is not arithmetic: measured,
      # `awk -v d="" 'BEGIN{print (d < -20) ? "yes":"no"}'` prints "yes", because an
      # empty string does not behave as the number zero in that comparison. That
      # would turn a parsing failure into a reported regression.
      #
      # The pattern check below is separate from the comparison on purpose. A
      # legitimately unchanged run reports "+0.0%", so "the delta is zero" cannot be
      # the test for "the delta failed to parse" — the two need different checks.
      case "$bench_delta" in
        [+-][0-9]*.[0-9]*) delta_is_numeric=yes ;;
        *) delta_is_numeric=no ;;
      esac
      if [ "$delta_is_numeric" = "no" ]; then
        fail "the ingest benchmark printed a delta that is not a number ('$bench_delta'); the gate cannot compare"
        printf '%s\n' "$bench_out" | sed 's/^/      /'
        failures=$((failures + 1))
      else
        regression=$(awk -v d="$bench_delta" 'BEGIN { print (d + 0 < -20) ? "yes" : "no" }')
        # `|| true` for the same reason as bench_delta: under `set -euo pipefail` a substitution
        # whose pipeline matches nothing exits nonzero, and that exits the SCRIPT from inside an
        # assignment — with no FAIL line, which is the least debuggable outcome available.
        throughput=$(printf '%s\n' "$bench_out" | grep -E '^  throughput' | sed 's/^ *//' || true)
        if [ "$regression" = "yes" ]; then
          fail "ingest throughput regressed by ${bench_delta}% (threshold -20%): $throughput"
          printf '%s\n' "$bench_out" | sed 's/^/      /'
          failures=$((failures + 1))
        else
          pass "ingest benchmark (${bench_delta}% vs baseline; $throughput)"
        fi
      fi
    fi
  fi
fi

# The frozen corpus above pins one property: a historical receipt still verifies.
# It does not exercise the paths that break when a NEW version is being added,
# which are the paths that are hardest to notice because everything green today
# stays green tomorrow. Three of those paths are named in the plan and gated here:
#
#   G2  cross-version rules     a v1 artifact must verify under v1 rules AND be
#                               refused under v2 rules; profiles must not collide
#   G3  unknown-field injection additive fields must be tolerated in the receipt
#                               payload and on the envelope
#   G4  negotiation no-overlap  two sides sharing no A2A version must fail with
#                               ErrVersionNotSupported, never fall back
#
# # Why this is a pattern and not a package
#
# The three concerns live in three packages (receipt, protocol, a2a) and will
# gain cases as versions are added. Naming a pattern keeps the gate pointed at
# the behaviour rather than at three specific test functions that someone must
# remember to extend.
#
# It is logged as a real gate because an empty selection would otherwise look
# like success: `go test -run` with no matches exits zero. The count check below
# turns "nothing ran" into a failure, so a rename cannot quietly retire the gate.
step "Version compatibility matrix (S9-0g: G2 cross-version, G3 unknown fields, G4 negotiation)"
version_pattern='TestTypedData_|TestValidateForMajor_|TestValidate_Additive|TestValidate_Unsupported|TestEnvelope_(AcceptsUnknownFields|ZeroChainFields|ChainFieldsRoundTrip)|TestNegotiate|TestNegotiator_|TestNewNegotiator_|TestParseVersion|TestVersionCompare|TestFrozenCorpus|TestSignedPayload_|TestCanonicalJSON_|TestEvent_|TestEventHash_|TestDecodeEvent_|TestSignedBytes_|TestValidateChain_|TestValidateEvent_|TestDerive|TestDeriveSession_|TestSessionEvent_|TestSessionIDFor_|TestTimeoutCoverage_|TestA2ATaskState_|TestVerification_|TestVerify_(Recompute|Empty|Evaluator|Dispute|ModeRefusal)|TestEvaluate_|TestDesignateEvaluator_|TestA2ATaskID_|TestSetA2ATaskID_|TestValidateA2ATaskID_|TestWS_|TestHTTPEndpointsSurviveWebSocket|TestBuild_(DeclaresBothBindings|OmitsWebSocketWhenUnset)|TestSelectInterface_SkipsWebSocket|TestAcceptance_|TestRoundTrip|TestCiphertext|TestThirdParty|TestTampered|TestRecipient|TestTwoSeals|TestSharedSecret|TestUnsupportedAlgorithm|TestUnmarshalSealed|TestSeal|TestKeyPair|TestSealedPayload|TestNoAlgorithm|TestNoScopeCoversReceipts|TestInitialize_|TestPrivateReceipt_|TestSessionKey_|TestSessionSealed_|TestToolsList_|TestListTasks_|TestRelayMessage_|TestUnknownToolIsReported|TestToolFailureIs|TestNotificationProducesNoResponse|TestParseErrorIsReported|TestMethodNotFoundIsReported|TestPingIsAnswered|TestUnreachableRelay|TestResponseSizeIsBounded|TestBatch_|TestAuthorizeEvent_|TestAllows_|TestIsRevokedBy_|TestScopesHash_|TestSignAndVerify|TestVerify_|TestSign_|TestScopeValid_|TestAllScopes_|TestObservations_|TestEvidence_|TestTasks_|TestAssert_|TestAssertion_|TestRunner_|TestCheck_|TestSignAndVerify|TestSign_|TestVerify_|TestAuthorizeEventWithGrant_|TestScopeOfEvent_|TestSignedBytes_|TestSession_|TestCriterion10_|TestNode_(PublishesNodeID|OmitsNodeID|HasNoKeyField|NoteIsAccurate)'
version_out=$(CGO_ENABLED=0 go test ./internal/receipt/ ./internal/protocol/ ./internal/a2a/ \
  -run "$version_pattern" -count=1 -v 2>&1) && version_status=0 || version_status=$?
version_pass=$(printf '%s' "$version_out" | grep -c '^--- PASS' || true)
if [ "$version_status" -ne 0 ]; then
  fail "cross-version matrix:"
  printf '%s\n' "$version_out" | grep -E '^(--- FAIL|    )' | sed 's/^/      /' | head -30
  failures=$((failures + 1))
elif [ "$version_pass" -lt 30 ]; then
  fail "cross-version matrix ran only $version_pass tests — the selection has gone stale (renamed tests?), so this gate is no longer checking anything"
  failures=$((failures + 1))
else
  pass "cross-version matrix ($version_pass tests across receipt/protocol/a2a)"
fi

# The import-graph separation gate (MVP.md §7.1 and §8.5).
#
# Two boundaries in this project are load-bearing security properties, and both
# are properties of the import graph rather than of reviewer attention:
#
#   1. A relay node must be UNABLE to verify a signature. If it could, it could
#      forge one, and "validate on the client" (MVP.md §5.4) is what stops a
#      hostile node from fabricating work. So the node binary must not link
#      eip712, receipt, publish or the miner-side stores.
#
#   2. The mining core must not depend on the A2A wire layer. MVP.md §8.5 permits
#      a2a-go only at the identity/serialization boundary; letting it into the
#      mining path would drag a wire-format concern into the one thing that must
#      stay stable (the receipt).
#
# Neither boundary can be verified by reading code, because a single transitive
# import re-links everything. `go list -deps` answers it exactly, so it is checked
# here rather than trusted. A violation is silent otherwise: the node would build
# and pass every functional test while having quietly gained the ability to forge.
step "Import-graph separation (MVP.md §7.1: node cannot verify; §8.5: mining core stays A2A-free)"
separation_ok=1

# e2ee is included because §7.1 and the S13 plan §9 require the same thing of it: a node that
# cannot verify signatures also cannot decrypt, and both follow from the node being unable to reach
# the code. "The relay only sees ciphertext" is therefore enforced rather than promised.
node_forbidden=$(CGO_ENABLED=0 go list -deps ./cmd/relayfirst-node 2>/dev/null \
  | grep -E 'relayfirst/internal/(eip712|receipt|publish|store|e2ee)$' || true)
if [ -n "$node_forbidden" ]; then
  fail "the node binary links signing/verification code, so it could forge:"
  printf '%s\n' "$node_forbidden" | sed 's/^/      /'
  separation_ok=0
fi

mining_a2a=$(CGO_ENABLED=0 go list -deps ./internal/mining 2>/dev/null \
  | grep -E 'relayfirst/internal/a2a$' || true)
if [ -n "$mining_a2a" ]; then
  fail "the mining core depends on the A2A wire layer (MVP.md §8.5 forbids this):"
  printf '%s\n' "$mining_a2a" | sed 's/^/      /'
  separation_ok=0
fi

# The verifier boundary (ADR-0004).
#
# ADR-0004 split the roles so two properties hold at once: a keyless node that cannot forge,
# and a keyed verifier whose conclusions are attributable. The node half is checked above; this
# is the verifier half. It signs ASSERTIONS -- "I checked receipt X" -- and must never be able
# to sign a RECEIPT, which claims work was performed and is what points are earned for.
#
# The enforcement is the same shape as the node's: the verifier must not link internal/mining
# or internal/scoring, so it cannot even construct a receipt, let alone submit one. Without
# this check the split would be a naming convention rather than a property.
verifier_forbidden=$(CGO_ENABLED=0 go list -deps ./cmd/relayfirst-verifier 2>/dev/null \
  | grep -E 'relayfirst/internal/(mining|scoring)$' || true)
if [ -n "$verifier_forbidden" ]; then
  fail "the verifier binary links the mining or scoring path, so it could sign work claims:"
  printf '%s\n' "$verifier_forbidden" | sed 's/^/      /'
  separation_ok=0
fi

# The MCP server must hold NO key (S13-4, MVP.md §9.2).
#
# This is the whole security property of that binary, and it is a property of the BUILD rather than of
# any behaviour: an MCP server runs inside the agent's process, so a prompt-injected agent could make
# it sign an arbitrary receipt on the user's behalf if it could sign at all. "Never hold the master
# key" is therefore enforced by making signing unreachable, not by remembering not to call it.
#
# It is checked here because a behavioural test cannot see it: a test can confirm no signing TOOL is
# offered, but only the dependency graph proves none could be implemented.
mcp_forbidden=$(CGO_ENABLED=0 go list -deps ./cmd/relayfirst-mcp 2>/dev/null \
  | grep -E 'relayfirst/internal/(eip712|receipt|publish|store|assertion|delegation|delegationsign|e2ee|mining|scoring)$' || true)
if [ -n "$mcp_forbidden" ]; then
  fail "the MCP server links signing or verification code, so a prompt-injected agent could make it sign:"
  printf '%s\n' "$mcp_forbidden" | sed 's/^/      /'
  separation_ok=0
fi

# And it MUST be a real binary that builds, or the check above would pass for a package that had been
# deleted — the same vacuity guard as the verifier's.
if ! CGO_ENABLED=0 go list -deps ./cmd/relayfirst-mcp >/dev/null 2>&1; then
  fail "cmd/relayfirst-mcp does not build, so the MCP separation check is vacuous"
  separation_ok=0
fi

# And it MUST reach the assertion machinery, or the check above would pass vacuously for a
# binary that had simply lost the ability to assert at all.
verifier_assertion=$(CGO_ENABLED=0 go list -deps ./cmd/relayfirst-verifier 2>/dev/null \
  | grep -E 'relayfirst/internal/assertion$' || true)
if [ -z "$verifier_assertion" ]; then
  fail "the verifier binary does not link internal/assertion, so it cannot assert anything and the check above is vacuous"
  separation_ok=0
fi

if [ "$separation_ok" -eq 1 ]; then
  pass "node cannot verify; mining core is A2A-free"
else
  failures=$((failures + 1))
fi

# The Merkle corpus is the S7 cross-language gate. It is checked by regeneration
# rather than by presence: a stale corpus would still pass every test on both sides
# while the two implementations quietly diverged, which is exactly the failure
# invariant A4 exists to catch. Regenerating and diffing is what makes it real.
step "Merkle vector freshness (invariant A4: Go and Solidity must agree)"
if [ ! -f testdata/merkle-vectors.json ]; then
  fail "testdata/merkle-vectors.json missing — run: go run internal/devtools/gen_merkle_vectors.go"
  failures=$((failures + 1))
else
  merkle_before=$(cat testdata/merkle-vectors.json contracts/test/MerkleVectors.sol)
  if go run internal/devtools/gen_merkle_vectors.go >/tmp/rf-ci-merkle.log 2>&1; then
    merkle_after=$(cat testdata/merkle-vectors.json contracts/test/MerkleVectors.sol)
    if [ "$merkle_before" = "$merkle_after" ]; then
      trees=$(grep -o '"name"' testdata/merkle-vectors.json | wc -l | tr -d ' ' || true)
      trees=${trees:-0}
      pass "merkle vectors are current ($trees trees, .json and .sol in sync)"
    else
      fail "merkle vectors are STALE — regenerate and commit: go run internal/devtools/gen_merkle_vectors.go"
      failures=$((failures + 1))
    fi
  else
    fail "could not regenerate merkle vectors:"
    sed 's/^/      /' /tmp/rf-ci-merkle.log | tail -10
    failures=$((failures + 1))
  fi
fi

# The Solidity half of the cross-language gate. It runs only when forge is
# available, because its absence is an environment fact rather than a defect in
# this repository — but the vectors above are still regenerated and diffed, so the
# Go side is gated either way.
step "Solidity contract tests (skipped when forge is absent)"
if command -v forge >/dev/null 2>&1 || [ -x "$HOME/.foundry/bin/forge" ]; then
  if ! command -v forge >/dev/null 2>&1; then
    export PATH="$HOME/.foundry/bin:$PATH"
  fi
  run "forge test" forge test
else
  printf '  \033[33mSKIP\033[0m forge not installed — install Foundry to gate the Solidity side\n'
fi

# The third implementation. Go and Solidity are both ours, so their agreement could be a
# shared misreading of the spec; viem is independently written and shares no code. It was a
# one-off manual check until now, and a check nobody re-runs cannot catch a regression.
#
# Runs only when node_modules is present, since it needs viem. That is an environment fact,
# not a defect, so its absence is reported rather than failed.
step "Merkle cross-check against viem (invariant A4: third independent implementation)"
if [ ! -d node_modules/viem ]; then
  printf '  \033[33mSKIP\033[0m viem not installed — run npm install to gate the third implementation\n'
elif ! command -v node >/dev/null 2>&1; then
  printf '  \033[33mSKIP\033[0m node not installed — run npm install to gate the third implementation\n'
else
  run "node scripts/verify-merkle-viem.mjs" node scripts/verify-merkle-viem.mjs
fi

# The npm packaging surface. `npx relayfirst` is the zero-install story (MVP.md §9.2),
# and it depends on the tarball CONTAINING a binary: the launchers only select and
# spawn one. When bin/ was gitignored and no cross-build ran, a clean checkout
# published no binary and the launcher silently fell back to `go build` — zero-install
# that required a toolchain. This gate keeps the packaging honest.
step "npm packaging (MVP.md §9.2: npx must work without a Go toolchain)"
npm_ok=1

if [ ! -f package.json ]; then
  fail "package.json is missing"
  npm_ok=0
else
  # (1) It must be publishable. `private: true` blocks publish outright.
  if grep -qE '"private"\s*:\s*true' package.json; then
    fail "package.json is private, so npm publish would refuse:"
    printf '      remove "private": true to ship the npx entry points\n'
    npm_ok=0
  fi

  # (2) The binary build must run at publish time, and bin/npm must be in `files`.
  if ! grep -qE '"prepublishOnly"' package.json; then
    fail "package.json has no prepublishOnly, so the platform binaries would not be built before publish"
    npm_ok=0
  fi
  if ! grep -qE '"bin/npm"' package.json; then
    fail "package.json 'files' does not include bin/npm, so the tarball would ship no binary"
    npm_ok=0
  fi
fi

# (3) The build must actually produce the binaries, and the launcher must resolve one.
if [ "$npm_ok" -eq 1 ]; then
  if bash scripts/build-npm-binaries.sh >/tmp/rf-ci-npm-build.log 2>&1; then
    arch="amd64"; [ "$(uname -m)" = "arm64" ] && arch="arm64"
    os="linux"; [ "$(uname -s)" = "Darwin" ] && os="darwin"
    want="bin/npm/relayfirst-$os-$arch"
    if [ ! -f "$want" ]; then
      fail "the cross-build did not produce $want (the platform this gate runs on):"
      ls -1 bin/npm 2>/dev/null | sed 's/^/      /' | head || true
      npm_ok=0
    elif ! node scripts/npx-relayfirst.mjs version >/tmp/rf-ci-npm-run.log 2>&1; then
      fail "the launcher did not run the prebuilt binary:"
      sed 's/^/      /' /tmp/rf-ci-npm-run.log | tail -10
      npm_ok=0
    else
      pass "npx launcher runs a prebuilt platform binary ($os/$arch)"
    fi
  else
    fail "scripts/build-npm-binaries.sh failed:"
    sed 's/^/      /' /tmp/rf-ci-npm-build.log | tail -10
    npm_ok=0
  fi
fi

if [ "$npm_ok" -ne 1 ]; then
  failures=$((failures + 1))
fi

# The release path must exist AND carry the mainnet tag, or the epoch genesis guard
# (internal/epoch) is decorative: nothing would build with -tags mainnet, so nothing
# would ever trigger it, and a release assembled by hand with a plain `go build`
# would bypass it. This gate builds with the tag and inspects the binary so the
# guard cannot rot unnoticed.
#
# It deliberately does NOT fail when the release refuses (the genesis is expected to
# be provisional until the launch date is pinned — BLK-4). It fails when the tag
# stops taking effect, which is the silent failure mode.
step "Release path arms the genesis guard (BLK-4: a provisional genesis must not ship)"
release_ok=1

# (1) The release path exists.
if [ ! -f scripts/build-release.sh ] || [ ! -f Makefile ]; then
  fail "the release path is missing (scripts/build-release.sh / Makefile); a manual go build would bypass the genesis guard"
  release_ok=0
fi

# (2) The mainnet build must actually FAIL to run while the genesis is provisional.
# This is the property: a tagged build refuses. If it stops refusing, the guard is gone.
if CGO_ENABLED=0 go build -tags mainnet -o /tmp/rf-release-probe ./cmd/relayfirst >/tmp/rf-ci-release.log 2>&1; then
  if /tmp/rf-release-probe version >/tmp/rf-ci-release-run.log 2>&1; then
    fail "a -tags mainnet build RAN with a provisional genesis, so the epoch guard is not armed:"
    sed 's/^/      /' /tmp/rf-ci-release-run.log | tail -10
    release_ok=0
  else
    # It refused, as it must. Confirm the refusal is the genesis guard, not some
    # unrelated failure, so the check is not satisfied by a binary that is broken
    # for another reason.
    if grep -q "provisional genesis" /tmp/rf-ci-release-run.log 2>/dev/null; then
      pass "release build refuses a provisional genesis (the guard is armed)"
    else
      fail "the release build refused for an unexpected reason:"
      sed 's/^/      /' /tmp/rf-ci-release-run.log | tail -10
      release_ok=0
    fi
  fi
else
  fail "cannot build with -tags mainnet, so the genesis guard cannot be exercised:"
  sed 's/^/      /' /tmp/rf-ci-release.log | tail -10
  release_ok=0
fi

# (3) A plain (untagged) build must still RUN, or development is broken while the
# guard holds. Without this, a guard that refused EVERY build would pass (2).
if [ "$release_ok" -eq 1 ]; then
  if CGO_ENABLED=0 go build -o /tmp/rf-dev-probe ./cmd/relayfirst >/dev/null 2>&1 && \
     /tmp/rf-dev-probe version >/dev/null 2>&1; then
    pass "development build still runs (the guard is release-only)"
  else
    fail "a plain build does not run, so the guard is blocking development too"
    release_ok=0
  fi
fi

if [ "$release_ok" -ne 1 ]; then
  failures=$((failures + 1))
fi

printf '\n'
if [ "$failures" -eq 0 ]; then
  printf '\033[32mAll gates passed.\033[0m\n'
else
  printf '\033[31m%d gate(s) failed.\033[0m\n' "$failures"
  exit 1
fi
