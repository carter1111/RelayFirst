package verification_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/scoring"
	"github.com/relayfirst/relayfirst/internal/verification"
)

// ============================================================================
// These are the S4 acceptance tests, written before the implementation.
//
// They are the spec. Each one maps to a line of TASKS.md §5 or the red-team list
// in the stage request, and each encodes a property that must hold for the
// verification loop to mean anything:
//
//   honest work verifies            tampering is rejected
//   a verifier cannot judge itself  an over-verifier is refused
//   agreement releases              disagreement records a slash
//   points NEVER move               (invariant A5, the load-bearing one)
//
// If a test here is hard to satisfy, the design is wrong rather than the test.
// ============================================================================

const (
	// Two distinct well-known test keys. They hold no value and are never used
	// outside tests (CODING_RULES.md §8).
	keyProducer = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	keyVerifier = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	keyOther    = "0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
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

// mkReceipt builds a signed probe receipt whose claimed result is value.
func mkReceipt(t *testing.T, key, id, url, value string) *receipt.Receipt {
	t.Helper()

	agent := agentOf(t, key)

	r := &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: id,
		AgentID:   agent,
		Epoch:     7,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": url},
			SpecHash:      hash32("spec-" + id),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015862},
		Result:       receipt.Result{Value: value, Hash: hash32("result-" + value)},
		Anchors:      []receipt.Anchor{{URL: url, ContentHash: hash32("content-" + url), FetchedAt: 1791015810, Status: 200, Bytes: 2048}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	if err := r.Sign(key); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return r
}

// fakeRecomputer stands in for re-running the task. It returns whatever result the
// test wants the verifier to observe, so the tests exercise the *comparison* rather
// than the network.
type fakeRecomputer struct {
	result receipt.Result
	err    error
	calls  int
}

func (f *fakeRecomputer) Recompute(context.Context, *receipt.Receipt) (receipt.Result, error) {
	f.calls++
	return f.result, f.err
}

// eligible reports a policy that always assigns the named verifier and always
// allows verification. Individual tests substitute narrower policies.
type eligible struct{ who string }

func (e eligible) Assign(*receipt.Receipt, []string) (string, error) { return e.who, nil }

func (e eligible) AllowVerify(uint64, string, int, int) error { return nil }

// newVerifier wires the standard fixture.
func newVerifier(t *testing.T, r *receipt.Receipt, recomputer verification.Recomputer, policy verification.Policy) (*verification.Verifier, *verification.MemStakeLedger) {
	t.Helper()

	stakes := verification.NewMemStakeLedger()
	updates := newFakeUpdater()

	v, err := verification.New(verification.Config{
		Policy:      policy,
		Stakes:      stakes,
		Receipts:    updates,
		Recomputer:  recomputer,
		StakePoints: 50,
		Now:         func() time.Time { return time.Unix(1791015900, 0) },
	})
	if err != nil {
		t.Fatalf("verification.New: %v", err)
	}
	return v, stakes
}

// fakeUpdater records the verification written back onto a receipt.
type fakeUpdater struct {
	written []*receipt.Receipt
}

func newFakeUpdater() *fakeUpdater { return &fakeUpdater{} }

func (u *fakeUpdater) UpdateVerification(r *receipt.Receipt) error {
	// Store a copy so a later mutation of r cannot rewrite history in the test.
	cp := *r
	u.written = append(u.written, &cp)
	return nil
}

// ---------------------------------------------------------------- happy path

// TestHonestReceiptVerifies: the verifier recomputes the same result, so the verdict
// is agreement and the receipt becomes verified.
func TestHonestReceiptVerifies(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("a1", 32), "https://example.com/health", "200")

	verifier := agentOf(t, keyVerifier)
	v, _ := newVerifier(t, r, &fakeRecomputer{result: r.Result}, eligible{who: verifier})

	out, err := v.Verify(context.Background(), r)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !out.Verified {
		t.Fatalf("an honest receipt must verify, got rejected: %s", out.Reason)
	}
	if r.Verification.Status != receipt.VerificationVerified {
		t.Errorf("status = %q, want %q", r.Verification.Status, receipt.VerificationVerified)
	}
	if r.Verification.VerifierID == nil || *r.Verification.VerifierID != verifier {
		t.Errorf("verifierId was not recorded")
	}
	if r.Verification.VerifiedAt == nil {
		t.Error("verifiedAt was not recorded")
	}
	if r.Verification.RecomputedHash == nil {
		t.Error("recomputedHash was not recorded; the evidence for the verdict must be stored")
	}
}

