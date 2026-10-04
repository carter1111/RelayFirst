package mining

import (
	"context"
	"fmt"
	"strings"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// AnchorConsistency checks that a receipt's anchor evidence still describes what its
// source returns (MVP.md §5.5, "re-fetch the anchor").
//
// # Why this is needed even though the task is re-run
//
// Re-running a task answers "what does this produce now". That is not the same question as
// "does the evidence recorded about that content still hold". A probe receipt records a
// status code *and* a content hash; if a source later served different bytes under the same
// status, re-running alone would report agreement and accept evidence that no longer
// describes anything real.
//
// The content hash sits inside the signed payload (MVP.md §4.2), and invariant A2 defines
// valid work as work with a re-fetchable anchor. So a verifier that skips this is accepting a
// signed claim it never checked.
//
// # The honest limitation
//
// This detects *drift*, not *fabrication at the time*. If content changed between capture
// and verification, an honest receipt is rejected — which is why the verification window
// (MVP.md §5.4) exists to bound how long that can happen. The two belong together: a wide
// window plus this check rejects honest work, and this check without a window is arbitrary.
type AnchorConsistency struct {
	// Fetcher captures the source's current bytes and hash. Required.
	Fetcher anchorFetcher
}

// anchorFetcher is the minimal interface this needs from the anchor package.
//
// It is declared locally so the check does not depend on the concrete fetcher type, which
// keeps it testable with a stub and means a future fetcher (a cached one, say) can be
// substituted without touching verification.
type anchorFetcher interface {
	Capture(ctx context.Context, url string) (receipt.Anchor, []byte, error)
}

// NewAnchorConsistency returns a checker over f.
func NewAnchorConsistency(f anchorFetcher) *AnchorConsistency {
	return &AnchorConsistency{Fetcher: f}
}

// Check verifies that every externally-fetchable anchor still matches.
//
// # Which anchors are checked
//
// Synthetic anchors (url == "inline", used by compute tasks) are skipped: they carry no
// external evidence to re-fetch, so there is nothing to compare. Counting them as a failure
// would reject every compute receipt; counting them as a pass without saying so would
// overstate what was checked. They are skipped explicitly.
func (c *AnchorConsistency) Check(ctx context.Context, r *receipt.Receipt) error {
	if r == nil {
		return fmt.Errorf("anchor consistency: nil receipt")
	}
	if c == nil || c.Fetcher == nil {
		return fmt.Errorf("anchor consistency: no fetcher configured")
	}

	checked := 0
	for i, a := range r.Anchors {
		if strings.TrimSpace(a.URL) == "" || a.URL == "inline" {
			continue
		}

		fresh, _, err := c.Fetcher.Capture(ctx, a.URL)
		if err != nil {
			// An unreachable source is not agreement. Treating it as a pass would make the
			// whole check avoidable by taking the source offline.
			return fmt.Errorf("anchor consistency: refetch %s: %w", a.URL, err)
		}
		checked++

		if fresh.ContentHash != a.ContentHash {
			return fmt.Errorf(
				"anchor consistency: anchors[%d] records content %s but %s now returns %s; the receipt's evidence no longer describes its source",
				i, shortHash(a.ContentHash), a.URL, shortHash(fresh.ContentHash))
		}
	}

	if checked == 0 {
		// Every anchor was synthetic. Reporting an error rather than silently passing makes
		// the limitation visible: nothing was actually checked, so "no error" must not be
		// read as "the evidence holds".
		return fmt.Errorf("anchor consistency: receipt %s has no externally fetchable anchor, so nothing could be checked", r.ReceiptID)
	}
	return nil
}

// shortHash renders a hash for an error message without spilling 71 characters.
func shortHash(h string) string {
	t := strings.TrimPrefix(h, "sha256:")
	if len(t) <= 12 {
		return h
	}
	return "sha256:" + t[:12] + "..."
}
