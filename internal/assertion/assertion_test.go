package assertion_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/assertion"
	"github.com/relayfirst/relayfirst/internal/eip712"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// These tests cover S10-5 (attributable verification) and S10-6 (independent client check).
//
// The properties that matter:
//
//   - a verdict is attributable, so a lie is a SIGNED lie rather than a bare HTTP 200;
//   - the assertion binds the receipt's exact bytes, so it cannot be replayed against a
//     different receipt;
//   - "could not check" stays distinct from "invalid", or an out-of-date verifier becomes an
//     accuser;
//   - and a client reaches its own conclusion even when the verifier says otherwise, which is
//     the half of criterion ⑩ that a trusted-verifier design would not have.

const (
	producerKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	verifierKey = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	otherKey    = "0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
	chainID     = uint64(8453)
)

func agentIDFor(t *testing.T, key string) string {
	t.Helper()
	id, err := receipt.DeriveAgentID(key, chainID)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}
	return id
}

// signedReceipt builds a valid receipt and returns it with its canonical bytes.
func signedReceipt(t *testing.T, key string) (*receipt.Receipt, []byte) {
	t.Helper()
	r := &receipt.Receipt{
		Schema:  receipt.Schema,
		AgentID: agentIDFor(t, key),
		Epoch:   42,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": "https://example.com"},
			SpecHash:      "sha256:" + strings.Repeat("3d", 32),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015862},
		Result:       receipt.Result{Value: "200", Hash: "sha256:" + strings.Repeat("c1", 32)},
		Anchors:      []receipt.Anchor{{URL: "https://example.com", ContentHash: "sha256:" + strings.Repeat("7b", 32), FetchedAt: 1791015810, Status: 200, Bytes: 2048}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	id, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	r.ReceiptID = id
	if err := r.Sign(key); err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return r, raw
}

func TestAssert_SignsAndVerifies(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)
	verifierID := agentIDFor(t, verifierKey)

	a, err := assertion.Assert(raw, r, verifierID, verifierKey, chainID,
		assertion.VerdictValid, "", time.Unix(1791015900, 0))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if a.Signature == "" {
		t.Fatal("the assertion must be signed; an unsigned verdict is not attributable")
	}
	if err := a.Verify(raw); err != nil {
		t.Errorf("a freshly signed assertion must verify: %v", err)
	}
}

// TestAssertion_BindsTheReceiptBytes is the anti-replay property.
//
// An assertion that named only a receipt id could be reused against any receipt sharing that
// id, and ids are chosen by the producer — so a dishonest producer could mint a receipt whose
// id collides with one that has a valid assertion.
func TestAssertion_BindsTheReceiptBytes(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)
	verifierID := agentIDFor(t, verifierKey)

	a, err := assertion.Assert(raw, r, verifierID, verifierKey, chainID,
		assertion.VerdictValid, "", time.Unix(1791015900, 0))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}

	// The same receipt id, but different bytes: a different result.
	tampered := append([]byte(nil), raw...)
	tampered = []byte(strings.Replace(string(tampered), `"value":"200"`, `"value":"404"`, 1))
	if string(tampered) == string(raw) {
		t.Fatal("the test did not actually change the bytes")
	}

	err = a.Verify(tampered)
	if err == nil {
		t.Fatal("an assertion must not verify against different bytes; " +
			"otherwise it could be replayed against a receipt that was never checked")
	}
	if !strings.Contains(err.Error(), "different receipt") {
		t.Errorf("the failure must name the mismatch, not a bad signature, got: %v", err)
	}
}

// TestAssertion_RejectsWrongSigner covers a claim attributed to someone who did not make it.
func TestAssertion_RejectsWrongSigner(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)
	verifierID := agentIDFor(t, verifierKey)

	a, err := assertion.Assert(raw, r, verifierID, verifierKey, chainID,
		assertion.VerdictValid, "", time.Unix(1791015900, 0))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}

	// Relabel the verifier without re-signing.
	a.VerifierID = agentIDFor(t, otherKey)
	if err := a.Verify(raw); err == nil {
		t.Fatal("a relabelled assertion must not verify: it would let one verifier's claim " +
			"be attributed to another")
	}
}