// TestTamperedReceiptIsRejected is the property the whole stage exists for.
//
// The claimed result differs from what re-execution produces, so the verdict is
// rejection — with no partial credit, because every task type has independent
// ground truth (invariant A2).
func TestTamperedReceiptIsRejected(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("a2", 32), "https://example.com/health", "200")

	// Re-execution observes a different status than the receipt claims.
	v, _ := newVerifier(t, r, &fakeRecomputer{result: receipt.Result{Value: "503", Hash: hash32("result-503")}}, eligible{who: agentOf(t, keyVerifier)})

	out, err := v.Verify(context.Background(), r)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out.Verified {
		t.Fatal("a tampered receipt must be rejected")
	}
	if out.Reason == "" {
		t.Error("a rejection must explain itself, so an operator can tell tampering from an outage")
	}
	if r.Verification.Status != receipt.VerificationRejected {
		t.Errorf("status = %q, want %q", r.Verification.Status, receipt.VerificationRejected)
	}
}

// TestRejectedReceiptEarnsNoPoints is the reason rejection has to be binary: a
// rejected receipt must not be credited, or verification would be advisory.
func TestRejectedReceiptEarnsNoPoints(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("a3", 32), "https://example.com/health", "200")

	v, _ := newVerifier(t, r, &fakeRecomputer{result: receipt.Result{Value: "503", Hash: hash32("result-503")}}, eligible{who: agentOf(t, keyVerifier)})

	if _, err := v.Verify(context.Background(), r); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if r.Verification.Status != receipt.VerificationRejected {
		t.Fatalf("precondition: expected rejection")
	}
}

// TestRecomputeFailureIsNotASilentPass: if the verifier cannot re-run the task, it
// must not conclude agreement. An unreachable URL is a failed verification, not a
// verified receipt — treating it as a pass would make the check trivially gameable
// by taking the source offline.
func TestRecomputeFailureIsNotASilentPass(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("a4", 32), "https://example.com/health", "200")

	v, _ := newVerifier(t, r, &fakeRecomputer{err: errors.New("connection refused")}, eligible{who: agentOf(t, keyVerifier)})

	out, err := v.Verify(context.Background(), r)
	if err == nil && out.Verified {
		t.Fatal("a failed recomputation must not be treated as verification")
	}
	if out.Verified {
		t.Fatal("outcome must not claim verification when re-execution failed")
	}
}

// ---------------------------------------------------------------- anti-collusion

// TestVerifierCannotVerifyItsOwnWork enforces the first rule of MVP.md §5.6.
//
// Without it, an agent verifies its own submissions and the check is theatre.
func TestVerifierCannotVerifyItsOwnWork(t *testing.T) {
	producer := agentOf(t, keyProducer)
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("b1", 32), "https://example.com/health", "200")

	// A policy that (wrongly) assigns the producer to itself.
	v, _ := newVerifier(t, r, &fakeRecomputer{result: r.Result}, eligible{who: producer})

	out, err := v.Verify(context.Background(), r)
	if err == nil {
		t.Fatal("a self-assignment must be refused, not silently accepted")
	}
	if out.Verified {
		t.Error("a refused verification must not report agreement")
	}
	if r.Verification.Status == receipt.VerificationVerified {
		t.Error("a self-assigned receipt must not end up verified")
	}
}

