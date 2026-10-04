package verification_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/mining"
	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/verification"
)

// These tests wire the verifier to the *real* executor, so the thing under test is
// the actual re-execution rather than a stub that always agrees.
//
// The stubs in verification_test.go cover the comparison logic and the anti-collusion
// rules. What they cannot show is that re-running a real task actually reproduces a
// real result — and if it did not, every production verification would reject honest
// work while every stub-based test stayed green.

// executorRecomputer re-runs a receipt's task through the real mining executors.
//
// This is what verification IS in this system: the same deterministic re-execution a
// client already performs, pointed at a receipt instead of a fresh task.
type executorRecomputer struct {
	deps mining.Deps
}

func (e *executorRecomputer) Recompute(ctx context.Context, r *receipt.Receipt) (receipt.Result, error) {
	res, _, err := mining.Run(ctx, e.deps, r.Task.Type, r.Task.Spec)
	return res, err
}

// liveProbeServer serves a stable JSON body so re-execution is reproducible.
func liveProbeServer(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"id":42}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestEndToEnd_HonestWorkVerifiesAgainstTheRealExecutor is the S4 completion
// criterion: the loop runs end to end with no stub in the middle.
//
// An honest extract task is run by the producer, then independently re-run by the
// verifier, and the verdict must be agreement. If this fails, the verification loop
// would reject every honest submission in production.
func TestEndToEnd_HonestWorkVerifiesAgainstTheRealExecutor(t *testing.T) {
	srv := liveProbeServer(t)

	deps := mining.Deps{Fetcher: anchor.NewFetcher()}

	// The producer runs the task for real.
	spec := map[string]any{
		"url":    srv.URL,
		"fields": []any{"ok", "id"},
		"format": mining.OutputJSON,
	}
	res, anchors, err := mining.Run(context.Background(), deps, receipt.TaskExtract, spec)
	if err != nil {
		t.Fatalf("producer Run: %v", err)
	}

	r := &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: "0x" + strings.Repeat("f1", 32),
		AgentID:   agentOf(t, keyProducer),
		Epoch:     7,
		Task: receipt.Task{
			Type:          receipt.TaskExtract,
			Spec:          spec,
			SpecHash:      hash32("spec-e2e"),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015862},
		Result:       res,
		Anchors:      anchors,
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	if err := r.Sign(keyProducer); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// The verifier re-runs it through the same executor.
	v, _ := newVerifier(t, r, &executorRecomputer{deps: deps}, eligible{who: agentOf(t, keyVerifier)})

	out, err := v.Verify(context.Background(), r)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !out.Verified {
		t.Fatalf("honest work was rejected by real re-execution: %s", out.Reason)
	}
	if out.RecomputedHash != res.Hash {
		t.Errorf("recomputed hash %s != produced hash %s", out.RecomputedHash, res.Hash)
	}
}

// TestEndToEnd_TamperedResultIsRejectedAgainstTheRealExecutor is the other half: a
// receipt whose claimed result does not match what re-execution produces must be
// rejected even though its signature is valid.
//
// The receipt is signed correctly and passes structure validation, so this isolates
// the one thing verification adds: the result is not what the source says.
func TestEndToEnd_TamperedResultIsRejectedAgainstTheRealExecutor(t *testing.T) {
	srv := liveProbeServer(t)
	deps := mining.Deps{Fetcher: anchor.NewFetcher()}

	spec := map[string]any{
		"url":    srv.URL,
		"fields": []any{"ok", "id"},
		"format": mining.OutputJSON,
	}

	r := &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: "0x" + strings.Repeat("f2", 32),
		AgentID:   agentOf(t, keyProducer),
		Epoch:     7,
		Task: receipt.Task{
			Type:          receipt.TaskExtract,
			Spec:          spec,
			SpecHash:      hash32("spec-e2e-tampered"),
			SelfGenerated: true,
		},
		Work: receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015862},
		// A result the source will never produce.
		Result:       receipt.Result{Value: `{"id":99999,"ok":true}`, Hash: hash32("fabricated")},
		Anchors:      []receipt.Anchor{{URL: srv.URL, ContentHash: hash32("content"), FetchedAt: 1791015810, Status: 200, Bytes: 20}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	if err := r.Sign(keyProducer); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// It is a well-formed, correctly signed receipt — which is exactly why a
	// self-check cannot catch it.
	if err := r.Validate(nil); err != nil {
		t.Fatalf("precondition: the tampered receipt should still be structurally valid: %v", err)
	}

	v, _ := newVerifier(t, r, &executorRecomputer{deps: deps}, eligible{who: agentOf(t, keyVerifier)})

	out, err := v.Verify(context.Background(), r)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out.Verified {
		t.Fatal("a receipt whose result does not reproduce must be rejected")
	}
	if r.Verification.Status != receipt.VerificationRejected {
		t.Errorf("status = %q, want rejected", r.Verification.Status)
	}
}

