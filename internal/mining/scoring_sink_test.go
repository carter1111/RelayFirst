package mining

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/scoring"
)

// fakeStore is a ReceiptSink and ReceiptLister backed by memory.
type fakeStore struct {
	mu       sync.Mutex
	byID     map[string]*receipt.Receipt
	order    []*receipt.Receipt
	failNext error
}

func newFakeStore() *fakeStore {
	return &fakeStore{byID: map[string]*receipt.Receipt{}}
}

func (f *fakeStore) Save(r *receipt.Receipt, _ string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return err
	}
	if _, exists := f.byID[r.ReceiptID]; exists {
		return nil // idempotent, like the SQLite store
	}
	f.byID[r.ReceiptID] = r
	f.order = append(f.order, r)
	return nil
}

func (f *fakeStore) ByAgent(agentID string, epoch uint64) ([]*receipt.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []*receipt.Receipt
	for _, r := range f.order {
		if r.AgentID == agentID && r.Epoch == epoch {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.order)
}

// testKey returns the nth well-known test key. These hold no value and are never
// used outside tests (CODING_RULES.md §8).
func testKey(n int) string {
	return "0x" + strings.Repeat("0", 63) + fmt.Sprintf("%x", n)
}

// contentHash returns a distinct, well-formed 32-byte hash per seed.
func contentHash(seed int) string {
	return "sha256:" + strings.Repeat(fmt.Sprintf("%02x", seed), 32)
}

// mkProbe builds a genuinely signed, structurally valid probe receipt.
//
// It is signed with key, so the receipt passes Validate(nil) — which the scoring
// sink requires before it will credit anything.
func mkProbe(t *testing.T, key, url, cHash, receiptID string, epoch uint64) *receipt.Receipt {
	t.Helper()

	agent, err := receipt.DeriveAgentID(key, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	r := &receipt.Receipt{
		Schema:  receipt.Schema,
		AgentID: agent,
		Epoch:   epoch,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": url},
			SpecHash:      contentHash(0x3d) + receiptID,
			SelfGenerated: true,
		},
		Work: receipt.Work{
			Provider:   "local",
			Model:      "local/lookup",
			StartedAt:  1791015800,
			FinishedAt: 1791015862,
		},
		Result: receipt.Result{
			Value: "200",
			Hash:  contentHash(0xc1),
		},
		Anchors: []receipt.Anchor{{
			URL:         url,
			ContentHash: cHash,
			FetchedAt:   1791015810,
			Status:      200,
			Bytes:       2048,
		}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}

	// Derive the id from the signed payload; Validate rejects a free-standing one
	// (S9-0h, finding B2). The `receiptID` argument still distinguishes fixtures
	// through SpecHash, so derived ids stay distinct.
	derived, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("DerivedReceiptID: %v", err)
	}
	r.ReceiptID = derived

	if err := r.Sign(key); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return r
}

const testEpoch = 42

func newScoringSink(t *testing.T) (*ScoringSink, *fakeStore, *scoring.MemLedger, *scoring.MemPointsLedger) {
	t.Helper()

	inner := newFakeStore()
	artifacts := scoring.NewMemLedger()
	points := scoring.NewMemPointsLedger()

	sink := &ScoringSink{
		Inner:     inner,
		Receipts:  inner,
		Artifacts: artifacts,
		Points:    points,
		Clock:     func() time.Time { return time.Unix(1791015800, 0) },
	}
	return sink, inner, artifacts, points
}

func at() time.Time { return time.Unix(1791015800, 0) }

// credit ----------------------------------------------------------------

// TestScoringSink_CreditsValidReceipt closes the loop the CLI was missing: a
// mined receipt must actually earn points.
func TestScoringSink_CreditsValidReceipt(t *testing.T) {
	sink, store, _, points := newScoringSink(t)

	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)

	if err := sink.Save(r, "sha256:keyA", at()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if store.Count() != 1 {
		t.Fatal("the receipt should have been persisted")
	}
	got := points.Balance(r.AgentID)
	if got <= 0 {
		t.Fatalf("Balance = %v, want a positive credit — mining must actually pay", got)
	}
	if got != scoring.BasePoints {
		t.Errorf("Balance = %v, want the base award %v", got, scoring.BasePoints)
	}
}

// TestScoringSink_ReplayDoesNotDoubleCredit: a replayed receipt must not pay
// twice at any layer.
func TestScoringSink_ReplayDoesNotDoubleCredit(t *testing.T) {
	sink, _, _, points := newScoringSink(t)

	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)

	for i := 0; i < 5; i++ {
		if err := sink.Save(r, "sha256:keyA", at()); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	if got := points.Balance(r.AgentID); got != scoring.BasePoints {
		t.Errorf("Balance after 5 saves = %v, want %v — a replay must not double-pay", got, scoring.BasePoints)
	}
	if got := len(points.Entries()); got != 1 {
		t.Errorf("Entries = %d, want 1", got)
	}
}

// TestScoringSink_InvalidReceiptEarnsNothing guards the boundary: work that does
// not validate must not be paid, and must not claim the artifact either.
func TestScoringSink_InvalidReceiptEarnsNothing(t *testing.T) {
	sink, _, artifacts, points := newScoringSink(t)

	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)
	// Tamper after signing: the signature no longer matches the payload.
	r.Result.Value = "500"

	if err := sink.Save(r, "sha256:keyA", at()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if got := points.Balance(r.AgentID); got != 0 {
		t.Errorf("Balance = %v, want 0 for an invalid receipt", got)
	}
	if artifacts.Len() != 0 {
		t.Errorf("artifacts = %d, want 0 — an invalid receipt must not claim an artifact", artifacts.Len())
	}
}

// RED TEAM at the integration level -------------------------------------

// TestScoringSink_FiveAgentsOneArtifactYieldsOneCredit is the S3-8 red team, run
// through the real sink rather than against the scorer in isolation.
//
// Five *distinct, validly signed* agents submit the identical observation. Each
// has a different receipt id, so the points ledger's idempotency cannot be what
// stops them; only the global dedup ledger can. Exactly one may be credited.
func TestScoringSink_FiveAgentsOneArtifactYieldsOneCredit(t *testing.T) {
	sink, _, artifacts, points := newScoringSink(t)

	const url = "https://example.com/same"
	const cHash = "sha256:7b2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e"

	type earner struct {
		agent  string
		points float64
	}
	var earners []earner

	for i := 1; i <= 5; i++ {
		key := testKey(i)
		agent, err := receipt.DeriveAgentID(key, 8453)
		if err != nil {
			t.Fatalf("DeriveAgentID(%d): %v", i, err)
		}

		// A distinct receipt id per agent, so replay protection is NOT what
		// rejects four of them.
		receiptID := "0x" + strings.Repeat("0", 62) + fmt.Sprintf("%02x", i)
		r := mkProbe(t, key, url, cHash, receiptID, testEpoch)

		if err := sink.Save(r, "sha256:same", at()); err != nil {
			t.Fatalf("agent %d: Save: %v", i, err)
		}
		earners = append(earners, earner{agent: agent})
	}

	credited := 0
	total := 0.0
	for i, e := range earners {
		got := points.Balance(e.agent)
		earners[i].points = got
		if got > 0 {
			credited++
		}
		total += got
	}

	if credited != 1 {
		t.Errorf("%d of 5 agents were credited, want exactly 1 (S3-8 at the integration level)", credited)
	}
	if !almostEqual(total, scoring.BasePoints, 1e-9) {
		t.Errorf("total credited = %v, want %v — farming must not multiply the award", total, scoring.BasePoints)
	}
	if artifacts.Len() != 1 {
		t.Errorf("artifacts = %d, want 1 distinct artifact", artifacts.Len())
	}
}

// persistence ordering --------------------------------------------------

// TestScoringSink_PersistsBeforeScoring: losing points is recoverable, paying for
// nothing is not, so the receipt must be stored even when scoring cannot run.
func TestScoringSink_PersistsBeforeScoring(t *testing.T) {
	inner := newFakeStore()
	sink := &ScoringSink{
		Inner: inner,
		// Artifacts and Points deliberately nil: scoring cannot run at all.
		Clock: func() time.Time { return time.Unix(1791015800, 0) },
	}

	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)
	if err := sink.Save(r, "sha256:keyA", at()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if inner.Count() != 1 {
		t.Error("the receipt must be persisted even when scoring is unavailable")
	}
}

func TestScoringSink_RequiresInnerSink(t *testing.T) {
	sink := &ScoringSink{}
	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)
	if err := sink.Save(r, "", at()); err == nil {
		t.Error("a sink with no inner store must refuse to save")
	}
}

