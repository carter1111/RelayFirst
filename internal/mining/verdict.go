package mining

import (
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
func (SelfCheckVerdicts) Verified(r *receipt.Receipt) bool {
	if r == nil {
		return false
	}
	return r.ValidateStructure() == nil && r.Validate(nil) == nil
}

// Describe implements a human-readable label for reporting, so a CLI can say which
// source is in use rather than leaving a reader to guess.
func (SelfCheckVerdicts) Describe() string {
	return "self-check (structure and signature only; NOT adversarial verification)"
}
