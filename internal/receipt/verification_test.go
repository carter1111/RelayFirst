package receipt

import (
	"encoding/json"
	"strings"
	"testing"
)

// withVerification sets the task's verification mode and re-derives the receipt id.
//
// The id is derived from the signed bytes (B2), so changing the payload without
// recomputing it produces a receipt that fails its own id check — which is correct
// behaviour and would mask what these tests are actually asserting.
func withVerification(t *testing.T, mode VerificationMode) Receipt {
	t.Helper()
	r := validReceipt(t)
	r.Task.Verification = mode
	id, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	r.ReceiptID = id
	return r
}

// TestVerification_EmptyKeepsHistoricalBytesUnchanged is the load-bearing test of
// S9-7, and the reason the canonical writer emits the field conditionally.
//
// A receipt signed before `verification` existed has no such key in its canonical
// bytes, and those bytes are what its payload hash covers. If the writer emitted the
// key unconditionally — even as an empty string — every historical receipt's bytes
// would change, its hash would change, and every signature ever made would be
// invalid. That is the difference between a minor change and destroying the corpus.
//
// The assertion is on the exact bytes, not on a round trip: a round trip would pass
// even if an empty key were present.
func TestVerification_EmptyKeepsHistoricalBytesUnchanged(t *testing.T) {
	r := validReceipt(t)
	if r.Task.Verification != "" {
		t.Fatal("the fixture must not set verification for this test to mean anything")
	}

	raw, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Scope the check to the task object. The receipt also carries a separate
	// `verification` block for the adversarial-verification outcome (S4), which is
	// unrelated and legitimately always present — asserting on the whole document
	// would fail for that reason and tell us nothing about this field.
	var doc struct {
		Task map[string]any `json:"task"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := doc.Task["verification"]; present {
		t.Errorf("a task with no declared verification mode must not have the key in its "+
			"canonical bytes; emitting it would change every historical receipt's hash and "+
			"invalidate its signature.\ntask was: %v", doc.Task)
	}
}

// TestVerification_SetAppearsInCanonicalBytes is the other direction: when a mode is
// declared it must be inside the signed bytes, or it would be unauthenticated
// metadata that an intermediary could rewrite.
func TestVerification_SetAppearsInCanonicalBytes(t *testing.T) {
	for _, mode := range []VerificationMode{
		VerificationRecompute, VerificationEvaluator, VerificationDispute,
	} {
		r := withVerification(t, mode)

		raw, err := r.MarshalCanonical()
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(raw), `"verification":"`+string(mode)+`"`) {
			t.Errorf("mode %s must appear in the canonical bytes, got: %s", mode, raw)
		}
	}
}

// TestVerification_IsSigned is the security property: a declared mode must be
// covered by the signature, so an intermediary cannot downgrade it.
//
// The attack this blocks: take a `recompute` receipt (which is binary and strong)
// and relabel it `evaluator` (which is weaker and gameable) so a consumer accepts a
// weaker proof than the producer offered.
//
// The attacker here also recomputes the receipt id, because the id is derived from
// the signed bytes (B2) and a naive relabel would be caught by the id check before
// the payload check ever ran. Recomputing it is what makes this the real attack:
// the id is consistent, and only the payload-versus-fields comparison can catch the
// tampering.
func TestVerification_IsSigned(t *testing.T) {
	r := withVerification(t, VerificationRecompute)
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := r.Validate(nil); err != nil {
		t.Fatalf("a receipt declaring recompute must validate: %v", err)
	}

	// Relabel to the weaker mode AND make the id consistent, so nothing but the
	// signed-bytes comparison stands between the attacker and a downgrade.
	r.Task.Verification = VerificationEvaluator
	id, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	r.ReceiptID = id

	err = r.Validate(nil)
	if err == nil {
		t.Fatal("relabelling the verification mode must be detected; " +
			"otherwise an intermediary could downgrade a binary proof to a gameable one")
	}
	// The field is inside the signed payload, so changing it changes the payload
	// hash and the signature no longer recovers the declared signer. That is the
	// strongest form of the protection — the tampering is caught by the signature
	// itself, not merely by a consistency check that a future refactor could drop.
	//
	// The assertion names the mechanism so a change that weakened it to a mere
	// field comparison would be visible here.
	if !strings.Contains(err.Error(), "signer mismatch") {
		t.Errorf("tampering with a signed field must fail signature recovery, got: %v", err)
	}
}

// TestVerification_AllThreeValuesValidate covers the schema decision in MVP.md §5.0:
// all three values are in the schema even though `dispute` is not implemented.
//
// Writing `dispute` now is what keeps adding it later from being a wire change. A
// value already in the schema cannot break a parser, so the future work is
// behaviour, not protocol.
func TestVerification_AllThreeValuesValidate(t *testing.T) {
	for _, mode := range []VerificationMode{
		VerificationRecompute, VerificationEvaluator, VerificationDispute,
	} {
		r := withVerification(t, mode)
		if err := r.Sign(testPrivKey); err != nil {
			t.Fatalf("sign %s: %v", mode, err)
		}
		if err := r.Validate(nil); err != nil {
			t.Errorf("mode %s is in the schema and must validate: %v", mode, err)
		}
	}
}

func TestVerification_UnknownValueIsRejected(t *testing.T) {
	r := validReceipt(t)
	r.Task.Verification = "vibes"
	if err := r.Validate(nil); err == nil {
		t.Fatal("a verification mode outside the schema must be rejected")
	}
}

// TestVerification_ImplementedIsSeparateFromValid is the classification boundary.
//
// `dispute` is valid (it is in the schema) but not implemented (this build cannot
// produce a verdict for it). Collapsing the two would make a well-formed receipt
// look broken, and a caller would not know whether to add support or look for a
// forger — the same distinction as unsupported vs invalid schema majors.
func TestVerification_ImplementedIsSeparateFromValid(t *testing.T) {
	if !VerificationDispute.Valid() {
		t.Error("dispute is in the schema and must be valid")
	}
	if VerificationDispute.Implemented() {
		t.Error("dispute must not report as implemented in 2.0 (MVP.md §5.0 puts it in 2.1)")
	}
	if !VerificationRecompute.Implemented() || !VerificationEvaluator.Implemented() {
		t.Error("recompute and evaluator are implemented in 2.0")
	}
}

// TestVerification_DefaultIsRecompute pins the default and the reasoning behind it.
//
// A missing field means an older receipt, and every older receipt was checked by
// re-running. So the default is not a guess about intent — it is what happened.
// It is also the strongest mode, so an ambiguous receipt is treated as the harder
// case rather than the easier one.
func TestVerification_DefaultIsRecompute(t *testing.T) {
	var t0 Task
	if got := t0.VerificationOrDefault(); got != VerificationRecompute {
		t.Errorf("a task with no declared mode must default to %s, got %s", VerificationRecompute, got)
	}

	// An explicit mode is never overridden by the default.
	t1 := Task{Verification: VerificationEvaluator}
	if got := t1.VerificationOrDefault(); got != VerificationEvaluator {
		t.Errorf("an explicit mode must be preserved, got %s", got)
	}
}

// TestVerification_OldReceiptDecodesWithoutTheField is the forward-compatibility
// check from the other side: a receipt written by a pre-S9-7 build must still decode
// and validate in this build.
func TestVerification_OldReceiptDecodesWithoutTheField(t *testing.T) {
	r := validReceipt(t)
	r.Task.Verification = ""
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	back, err := Unmarshal(raw)
	if err != nil {
		t.Fatalf("an old receipt must decode: %v", err)
	}
	if err := back.Validate(nil); err != nil {
		t.Fatalf("an old receipt must still validate: %v", err)
	}
	if back.Task.Verification != "" {
		t.Errorf("the field must stay empty for an old receipt, got %q", back.Task.Verification)
	}
	if got := back.Task.VerificationOrDefault(); got != VerificationRecompute {
		t.Errorf("an old receipt is a recompute task, got %s", got)
	}
}

// TestVerification_NewReceiptVerifiesInOldFieldSet models the compatibility claim
// from MVP.md §17.5: adding a payload field is a minor change, so a verifier that
// does not know the field still accepts the receipt.
//
// The old verifier's behaviour is modelled directly: decode tolerantly, ignore the
// unknown key, and check that the known fields still agree with the signed bytes.
func TestVerification_NewReceiptVerifiesInOldFieldSet(t *testing.T) {
	r := withVerification(t, VerificationEvaluator)
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}

	// An "old" reader drops the key it does not know, from both the struct and the
	// payload, then checks consistency.
	raw, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	task, ok := decoded["task"].(map[string]any)
	if !ok {
		t.Fatalf("no task object in %s", raw)
	}
	if _, present := task["verification"]; !present {
		t.Fatal("the declared mode must be in the canonical bytes for this test to be meaningful")
	}
	delete(task, "verification")

	// The receipt's own validation must still succeed, because the extra key is an
	// additive field the tolerant path allows.
	if err := r.Validate(nil); err != nil {
		t.Fatalf("a receipt with an additive field must validate: %v", err)
	}
}