// TestScoringSink_InnerFailurePropagates: a failed write must surface, because a
// receipt that was not stored is lost work.
func TestScoringSink_InnerFailurePropagates(t *testing.T) {
	inner := newFakeStore()
	inner.failNext = errors.New("disk full")

	sink := &ScoringSink{
		Inner:     inner,
		Artifacts: scoring.NewMemLedger(),
		Points:    scoring.NewMemPointsLedger(),
		Clock:     func() time.Time { return time.Unix(1791015800, 0) },
	}

	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)
	err := sink.Save(r, "", at())
	if err == nil {
		t.Fatal("a write failure must propagate")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error should wrap the cause, got %v", err)
	}
}

// diversity --------------------------------------------------------------

// TestScoringSink_DiversityPenalisesSameDomain: repeated work on one host must
// score less than the first time.
func TestScoringSink_DiversityPenalisesSameDomain(t *testing.T) {
	sink, _, _, points := newScoringSink(t)
	key := testKey(1)

	for i, path := range []string{"/a", "/b", "/c"} {
		id := "0x" + strings.Repeat("cd", 31) + fmt.Sprintf("%02x", i)
		r := mkProbe(t, key, "https://same-host.example"+path, contentHash(0x10+i), id, testEpoch)
		if err := sink.Save(r, fmt.Sprintf("sha256:k%d", i), at()); err != nil {
			t.Fatalf("Save %s: %v", path, err)
		}
	}

	entries := points.Entries()
	if len(entries) != 3 {
		t.Fatalf("Entries = %d, want 3", len(entries))
	}
	if !(entries[2].Points < entries[0].Points) {
		t.Errorf("third award (%v) should be smaller than the first (%v) on the same domain",
			entries[2].Points, entries[0].Points)
	}
}

