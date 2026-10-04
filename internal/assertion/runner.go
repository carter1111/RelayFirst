package assertion

import (
	"context"
	"fmt"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// VerifierRunner re-checks receipts and signs an assertion about each (S10-5).
//
// # What it does, and the one thing it must not be trusted for
//
// It re-verifies a receipt and signs its conclusion. The signature is what makes the
// conclusion attributable: a verifier that says "valid" about a receipt it never checked has
// produced a signed lie, and a signed lie is evidence in a way a bare HTTP 200 is not.
//
// It is still not an authority. A client that reads an assertion and stops checking has
// replaced a verifiable system with a trusted one — the signature tells it WHO claimed the
// work was valid, not that the claim is true. Independent verification (S10-6) is the check;
// this is an accelerator.
type VerifierRunner struct {
	// VerifierID is this verifier's identity, which is what its assertions are attributed to.
	VerifierID string

	// PrivateKeyHex signs the assertions.
	PrivateKeyHex string

	// ChainID is the EVM chain the verifier's identity is anchored to.
	ChainID uint64

	// Recheck performs the actual verification.
	//
	// It is injected rather than fixed because the heavy lifting lives in
	// internal/verification, which already owns the recompute, anchor and policy machinery.
	// Duplicating that here would mean two implementations of "is this receipt good", and the
	// two would eventually disagree.
	Recheck func(ctx context.Context, r *receipt.Receipt) (bool, error)

	// Now is injected for deterministic tests.
	Now func() time.Time
}

// Assert re-checks a receipt and returns a signed assertion about it.
//
// # Why every outcome produces an assertion, including failure to check
//
// A verifier that returned nothing when it could not decide would be indistinguishable from
// one that was never asked. Since the value of an assertion is that it is attributable, the
// honest answers — "valid", "invalid", "I could not check this" — all need to be signed.
// Silence would leave a client unable to tell an unreachable verifier from a lazy one.
//
// # Why canonicalReceipt is a parameter
//
// The assertion binds the receipt's exact bytes, so the hash must be over what the verifier
// actually checked. Deriving it here from the struct would re-serialize, and a
// re-serialization that dropped an unknown field would bind bytes nobody else has — the same
// trap the receipt layer's frozen payload avoids.
func (v *VerifierRunner) Assert(ctx context.Context, r *receipt.Receipt, canonicalReceipt []byte) (Assertion, error) {
	if r == nil {
		return Assertion{}, fmt.Errorf("assertion: nil receipt")
	}
	if len(canonicalReceipt) == 0 {
		return Assertion{}, fmt.Errorf("assertion: no receipt bytes")
	}
	if v.Recheck == nil {
		return Assertion{}, fmt.Errorf("assertion: no recheck function configured")
	}
	if v.VerifierID == "" || v.PrivateKeyHex == "" {
		return Assertion{}, fmt.Errorf("assertion: the runner has no verifier identity or key")
	}

	now := time.Now
	if v.Now != nil {
		now = v.Now
	}

	ok, err := v.Recheck(ctx, r)
	if err != nil {
		// A failure to check is reported as unsupported, not as invalid. The distinction is
		// the whole reason the verdict set has three values: an out-of-date or blocked
		// verifier must not look like an accuser.
		return Assert(canonicalReceipt, r, v.VerifierID, v.PrivateKeyHex, v.ChainID,
			VerdictUnsupported, err.Error(), now())
	}
	if ok {
		return Assert(canonicalReceipt, r, v.VerifierID, v.PrivateKeyHex, v.ChainID,
			VerdictValid, "", now())
	}
	return Assert(canonicalReceipt, r, v.VerifierID, v.PrivateKeyHex, v.ChainID,
		VerdictInvalid, "the result did not reproduce", now())
}

// IndependentCheck is the client-side re-verification (S10-6).
//
// # Why this exists as a function and not as a note in a doc
//
// The property criterion ⑩ asks for is "the client can still decide even when the node lies".
// A design that relies on clients remembering to check is not that property; it is a
// convention. So the check is code, and it takes the assertion as an INPUT to be evaluated
// rather than as an answer to be believed.
//
// # The three things it establishes, in order
//
//  1. The receipt is internally valid — signature, structure, id.
//  2. The assertion is a genuine signed claim by the verifier it names, about THESE bytes.
//  3. The client's own conclusion agrees with the assertion, or the disagreement is reported.
//
// A disagreement is not necessarily the verifier lying: it could be a newer schema the
// verifier could not check, or a genuine difference of view. So it is reported with both
// sides visible rather than collapsed into a boolean.
type IndependentCheck struct {
	// ReceiptValid is what the client concluded about the receipt itself.
	ReceiptValid bool

	// AssertionPresent reports whether an assertion was supplied at all.
	AssertionPresent bool

	// AssertionGenuine reports whether the assertion's signature verified.
	AssertionGenuine bool

	// AssertionVerdict is what the verifier claimed, when the assertion was genuine.
	AssertionVerdict Verdict

	// Agrees reports whether the client's conclusion matches the verifier's.
	//
	// It is false when there is no genuine assertion, because "no claim" cannot agree with
	// anything — treating silence as agreement is exactly the blind trust this exists to
	// prevent.
	Agrees bool

	// Note explains a disagreement.
	Note string
}

// Check verifies a receipt independently and, if an assertion is supplied, evaluates it.
//
// # Why the assertion is optional
//
// A client must be able to work with no verifier at all: acceptance criterion ② requires a
// receipt to be verifiable offline with every server switched off. So the assertion is an
// extra input that can be absent, and its absence is reported rather than treated as a
// failure.
//
// # Why the receipt is checked first
//
// Because the client's own conclusion is the thing that must not be replaced. If the receipt
// is invalid, no assertion can make it valid, and a check that consulted the assertion first
// would be letting the verifier decide the outcome.
func Check(canonicalReceipt []byte, expectedAgent []byte, a *Assertion) IndependentCheck {
	out := IndependentCheck{}

	var r receipt.Receipt
	parsed, err := receipt.Unmarshal(canonicalReceipt)
	if err != nil {
		out.Note = "the receipt bytes do not parse: " + err.Error()
		return out
	}
	r = *parsed

	// The client's own conclusion. This is the check that survives every server being down.
	if err := r.Validate(expectedAgent); err != nil {
		out.Note = "the receipt is not valid on its own: " + err.Error()
		return out
	}
	out.ReceiptValid = true

	if a == nil {
		out.Note = "no assertion supplied; the receipt verified independently, which is what matters"
		return out
	}
	out.AssertionPresent = true

	// Evaluating the assertion, not believing it. A bad signature means the claim is not
	// attributable, so it carries no weight at all — which is different from a claim that is
	// attributable but wrong.
	if err := a.Verify(canonicalReceipt); err != nil {
		out.Note = "the assertion did not verify, so it carries no weight: " + err.Error()
		return out
	}
	out.AssertionGenuine = true
	out.AssertionVerdict = a.Verdict

	switch a.Verdict {
	case VerdictValid:
		out.Agrees = true
	case VerdictInvalid:
		out.Agrees = false
		out.Note = "the verifier says invalid but this client found it valid; " +
			"one of the two is wrong, and the client's own check is the one it can prove"
	case VerdictUnsupported:
		out.Agrees = false
		out.Note = "the verifier could not check this receipt (" + a.Reason + "); " +
			"that is not an accusation, and the client's own check still stands"
	}
	return out
}