// TestAssert_RefusesMismatchedKey keeps an unverifiable assertion from being produced.
func TestAssert_RefusesMismatchedKey(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)

	// Claim the verifier's identity while signing with another key.
	if _, err := assertion.Assert(raw, r, agentIDFor(t, verifierKey), otherKey, chainID,
		assertion.VerdictValid, "", time.Unix(1791015900, 0)); err == nil {
		t.Fatal("signing with a key that does not match the declared verifier must fail")
	}
}

// TestAssertion_NonValidVerdictRequiresReason is the reviewability rule.
//
// An accusation with no reason cannot be reviewed, and review is the only thing that makes a
// negative verdict usable. A verifier saying "invalid" and nothing else is asking to be
// believed on the strength of its own assertion.
func TestAssertion_NonValidVerdictRequiresReason(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)
	verifierID := agentIDFor(t, verifierKey)

	for _, v := range []assertion.Verdict{assertion.VerdictInvalid, assertion.VerdictUnsupported} {
		if _, err := assertion.Assert(raw, r, verifierID, verifierKey, chainID,
			v, "", time.Unix(1791015900, 0)); err == nil {
			t.Errorf("a %s verdict with no reason must be rejected", v)
		}
	}
	// Valid needs no reason, because there is nothing to explain.
	if _, err := assertion.Assert(raw, r, verifierID, verifierKey, chainID,
		assertion.VerdictValid, "", time.Unix(1791015900, 0)); err != nil {
		t.Errorf("a valid verdict needs no reason: %v", err)
	}
}

// TestRunner_UnsupportedIsNotAnAccusation is the three-valued verdict's reason for existing.
//
// A verifier that could not check a receipt must not report it as invalid, or an out-of-date
// verifier becomes an accuser — the same mistake the receipt layer's unsupported/invalid split
// prevents.
func TestRunner_UnsupportedIsNotAnAccusation(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)

	runner := &assertion.VerifierRunner{
		VerifierID:    agentIDFor(t, verifierKey),
		PrivateKeyHex: verifierKey,
		ChainID:       chainID,
		Recheck: func(context.Context, *receipt.Receipt) (bool, error) {
			return false, errors.New("this build cannot check schema v99")
		},
		Now: func() time.Time { return time.Unix(1791015900, 0) },
	}

	a, err := runner.Assert(context.Background(), r, raw)
	if err != nil {
		t.Fatalf("a failed check must still produce an assertion: %v", err)
	}
	if a.Verdict != assertion.VerdictUnsupported {
		t.Errorf("verdict = %s, want %s: a check that could not run must not read as an accusation",
			a.Verdict, assertion.VerdictUnsupported)
	}
	if !strings.Contains(a.Reason, "schema v99") {
		t.Errorf("the reason must explain why the check could not run, got: %q", a.Reason)
	}
}

// TestRunner_InvalidIsAnAccusationWithAReason is the contrast: a real failure is reported as
// one, and it explains itself.
func TestRunner_InvalidIsAnAccusationWithAReason(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)

	runner := &assertion.VerifierRunner{
		VerifierID:    agentIDFor(t, verifierKey),
		PrivateKeyHex: verifierKey,
		ChainID:       chainID,
		Recheck:       func(context.Context, *receipt.Receipt) (bool, error) { return false, nil },
		Now:           func() time.Time { return time.Unix(1791015900, 0) },
	}

	a, err := runner.Assert(context.Background(), r, raw)
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if a.Verdict != assertion.VerdictInvalid {
		t.Errorf("verdict = %s, want %s", a.Verdict, assertion.VerdictInvalid)
	}
	if a.Reason == "" {
		t.Error("an accusation must explain itself")
	}
}

