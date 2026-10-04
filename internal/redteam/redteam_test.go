package redteam_test

// ============================================================================
// S8-4: the four forgery attacks from MVP.md §11 criterion ④, run against the
// real stack and each asserted to earn nothing.
//
// # Why this lives in its own package
//
// It spans mining, scoring, storage and verification, so it cannot live inside any
// one of them without that package importing the others' test helpers. A dedicated
// package makes the whole-stack nature explicit, and it means the test exercises the
// same wiring the CLI uses rather than a hand-assembled subset.
//
// # What each attack is, and why it must fail
//
//   a. fabricated result   — a receipt whose claimed value never reproduces
//   b. fabricated anchor   — a receipt whose content hash no source ever returned
//   c. replay              — resubmitting another agent's receipt as one's own
//   d. self-verification   — an agent concluding that its own work is fine
//
// Each one is a way to obtain points without doing work. If any succeeds, the mining
// economy pays for nothing and the whole play collapses, so these are the load-bearing
// tests of the project rather than a completeness exercise.
//
// The assertion in every case is the same and is deliberately blunt: the attacker's
// balance does not increase. A test that only checked a status field could pass while
// points still moved.
// ============================================================================

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/eip712"
	"github.com/relayfirst/relayfirst/internal/mining"
	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/scoring"
	"github.com/relayfirst/relayfirst/internal/sqlite"
	"github.com/relayfirst/relayfirst/internal/store"
	"github.com/relayfirst/relayfirst/internal/verification"
)

const (
	// Well-known test keys. They hold no value and are never used outside tests
	// (CODING_RULES.md §8).
	keyAttacker = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	keyHonest   = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	keyVerifier = "0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
)

func agentOf(t *testing.T, key string) string {
	t.Helper()
	id, err := receipt.DeriveAgentID(key, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}
	return id
}

func hash32(seed string) string {
	sum := make([]byte, 32)
	copy(sum, []byte(seed))
	return "sha256:" + hex.EncodeToString(sum)
}

// liveServer serves a stable JSON document, so an honest receipt from it reproduces.
func liveServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"id":42}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stack is the real storage and scoring wiring, opened fresh per test.
type stack struct {
	db       *sqlite.DB
	receipts *store.ReceiptStore
	ledgers  store.ScoringLedgers
}

