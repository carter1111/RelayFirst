package main

import (
	"context"
	"fmt"
	"time"

	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// checkAnchor re-fetches a receipt's anchors and compares their content hashes.
//
// # What this adds over the offline check, and its limit
//
// The offline check confirms the signature and structure, which proves the claim is
// internally consistent and was made by the named agent. It does NOT prove the claim is
// still true: the source could have changed, or the claim could have been false from the
// start.
//
// Re-fetching narrows that. It detects DRIFT, not fabrication at the time — if content
// changed since capture, an honest receipt fails. That is exactly why the verification
// window exists in MVP.md §5.4 to bound how long drift can be held against a producer, and
// why this is opt-in rather than the default: an honest receipt can legitimately fail it
// later, so a verifier that always re-fetched would produce accusations of forgery for
// content that simply moved on.
//
// The framing is deliberate. This reports the check it performed in the verdict's reason, so
// a reader can tell "the anchor still matches" from "the signature was valid", which are
// different amounts of evidence against the same claim.
func checkAnchor(r *receipt.Receipt, onlyURL string) (bool, error) {
	fetcher := anchor.NewFetcher()

	var checked int
	for _, a := range r.Anchors {
		// A synthetic anchor carries no external evidence to re-fetch, so there is nothing to
		// compare. Counting it as a failure would reject every compute receipt; counting it as
		// a pass without saying so would overstate what was checked. It is skipped, and the
		// count below records whether anything was actually verified.
		if a.URL == "inline" {
			continue
		}
		if onlyURL != "" && a.URL != onlyURL {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		ok, err := fetcher.Verify(ctx, a)
		cancel()
		if err != nil {
			return false, fmt.Errorf("re-fetching %s failed: %w", a.URL, err)
		}
		if !ok {
			// Drift, or a claim that was never true. Either way the anchor no longer supports
			// the receipt, and the verdict must say which anchor disagreed.
			return false, fmt.Errorf(
				"anchor %s no longer matches: the content hash it claims does not match what the source serves now "+
					"(this detects drift, and an honest receipt can fail it if the source changed; see MVP.md §5.4)",
				a.URL)
		}
		checked++
	}

	if checked == 0 {
		// Nothing external was verifiable. Returning true would claim a check that did not
		// happen, which is the failure this whole stage exists to remove.
		return false, fmt.Errorf(
			"no externally re-fetchable anchor was checked: every anchor was synthetic or filtered out, " +
				"so re-fetching establishes nothing here")
	}
	return true, nil
}
