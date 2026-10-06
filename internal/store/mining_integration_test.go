package store_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/mining"
	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/scoring"
	"github.com/relayfirst/relayfirst/internal/store"
)

// These tests exercise the mining→points loop against the *durable* ledgers the
// CLI actually uses, rather than the in-memory ones. The distinction matters:
// the in-memory ledger and the SQLite ledger implement idempotency differently
// (a map lookup versus `ON CONFLICT DO NOTHING`), so a wiring bug that only
// appears on the real path would pass the mining-package tests and still lose
// points in production.

const (
	testKey1 = "0x0000000000000000000000000000000000000000000000000000000000000001"
	testKey2 = "0x0000000000000000000000000000000000000000000000000000000000000002"
	testKey3 = "0x0000000000000000000000000000000000000000000000000000000000000003"
	testKey4 = "0x0000000000000000000000000000000000000000000000000000000000000004"
	testKey5 = "0x0000000000000000000000000000000000000000000000000000000000000005"

	testEpoch = 42
)

func ch(seed int) string { return "sha256:" + strings.Repeat(fmt.Sprintf("%02x", seed), 32) }

func at() time.Time { return time.Unix(1791015800, 0) }

// mkProbe builds a validly signed probe receipt.
func mkProbe(t *testing.T, key, url, cHash, id string) *receipt.Receipt {
	t.Helper()

	agent, err := receipt.DeriveAgentID(key, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	r := &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: id,
		AgentID:   agent,
		Epoch:     testEpoch,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": url},
			SpecHash:      ch(0x3d),
			SelfGenerated: true,
		},
		Work: receipt.Work{
			Provider:   "local",
			Model:      "local/lookup",
			StartedAt:  1791015800,
			FinishedAt: 1791015862,
		},
		Result:       receipt.Result{Value: "200", Hash: ch(0xc1)},
		Anchors:      []receipt.Anchor{{URL: url, ContentHash: cHash, FetchedAt: 1791015810, Status: 200, Bytes: 2048}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}

	// The id is derived from the signed payload, not chosen (S9-0h, finding B2).
	// The `id` parameter is ignored so this fixture cannot accidentally exercise
	// the id guard instead of the dedup behaviour under test.
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

// openStore opens a fresh database in a temp directory.
func openStore(t *testing.T) *store.DB {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "relayfirst.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newSink builds the same sink the CLI builds.
func newSink(db *store.DB) *mining.ScoringSink {
	receipts := store.NewReceiptStore(db)
	ledgers := store.NewScoringLedgers(db)
	return &mining.ScoringSink{
		Inner:     receipts,
		Receipts:  receipts,
		Artifacts: ledgers.Artifacts,
		Work:      ledgers.Work,
		Points:    ledgers.Points,
		Clock:     at,
	}
}

func agentOf(t *testing.T, key string) string {
	t.Helper()
	id, err := receipt.DeriveAgentID(key, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}
	return id
}

// TestMiningLoopCreditsPointsDurably is the core assertion of this change:
// mining through the real store must move the points balance — via the work ledger
// and an explicit settlement now (D1), not a per-receipt credit.
func TestMiningLoopCreditsPointsDurably(t *testing.T) {
	db := openStore(t)
	sink := newSink(db)

	r := mkProbe(t, testKey1, "https://example.com/a", ch(0x7b), "0x"+strings.Repeat("ab", 32))
	if err := sink.Save(r, "sha256:k1", at()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ledgers := store.NewScoringLedgers(db)
	agent := agentOf(t, testKey1)

	// Before settlement the work exists but no points do.
	if got := ledgers.Work.Count(); got != 1 {
		t.Errorf("work records = %d, want 1", got)
	}
	if got := ledgers.Points.Balance(agent); got != 0 {
		t.Errorf("points before settlement = %v, want 0", got)
	}

	// Settling turns the work into points.
	if _, err := sink.Finalize(r.Epoch, at()); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if got := ledgers.Points.Balance(agent); got <= 0 {
		t.Errorf("Balance after settlement = %v, want a positive award", got)
	}
	if got := store.NewReceiptStore(db).Count(); got != 1 {
		t.Errorf("receipts = %d, want 1", got)
	}
}

// TestMiningLoopReplayIsNotDoubleCredited: the SQLite ledger's ON CONFLICT clause
// must make a replay a no-op, exactly like the in-memory one.
func TestMiningLoopReplayIsNotDoubleCredited(t *testing.T) {
	db := openStore(t)
	sink := newSink(db)

	r := mkProbe(t, testKey1, "https://example.com/a", ch(0x7b), "0x"+strings.Repeat("ab", 32))

	for i := 0; i < 5; i++ {
		if err := sink.Save(r, "sha256:k1", at()); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	ledgers := store.NewScoringLedgers(db)
	agent := agentOf(t, testKey1)

	// A replay must not accumulate work twice.
	if got := ledgers.Work.Count(); got != 1 {
		t.Errorf("work records after 5 saves = %d, want 1", got)
	}
	// Settle twice, as a retry would: the points must not double.
	if _, err := sink.Finalize(r.Epoch, at()); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	first := ledgers.Points.Balance(agent)
	if _, err := sink.Finalize(r.Epoch, at()); err != nil {
		t.Fatalf("Finalize (second): %v", err)
	}
	if got := ledgers.Points.Balance(agent); got != first {
		t.Errorf("Balance after re-settling = %v, want the same %v", got, first)
	}
	if got := len(ledgers.Points.Entries()); got != 1 {
		t.Errorf("entries = %d, want 1", got)
	}
	if got := store.NewReceiptStore(db).Count(); got != 1 {
		t.Errorf("receipts = %d, want 1", got)
	}
}

// TestMiningLoopInvalidReceiptEarnsNothingDurably: a tampered receipt must not be
// credited and must not claim an artifact.
func TestMiningLoopInvalidReceiptEarnsNothingDurably(t *testing.T) {
	db := openStore(t)
	sink := newSink(db)

	r := mkProbe(t, testKey1, "https://example.com/a", ch(0x7b), "0x"+strings.Repeat("ab", 32))
	r.Result.Value = "500" // tamper after signing

	if err := sink.Save(r, "sha256:k1", at()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ledgers := store.NewScoringLedgers(db)
	if got := ledgers.Points.Balance(agentOf(t, testKey1)); got != 0 {
		t.Errorf("Balance = %v, want 0", got)
	}
	if got := ledgers.Artifacts.Len(); got != 0 {
		t.Errorf("artifacts = %d, want 0 — an invalid receipt must not claim one", got)
	}
}

// TestMiningLoopFiveAgentsOneArtifactDurably is the S3-8 red team against the real
// database. Five distinct, validly signed agents submit the same observation;
// exactly one may be credited, and the total must not multiply.
func TestMiningLoopFiveAgentsOneArtifactDurably(t *testing.T) {
	db := openStore(t)
	sink := newSink(db)

	const url = "https://example.com/same"
	const cHash = "sha256:7b2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e"

	keys := []string{testKey1, testKey2, testKey3, testKey4, testKey5}

	for i, key := range keys {
		// Distinct receipt ids, so replay protection cannot be what rejects them.
		id := "0x" + strings.Repeat("0", 62) + fmt.Sprintf("%02x", i+1)
		r := mkProbe(t, key, url, cHash, id)
		if err := sink.Save(r, "sha256:same", at()); err != nil {
			t.Fatalf("agent %d: Save: %v", i, err)
		}
	}

	ledgers := store.NewScoringLedgers(db)

	// The dedup gate is at the WORK level (D1). Exactly one agent may have recorded work.
	credited := 0
	total := 0.0
	for _, key := range keys {
		recs, err := ledgers.Work.ForAgent(agentOf(t, key), testEpoch)
		if err != nil {
			t.Fatalf("ForAgent: %v", err)
		}
		if len(recs) > 0 {
			credited++
			total += recs[0].Work
		}
	}

	if credited != 1 {
		t.Errorf("%d of 5 agents recorded work, want exactly 1", credited)
	}
	if diff := total - scoring.BasePoints; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("total work = %v, want %v", total, scoring.BasePoints)
	}
	if got := ledgers.Artifacts.Len(); got != 1 {
		t.Errorf("distinct artifacts = %d, want 1", got)
	}

	// Settlement pays exactly one agent.
	if _, err := sink.Finalize(testEpoch, at()); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	paid := 0
	for _, key := range keys {
		if ledgers.Points.Balance(agentOf(t, key)) > 0 {
			paid++
		}
	}
	if paid != 1 {
		t.Errorf("settlement paid %d agents, want exactly 1", paid)
	}
}

// TestMiningPointsSurviveReopen: credit written through one connection must be
// visible after the database is closed and reopened. An in-memory-only balance
// would look identical inside one process and be worthless in reality.
func TestMiningPointsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "relayfirst.db")

	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	sink := newSink(db)
	r := mkProbe(t, testKey1, "https://example.com/a", ch(0x7b), "0x"+strings.Repeat("ab", 32))
	if err := sink.Save(r, "sha256:k1", at()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Settle before closing, so the points (not just the work) are what must survive.
	if _, err := sink.Finalize(r.Epoch, at()); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	ledgers := store.NewScoringLedgers(reopened)
	// The settled points survive...
	if got := ledgers.Points.Balance(agentOf(t, testKey1)); got <= 0 {
		t.Errorf("balance after reopen = %v, want a positive settled balance", got)
	}
	// ...and so does the work, so a re-settlement after reopen recomputes the same.
	if got := ledgers.Work.Count(); got != 1 {
		t.Errorf("work records after reopen = %d, want 1", got)
	}
}
