package store_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/scoring"
	"github.com/relayfirst/relayfirst/internal/store"
)

// mkStoredReceipt builds a validly signed probe receipt with a given id, agent and
// anchor, so the list/export paths can be exercised without a miner.
func mkStoredReceipt(t *testing.T, key, url, cHash, id string, created int64) *receipt.Receipt {
	t.Helper()

	agent, err := receipt.DeriveAgentID(key, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	r := &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: id,
		AgentID:   agent,
		Epoch:     scoring.EpochOf(time.Unix(created, 0)),
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": url},
			SpecHash:      "sha256:" + fmt.Sprintf("%064x", created),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: created, FinishedAt: created + 1},
		Result:       receipt.Result{Value: "200", Hash: "sha256:" + cHash[7:]},
		Anchors:      []receipt.Anchor{{URL: url, ContentHash: cHash, FetchedAt: created, Status: 200, Bytes: 10}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	if err := r.Sign(key); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return r
}

func openReceiptStore(t *testing.T) *store.ReceiptStore {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store.NewReceiptStore(db)
}

const (
	storeTestKey1 = "0x0000000000000000000000000000000000000000000000000000000000000001"
	storeTestKey2 = "0x0000000000000000000000000000000000000000000000000000000000000002"
)

func expCh(seed int) string { return fmt.Sprintf("sha256:%064x", seed) }

// TestReceiptStore_AllIsOldestFirst: an export should read as a chronological
// record, not an arbitrary shuffle.
func TestReceiptStore_AllIsOldestFirst(t *testing.T) {
	rs := openReceiptStore(t)

	for i := 0; i < 5; i++ {
		r := mkStoredReceipt(t, storeTestKey1,
			fmt.Sprintf("https://example.com/%d", i),
			expCh(0x100+i),
			fmt.Sprintf("0x%064x", i+1),
			1791015800+int64(i))
		if err := rs.Save(r, "", time.Unix(1791015800+int64(i), 0)); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	all, err := rs.All(0)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("All returned %d, want 5", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].ReceiptID < all[i-1].ReceiptID {
			t.Errorf("receipts are not oldest first at index %d", i)
		}
	}
}

func TestReceiptStore_AllLimit(t *testing.T) {
	rs := openReceiptStore(t)

	for i := 0; i < 6; i++ {
		r := mkStoredReceipt(t, storeTestKey1,
			fmt.Sprintf("https://example.com/%d", i),
			expCh(0x200+i),
			fmt.Sprintf("0x%064x", i+1),
			1791015800+int64(i))
		if err := rs.Save(r, "", time.Unix(1791015800+int64(i), 0)); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	got, err := rs.All(3)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("All(3) returned %d, want 3", len(got))
	}
}

// TestReceiptStore_ByAgentAllIsNotEpochScoped: passing zero epochs to ByAgent would
// silently select only epoch 0, which is why this method exists separately.
func TestReceiptStore_ByAgentAllIsNotEpochScoped(t *testing.T) {
	rs := openReceiptStore(t)

	agent1, err := receipt.DeriveAgentID(storeTestKey1, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	// Two receipts from agent 1 on widely separated days, plus one from agent 2.
	times := []struct {
		key  string
		seed int
		id   int
		unix int64
	}{
		{storeTestKey1, 0x300, 1, 1790841600},
		{storeTestKey1, 0x301, 2, 1791015800},
		{storeTestKey2, 0x302, 3, 1791015800},
	}

	for _, c := range times {
		r := mkStoredReceipt(t, c.key, fmt.Sprintf("https://example.com/%d", c.id), expCh(c.seed), fmt.Sprintf("0x%064x", c.id), c.unix)
		if err := rs.Save(r, "", time.Unix(c.unix, 0)); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	got, err := rs.ByAgentAll(agent1)
	if err != nil {
		t.Fatalf("ByAgentAll: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ByAgentAll returned %d, want 2 across both epochs", len(got))
	}
	for _, r := range got {
		if r.AgentID != agent1 {
			t.Errorf("ByAgentAll leaked a receipt for %q", r.AgentID)
		}
	}
}

// TestReceiptStore_AnchorsForCountsDistinctAndSkipsInline asserts that the anchor
// count is what a miner reads to judge how much real evidence their work rests on,
// so an inflated number would be worse than no number.
func TestReceiptStore_AnchorsForCountsDistinctAndSkipsInline(t *testing.T) {
	rs := openReceiptStore(t)

	// r1 and r2 share a content hash, so it must be counted once.
	r1 := mkStoredReceipt(t, storeTestKey1, "https://a.example", expCh(0x400), fmt.Sprintf("0x%064x", 1), 1791015800)
	r2 := mkStoredReceipt(t, storeTestKey1, "https://b.example", expCh(0x400), fmt.Sprintf("0x%064x", 2), 1791015801)

	// r3 carries a second distinct real anchor.
	r3 := mkStoredReceipt(t, storeTestKey1, "https://c.example", expCh(0x401), fmt.Sprintf("0x%064x", 3), 1791015802)

	// r4 is a compute-style receipt whose only anchor is synthetic, so it carries
	// no independently re-fetchable evidence and must not be counted.
	r4 := mkStoredReceipt(t, storeTestKey1, "https://d.example", expCh(0x402), fmt.Sprintf("0x%064x", 4), 1791015803)
	r4.Anchors = []receipt.Anchor{{URL: "inline", ContentHash: expCh(0x403)}}
	if err := r4.Sign(storeTestKey1); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	for i, r := range []*receipt.Receipt{r1, r2, r3, r4} {
		if err := rs.Save(r, "", time.Unix(1791015800+int64(i), 0)); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	all, err := rs.All(0)
	if err != nil {
		t.Fatalf("All: %v", err)
	}

	// 0x400 (shared by r1 and r2, counted once) + 0x401 = 2. r4's inline anchor is
	// excluded because it is not independently re-fetchable.
	if got := rs.AnchorsFor(all); got != 2 {
		t.Errorf("AnchorsFor = %d, want 2 distinct re-fetchable anchors", got)
	}
}

// TestReceiptStore_ExportRoundTripVerifies is the S6-6 acceptance property.
//
// The exported bytes must be exactly the signed bytes, or a user who walks away
// with their data would find it no longer verifies — which would make the export
// worthless precisely when they needed it.
func TestReceiptStore_ExportRoundTripVerifies(t *testing.T) {
	rs := openReceiptStore(t)

	for i := 0; i < 3; i++ {
		r := mkStoredReceipt(t, storeTestKey1,
			fmt.Sprintf("https://example.com/%d", i),
			expCh(0x500+i),
			fmt.Sprintf("0x%064x", i+1),
			1791015800+int64(i))
		if err := rs.Save(r, "", time.Unix(1791015800+int64(i), 0)); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	all, err := rs.All(0)
	if err != nil {
		t.Fatalf("All: %v", err)
	}

	for _, r := range all {
		// The canonical bytes are what an export writes, so round-tripping them
		// through the parser is the real test.
		raw, err := r.MarshalCanonical()
		if err != nil {
			t.Fatalf("MarshalCanonical: %v", err)
		}
		back, err := receipt.Unmarshal(raw)
		if err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if err := back.Validate(nil); err != nil {
			t.Errorf("an exported receipt no longer verifies: %v", err)
		}
	}
}
