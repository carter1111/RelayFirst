package verification_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/verification"
)

// Concurrency tests for the verifier.
//
// # Why these exist
//
// The verifier does three things that share state across calls:
//
//  1. it mutates the receipt it was given — `r.Verification = ...` — so two goroutines
//     verifying the *same* receipt object are writing the same memory;
//  2. it records a commitment in a shared ledger, and settles it;
//  3. it writes a verdict through a shared updater.
//
// The race-detector gate was documented as bounded by the concurrency in the tests, and the
// verifier had none, so this is where a real race was most likely to be hiding.
//
// # What they assert beyond "no race"
//
// The important property is idempotency under concurrency: many simultaneous verifications of
// one receipt must produce exactly one commitment, because a commitment that could be created
// twice would let a repeated verdict double-count.

// countingUpdater records how many verdicts were written, safely.
type countingUpdater struct {
	mu      sync.Mutex
	writes  int
	lastIDs []string
}

func (u *countingUpdater) UpdateVerification(r *receipt.Receipt) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.writes++
	u.lastIDs = append(u.lastIDs, r.ReceiptID)
	return nil
}

func (u *countingUpdater) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.writes
}

// distinctReceipt builds n receipts that differ in task spec, so their artifacts differ and
// each is independently verifiable.
func distinctReceipt(t *testing.T, n int) *receipt.Receipt {
	t.Helper()

	id := fmt.Sprintf("0x%064x", n)
	url := fmt.Sprintf("https://example.com/verify/%d", n)

	r := mkReceipt(t, keyProducer, id, url, "200")
	return r
}

