package store

import (
	"fmt"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// Activity answers the per-epoch counts the anti-collusion ratio cap is measured
// against (MVP.md §5.6 rule 2).
//
// # Why this lives with the store rather than with verification
//
// internal/verification deliberately does not own epoch statistics: reading them there
// would give verification a second, divergent view of the same facts. The store already
// owns receipts, so it can answer both questions with a COUNT.
//
// This is the piece that makes the ratio cap a real constraint. Without it the verifier
// passed zeros, and a ratio-configured policy would either have refused every
// verification or enforced nothing.
type Activity struct {
	receipts *ReceiptStore
}

// NewActivity returns an Activity over the receipt store.
//
// A nil store is not rejected here; instead every query reports an error, which the
// verifier turns into a refusal. Failing closed matters more than failing early: an
// unreadable count must never be silently read as zero production, because zero
// production means "pure verifier" and would refuse a legitimate verifier for the wrong
// reason.
func NewActivity(receipts *ReceiptStore) *Activity {
	return &Activity{receipts: receipts}
}

// Produced returns how many receipts the agent recorded in the epoch.
func (a *Activity) Produced(epoch uint64, agent string) (int, error) {
	if a == nil || a.receipts == nil {
		return 0, fmt.Errorf("store: activity has no receipt store")
	}
	return a.receipts.CountByEpochAndAgent(epoch, agent)
}

// Verified returns how many receipts the agent has already verified in the epoch.
//
// # Why "verified or rejected" rather than only "verified"
//
// The cap bounds how much judging an agent does, not how often it agreed. Counting only
// agreements would let a verifier issue unlimited rejections — which is exactly the
// behaviour an agent with no production has an incentive to produce, since rejecting
// costs it nothing under this design. Counting every verdict it has cast makes the cap
// bound the activity itself.
func (a *Activity) Verified(epoch uint64, agent string) (int, error) {
	if a == nil || a.receipts == nil {
		return 0, fmt.Errorf("store: activity has no receipt store")
	}

	verified, err := a.receipts.CountVerifiedByEpochAndAgent(epoch, agent, string(receipt.VerificationVerified))
	if err != nil {
		return 0, err
	}
	rejected, err := a.receipts.CountVerifiedByEpochAndAgent(epoch, agent, string(receipt.VerificationRejected))
	if err != nil {
		return 0, err
	}
	return verified + rejected, nil
}