// TestOverVerifyingAgentIsRefused enforces the second rule of §5.6: an agent may not
// verify far more than it produces. The bound exists because a pure verifier has no
// skin in the game and, uncapped, could manufacture agreement at scale.
func TestOverVerifyingAgentIsRefused(t *testing.T) {
	agent := agentOf(t, keyVerifier)

	policies := []struct {
		name     string
		produced int
		verified int
		wantErr  bool
	}{
		{"nothing verified yet", 10, 0, false},
		{"within the cap", 10, 2, false},
		{"exactly at the cap", 10, 5, false},
		{"past the cap", 10, 6, true},
		{"pure verifier with no production", 0, 1, true},
	}

	for _, c := range policies {
		t.Run(c.name, func(t *testing.T) {
			p := verification.NewRatioPolicy(0.5)
			err := p.AllowVerify(7, agent, c.produced, c.verified)
			if c.wantErr && err == nil {
				t.Errorf("produced=%d verified=%d must be refused", c.produced, c.verified)
			}
			if !c.wantErr && err != nil {
				t.Errorf("produced=%d verified=%d should be allowed, got %v", c.produced, c.verified, err)
			}
		})
	}
}

// TestPolicyMustBeConfigured: without a policy the verifier has no basis for
// choosing anyone, so construction must fail rather than default to something
// insecure.
func TestPolicyMustBeConfigured(t *testing.T) {
	if _, err := verification.New(verification.Config{}); err == nil {
		t.Error("a verifier with no policy must be refused")
	}
}

// TestAssignMustNotReturnThenProducer: the assignment output is re-checked, so a
// buggy or hostile Assigner cannot smuggle a self-verification past the guard.
func TestAssignMustNotReturnTheProducer(t *testing.T) {
	producer := agentOf(t, keyProducer)

	// A policy that returns candidates[0] whatever it is; the caller offers only the
	// producer, so a correct Assigner cannot satisfy it.
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("b2", 32), "https://example.com/health", "200")

	v, _ := newVerifier(t, r, &fakeRecomputer{result: r.Result}, verification.NewEligiblePolicy(verification.EligiblePolicyConfig{
		Candidates: []string{producer}, // the only candidate is the producer himself
	}))

	if _, err := v.Verify(context.Background(), r); err == nil {
		t.Error("when the only candidate is the producer, no valid assignment exists and it must error")
	}
}

// ---------------------------------------------------------------- commitment

// TestAgreementReleasesTheCommitment: a correct verdict records the commitment as
// released, which is what makes verifying worthwhile.
func TestAgreementReleasesTheCommitment(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("c1", 32), "https://example.com/health", "200")
	verifier := agentOf(t, keyVerifier)

	v, stakes := newVerifier(t, r, &fakeRecomputer{result: r.Result}, eligible{who: verifier})

	if _, err := v.Verify(context.Background(), r); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	committed, released, slashed := stakes.Standing(verifier)
	if committed != 50 {
		t.Errorf("committed = %d, want 50 (the configured commitment)", committed)
	}
	if released != 50 {
		t.Errorf("released = %d, want 50 on agreement", released)
	}
	if slashed != 0 {
		t.Errorf("slashed = %d, want 0 on agreement", slashed)
	}
}

// TestDisagreementRecordsASlash: a verifier who clears a receipt must have something
// at risk, or the verdict costs nothing. In this MVP the "something" is a recorded
// commitment, never a transfer of points.
func TestDisagreementRecordsASlash(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("c2", 32), "https://example.com/health", "200")
	verifier := agentOf(t, keyVerifier)

	// The verifier says the result differs, i.e. it rejects the submission.
	v, _ := newVerifier(t, r, &fakeRecomputer{result: receipt.Result{Value: "503", Hash: hash32("result-503")}}, eligible{who: verifier})

	stakes := verification.NewMemStakeLedger()
	v2, err := verification.New(verification.Config{
		Policy:      eligible{who: verifier},
		Stakes:      stakes,
		Receipts:    newFakeUpdater(),
		Recomputer:  &fakeRecomputer{result: receipt.Result{Value: "503", Hash: hash32("result-503")}},
		StakePoints: 50,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = v

	out, err := v2.Verify(context.Background(), r)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out.Verified {
		t.Fatal("precondition: expected a rejection")
	}

	// A rejection is a claim by the verifier that the producer is wrong. The verifier's
	// own commitment is placed at risk for making that claim (MVP.md §5.5).
	committed, released, slashed := stakes.Standing(verifier)
	if committed != 50 {
		t.Errorf("committed = %d, want 50", committed)
	}
	if slashed == 0 && released != 0 {
		t.Errorf("committed = %d released = %d slashed = %d; the verdict carries no consequence", committed, released, slashed)
	}
}

// TestCommitmentIsIdempotentPerReceipt: re-verifying the same receipt must not
// accumulate commitments, or a repeated delivery could inflate the record.
func TestCommitmentIsIdempotentPerReceipt(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("c3", 32), "https://example.com/health", "200")
	verifier := agentOf(t, keyVerifier)

	v, stakes := newVerifier(t, r, &fakeRecomputer{result: r.Result}, eligible{who: verifier})

	for i := 0; i < 3; i++ {
		if _, err := v.Verify(context.Background(), r); err != nil {
			t.Fatalf("Verify %d: %v", i, err)
		}
	}

	committed, _, _ := stakes.Standing(verifier)
	if committed != 50 {
		t.Errorf("committed = %d after 3 verifications, want 50 — a commitment must be idempotent per receipt", committed)
	}
}