// TestCheck_ClientDecidesIndependently is criterion ⑩'s second half.
//
// A verifier asserts VALID about a receipt the client can see is broken. The client must reach
// its own conclusion, and the disagreement must be visible — not silently overridden by the
// assertion, and not silently accepted either.
func TestCheck_ClientDecidesIndependently(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)

	// The verifier signs "valid" over the ORIGINAL bytes, which it did check.
	a, err := assertion.Assert(raw, r, agentIDFor(t, verifierKey), verifierKey, chainID,
		assertion.VerdictValid, "", time.Unix(1791015900, 0))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}

	// The client receives DIFFERENT bytes: a result that was rewritten in transit.
	tampered := []byte(strings.Replace(string(raw), `"value":"200"`, `"value":"999"`, 1))
	if string(tampered) == string(raw) {
		t.Fatal("the test did not actually change the bytes")
	}

	out := assertion.Check(tampered, nil, &a)
	if out.ReceiptValid {
		t.Fatal("the client must reject the tampered receipt regardless of what the verifier said")
	}
	if out.Agrees {
		t.Error("a client that found the receipt invalid must not agree with a 'valid' assertion")
	}
	if !strings.Contains(out.Note, "not valid on its own") {
		t.Errorf("the note must say the receipt failed on its own, got: %q", out.Note)
	}
}

// TestCheck_DisagreementIsReportedNotResolved covers the reverse: the verifier says invalid,
// the client's own check says valid.
//
// The client's conclusion stands, and the disagreement is surfaced. It is not necessarily the
// verifier lying — it could be a different view — so it is reported with both sides visible
// rather than collapsed into a boolean.
func TestCheck_DisagreementIsReportedNotResolved(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)

	a, err := assertion.Assert(raw, r, agentIDFor(t, verifierKey), verifierKey, chainID,
		assertion.VerdictInvalid, "the result did not reproduce", time.Unix(1791015900, 0))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}

	out := assertion.Check(raw, nil, &a)
	if !out.ReceiptValid {
		t.Fatal("the client's own check must stand: the receipt is valid")
	}
	if !out.AssertionGenuine {
		t.Fatal("the assertion is genuine; the disagreement is about the verdict, not the signature")
	}
	if out.Agrees {
		t.Error("the verdicts differ, so Agrees must be false")
	}
	if !strings.Contains(out.Note, "one of the two is wrong") {
		t.Errorf("the disagreement must be reported, got: %q", out.Note)
	}
}

// TestCheck_WorksWithNoAssertion is acceptance criterion ②: a receipt verifies offline with
// every server switched off.
func TestCheck_WorksWithNoAssertion(t *testing.T) {
	_, raw := signedReceipt(t, producerKey)

	out := assertion.Check(raw, nil, nil)
	if !out.ReceiptValid {
		t.Fatal("a receipt must verify with no assertion at all; " +
			"criterion ② requires it to work with every server down")
	}
	if out.AssertionPresent {
		t.Error("no assertion was supplied")
	}
	if out.Agrees {
		t.Error("silence must not count as agreement: 'no claim' cannot agree with anything")
	}
}

// TestCheck_ForgedAssertionCarriesNoWeight covers a client that receives a claim not signed by
// the verifier it names.
func TestCheck_ForgedAssertionCarriesNoWeight(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)

	a, err := assertion.Assert(raw, r, agentIDFor(t, verifierKey), verifierKey, chainID,
		assertion.VerdictValid, "", time.Unix(1791015900, 0))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	// Someone relabels it as another verifier.
	a.VerifierID = agentIDFor(t, otherKey)

	out := assertion.Check(raw, nil, &a)
	if !out.ReceiptValid {
		t.Fatal("the receipt itself is valid and must still verify")
	}
	if out.AssertionGenuine {
		t.Error("a forged assertion must not be treated as genuine")
	}
	if out.Agrees {
		t.Error("a claim that is not attributable cannot be agreed with")
	}
	if !strings.Contains(out.Note, "carries no weight") {
		t.Errorf("the note must say the assertion carries no weight, got: %q", out.Note)
	}
}

// TestCheck_UnsupportedIsNotAnAccusation keeps the third verdict honest on the client side too.
func TestCheck_UnsupportedIsNotAnAccusation(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)

	a, err := assertion.Assert(raw, r, agentIDFor(t, verifierKey), verifierKey, chainID,
		assertion.VerdictUnsupported, "this build cannot check schema v99", time.Unix(1791015900, 0))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}

	out := assertion.Check(raw, nil, &a)
	if !out.ReceiptValid {
		t.Fatal("the client's own check must stand")
	}
	if out.Agrees {
		t.Error("'could not check' must not read as agreement")
	}
	if !strings.Contains(out.Note, "not an accusation") {
		t.Errorf("the note must distinguish inability from accusation, got: %q", out.Note)
	}
}

