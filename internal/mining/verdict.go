package mining

import (
	"log"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// VerdictSource reports whether a receipt's claimed result was reproduced by a
// verifier.
//
// # Why this exists (S4-0)
//
// Until S4, ScoringSink decided "verified" by checking the receipt's structure and
// signature — that is, it asked the *submitter* whether the submitter's own work was
// good. That was an explicit stopgap, recorded in the S3b report as an honesty gap:
// it kept the miner able to earn something, but it made the score trust the party it
// was supposed to be checking.
//
// Making the source injectable is what closes the gap. The production source reads
// the verdict a verifier wrote onto the receipt; the self-check survives only as a
// named fallback for tests and dry runs, so it is visible at every call site rather
// than implicit.
//
// # The consequence, stated plainly
//
// When a real VerdictSource is wired, a freshly mined receipt is `pending` and
// therefore earns nothing until a verifier has looked at it. That is the intended
// end state — verification is what makes the score meaningful — but it does mean a
// miner running alone produces receipts that accrue no points. That is a behaviour
// change from the stopgap, and it is the correct one.
type VerdictSource interface {
	// Verified reports whether r's result was reproduced.
	//
	// Implementations must be conservative: anything other than an affirmative
	// verdict must return false. A source that defaults to true would make the whole
	// mechanism advisory.
	Verified(r *receipt.Receipt) bool
}

// SelfCheckVerdicts derives "verified" from the receipt's own structure and
// signature.
//
// # This is NOT verification, and must not be described as such
//
// It confirms the receipt is well-formed and signed by its declared agent. It cannot
// tell whether the claimed work was actually done, because the only evidence it
// examines is the submitter's own assertion. A receipt can pass this and be entirely
// fabricated.
//
// It exists so tests and dry runs can exercise the scoring path without standing up a
// verifier. A deployment that intends points to mean anything must inject a
// verifier-derived source instead.
type SelfCheckVerdicts struct{}

// Verified implements VerdictSource using the receipt's own validation.
//
// # Why the unsupported case is reported rather than silently returning false
//
// A receipt from a version this build cannot check is not evidence of forgery,
// and scoring it as one would penalise an honest producer for a verifier that is
// merely out of date (H2, S9-0j). The distinction is logged so an operator can
// tell "this node is behind" from "this submission is bad".
//
// The verdict is still false: without being able to check the version, this
// source cannot assert the work was done, and defaulting to true would be worse.
// What changes is that the reason is visible instead of being indistinguishable
// from a rejection.
func (SelfCheckVerdicts) Verified(r *receipt.Receipt) bool {
	if r == nil {
		return false
	}
	if err := r.ValidateStructure(); err != nil {
		if receipt.IsUnsupported(err) {
			log.Printf(
				"self-check: receipt %s declares a schema this build cannot check, so it is NOT credited here. "+
					"This is not a finding against the submitter — upgrade the verifier. (%v)",
				r.ReceiptID, err)
		}
		return false
	}
	if err := r.Validate(nil); err != nil {
		if receipt.IsUnsupported(err) {
			log.Printf("self-check: receipt %s cannot be checked by this build; upgrade the verifier. (%v)",
				r.ReceiptID, err)
		}
		return false
	}
	return true
}

// Describe implements a human-readable label for reporting, so a CLI can say which
// source is in use rather than leaving a reader to guess.
func (SelfCheckVerdicts) Describe() string {
	return "self-check (structure and signature only; NOT adversarial verification)"
}