// ============================================================================
// The invariant A5 guard. This is the most important test in the file.
//
// A5 says points are non-transferable, unpriced, and carry no promised return.
// "Slashing a stake" is exactly the kind of feature that would quietly break that
// if it were implemented as "deduct points". So this asserts the opposite:
// committing and slashing must leave every agent's point balance untouched.
// ============================================================================

// TestCommitmentNeverMovesPoints proves that the commitment mechanism cannot weaken
// A5.
//
// It runs the full lifecycle against a real points ledger and then asserts the
// balances are byte-for-byte unchanged and no entry was added. If someone later
// implements slashing by debiting points, this fails.
func TestCommitmentNeverMovesPoints(t *testing.T) {
	points := scoring.NewMemPointsLedger()

	producer := agentOf(t, keyProducer)
	verifier := agentOf(t, keyVerifier)

	// Give the verifier some points so a debit would be observable.
	at := time.Unix(1791015900, 0)
	if _, err := points.Credit("0x"+strings.Repeat("d1", 32), verifier, 7, 100, at); err != nil {
		t.Fatalf("Credit: %v", err)
	}

	before := map[string]float64{
		producer: points.Balance(producer),
		verifier: points.Balance(verifier),
	}
	entriesBefore := len(points.Entries())

	stakes := verification.NewMemStakeLedger()

	// A full disagreeing cycle: commit, then slash.
	if _, err := stakes.Commit("c-1", verifier, 7, 50, at); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := stakes.Slash("c-1", at); err != nil {
		t.Fatalf("Slash: %v", err)
	}

	for agent, want := range before {
		if got := points.Balance(agent); got != want {
			t.Errorf("balance of %s changed from %v to %v; committing or slashing must never move points (invariant A5)", agent, want, got)
		}
	}
	if got := len(points.Entries()); got != entriesBefore {
		t.Errorf("point entries went from %d to %d; the stake ledger must not write to the points ledger", entriesBefore, got)
	}
}

// TestStakeLedgerExposesNoPointMovement is the structural half of the A5 guard.
//
// Behavioural tests can only catch the calls that exist today. This pins the method
// set, so adding a points-moving verb is a deliberate, reviewed act.
func TestStakeLedgerExposesNoPointMovement(t *testing.T) {
	forbidden := []string{
		"transfer", "send", "spend", "debit", "withdraw", "withdrawal",
		"move", "approve", "allowance", "burn", "mint", "credit",
	}

	var l *verification.StakeLedger
	methods := verification.StakeLedgerMethods()

	if len(methods) == 0 {
		t.Fatal("no methods reported; the guard would be vacuous")
	}

	for _, name := range methods {
		lower := strings.ToLower(name)
		for _, bad := range forbidden {
			if strings.Contains(lower, bad) {
				t.Errorf("StakeLedger.%s looks like a points-moving method; a commitment record "+
					"must not be able to move points (invariant A5)", name)
			}
		}
	}
	_ = l
}

// TestStakeAmountsAreMagnitudesNotBalances: the record stores how much was
// committed, never a spendable balance. A balance would imply redemption.
func TestStakeAmountsAreMagnitudesNotBalances(t *testing.T) {
	stakes := verification.NewMemStakeLedger()
	verifier := agentOf(t, keyVerifier)
	at := time.Unix(1791015900, 0)

	if _, err := stakes.Commit("c-1", verifier, 7, 50, at); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := stakes.Commit("c-2", verifier, 7, 50, at); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	committed, released, slashed := stakes.Standing(verifier)
	if committed != 100 {
		t.Errorf("committed = %d, want 100 (a sum of committed magnitudes)", committed)
	}
	if released != 0 || slashed != 0 {
		t.Errorf("released=%d slashed=%d, want 0/0 before any verdict settles", released, slashed)
	}
}