// TestScoringSink_DifferentDomainsNotPenalised is the converse, and is what caught
// an earlier version that counted every credit as same-domain.
func TestScoringSink_DifferentDomainsNotPenalised(t *testing.T) {
	sink, _, _, points := newScoringSink(t)
	key := testKey(1)

	for i, host := range []string{"https://one.example/a", "https://two.example/a", "https://three.example/a"} {
		id := "0x" + strings.Repeat("ef", 31) + fmt.Sprintf("%02x", i)
		r := mkProbe(t, key, host, contentHash(0x20+i), id, testEpoch)
		if err := sink.Save(r, fmt.Sprintf("sha256:d%d", i), at()); err != nil {
			t.Fatalf("Save %s: %v", host, err)
		}
	}

	entries := points.Entries()
	if len(entries) != 3 {
		t.Fatalf("Entries = %d, want 3", len(entries))
	}

	// Work spread across hosts must not be penalised for diversity.
	//
	// The awards are not exactly BasePoints, because the per-agent epoch budget
	// attenuates each credit slightly (BudgetFactor = 1 - earned/cap). That is a
	// different and much smaller effect than diversity — which would halve the
	// award for a second repeat on the same domain. Bounding below at 99% of the
	// base therefore isolates diversity: the values must be nowhere near the
	// 50% a same-domain penalty would produce.
	for i, e := range entries {
		if e.Points < scoring.BasePoints*0.99 {
			t.Errorf("entry %d on a distinct domain scored %v, want close to the full %v "+
				"(a diversity penalty would indicate domains are being conflated)",
				i, e.Points, scoring.BasePoints)
		}
	}
}

// helpers ----------------------------------------------------------------

func TestDescribeVerdict(t *testing.T) {
	if got := DescribeVerdict(scoring.Verdict{Points: 10}); !strings.Contains(got, "10") {
		t.Errorf("DescribeVerdict = %q", got)
	}
	if got := DescribeVerdict(scoring.Verdict{Points: 0, Reason: "already seen"}); !strings.Contains(got, "already seen") {
		t.Errorf("DescribeVerdict = %q", got)
	}
	if got := DescribeVerdict(scoring.Verdict{}); got != "no credit" {
		t.Errorf("DescribeVerdict = %q, want \"no credit\"", got)
	}
}

