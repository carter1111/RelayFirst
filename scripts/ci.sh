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
  count=$(grep -o '"name"' testdata/eip712-vectors.json | wc -l | tr -d ' ')
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
version_pattern='TestTypedData_|TestValidateForMajor_|TestValidate_Additive|TestValidate_Unsupported|TestEnvelope_(AcceptsUnknownFields|ZeroChainFields|ChainFieldsRoundTrip)|TestNegotiate|TestNegotiator_|TestNewNegotiator_|TestParseVersion|TestVersionCompare|TestFrozenCorpus|TestSignedPayload_|TestCanonicalJSON_|TestEvent_|TestEventHash_|TestDecodeEvent_|TestSignedBytes_|TestValidateChain_|TestValidateEvent_|TestDerive|TestDeriveSession_|TestSessionEvent_|TestSessionIDFor_|TestTimeoutCoverage_|TestA2ATaskState_|TestVerification_|TestVerify_(Recompute|Empty|Evaluator|Dispute|ModeRefusal)|TestEvaluate_|TestDesignateEvaluator_|TestA2ATaskID_|TestSetA2ATaskID_|TestValidateA2ATaskID_|TestWS_|TestHTTPEndpointsSurviveWebSocket|TestBuild_(DeclaresBothBindings|OmitsWebSocketWhenUnset)|TestSelectInterface_SkipsWebSocket|TestAcceptance_|TestRoundTrip|TestCiphertext|TestThirdParty|TestTampered|TestRecipient|TestTwoSeals|TestSharedSecret|TestUnsupportedAlgorithm|TestUnmarshalSealed|TestSeal|TestKeyPair|TestSealedPayload|TestNoAlgorithm|TestNoScopeCoversReceipts|TestInitialize_|TestToolsList_|TestListTasks_|TestRelayMessage_|TestUnknownToolIsReported|TestToolFailureIs|TestNotificationProducesNoResponse|TestParseErrorIsReported|TestMethodNotFoundIsReported|TestPingIsAnswered|TestUnreachableRelay|TestResponseSizeIsBounded|TestAuthorizeEvent_|TestAllows_|TestIsRevokedBy_|TestScopesHash_|TestSignAndVerify|TestVerify_|TestSign_|TestScopeValid_|TestAllScopes_|TestObservations_|TestEvidence_|TestTasks_|TestAssert_|TestAssertion_|TestRunner_|TestCheck_|TestCriterion10_|TestNode_(PublishesNodeID|OmitsNodeID|HasNoKeyField|NoteIsAccurate)'
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
      trees=$(grep -o '"name"' testdata/merkle-vectors.json | wc -l | tr -d ' ')
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

printf '\n'
if [ "$failures" -eq 0 ]; then
  printf '\033[32mAll gates passed.\033[0m\n'
else
  printf '\033[31m%d gate(s) failed.\033[0m\n' "$failures"
  exit 1
fi