// TestAssertion_DomainIsDistinct keeps an assertion signature from being replayable as a
// receipt or card signature.
func TestAssertion_DomainIsDistinct(t *testing.T) {
	if assertion.DomainVersion == receipt.Schema {
		t.Fatal("the assertion domain must differ from the receipt's, or a signature could be replayed")
	}
	// The struct type also differs, and EIP-712 includes the type hash, so the protection is
	// independent of the version string.
	if assertion.DomainName == "" {
		t.Error("the domain name must be set")
	}
}

// TestAssertion_RejectsMalformedInputs covers the shape checks.
func TestAssertion_RejectsMalformedInputs(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)
	good, err := assertion.Assert(raw, r, agentIDFor(t, verifierKey), verifierKey, chainID,
		assertion.VerdictValid, "", time.Unix(1791015900, 0))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*assertion.Assertion)
	}{
		{"no receipt id", func(a *assertion.Assertion) { a.ReceiptID = "" }},
		{"no receipt hash", func(a *assertion.Assertion) { a.ReceiptHash = "" }},
		{"malformed verifier", func(a *assertion.Assertion) { a.VerifierID = "not-an-agent" }},
		{"unknown verdict", func(a *assertion.Assertion) { a.Verdict = "probably" }},
		{"no timestamp", func(a *assertion.Assertion) { a.AssertedAt = time.Time{} }},
		{"no signature", func(a *assertion.Assertion) { a.Signature = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := good
			c.mutate(&a)
			if err := a.Verify(raw); err == nil {
				t.Errorf("an assertion with %s must not verify", c.name)
			}
		})
	}

	if err := good.Verify(nil); err == nil {
		t.Error("verifying against no bytes must fail")
	}
}

// TestAssertion_RoundTripsThroughJSON confirms an assertion survives transport.
func TestAssertion_RoundTripsThroughJSON(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)
	a, err := assertion.Assert(raw, r, agentIDFor(t, verifierKey), verifierKey, chainID,
		assertion.VerdictValid, "", time.Unix(1791015900, 0))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}

	encoded, err := a.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	back, err := assertion.UnmarshalAssertion(encoded)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := back.Verify(raw); err != nil {
		t.Errorf("an assertion must verify after a JSON round trip: %v", err)
	}
}

// TestAssertion_VerifyDoesNotClaimCorrectness documents the boundary in code: Verify answers
// "who said what", never "is it so".
//
// The fixture is a verifier that signed "valid" over bytes that are themselves broken. The
// claim is genuine and attributable, and it is wrong. Verify must accept it — attributability
// and truth are different questions — and the client's own check is what catches the
// wrongness.
func TestAssertion_VerifyDoesNotClaimCorrectness(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)

	// The bytes the verifier signs are a broken receipt: the result was rewritten.
	broken := []byte(strings.Replace(string(raw), `"value":"200"`, `"value":"999"`, 1))
	if string(broken) == string(raw) {
		t.Fatal("the test did not actually change the bytes")
	}

	// The verifier signs "valid" over those broken bytes. The assertion is genuine.
	a, err := assertion.Assert(broken, r, agentIDFor(t, verifierKey), verifierKey, chainID,
		assertion.VerdictValid, "", time.Unix(1791015900, 0))
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if err := a.Verify(broken); err != nil {
		t.Fatalf("Verify must confirm attributability even for a wrong claim: %v", err)
	}

	// And the client, checking on its own, sees that the receipt does not hold up. This is
	// the check that does not depend on the verifier being honest.
	out := assertion.Check(broken, nil, &a)
	if out.ReceiptValid {
		t.Error("the client must reject the broken receipt on its own check, " +
			"even though the verifier signed a 'valid' claim about it")
	}
	if out.Agrees {
		t.Error("the client's own conclusion must not be overridden by the assertion")
	}
}

// unused guard for the eip712 import in this file.
var _ = eip712.Keccak256
