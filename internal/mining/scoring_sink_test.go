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

func newScoringSink(t *testing.T) (*ScoringSink, *fakeStore, *scoring.MemLedger, *scoring.MemWorkLedger, *scoring.MemPointsLedger) {
	t.Helper()

	inner := newFakeStore()
	artifacts := scoring.NewMemLedger()
	work := scoring.NewMemWorkLedger()
	points := scoring.NewMemPointsLedger()

	sink := &ScoringSink{
		Inner:     inner,
		Receipts:  inner,
		Artifacts: artifacts,
		Work:      work,
		Points:    points,
		Clock:     func() time.Time { return time.Unix(1791015800, 0) },
	}
	return sink, inner, artifacts, work, points
}

func at() time.Time { return time.Unix(1791015800, 0) }

// work -> settlement ----------------------------------------------------

// TestScoringSink_RecordsWorkNotPoints is the model change (D1) made executable: a
// mined receipt contributes WORK, and NO points appear until the epoch settles.
func TestScoringSink_RecordsWorkNotPoints(t *testing.T) {
	sink, store, _, work, points := newScoringSink(t)

	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)

	if err := sink.Save(r, "sha256:keyA", at()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if store.Count() != 1 {
		t.Fatal("the receipt should have been persisted")
	}

	// Work is recorded...
	if work.Count() != 1 {
		t.Fatalf("work records = %d, want 1", work.Count())
	}
	totals, err := work.Totals(testEpoch)
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	if totals[r.AgentID] != scoring.BasePoints {
		t.Errorf("work total = %v, want %v", totals[r.AgentID], scoring.BasePoints)
	}

	// ...and points are NOT, until settlement.
	if got := points.Balance(r.AgentID); got != 0 {
		t.Errorf("points before settlement = %v, want 0 — a receipt earns work, not points", got)
	}

	// Settlement turns the work into points.
	settled, err := sink.Finalize(testEpoch, at())
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if settled[r.AgentID] <= 0 {
		t.Fatalf("settled points = %v, want > 0", settled[r.AgentID])
	}
	// Compare with tolerance: Balance reads fixed-point micro-points (rounded to 6
	// places), while the returned map is the full precision float. The difference is a
	// storage representation, not a discrepancy in what was settled.
	if got := points.Balance(r.AgentID); !almostEqual(got, settled[r.AgentID], 1e-4) {
		t.Errorf("points after settlement = %v, want ~the settled %v", got, settled[r.AgentID])
	}
}

// TestScoringSink_FinalizeIsIdempotent: re-running settlement must not double-pay.
func TestScoringSink_FinalizeIsIdempotent(t *testing.T) {
	sink, _, _, _, points := newScoringSink(t)

	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)
	if err := sink.Save(r, "sha256:keyA", at()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	first, err := sink.Finalize(testEpoch, at())
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	// Settle again, as a restart or a scheduler retry would.
	if _, err := sink.Finalize(testEpoch, at()); err != nil {
		t.Fatalf("Finalize (second): %v", err)
	}

	if got := points.Balance(r.AgentID); !almostEqual(got, first[r.AgentID], 1e-4) {
		t.Errorf("balance after re-settling = %v, want ~the same %v — settlement must be idempotent",
			got, first[r.AgentID])
	}
	if got := len(points.Entries()); got != 1 {
		t.Errorf("entries = %d, want 1 — a re-run must not add an entry", got)
	}
}

// TestScoringSink_ReplayRecordsWorkOnce: a replayed receipt must not accumulate work
// twice, or settlement would pay it twice.
func TestScoringSink_ReplayRecordsWorkOnce(t *testing.T) {
	sink, _, _, work, _ := newScoringSink(t)

	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)
	for i := 0; i < 5; i++ {
		if err := sink.Save(r, "sha256:keyA", at()); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}
	if got := work.Count(); got != 1 {
		t.Errorf("work records = %d, want 1 — a replay must not accumulate work", got)
	}
	totals, _ := work.Totals(testEpoch)
	if totals[r.AgentID] != scoring.BasePoints {
		t.Errorf("work total = %v, want a single %v", totals[r.AgentID], scoring.BasePoints)
	}
}

