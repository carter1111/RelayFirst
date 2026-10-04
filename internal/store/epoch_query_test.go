package store_test

import (
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/store"
)

// These tests cover the epoch-scoped queries that the anchoring path and the anti-collusion
// ratio cap depend on.
//
// Both matter for the same reason: the queries replaced in-Go filtering, and an error in
// either would silently change which receipts belong to a root, or how much an agent is
// judged to have produced.

func openEpochStore(t *testing.T) *store.ReceiptStore {
	t.Helper()

	db, err := store.Open(t.TempDir() + "/e.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store.NewReceiptStore(db)
}

// mkEpochReceipt builds a signed receipt in a given epoch with a given status.
func mkEpochReceipt(t *testing.T, key string, epoch uint64, id string, status receipt.VerificationStatus) *receipt.Receipt {
	t.Helper()

	r := mkStoredReceipt(t, key,
		"https://example.com/"+id,
		expCh(int(epoch)+int(id[2])),
		id,
		int64(1791015800))
	r.Epoch = epoch
	r.Verification = receipt.Verification{Status: status}

	// Re-sign so the receipt is internally consistent for its epoch and status.
	if err := r.Sign(key); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return r
}

func TestReceiptStore_ByEpochFiltersInSQL(t *testing.T) {
	rs := openEpochStore(t)

	for i, epoch := range []uint64{1, 1, 2, 3, 3, 3} {
		r := mkEpochReceipt(t, storeTestKey1, epoch, padID(i), receipt.VerificationPending)
		if err := rs.Save(r, "", time.Unix(1791015800, 0)); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	for epoch, want := range map[uint64]int{1: 2, 2: 1, 3: 3, 9: 0} {
		got, err := rs.ByEpoch(epoch)
		if err != nil {
			t.Fatalf("ByEpoch(%d): %v", epoch, err)
		}
		if len(got) != want {
			t.Errorf("ByEpoch(%d) returned %d, want %d", epoch, len(got), want)
		}
		for _, r := range got {
			if r.Epoch != epoch {
				t.Errorf("ByEpoch(%d) returned a receipt from epoch %d", epoch, r.Epoch)
			}
		}
	}
}

// TestReceiptStore_ByEpochIsSorted: the ordering must be deterministic, because an
// independent party recomputing an epoch root has to arrive at the same tree.
func TestReceiptStore_ByEpochIsSorted(t *testing.T) {
	rs := openEpochStore(t)

	for i := 0; i < 5; i++ {
		r := mkEpochReceipt(t, storeTestKey1, 7, padID(i), receipt.VerificationPending)
		if err := rs.Save(r, "", time.Unix(1791015800, 0)); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	got, err := rs.ByEpoch(7)
	if err != nil {
		t.Fatalf("ByEpoch: %v", err)
	}
	for i := 1; i < len(got); i++ {
		if got[i].ReceiptID < got[i-1].ReceiptID {
			t.Errorf("ByEpoch is not sorted by receipt id at index %d", i)
		}
	}
}

func TestReceiptStore_CountByEpochAndAgent(t *testing.T) {
	rs := openEpochStore(t)

	// Two receipts from agent 1 in epoch 5, one from agent 2.
	for i := 0; i < 2; i++ {
		r := mkEpochReceipt(t, storeTestKey1, 5, padID(i), receipt.VerificationPending)
		if err := rs.Save(r, "", time.Unix(1791015800, 0)); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	r := mkEpochReceipt(t, storeTestKey2, 5, padID(9), receipt.VerificationPending)
	if err := rs.Save(r, "", time.Unix(1791015800, 0)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	agent1, err := receipt.DeriveAgentID(storeTestKey1, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	got, err := rs.CountByEpochAndAgent(5, agent1)
	if err != nil {
		t.Fatalf("CountByEpochAndAgent: %v", err)
	}
	if got != 2 {
		t.Errorf("count = %d, want 2", got)
	}

	if got, err := rs.CountByEpochAndAgent(5, "agent:unknown"); err != nil || got != 0 {
		t.Errorf("unknown agent count = %d, %v; want 0, nil", got, err)
	}
}

// TestReceiptStore_CountVerifiedReadsTheStoredJSON: the verification block is not part of the
// signed payload, so it lives inside the stored body and is read with json_extract. This
// asserts that the agreed path actually finds it.
func TestReceiptStore_CountVerifiedReadsTheStoredJSON(t *testing.T) {
	rs := openEpochStore(t)

	agent, err := receipt.DeriveAgentID(storeTestKey1, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	statuses := []receipt.VerificationStatus{
		receipt.VerificationVerified,
		receipt.VerificationVerified,
		receipt.VerificationRejected,
		receipt.VerificationPending,
	}
	for i, status := range statuses {
		r := mkEpochReceipt(t, storeTestKey1, 4, padID(i), status)
		if err := rs.Save(r, "", time.Unix(1791015800, 0)); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	verified, err := rs.CountVerifiedByEpochAndAgent(4, agent, string(receipt.VerificationVerified))
	if err != nil {
		t.Fatalf("CountVerified: %v", err)
	}
	if verified != 2 {
		t.Errorf("verified count = %d, want 2", verified)
	}

	rejected, err := rs.CountVerifiedByEpochAndAgent(4, agent, string(receipt.VerificationRejected))
	if err != nil {
		t.Fatalf("CountVerified: %v", err)
	}
	if rejected != 1 {
		t.Errorf("rejected count = %d, want 1", rejected)
	}

	pending, err := rs.CountVerifiedByEpochAndAgent(4, agent, string(receipt.VerificationPending))
	if err != nil {
		t.Fatalf("CountVerified: %v", err)
	}
	if pending != 1 {
		t.Errorf("pending count = %d, want 1", pending)
	}
}

func TestReceiptStore_CountVerifiedRejectsEmptyStatus(t *testing.T) {
	rs := openEpochStore(t)
	if _, err := rs.CountVerifiedByEpochAndAgent(1, "agent:a", ""); err == nil {
		t.Error("an empty status must be rejected rather than matching everything")
	}
}

// ---------------------------------------------------------------- Activity

// TestActivity_CountsProducedAndVerified is the piece that makes the ratio cap a real
// constraint: it supplies the counts the policy is measured against.
func TestActivity_CountsProducedAndVerified(t *testing.T) {
	rs := openEpochStore(t)
	activity := store.NewActivity(rs)

	agent, err := receipt.DeriveAgentID(storeTestKey1, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	// Three produced: one pending, one verified, one rejected.
	for i, status := range []receipt.VerificationStatus{
		receipt.VerificationPending,
		receipt.VerificationVerified,
		receipt.VerificationRejected,
	} {
		r := mkEpochReceipt(t, storeTestKey1, 6, padID(i), status)
		if err := rs.Save(r, "", time.Unix(1791015800, 0)); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	produced, err := activity.Produced(6, agent)
	if err != nil {
		t.Fatalf("Produced: %v", err)
	}
	if produced != 3 {
		t.Errorf("produced = %d, want 3", produced)
	}

	// Verified counts every verdict the agent has cast, not only agreements: counting only
	// agreements would let a verifier issue unlimited rejections for free.
	verified, err := activity.Verified(6, agent)
	if err != nil {
		t.Fatalf("Verified: %v", err)
	}
	if verified != 2 {
		t.Errorf("verified = %d, want 2 (one verified + one rejected)", verified)
	}
}

// TestActivity_FailsClosedWithoutAStore: an unreadable count must be an error, never a silent
// zero. Zero production means "pure verifier", which would refuse a legitimate verifier for
// the wrong reason.
func TestActivity_FailsClosedWithoutAStore(t *testing.T) {
	var activity *store.Activity

	if _, err := activity.Produced(1, "agent:a"); err == nil {
		t.Error("Produced with no store must error, not report zero")
	}
	if _, err := activity.Verified(1, "agent:a"); err == nil {
		t.Error("Verified with no store must error, not report zero")
	}
}

func padID(n int) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 64)
	for i := range out {
		out[i] = hexdigits[(n+i)%16]
	}
	return "0x" + string(out)
}