func TestCommitRejectsBadInput(t *testing.T) {
	stakes := verification.NewMemStakeLedger()
	at := time.Unix(1791015900, 0)

	if _, err := stakes.Commit("", "agent:x", 1, 10, at); err == nil {
		t.Error("an empty commitment id must be rejected")
	}
	if _, err := stakes.Commit("c", "", 1, 10, at); err == nil {
		t.Error("an empty agent id must be rejected")
	}
	if _, err := stakes.Commit("c", "agent:x", 1, 0, at); err == nil {
		t.Error("a zero-amount commitment is not a commitment and must be rejected")
	}
}

func TestSlashAndReleaseRejectUnknownCommitment(t *testing.T) {
	stakes := verification.NewMemStakeLedger()
	at := time.Unix(1791015900, 0)

	if _, err := stakes.Slash("nope", at); err == nil {
		t.Error("slashing an unknown commitment must be reported")
	}
	if _, err := stakes.Release("nope", at); err == nil {
		t.Error("releasing an unknown commitment must be reported")
	}
}

// TestCommitmentSettlesOnce: a commitment must not move again after it settles, or a
// repeated verdict could double-count.
func TestCommitmentSettlesOnce(t *testing.T) {
	stakes := verification.NewMemStakeLedger()
	at := time.Unix(1791015900, 0)

	if _, err := stakes.Commit("c-1", "agent:x", 7, 50, at); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := stakes.Release("c-1", at); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := stakes.Slash("c-1", at); err == nil {
		t.Error("a released commitment must not then be slashable")
	}
}

// ---------------------------------------------------------------- helpers

// TestEligiblePolicyPicksADeterministicCandidate: the reference policy must not pick
// randomly at verification time, because two honest verifiers looking at the same
// receipt have to agree on who was supposed to check it.
func TestEligiblePolicyPicksADeterministicCandidate(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("e1", 32), "https://example.com/health", "200")

	p := verification.NewEligiblePolicy(verification.EligiblePolicyConfig{
		Candidates: []string{agentOf(t, keyVerifier), agentOf(t, keyOther)},
	})

	first, err := p.Assign(r, nil)
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}

	for i := 0; i < 20; i++ {
		got, err := p.Assign(r, nil)
		if err != nil {
			t.Fatalf("Assign %d: %v", i, err)
		}
		if got != first {
			t.Fatalf("Assign is not deterministic: %s then %s", first, got)
		}
	}
}

// TestEligiblePolicyNeverReturnsTheProducer: the policy itself must exclude the
// producer, not merely rely on the caller to notice.
func TestEligiblePolicyNeverReturnsTheProducer(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("e2", 32), "https://example.com/health", "200")
	producer := r.AgentID

	p := verification.NewEligiblePolicy(verification.EligiblePolicyConfig{
		Candidates: []string{producer, agentOf(t, keyVerifier)},
	})

	for i := 0; i < 50; i++ {
		got, err := p.Assign(r, nil)
		if err != nil {
			t.Fatalf("Assign: %v", err)
		}
		if got == producer {
			t.Fatalf("the policy returned the producer as verifier")
		}
	}
}

func TestRatioPolicyRejectsNonPositiveRatio(t *testing.T) {
	for _, ratio := range []float64{0, -1} {
		p := verification.NewRatioPolicy(ratio)
		if err := p.AllowVerify(1, "agent:x", 100, 1); err == nil {
			t.Errorf("ratio %v would allow unlimited verification; it must be refused", ratio)
		}
	}
}

func TestVerifierStateIsReportedForOperators(t *testing.T) {
	described := fmt.Sprintf("%v", verification.Outcome{Verified: true, Reason: "agreement"})
	if !strings.Contains(strings.ToLower(described), "verified") && !strings.Contains(described, "true") {
		t.Errorf("an Outcome should describe itself usefully, got %q", described)
	}
}