// TestVerifier_ConcurrentDistinctReceipts runs many independent verifications at once against
// one verifier, which shares the stake ledger and the updater.
func TestVerifier_ConcurrentDistinctReceipts(t *testing.T) {
	const workers = 16

	stakes := verification.NewMemStakeLedger()
	updater := &countingUpdater{}

	v, err := verification.New(verification.Config{
		Policy:      verification.AllowAll{Verifier: agentOf(t, keyVerifier)},
		Stakes:      stakes,
		Receipts:    updater,
		Recomputer:  &echoRecomputer{},
		Window:      verification.AlwaysOpen{},
		StakePoints: 50,
		Now:         func() time.Time { return time.Unix(1791015900, 0) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	receipts := make([]*receipt.Receipt, 0, workers)
	for i := 0; i < workers; i++ {
		receipts = append(receipts, distinctReceipt(t, i))
	}

	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(r *receipt.Receipt) {
			defer wg.Done()
			<-start
			if _, err := v.Verify(context.Background(), r); err != nil {
				t.Errorf("Verify(%s): %v", r.ReceiptID, err)
			}
		}(receipts[i])
	}
	close(start)
	wg.Wait()

	if got := updater.count(); got != workers {
		t.Errorf("updater wrote %d verdicts, want %d", got, workers)
	}
	if got := len(stakes.Entries()); got != workers {
		t.Errorf("stake ledger holds %d commitments, want %d", got, workers)
	}
	committed, released, _ := stakes.Standing(agentOf(t, keyVerifier))
	if want := uint64(workers * 50); committed != want {
		t.Errorf("committed = %d, want %d", committed, want)
	}
	if released != committed {
		t.Errorf("released = %d, want %d (every verdict agreed)", released, committed)
	}
}

// TestVerifier_ConcurrentSameReceiptIsIdempotent is the important one.
//
// Many goroutines verify the *same* receipt object simultaneously. Two things must hold:
//
//   - the commitment must be created exactly once, because a commitment that could be created
//     twice would let a repeated verdict double-count;
//   - the receipt's verification block must end up in a consistent state, since every goroutine
//     writes it in place.
//
// This deliberately passes the same pointer to every goroutine, which is the worst case: it is
// not a realistic usage pattern, but it is what makes the in-place mutation visible.
func TestVerifier_ConcurrentSameReceiptIsIdempotent(t *testing.T) {
	const workers = 16

	stakes := verification.NewMemStakeLedger()
	updater := &countingUpdater{}

	v, err := verification.New(verification.Config{
		Policy:      verification.AllowAll{Verifier: agentOf(t, keyVerifier)},
		Stakes:      stakes,
		Receipts:    updater,
		Recomputer:  &echoRecomputer{},
		Window:      verification.AlwaysOpen{},
		StakePoints: 50,
		Now:         func() time.Time { return time.Unix(1791015900, 0) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r := distinctReceipt(t, 42)

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]bool, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			out, err := v.Verify(context.Background(), r)
			if err != nil {
				// A concurrent settlement of an already-settled commitment is reported rather
				// than ignored. That is acceptable for this test as long as the *state* ends
				// consistent, which is asserted below.
				return
			}
			results[i] = out.Verified
		}(i)
	}
	close(start)
	wg.Wait()

	// Exactly one commitment, whatever the interleaving.
	if got := len(stakes.Entries()); got != 1 {
		t.Errorf("stake ledger holds %d commitment(s), want 1 — a repeated verdict must not create a second", got)
	}

	committed, released, slashed := stakes.Standing(agentOf(t, keyVerifier))
	if committed != 50 {
		t.Errorf("committed = %d, want 50", committed)
	}
	// It must have settled exactly once, in one direction.
	if released+slashed != 50 {
		t.Errorf("released=%d slashed=%d, want one of them to be 50", released, slashed)
	}
	if released != 0 && slashed != 0 {
		t.Errorf("the commitment settled both ways: released=%d slashed=%d", released, slashed)
	}

	// The receipt's verification block must be one of the two terminal states, not a mixture
	// or a zero value.
	if r.Verification.Status != receipt.VerificationVerified && r.Verification.Status != receipt.VerificationRejected {
		t.Errorf("final status = %q, want verified or rejected", r.Verification.Status)
	}
	if r.Verification.VerifierID == nil || *r.Verification.VerifierID == "" {
		t.Error("the final verification block has no verifier")
	}
}

// TestVerifier_ConcurrentMixedOutcomes interleaves agreeing and rejecting verifications, so the
// settlement path takes both branches under concurrency.
func TestVerifier_ConcurrentMixedOutcomes(t *testing.T) {
	const pairs = 12

	stakes := verification.NewMemStakeLedger()
	updater := &countingUpdater{}

	v, err := verification.New(verification.Config{
		Policy:     verification.AllowAll{Verifier: agentOf(t, keyVerifier)},
		Stakes:     stakes,
		Receipts:   updater,
		Recomputer: &conditionalRecomputer{},
		Window:     verification.AlwaysOpen{},
		Now:        func() time.Time { return time.Unix(1791015900, 0) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Half the receipts will reproduce (their spec url ends in "agree"), half will not.
	agree := make([]*receipt.Receipt, 0, pairs)
	reject := make([]*receipt.Receipt, 0, pairs)
	for i := 0; i < pairs; i++ {
		agree = append(agree, mkReceipt(t, keyProducer, fmt.Sprintf("0x%064x", i), "https://example.com/agree", "200"))
		reject = append(reject, mkReceipt(t, keyProducer, fmt.Sprintf("0x%064x", i+1000), "https://example.com/disagree", "200"))
	}

	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := range agree {
		wg.Add(2)
		go func(r *receipt.Receipt) {
			defer wg.Done()
			<-start
			_, _ = v.Verify(context.Background(), r)
		}(agree[i])
		go func(r *receipt.Receipt) {
			defer wg.Done()
			<-start
			_, _ = v.Verify(context.Background(), r)
		}(reject[i])
	}
	close(start)
	wg.Wait()

	if got := len(stakes.Entries()); got != pairs*2 {
		t.Errorf("stake ledger holds %d commitments, want %d", got, pairs*2)
	}

	_, released, slashed := stakes.Standing(agentOf(t, keyVerifier))
	if released == 0 {
		t.Error("no commitment was released; the agreeing path did not run")
	}
	if slashed == 0 {
		t.Error("no commitment was slashed; the disagreeing path did not run")
	}
}

// ---------------------------------------------------------------- recomputers

// echoRecomputer reproduces whatever the receipt claims, so every verification agrees.
type echoRecomputer struct{}

func (e *echoRecomputer) Recompute(_ context.Context, r *receipt.Receipt) (receipt.Result, error) {
	return r.Result, nil
}

// conditionalRecomputer reproduces the claim only for receipts whose url contains "/agree".
//
// The match is on a path segment rather than a suffix, because a suffix test is exactly the bug
// that made an earlier version of this test useless: "/disagree" ends with "agree", so both
// branches took the agreeing path and the disagreement case was never exercised.
type conditionalRecomputer struct{}

func (c *conditionalRecomputer) Recompute(_ context.Context, r *receipt.Receipt) (receipt.Result, error) {
	url, _ := r.Task.Spec["url"].(string)
	if strings.Contains(url, "/agree") {
		return r.Result, nil
	}
	return receipt.Result{Value: "different", Hash: hashOf("different")}, nil
}

func hashOf(s string) string {
	sum := make([]byte, 32)
	copy(sum, []byte(s))
	return "sha256:" + hex.EncodeToString(sum)
}