// TestScoringSink_InvalidReceiptEarnsNothing guards the boundary: work that does
// not validate must not be recorded, and must not claim the artifact either.
func TestScoringSink_InvalidReceiptEarnsNothing(t *testing.T) {
	sink, _, artifacts, work, points := newScoringSink(t)

	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)
	// Tamper after signing: the signature no longer matches the payload.
	r.Result.Value = "500"

	if err := sink.Save(r, "sha256:keyA", at()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if got := work.Count(); got != 0 {
		t.Errorf("work records = %d, want 0 — an invalid receipt has no work", got)
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
	sink, _, artifacts, work, points := newScoringSink(t)

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

	// The dedup gate is at the WORK level now, so unsettled points are all zero and
	// the check is on who recorded work. Exactly one agent may have done so.
	credited := 0
	total := 0.0
	for i, e := range earners {
		got := 0.0
		if recs, _ := work.ForAgent(e.agent, testEpoch); len(recs) > 0 {
			got = recs[0].Work
		}
		earners[i].points = got
		if got > 0 {
			credited++
		}
		total += got
	}

	if credited != 1 {
		t.Errorf("%d of 5 agents recorded work, want exactly 1 (S3-8 at the integration level)", credited)
	}
	if !almostEqual(total, scoring.BasePoints, 1e-9) {
		t.Errorf("total work = %v, want %v — farming must not multiply the award", total, scoring.BasePoints)
	}
	if artifacts.Len() != 1 {
		t.Errorf("artifacts = %d, want 1 distinct artifact", artifacts.Len())
	}

	// And settlement pays exactly one agent.
	settled, err := sink.Finalize(testEpoch, at())
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	paid := 0
	for _, v := range settled {
		if v > 0 {
			paid++
		}
	}
	if paid != 1 {
		t.Errorf("settlement paid %d agents, want exactly 1", paid)
	}
	if got := points.AgentCount(); got != 1 {
		t.Errorf("credited agents = %d, want 1", got)
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
	sink, _, _, work, _ := newScoringSink(t)
	key := testKey(1)

	for i, path := range []string{"/a", "/b", "/c"} {
		id := "0x" + strings.Repeat("cd", 31) + fmt.Sprintf("%02x", i)
		r := mkProbe(t, key, "https://same-host.example"+path, contentHash(0x10+i), id, testEpoch)
		if err := sink.Save(r, fmt.Sprintf("sha256:k%d", i), at()); err != nil {
			t.Fatalf("Save %s: %v", path, err)
		}
	}

	// Diversity shows up in the recorded WORK (D1): points no longer exist per
	// receipt, so the comparison is between work records.
	recs, err := work.ForAgent(agentOfKey(t, key), testEpoch)
	if err != nil {
		t.Fatalf("ForAgent: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("work records = %d, want 3", len(recs))
	}
	if !(recs[2].Work < recs[0].Work) {
		t.Errorf("third work (%v) should be smaller than the first (%v) on the same domain",
			recs[2].Work, recs[0].Work)
	}
}

// TestScoringSink_DifferentDomainsNotPenalised is the converse, and is what caught
// an earlier version that counted every credit as same-domain.
func TestScoringSink_DifferentDomainsNotPenalised(t *testing.T) {
	sink, _, _, work, _ := newScoringSink(t)
	key := testKey(1)

	for i, host := range []string{"https://one.example/a", "https://two.example/a", "https://three.example/a"} {
		id := "0x" + strings.Repeat("ef", 31) + fmt.Sprintf("%02x", i)
		r := mkProbe(t, key, host, contentHash(0x20+i), id, testEpoch)
		if err := sink.Save(r, fmt.Sprintf("sha256:d%d", i), at()); err != nil {
			t.Fatalf("Save %s: %v", host, err)
		}
	}

	recs, err := work.ForAgent(agentOfKey(t, key), testEpoch)
	if err != nil {
		t.Fatalf("ForAgent: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("work records = %d, want 3", len(recs))
	}

	// Work spread across hosts must not be penalised for diversity. Each record must be
	// the full base work; a diversity penalty would halve a repeat on the same domain,
	// so values nowhere near 50% prove the domains are not being conflated.
	for i, rec := range recs {
		if rec.Work < scoring.BasePoints*0.99 {
			t.Errorf("work %d on a distinct domain was %v, want close to the full %v "+
				"(a diversity penalty would indicate domains are being conflated)",
				i, rec.Work, scoring.BasePoints)
		}
	}
}

// agentOfKey derives the agent id a test key maps to, for looking up its records.
func agentOfKey(t *testing.T, key string) string {
	t.Helper()
	id, err := receipt.DeriveAgentID(key, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}
	return id
}

// helpers ----------------------------------------------------------------

func TestDescribeVerdict(t *testing.T) {
	// A verdict reports WORK now, not points (D1).
	if got := DescribeVerdict(scoring.Verdict{Work: 10}); !strings.Contains(got, "10") {
		t.Errorf("DescribeVerdict = %q", got)
	}
	if got := DescribeVerdict(scoring.Verdict{Work: 0, Reason: "already seen"}); !strings.Contains(got, "already seen") {
		t.Errorf("DescribeVerdict = %q", got)
	}
	if got := DescribeVerdict(scoring.Verdict{}); got != "no work" {
		t.Errorf("DescribeVerdict = %q, want \"no work\"", got)
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

// TestScoringSink_FailedWorkRecordIsReported is the property that was missing: a
// scoring failure must reach a reporter, because a swallowed one is indistinguishable
// from "this receipt earned nothing".
func TestScoringSink_FailedWorkRecordIsReported(t *testing.T) {
	inner := newFakeStore()
	var gotStage string
	var gotErr error

	sink := &ScoringSink{
		Inner:     inner,
		Receipts:  inner,
		Artifacts: failLedger{err: errLedgerDown},
		Work:      scoring.NewMemWorkLedger(),
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
	if gotStage != "record-work" {
		t.Errorf("the failure must be reported as stage \"record-work\", got %q", gotStage)
	}
	if gotErr == nil {
		t.Error("the error must be reported, not just the stage")
	}
}

// TestScoringSink_FinalizeReportsACreditFailure: a settlement that could not write the
// points must return the error rather than appearing to succeed.
//
// Unlike the per-receipt path, settlement is an explicit, once-per-epoch step, so
// failing loudly is correct here — a caller must retry a settlement that did not land.
func TestScoringSink_FinalizeReportsACreditFailure(t *testing.T) {
	inner := newFakeStore()
	work := scoring.NewMemWorkLedger()
	sink := &ScoringSink{
		Inner:     inner,
		Receipts:  inner,
		Artifacts: scoring.NewMemLedger(),
		Work:      work,
		Points:    failPoints{err: errLedgerDown},
		Clock:     func() time.Time { return at() },
	}

	r := mkProbe(t, testKey(1), "https://example.com/a", contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)
	if err := sink.Save(r, "sha256:keyA", at()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := sink.Finalize(testEpoch, at()); err == nil {
		t.Fatal("a settlement that could not credit must return an error, not look successful")
	}
}

// TestScoringSink_NoErrorWhenAllIsWell keeps the reporter from firing on the happy path.
func TestScoringSink_NoErrorWhenAllIsWell(t *testing.T) {
	sink, _, _, _, _ := newScoringSink(t)
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
