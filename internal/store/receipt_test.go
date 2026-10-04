package store

import (
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

func mkReceipt(id, agentID string, epoch uint64) *receipt.Receipt {
	return &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: id,
		AgentID:   agentID,
		Epoch:     epoch,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": "https://example.com"},
			SpecHash:      "sha256:" + strings.Repeat("3d", 32),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1, FinishedAt: 2},
		Result:       receipt.Result{Value: "200", Hash: "sha256:" + strings.Repeat("c1", 32)},
		Anchors:      []receipt.Anchor{{URL: "https://example.com", ContentHash: "sha256:" + strings.Repeat("7b", 32), FetchedAt: 1, Status: 200, Bytes: 10}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
}

func TestReceiptStore_SaveAndLoad(t *testing.T) {
	db, _ := newTestDB(t)
	s := NewReceiptStore(db)
	at := time.Unix(1791015800, 0)

	r := mkReceipt("0xaaa", agentA, 42)
	if err := s.Save(r, "sha256:key1", at); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := s.Load("0xaaa")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ok {
		t.Fatal("Load should find the receipt")
	}
	if got.ReceiptID != r.ReceiptID || got.AgentID != r.AgentID || got.Epoch != r.Epoch {
		t.Errorf("loaded receipt differs: %+v", got)
	}
	if got.Result.Value != "200" {
		t.Errorf("result value = %q, want 200", got.Result.Value)
	}
}

// TestReceiptStore_RoundTripPreservesSignature is why the canonical body is stored
// verbatim rather than reassembled from columns. A receipt read back must still
// pass its own structural validation.
func TestReceiptStore_RoundTripPreservesSignature(t *testing.T) {
	db, _ := newTestDB(t)
	s := NewReceiptStore(db)
	at := time.Unix(1791015800, 0)

	r := mkReceipt("0xbbb", agentA, 42)
	if err := r.ValidateStructure(); err != nil {
		t.Fatalf("the fixture itself should be valid: %v", err)
	}
	if err := s.Save(r, "", at); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := s.Load("0xbbb")
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if err := got.ValidateStructure(); err != nil {
		t.Errorf("a stored receipt must still validate after a round trip: %v", err)
	}

	// The canonical bytes must be byte-identical, since the signature covers them.
	before, _ := r.MarshalCanonical()
	after, _ := got.MarshalCanonical()
	if string(before) != string(after) {
		t.Errorf("canonical form changed across persistence:\n  before: %s\n  after:  %s", before, after)
	}
}

func TestReceiptStore_LoadMissing(t *testing.T) {
	db, _ := newTestDB(t)
	s := NewReceiptStore(db)

	_, ok, err := s.Load("0xmissing")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ok {
		t.Error("Load for an unknown id should report not-found")
	}
}

// TestReceiptStore_SaveIsIdempotent: receipts are immutable once signed, so a
// replayed save must not alter the stored row.
func TestReceiptStore_SaveIsIdempotent(t *testing.T) {
	db, _ := newTestDB(t)
	s := NewReceiptStore(db)
	at := time.Unix(1791015800, 0)

	r := mkReceipt("0xccc", agentA, 1)
	if err := s.Save(r, "", at); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A different receipt claiming the same id must not overwrite the original.
	impostor := mkReceipt("0xccc", agentB, 99)
	if err := s.Save(impostor, "", at); err != nil {
		t.Fatalf("Save impostor: %v", err)
	}

	got, _, err := s.Load("0xccc")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AgentID != agentA {
		t.Errorf("stored receipt was overwritten: agent is %q, want the original %q", got.AgentID, agentA)
	}
	if s.Count() != 1 {
		t.Errorf("Count = %d, want 1", s.Count())
	}
}

func TestReceiptStore_SurvivesReopen(t *testing.T) {
	db, path := newTestDB(t)
	s := NewReceiptStore(db)
	at := time.Unix(1791015800, 0)

	if err := s.Save(mkReceipt("0xddd", agentA, 7), "sha256:k", at); err != nil {
		t.Fatalf("Save: %v", err)
	}
	db.Close()

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()

	got, ok, err := NewReceiptStore(db2).Load("0xddd")
	if err != nil || !ok {
		t.Fatalf("Load after reopen: ok=%v err=%v", ok, err)
	}
	if got.Epoch != 7 {
		t.Errorf("epoch = %d, want 7 — receipts must be durable", got.Epoch)
	}
}

func TestReceiptStore_Queries(t *testing.T) {
	db, _ := newTestDB(t)
	s := NewReceiptStore(db)
	at := time.Unix(1791015800, 0)

	// Two for agentA in epoch 42, one for agentA in epoch 43, one for agentB.
	mustSave := func(id, agent string, epoch uint64, key string) {
		t.Helper()
		if err := s.Save(mkReceipt(id, agent, epoch), key, at); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}
	mustSave("0x1", agentA, 42, "sha256:k1")
	mustSave("0x2", agentA, 42, "sha256:k1")
	mustSave("0x3", agentA, 43, "sha256:k3")
	mustSave("0x4", agentB, 42, "sha256:k4")

	byAgent, err := s.ByAgent(agentA, 42)
	if err != nil {
		t.Fatalf("ByAgent: %v", err)
	}
	if len(byAgent) != 2 {
		t.Errorf("ByAgent(agentA, 42) = %d receipts, want 2", len(byAgent))
	}

	byArtifact, err := s.ByArtifact("sha256:k1")
	if err != nil {
		t.Fatalf("ByArtifact: %v", err)
	}
	if len(byArtifact) != 2 {
		t.Errorf("ByArtifact(k1) = %d receipts, want 2", len(byArtifact))
	}
}

func TestReceiptStore_RejectsBadInput(t *testing.T) {
	db, _ := newTestDB(t)
	s := NewReceiptStore(db)
	at := time.Unix(1791015800, 0)

	if err := s.Save(nil, "", at); err == nil {
		t.Error("a nil receipt must be rejected")
	}

	noID := mkReceipt("", agentA, 1)
	if err := s.Save(noID, "", at); err == nil {
		t.Error("a receipt with no id must be rejected")
	}

	if _, err := s.ByArtifact(""); err == nil {
		t.Error("an empty artifact key must be rejected")
	}
}