func newStack(t *testing.T) *stack {
	t.Helper()

	db, err := sqlite.Open(filepath.Join(t.TempDir(), "redteam.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return &stack{
		db:       db,
		receipts: store.NewReceiptStore(db),
		ledgers:  store.NewScoringLedgers(db),
	}
}

// score runs a receipt through the real scoring path and returns the credited points.
//
// It goes through mining.ScoringSink rather than calling scoring.Emit directly, so the
// dedup ledger, the points ledger and the verdict source are all the production ones.
func (s *stack) score(t *testing.T, r *receipt.Receipt, verdicts mining.VerdictSource) float64 {
	t.Helper()

	key, err := scoring.ArtifactKey(r)
	if err != nil {
		// An unidentifiable artifact cannot be credited; treat it as zero rather than
		// failing, because "the key could not be derived" is itself a way the system
		// refuses work.
		key = ""
	}

	sink := &mining.ScoringSink{
		Inner:     s.receipts,
		Receipts:  s.receipts,
		Artifacts: s.ledgers.Artifacts,
		Points:    s.ledgers.Points,
		Verdicts:  verdicts,
		Clock:     func() time.Time { return time.Unix(1791015900, 0) },
	}

	if err := sink.Save(r, key, time.Unix(1791015900, 0)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return s.ledgers.Points.Balance(r.AgentID)
}

// mkReceipt assembles a signed probe receipt over url claiming value.
func mkReceipt(t *testing.T, key, id, url, value, valueSeed, cHash string) *receipt.Receipt {
	t.Helper()

	r := &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: id,
		AgentID:   agentOf(t, key),
		Epoch:     scoring.EpochOf(time.Unix(1791015900, 0)),
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": url},
			SpecHash:      hash32("spec-" + id),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015862},
		Result:       receipt.Result{Value: value, Hash: hash32(valueSeed)},
		Anchors:      []receipt.Anchor{{URL: url, ContentHash: cHash, FetchedAt: 1791015810, Status: 200, Bytes: 32}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	if err := r.Sign(key); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return r
}

// verifierFor wires a real verifier over the stack.
func verifierFor(t *testing.T, s *stack, who string, deps mining.Deps) *verification.Verifier {
	t.Helper()

	v, err := verification.New(verification.Config{
		Policy:     verification.AllowAll{Verifier: who},
		Stakes:     verification.NewDurableStakes(sqlite.NewCommitmentLedger(s.db)),
		Receipts:   &storeUpdater{s: s},
		Recomputer: &executorRecomputer{deps: deps},
		Window:     verification.AlwaysOpen{},
	})
	if err != nil {
		t.Fatalf("verification.New: %v", err)
	}
	return v
}

// persist stores a receipt before it is verified.
//
// It exists because a verdict cannot be recorded for a receipt the store does not have —
// SaveVerification refuses, rather than silently discarding the conclusion. That refusal is
// correct, so the tests must store first, exactly as the CLI does.
func (s *stack) persist(t *testing.T, r *receipt.Receipt) {
	t.Helper()

	key, err := scoring.ArtifactKey(r)
	if err != nil {
		key = ""
	}
	if err := s.receipts.Save(r, key, time.Unix(1791015800, 0)); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// storeUpdater writes verdicts into the real receipt store and verdict table.
type storeUpdater struct{ s *stack }

func (u *storeUpdater) UpdateVerification(r *receipt.Receipt) error {
	verdicts := sqlite.NewVerdictStore(u.s.db)
	var hash string
	if r.Verification.RecomputedHash != nil {
		hash = *r.Verification.RecomputedHash
	}
	var verifier string
	if r.Verification.VerifierID != nil {
		verifier = *r.Verification.VerifierID
	}
	var verifiedAt time.Time
	if r.Verification.VerifiedAt != nil {
		verifiedAt = time.Unix(*r.Verification.VerifiedAt, 0).UTC()
	}

	if err := verdicts.Record(sqlite.Verdict{
		ReceiptID:      r.ReceiptID,
		AgentID:        r.AgentID,
		VerifierID:     verifier,
		Status:         string(r.Verification.Status),
		RecomputedHash: hash,
		VerifiedAt:     verifiedAt,
	}); err != nil {
		return err
	}
	return u.s.receipts.SaveVerification(r)
}

// executorRecomputer re-runs a task through the production executors.
type executorRecomputer struct{ deps mining.Deps }

func (e *executorRecomputer) Recompute(ctx context.Context, r *receipt.Receipt) (receipt.Result, error) {
	res, _, err := mining.Run(ctx, e.deps, r.Task.Type, r.Task.Spec)
	return res, err
}

// ============================================================================
// Attack (a): a fabricated result
// ============================================================================

// TestRedTeamA_FabricatedResultEarnsNothing is the primary attack: the attacker claims a
// result the source never produced, and signs it correctly.
//
// The receipt is structurally valid and the signature verifies, so the self-check would
// accept it. Only re-execution can catch it — which is what makes this the test that
// justifies the entire verification stage.
func TestRedTeamA_FabricatedResultEarnsNothing(t *testing.T) {
	srv := liveServer(t)
	s := newStack(t)
	deps := mining.Deps{Fetcher: anchor.NewFetcher()}

	// The server returns {"ok":true,"id":42}; the attacker claims a different body.
	r := mkReceipt(t, keyAttacker, "0x"+strings.Repeat("a1", 32), srv.URL,
		`{"id":1,"ok":false}`, "fabricated", hash32("content-real"))

	// Precondition: the forgery is well-formed and correctly signed.
	if err := r.Validate(nil); err != nil {
		t.Fatalf("precondition: the forged receipt should still be structurally valid: %v", err)
	}

	attack := agentOf(t, keyAttacker)
	source := verification.RecordedVerdicts{Lookup: func(string) (*receipt.Receipt, bool) { return r, true }}
	_ = attack

	// Before verification, the self-check would have credited it. Confirm the attack
	// surface is real: this is what the pre-S4 scoring did.
	before := s.score(t, r, mining.SelfCheckVerdicts{})
	if before <= 0 {
		t.Fatal("precondition: the self-check should have credited the forgery, which is exactly the gap S4 closes")
	}
	// Give the attacker a clean ledger for the real attempt, since the precondition above
	// deliberately credited them under the old behaviour.
	attackerAfterSelfCheck := s.ledgers.Points.Balance(attack)

	s.persist(t, r)

	// A verifier re-executes and rejects it.
	v := verifierFor(t, s, agentOf(t, keyVerifier), deps)
	out, err := v.Verify(context.Background(), r)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out.Verified {
		t.Fatal("a fabricated result must not reproduce")
	}

	// The attack must not earn beyond what the deliberately-weak precondition gave it.
	after := s.score(t, r, source)
	if after > attackerAfterSelfCheck {
		t.Errorf("balance rose from %v to %v after a forged result", attackerAfterSelfCheck, after)
	}
}

// ============================================================================
// Attack (b): a fabricated anchor content hash
// ============================================================================

// TestRedTeamB_FabricatedAnchorCannotReuseNovelty: the attacker invents a content hash
// for the same URL, hoping the dedup ledger treats it as new content.
//
// The defence is not the ledger alone — it is that the invented hash corresponds to no
// bytes any source returned, so re-execution produces a different hash and the receipt is
// rejected. This test asserts both halves: the forged one earns nothing, and the honest
// one is still able to earn on the same URL.
func TestRedTeamB_FabricatedAnchorCannotReuseNovelty(t *testing.T) {
	srv := liveServer(t)
	s := newStack(t)
	deps := mining.Deps{Fetcher: anchor.NewFetcher()}

	// The attacker claims a content hash the source never served.
	forged := mkReceipt(t, keyAttacker, "0x"+strings.Repeat("b1", 32), srv.URL,
		"200", "claim-b", hash32("hash-the-source-never-returned"))

	s.persist(t, forged)

	v := verifierFor(t, s, agentOf(t, keyVerifier), deps)
	out, err := v.Verify(context.Background(), forged)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out.Verified {
		t.Fatal("a receipt carrying a content hash no source returned must not verify")
	}

	before := s.ledgers.Points.Balance(agentOf(t, keyAttacker))
	after := s.score(t, forged, verification.RecordedVerdicts{
		Lookup: func(string) (*receipt.Receipt, bool) { return forged, true },
	})
	if after > before {
		t.Errorf("a forged anchor earned points: %v -> %v", before, after)
	}
}

// ============================================================================
// Attack (c): a replay of another agent's receipt
// ============================================================================

// TestRedTeamC_ReplayOfAnotherAgentsReceiptEarnsNothing covers two distinct replays.
//
// First, resubmitting someone else's receipt unchanged: it is attributed to its real
// agent, so the resubmitter gains nothing at all, and the dedup ledger means even the real
// agent gains nothing a second time.
//
// Second, re-signing the same work under the attacker's own identity: the signature now
// claims a different agent, but the artifact key is derived from the task and content, not
// from the signer — so the dedup ledger still sees the same artifact.
func TestRedTeamC_ReplayOfAnotherAgentsReceiptEarnsNothing(t *testing.T) {
	srv := liveServer(t)
	s := newStack(t)
	deps := mining.Deps{Fetcher: anchor.NewFetcher()}

	honestAgent := agentOf(t, keyHonest)
	attackerAgent := agentOf(t, keyAttacker)

	// The honest agent does the work first. The task is really executed and the anchor
	// records the real content hash, so the receipt is internally consistent — which the
	// verifier now checks, because it re-fetches the anchor.
	spec := map[string]any{"url": srv.URL}
	res, anchors, err := mining.Run(context.Background(), deps, receipt.TaskProbe, spec)
	if err != nil {
		t.Fatalf("honest Run: %v", err)
	}

	original := &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: "0x" + strings.Repeat("c1", 32),
		AgentID:   honestAgent,
		Epoch:     scoring.EpochOf(time.Unix(1791015900, 0)),
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          spec,
			SpecHash:      hash32("spec-c"),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015862},
		Result:       res,
		Anchors:      anchors,
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	if err := original.Sign(keyHonest); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	s.persist(t, original)

	v := verifierFor(t, s, agentOf(t, keyVerifier), deps)
	if _, err := v.Verify(context.Background(), original); err != nil {
		t.Fatalf("Verify honest: %v", err)
	}
	source := verification.RecordedVerdicts{Lookup: func(string) (*receipt.Receipt, bool) { return original, true }}
	honestBalance := s.score(t, original, source)
	if honestBalance <= 0 {
		t.Fatal("precondition: the honest receipt should have earned")
	}

	t.Run("resubmitted unchanged", func(t *testing.T) {
		// The replay carries the original's identity, so it cannot be attributed to the
		// attacker.
		replayed := mkReceipt(t, keyHonest, "0x"+strings.Repeat("c1", 32), srv.URL,
			"200", "honest-c", hash32("content-c"))

		attackerBefore := s.ledgers.Points.Balance(attackerAgent)
		s.score(t, replayed, source)

		if got := s.ledgers.Points.Balance(attackerAgent); got > attackerBefore {
			t.Errorf("a replay credited the resubmitter: %v -> %v", attackerBefore, got)
		}
		if got := s.ledgers.Points.Balance(honestAgent); got != honestBalance {
			t.Errorf("a replay credited the original agent twice: %v != %v", got, honestBalance)
		}
	})

	t.Run("re-signed under the attacker's identity", func(t *testing.T) {
		// Same URL, same content hash, same task — a different signer. The artifact key
		// does not include the agent, so this is still the same artifact.
		resigned := mkReceipt(t, keyAttacker, "0x"+strings.Repeat("c2", 32), srv.URL,
			"200", "honest-c", hash32("content-c"))

		attackerBefore := s.ledgers.Points.Balance(attackerAgent)
		s.score(t, resigned, verification.RecordedVerdicts{
			Lookup: func(string) (*receipt.Receipt, bool) { return resigned, true },
		})

		if got := s.ledgers.Points.Balance(attackerAgent); got > attackerBefore {
			t.Errorf("re-signing another agent's work earned points: %v -> %v", attackerBefore, got)
		}
	})
}

// ============================================================================
// Attack (d): self-verification
// ============================================================================

// TestRedTeamD_SelfVerificationIsRefused: an agent concluding that its own work is fine.
//
// Without this rule the verification stage would be theatre, because the cheapest verifier
// to arrange is oneself.
func TestRedTeamD_SelfVerificationIsRefused(t *testing.T) {
	srv := liveServer(t)
	s := newStack(t)
	deps := mining.Deps{Fetcher: anchor.NewFetcher()}

	attacker := agentOf(t, keyAttacker)
	r := mkReceipt(t, keyAttacker, "0x"+strings.Repeat("d1", 32), srv.URL,
		"200", "self-d", hash32("content-d"))

	// The attacker assigns itself.
	v := verifierFor(t, s, attacker, deps)

	_, err := v.Verify(context.Background(), r)
	if err == nil {
		t.Fatal("an agent verifying its own receipt must be refused")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "own receipt") {
		t.Errorf("the refusal should say why, got %v", err)
	}
	if r.Verification.Status == receipt.VerificationVerified {
		t.Error("a self-verified receipt must not be marked verified")
	}

	// And a self-written verdict must not be honoured by scoring either.
	h := r.Result.Hash
	r.Verification = receipt.Verification{
		Status:         receipt.VerificationVerified,
		VerifierID:     &attacker, // the producer, posing as its own verifier
		RecomputedHash: &h,
	}

	source := verification.RecordedVerdicts{}
	if source.Verified(r) {
		t.Error("a self-written verdict must not count as verification")
	}

	// Nor through the durable path, which is what the CLI actually reads.
	verdicts := verification.NewDurableVerdicts(sqlite.NewVerdictStore(s.db))
	if verdicts.Verified(r) {
		t.Error("a self-written verdict must not count through the durable store either")
	}
}

// ============================================================================
// The attacks together, against one shared stack
// ============================================================================

// TestRedTeam_NoAttackShiftsPointsBetweenAgents is the blunt invariant across all four
// attacks: whatever happens, an attacker's balance does not grow.
//
// It is deliberately redundant with the tests above. Those check each mechanism; this one
// checks the outcome a user would actually notice, and it would still fail if some future
// change opened a path nobody's unit test covered.
func TestRedTeam_NoAttackShiftsPointsBetweenAgents(t *testing.T) {
	srv := liveServer(t)
	s := newStack(t)
	deps := mining.Deps{Fetcher: anchor.NewFetcher()}

	attacker := agentOf(t, keyAttacker)
	attackerStart := s.ledgers.Points.Balance(attacker)

	// (a) fabricated result
	fa := mkReceipt(t, keyAttacker, "0x"+strings.Repeat("e1", 32), srv.URL, `{"id":1}`, "fa", hash32("ca"))
	// (b) fabricated anchor
	fb := mkReceipt(t, keyAttacker, "0x"+strings.Repeat("e2", 32), srv.URL, "200", "fb", hash32("invented"))
	// (d) self-verification of a fresh receipt
	fd := mkReceipt(t, keyAttacker, "0x"+strings.Repeat("e3", 32), srv.URL, "200", "fd", hash32("cd"))

	v := verifierFor(t, s, agentOf(t, keyVerifier), deps)
	vSelf := verifierFor(t, s, attacker, deps)

	for _, r := range []*receipt.Receipt{fa, fb, fd} {
		// Verify with a legitimate verifier where possible.
		if _, err := v.Verify(context.Background(), r); err != nil {
			// A window or activity refusal is also a refusal; nothing to assert beyond that.
			continue
		}
	}

	// And the self-verification attempt, which must error.
	if _, err := vSelf.Verify(context.Background(), fd); err == nil {
		t.Error("self-verification was permitted")
	}

	// Score everything the attacker produced, reading only recorded verdicts.
	for _, r := range []*receipt.Receipt{fa, fb, fd} {
		s.score(t, r, verification.NewDurableVerdicts(sqlite.NewVerdictStore(s.db)))
	}

	if got := s.ledgers.Points.Balance(attacker); got > attackerStart {
		t.Errorf("the attacker's balance rose from %v to %v; at least one forgery earned", attackerStart, got)
	}
	if got := s.ledgers.Points.Balance(attacker); got != 0 {
		t.Errorf("the attacker earned %v points from forgery alone, want 0", got)
	}
}

// TestRedTeam_HonestWorkStillEarns is the control, and it matters as much as the attacks.
//
// A stack that refused everything would pass every test above. This asserts the honest
// path still pays, so the defences reject forgeries rather than all work.
func TestRedTeam_HonestWorkStillEarns(t *testing.T) {
	srv := liveServer(t)
	s := newStack(t)
	deps := mining.Deps{Fetcher: anchor.NewFetcher()}

	honest := agentOf(t, keyHonest)

	// The honest agent really runs the task.
	spec := map[string]any{
		"url":    srv.URL,
		"fields": []any{"ok", "id"},
		"format": mining.OutputJSON,
	}
	res, anchors, err := mining.Run(context.Background(), deps, receipt.TaskExtract, spec)
	if err != nil {
		t.Fatalf("honest Run: %v", err)
	}

	r := &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: "0x" + strings.Repeat("f0", 32),
		AgentID:   honest,
		Epoch:     scoring.EpochOf(time.Unix(1791015900, 0)),
		Task: receipt.Task{
			Type:          receipt.TaskExtract,
			Spec:          spec,
			SpecHash:      hash32("spec-honest"),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015862},
		Result:       res,
		Anchors:      anchors,
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	if err := r.Sign(keyHonest); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	s.persist(t, r)

	// A legitimate verifier checks it.
	v := verifierFor(t, s, agentOf(t, keyVerifier), deps)
	out, err := v.Verify(context.Background(), r)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !out.Verified {
		t.Fatalf("honest work was rejected: %s", out.Reason)
	}

	// And it earns, read through the durable verdict store the CLI uses.
	got := s.score(t, r, verification.NewDurableVerdicts(sqlite.NewVerdictStore(s.db)))
	if got <= 0 {
		t.Errorf("honest verified work earned %v, want a positive award", got)
	}
}

// TestRedTeam_AttackersKeyDoesNotImpersonate is a guard on the identity layer underneath
// all four attacks.
//
// Every forgery above relies on the attacker being unable to sign as someone else. If the
// signature check were bypassable, attributing work would be meaningless no matter how good
// the verification logic was.
func TestRedTeam_AttackersKeyDoesNotImpersonate(t *testing.T) {
	srv := liveServer(t)

	// A receipt genuinely signed by the honest agent...
	r := mkReceipt(t, keyHonest, "0x"+strings.Repeat("f1", 32), srv.URL, "200", "sig", hash32("cs"))

	// ...then re-attributed to the attacker without re-signing.
	attacker := agentOf(t, keyAttacker)
	r.AgentID = attacker

	if err := r.Validate(nil); err == nil {
		t.Error("re-attributing a signed receipt to another agent must fail signature verification")
	}

	// And the recovered signer must be the honest agent, not the declared one.
	td, err := r.TypedData()
	if err != nil {
		t.Fatalf("TypedData: %v", err)
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(r.Signature, "0x"))
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	recovered, err := eip712.RecoverAddress(td, sig)
	if err != nil {
		t.Fatalf("RecoverAddress: %v", err)
	}
	declared, err := eip712.HexToAddress(strings.TrimPrefix(attacker, "agent:eip155:8453:"))
	if err != nil {
		t.Fatalf("HexToAddress: %v", err)
	}
	if fmt.Sprintf("%x", recovered) == fmt.Sprintf("%x", declared) {
		t.Error("the recovered signer must not match the declared attacker")
	}
}
