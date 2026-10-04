package verification_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/verification"
)

// These tests cover S9-8: the recompute verifier must accept a task that declares
// `recompute`, and must REFUSE one that declares a mode it cannot check.
//
// The refusal is the important half. A verifier that silently fell back to
// recomputing an `evaluator` task would report a binary verdict for something that
// was never eligible for one, which is worse than no verdict: it would launder a
// weak claim into a strong one.

// modeReceipt builds a signed receipt declaring the given verification mode.
//
// The receipt id is derived from the signed bytes, so the mode has to be set before
// signing and the id computed after — a hardcoded id would fail its own id check and
// mask what these tests assert.
func modeReceipt(t *testing.T, mode receipt.VerificationMode) *receipt.Receipt {
	t.Helper()
	r := mkReceipt(t, keyProducer, "", "https://example.com/mode", "200")
	r.Task.Verification = mode

	id, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	r.ReceiptID = id
	if err := r.Sign(keyProducer); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return r
}

// verifierFor wires a verifier whose recomputer reproduces the receipt it is given,
// so a receipt that reaches the comparison step verifies.
func verifierFor(t *testing.T, r *receipt.Receipt, rec verification.Recomputer) *verification.Verifier {
	t.Helper()
	v, _ := newVerifier(t, r, rec, eligible{who: "agent:eip155:8453:0x00000000000000000000000000000000000000f1"})
	return v
}

// TestVerify_RecomputeModeIsAccepted is the control: the mode this verifier
// implements must go through the normal path.
func TestVerify_RecomputeModeIsAccepted(t *testing.T) {
	r := modeReceipt(t, receipt.VerificationRecompute)

	// A recomputer that echoes the receipt's own result, so reproduction succeeds.
	rec := &fakeRecomputer{result: r.Result}
	v := verifierFor(t, r, rec)

	out, err := v.Verify(context.Background(), r)
	if err != nil {
		t.Fatalf("a recompute receipt must be verifiable, got: %v", err)
	}
	if !out.Verified {
		t.Errorf("the fixture reproduces, so it must verify: %s", out.Reason)
	}
	if rec.calls != 1 {
		t.Errorf("the recomputer ran %d times, want exactly 1", rec.calls)
	}
}

// TestVerify_EmptyModeIsTreatedAsRecompute is the compatibility rule.
//
// A receipt signed before the field existed has no mode. Every such receipt was
// checked by re-running, so empty means recompute — not "unknown". Treating it as
// unsupported would make every historical receipt unverifiable, which would break
// acceptance criterion ② (a receipt stays verifiable offline, forever).
func TestVerify_EmptyModeIsTreatedAsRecompute(t *testing.T) {
	r := modeReceipt(t, "")
	if r.Task.Verification != "" {
		t.Fatal("the fixture must leave the mode unset")
	}

	rec := &fakeRecomputer{result: r.Result}
	v := verifierFor(t, r, rec)

	out, err := v.Verify(context.Background(), r)
	if err != nil {
		t.Fatalf("a receipt with no declared mode must be treated as recompute: %v", err)
	}
	if !out.Verified {
		t.Errorf("it must verify like any recompute task: %s", out.Reason)
	}
}

// TestVerify_EvaluatorModeIsRefused is the central S9-8 property.
func TestVerify_EvaluatorModeIsRefused(t *testing.T) {
	r := modeReceipt(t, receipt.VerificationEvaluator)

	rec := &fakeRecomputer{result: r.Result}
	v := verifierFor(t, r, rec)

	_, err := v.Verify(context.Background(), r)
	if err == nil {
		t.Fatal("a recompute verifier must refuse an evaluator task: " +
			"re-running it would produce a comparison that need not match, and reporting a " +
			"binary verdict anyway would launder a weak claim into a strong one")
	}
	if !errors.Is(err, verification.ErrModeNotSupported) {
		t.Errorf("a mode mismatch must be ErrModeNotSupported so a caller can route it, got: %v", err)
	}
}

// TestVerify_DisputeModeIsRefused covers the reserved mode: it is valid in the
// schema but not implemented in 2.0 (MVP.md §5.0 puts it in 2.1).
func TestVerify_DisputeModeIsRefused(t *testing.T) {
	r := modeReceipt(t, receipt.VerificationDispute)

	v := verifierFor(t, r, &fakeRecomputer{result: r.Result})
	_, err := v.Verify(context.Background(), r)
	if err == nil {
		t.Fatal("a dispute task must not be verified by the recompute verifier")
	}
	if !errors.Is(err, verification.ErrModeNotSupported) {
		t.Errorf("want ErrModeNotSupported, got: %v", err)
	}
}

// TestVerify_ModeRefusalHappensBeforeExpensiveWork confirms the gate is early.
//
// The mode is known from the receipt's own fields, so refusing should cost nothing.
// Checking it after re-execution would mean paying for a network fetch and a task
// run before discarding the answer — and would let a caller that ignored the error
// receive a populated Outcome.
func TestVerify_ModeRefusalHappensBeforeExpensiveWork(t *testing.T) {
	r := modeReceipt(t, receipt.VerificationEvaluator)

	rec := &fakeRecomputer{result: r.Result}
	v := verifierFor(t, r, rec)

	if _, err := v.Verify(context.Background(), r); err == nil {
		t.Fatal("expected a refusal")
	}
	if rec.calls != 0 {
		t.Errorf("the recomputer ran %d times for an unsupported mode; "+
			"the mode gate must come first", rec.calls)
	}
}

// TestVerify_ModeRefusalIsDistinctFromAFailedReceipt is the classification boundary.
//
// A mode mismatch and a receipt that fails verification are different problems. An
// operator who cannot tell them apart will look for a forger when the real issue is
// that the wrong verifier was asked.
func TestVerify_ModeRefusalIsDistinctFromAFailedReceipt(t *testing.T) {
	// A recompute receipt that genuinely does not reproduce: a real failure, reported
	// as a verdict rather than an error.
	bad := modeReceipt(t, receipt.VerificationRecompute)
	rec := &fakeRecomputer{result: receipt.Result{Value: "999", Hash: hash32("different")}}
	v := verifierFor(t, bad, rec)

	out, err := v.Verify(context.Background(), bad)
	if err != nil {
		t.Fatalf("a non-reproducing receipt is a verdict, not an error: %v", err)
	}
	if out.Verified {
		t.Fatal("the non-reproducing receipt must not verify")
	}
	if out.Reason == "" {
		t.Error("a rejection must explain itself")
	}

	// A mode mismatch: an error, and a distinguishable one.
	_, err = v.Verify(context.Background(), modeReceipt(t, receipt.VerificationEvaluator))
	if !errors.Is(err, verification.ErrModeNotSupported) {
		t.Errorf("the two situations must be distinguishable, got: %v", err)
	}
	if errors.Is(err, verification.ErrSelfVerification) {
		t.Error("a mode mismatch must not be reported as a self-verification problem")
	}
}

// TestVerify_ModeRefusalNamesBothModes keeps the error actionable: the operator needs
// to know which verifier to use, not merely that this one declined.
func TestVerify_ModeRefusalNamesBothModes(t *testing.T) {
	r := modeReceipt(t, receipt.VerificationEvaluator)
	v := verifierFor(t, r, &fakeRecomputer{result: r.Result})

	_, err := v.Verify(context.Background(), r)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	msg := err.Error()
	if !strings.Contains(msg, "recompute") {
		t.Errorf("the error must name the mode this verifier implements, got: %q", msg)
	}
	if !strings.Contains(msg, "evaluator") {
		t.Errorf("the error must name the mode the receipt declares, got: %q", msg)
	}
}