func TestShortArtifact(t *testing.T) {
	got := ShortArtifact("sha256:" + strings.Repeat("ab", 32))
	if strings.HasPrefix(got, "sha256:") {
		t.Errorf("the prefix should be trimmed, got %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("a long key should be elided, got %q", got)
	}
	if ShortArtifact("short") != "short" {
		t.Errorf("a short key should pass through")
	}
}

func almostEqual(a, b, tol float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// failLedger is a scoring.Ledger whose Observe always fails, so the emit stage can
// be exercised.
type failLedger struct{ err error }

func (f failLedger) Observe(string, string, time.Time) (bool, error) { return false, f.err }
func (f failLedger) Lookup(string) (scoring.Sighting, bool)          { return scoring.Sighting{}, false }
func (f failLedger) Len() int                                        { return 0 }

// failPoints is a scoring.PointsLedger whose Credit always fails, so the credit
// stage can be exercised.
type failPoints struct{ err error }

func (f failPoints) Credit(string, string, uint64, float64, time.Time) (bool, error) {
	return false, f.err
}
func (f failPoints) Balance(string) float64              { return 0 }
func (f failPoints) EpochBalance(string, uint64) float64 { return 0 }
func (f failPoints) Entry(string) (scoring.Entry, bool)  { return scoring.Entry{}, false }
func (f failPoints) Entries() []scoring.Entry            { return nil }
func (f failPoints) AgentCount() int                     { return 0 }

// TestScoringSink_A FailedEmitIsReportedNotSwallowed is the property that was missing:
// a scoring failure must reach a reporter, because a swallowed one is indistinguishable
// from "this receipt earned nothing".
func TestScoringSink_FailedEmitIsReportedNotSwallowed(t *testing.T) {
	inner := newFakeStore()
	var gotStage string
	var gotErr error

	sink := &ScoringSink{
		Inner:     inner,
		Receipts:  inner,
		Artifacts: failLedger{err: errLedgerDown},
		Points:    scoring.NewMemPointsLedger(),
		Clock:     func() time.Time { return at() },
		OnError: func(_ *receipt.Receipt, stage string, err error) {
			gotStage, gotErr = stage, err
		},
	}

	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)
	// Save must still succeed: the receipt is stored and can be re-scored.
	if err := sink.Save(r, "sha256:keyA", at()); err != nil {
		t.Fatalf("a scoring failure must not fail Save, got: %v", err)
	}
	if inner.Count() != 1 {
		t.Fatal("the receipt must still be persisted")
	}
	if gotStage != "emit" {
		t.Errorf("the emit failure must be reported as stage \"emit\", got %q", gotStage)
	}
	if gotErr == nil {
		t.Error("the error must be reported, not just the stage")
	}
}

// TestScoringSink_FailedCreditIsReported is the case a miner most needs: the receipt
// EARNED points that were not recorded.
func TestScoringSink_FailedCreditIsReported(t *testing.T) {
	inner := newFakeStore()
	var gotStage string

	sink := &ScoringSink{
		Inner:     inner,
		Receipts:  inner,
		Artifacts: scoring.NewMemLedger(),
		Points:    failPoints{err: errLedgerDown},
		Clock:     func() time.Time { return at() },
		OnError: func(_ *receipt.Receipt, stage string, err error) {
			gotStage = stage
		},
	}

	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)
	if err := sink.Save(r, "sha256:keyA", at()); err != nil {
		t.Fatalf("a credit failure must not fail Save, got: %v", err)
	}
	if gotStage != "credit" {
		t.Errorf("a failed credit must be reported as stage \"credit\", got %q", gotStage)
	}
}

// TestScoringSink_NoErrorWhenAllIsWell keeps the reporter from firing on the happy path.
func TestScoringSink_NoErrorWhenAllIsWell(t *testing.T) {
	sink, _, _, _ := newScoringSink(t)
	called := false
	sink.OnError = func(*receipt.Receipt, string, error) { called = true }

	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)
	if err := sink.Save(r, "sha256:keyA", at()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if called {
		t.Error("OnError must not fire when scoring succeeds")
	}
}

var errLedgerDown = errors.New("ledger unavailable")
