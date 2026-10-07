package verification

import (
	"fmt"
	"time"

	"github.com/relayfirst/relayfirst/internal/epoch"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// Window decides whether a receipt may still be verified (MVP.md §5.4).
//
// # What problem this solves, and for whom
//
// A receipt's evidence is an anchor: a URL plus the content hash observed at capture
// time. Web content changes. If a verifier re-fetches an old receipt's URL weeks
// later, the content will often differ — and the re-execution will "fail" even though
// the producer was entirely honest at the time.
//
// So the window protects the *producer* from a false rejection. It is not primarily an
// anti-attack measure, and that distinction drives the default below: an attacker
// gains nothing from a wide window, because a fabricated result fails reproduction
// whenever it is checked.
//
// # Epoch-length, not wall-clock hours
//
// The window is expressed in whole epochs because that is what MVP.md §5.4 specifies
// and because epochs are already the settlement unit. Using the same boundary avoids a
// second notion of time that could disagree with the ledger.
type Window interface {
	// Open reports whether r may be verified at now. A nil error means the window is
	// open; a non-nil error explains why verification is being refused.
	Open(r *receipt.Receipt, now time.Time) error
}

// EpochWindow is the default Window: verification must happen within the receipt's own
// epoch.
type EpochWindow struct {
	// Length is the epoch duration. Zero means DefaultEpochLength.
	Length time.Duration
}

// DefaultEpochLength is the 7-day epoch from MVP.md §6.2 (2026-10-08).
//
// It matches scoring's epoch length by value rather than by import: verification does
// not otherwise depend on the scoring package, and importing it purely for a constant
// would couple two layers that have no other reason to know about each other. If the
// two ever diverge, the mismatch shows up as a window that closes at the wrong time,
// which a test pins.
const DefaultEpochLength = 7 * 24 * time.Hour

// NewEpochWindow returns a window over epochs of the given length.
func NewEpochWindow(length time.Duration) EpochWindow {
	if length <= 0 {
		length = DefaultEpochLength
	}
	return EpochWindow{Length: length}
}

// Open reports whether r's epoch has ended.
func (w EpochWindow) Open(r *receipt.Receipt, now time.Time) error {
	if r == nil {
		return fmt.Errorf("verification: no receipt to check")
	}

	length := w.Length
	if length <= 0 {
		length = DefaultEpochLength
	}

	start, end := epoch.Bounds(r.Epoch, length)

	if now.Before(start) {
		// A receipt dated in the future is either a clock problem or a receipt that
		// has not happened yet. Either way, verifying it now would be meaningless.
		return fmt.Errorf("verification: epoch %d has not started yet (starts %s)", r.Epoch, start.UTC().Format(time.RFC3339))
	}
	if !now.Before(end) {
		return fmt.Errorf("verification: epoch %d closed at %s; the anchor content may have changed since, so re-checking now could reject honest work",
			r.Epoch, end.UTC().Format(time.RFC3339))
	}
	return nil
}

// AlwaysOpen is a Window that permits verification at any time.
//
// # Why this is the default rather than a closed window
//
// A missing window would otherwise make the verifier refuse everything, which is worse
// than the problem the window solves. The window exists to avoid *false rejections* of
// honest producers; it is not a gate that keeps bad work out. Leaving it open therefore
// costs accuracy, not safety — a fabricated result still fails reproduction whenever it
// is checked.
//
// A deployment that cares about accuracy should configure EpochWindow. The default is
// documented rather than silent: Describe says what it is.
type AlwaysOpen struct{}

// Open always permits verification.
func (AlwaysOpen) Open(*receipt.Receipt, time.Time) error { return nil }

// Describe implements the describer interface, so a CLI can report which window is
// active rather than leaving an operator to guess.
func (AlwaysOpen) Describe() string {
	return "no window (verification permitted at any time; honest work may be falsely rejected if content changed)"
}

// Describe implements the describer interface.
func (w EpochWindow) Describe() string {
	length := w.Length
	if length <= 0 {
		length = DefaultEpochLength
	}
	return fmt.Sprintf("epoch window (%s per epoch; a receipt may only be verified before its epoch ends)", length)
}