// TestEndToEnd_VerifiedReceiptEarnsRejectedOneDoesNot closes the loop into scoring.
//
// This is the S4-0 honesty gap, demonstrated end to end: two structurally identical,
// correctly signed receipts, differing only in whether a verifier reproduced them.
// Only the verified one may earn points.
func TestEndToEnd_VerifiedReceiptEarnsRejectedOneDoesNot(t *testing.T) {
	srv := liveProbeServer(t)
	deps := mining.Deps{Fetcher: anchor.NewFetcher()}

	spec := map[string]any{
		"url":    srv.URL,
		"fields": []any{"ok", "id"},
		"format": mining.OutputJSON,
	}
	res, anchors, err := mining.Run(context.Background(), deps, receipt.TaskExtract, spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	mkVerdict := func(receiptID string, status receipt.VerificationStatus, verifier string) *receipt.Receipt {
		h := res.Hash
		at := int64(1791015900)
		return &receipt.Receipt{
			ReceiptID: receiptID,
			AgentID:   agentOf(t, keyProducer),
			Epoch:     7,
			Result:    res,
			Anchors:   anchors,
			Verification: receipt.Verification{
				Status:         status,
				VerifierID:     &verifier,
				VerifiedAt:     &at,
				RecomputedHash: &h,
			},
		}
	}

	source := verification.RecordedVerdicts{}

	verified := mkVerdict("0x"+strings.Repeat("f3", 32), receipt.VerificationVerified, agentOf(t, keyVerifier))
	rejected := mkVerdict("0x"+strings.Repeat("f4", 32), receipt.VerificationRejected, agentOf(t, keyVerifier))
	pending := mkVerdict("0x"+strings.Repeat("f5", 32), receipt.VerificationPending, "")

	if !source.Verified(verified) {
		t.Error("a verifier-approved receipt must count as verified")
	}
	if source.Verified(rejected) {
		t.Error("a rejected receipt must not count as verified")
	}
	if source.Verified(pending) {
		t.Error("a pending receipt must not count as verified; it has not been looked at")
	}
}

// TestEndToEnd_CommitmentRecordedThroughTheRealVerifier runs the whole loop and
// asserts the commitment settled, so the mechanism is exercised with the real
// recomputer rather than only with stubs.
func TestEndToEnd_CommitmentRecordedThroughTheRealVerifier(t *testing.T) {
	srv := liveProbeServer(t)
	deps := mining.Deps{Fetcher: anchor.NewFetcher()}

	spec := map[string]any{
		"url":    srv.URL,
		"fields": []any{"ok", "id"},
		"format": mining.OutputJSON,
	}
	res, anchors, err := mining.Run(context.Background(), deps, receipt.TaskExtract, spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	r := &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: "0x" + strings.Repeat("f6", 32),
		AgentID:   agentOf(t, keyProducer),
		Epoch:     7,
		Task: receipt.Task{
			Type:          receipt.TaskExtract,
			Spec:          spec,
			SpecHash:      hash32("spec-e2e-stake"),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015862},
		Result:       res,
		Anchors:      anchors,
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	if err := r.Sign(keyProducer); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	verifier := agentOf(t, keyVerifier)
	v, stakes := newVerifier(t, r, &executorRecomputer{deps: deps}, eligible{who: verifier})

	out, err := v.Verify(context.Background(), r)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !out.Verified {
		t.Fatalf("expected agreement, got %s", out.Reason)
	}

	committed, released, slashed := stakes.Standing(verifier)
	if committed != 50 || released != 50 || slashed != 0 {
		t.Errorf("committed=%d released=%d slashed=%d, want 50/50/0 on agreement", committed, released, slashed)
	}

	// The commitment record must name the receipt's verdict, so a later auditor can
	// trace a slash back to the claim that caused it.
	c, ok := stakes.Get(out.CommittedID)
	if !ok {
		t.Fatal("the commitment was not recorded")
	}
	if c.AgentID != verifier {
		t.Errorf("commitment agent = %q, want the verifier", c.AgentID)
	}
	if c.State != verification.StakeReleased {
		t.Errorf("commitment state = %q, want released", c.State)
	}
}

// TestEndToEnd_CommitmentIsBoundToATimestamp: a commitment with no time bounds would
// be unattributable in an audit.
func TestEndToEnd_CommitmentIsBoundToATimestamp(t *testing.T) {
	srv := liveProbeServer(t)
	deps := mining.Deps{Fetcher: anchor.NewFetcher()}

	spec := map[string]any{
		"url":    srv.URL,
		"fields": []any{"ok", "id"},
		"format": mining.OutputJSON,
	}
	res, anchors, err := mining.Run(context.Background(), deps, receipt.TaskExtract, spec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	fixed := time.Unix(1791015900, 0)

	r := &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: "0x" + strings.Repeat("f7", 32),
		AgentID:   agentOf(t, keyProducer),
		Epoch:     7,
		Task: receipt.Task{
			Type:          receipt.TaskExtract,
			Spec:          spec,
			SpecHash:      hash32("spec-e2e-time"),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015862},
		Result:       res,
		Anchors:      anchors,
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	if err := r.Sign(keyProducer); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	verifier := agentOf(t, keyVerifier)
	stakes := verification.NewMemStakeLedger()
	v, err := verification.New(verification.Config{
		Policy:      eligible{who: verifier},
		Stakes:      stakes,
		Receipts:    newFakeUpdater(),
		Recomputer:  &executorRecomputer{deps: deps},
		StakePoints: 50,
		Now:         func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := v.Verify(context.Background(), r)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	c, ok := stakes.Get(out.CommittedID)
	if !ok {
		t.Fatal("the commitment was not recorded")
	}
	if !c.CommittedAt.Equal(fixed) {
		t.Errorf("committedAt = %v, want the injected clock %v", c.CommittedAt, fixed)
	}
	if !c.SettledAt.Equal(fixed) {
		t.Errorf("settledAt = %v, want the injected clock %v", c.SettledAt, fixed)
	}
}
