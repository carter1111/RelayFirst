package store

import (
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// mkReceipt builds a valid receipt whose id is derived from its signed payload.
//
// It returns the receipt and its derived id. Callers must use the returned id for
// lookups rather than assuming the label they passed in: Validate rejects a
// free-standing id (S9-0h, finding B2), so the derived value is the only one that
// will match.
//
// `variant` distinguishes otherwise-identical fixtures. Since the id is a hash of
// the payload, two receipts built with the same agent and epoch would collapse to
// the same id and the second save would be a no-op — which silently broke a test
// that needed two distinct rows. The variant feeds a signed field, so the ids
// genuinely differ.
func mkReceipt(agentID string, epoch uint64, variant string) (*receipt.Receipt, string) {
	r := &receipt.Receipt{
		Schema:  receipt.Schema,
		AgentID: agentID,
		Epoch:   epoch,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": "https://example.com/" + variant},
			SpecHash:      "sha256:" + strings.Repeat("3d", 32),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1, FinishedAt: 2},
		Result:       receipt.Result{Value: "200", Hash: "sha256:" + strings.Repeat("c1", 32)},
		Anchors:      []receipt.Anchor{{URL: "https://example.com/" + variant, ContentHash: "sha256:" + strings.Repeat("7b", 32), FetchedAt: 1, Status: 200, Bytes: 10}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}

	derived, err := r.DerivedReceiptID()
	if err != nil {
		panic(err)
	}
	r.ReceiptID = derived
	return r, derived
}

func TestReceiptStore_SaveAndLoad(t *testing.T) {
	db, _ := newTestDB(t)
	s := NewReceiptStore(db)
	at := time.Unix(1791015800, 0)

	r, id := mkReceipt(agentA, 42, "a")
	if err := s.Save(r, "sha256:key1", at); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := s.Load(id)
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

	r, id := mkReceipt(agentA, 42, "b")
	if err := r.ValidateStructure(); err != nil {
		t.Fatalf("the fixture itself should be valid: %v", err)
	}
	if err := s.Save(r, "", at); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := s.Load(id)
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

	r, id := mkReceipt(agentA, 1, "c")
	if err := s.Save(r, "", at); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A different receipt claiming the same id must not overwrite the original.
	// A different receipt must not be able to overwrite by reusing an id: the id
	// is derived from the payload, so an impostor necessarily has a different one.
	impostor, _ := mkReceipt(agentB, 99, "d")
	impostor.ReceiptID = id
	if err := s.Save(impostor, "", at); err != nil {
		t.Fatalf("Save impostor: %v", err)
	}

	got, _, err := s.Load(id)
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

	rD, idD := mkReceipt(agentA, 7, "e")
	if err := s.Save(rD, "sha256:k", at); err != nil {
		t.Fatalf("Save: %v", err)
	}
	db.Close()

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()

	got, ok, err := NewReceiptStore(db2).Load(idD)
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
	mustSave := func(agent string, epoch uint64, variant, key string) {
		t.Helper()
		r, _ := mkReceipt(agent, epoch, variant)
		if err := s.Save(r, key, at); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	// Two for agentA in epoch 42 with the same artifact key, one for agentA in
	// epoch 43, one for agentB.
	mustSave(agentA, 42, "q1", "sha256:k1")
	mustSave(agentA, 42, "q2", "sha256:k1")
	mustSave(agentA, 43, "q3", "sha256:k3")
	mustSave(agentB, 42, "q4", "sha256:k4")

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

	noID, _ := mkReceipt(agentA, 1, "z")
	noID.ReceiptID = ""
	if err := s.Save(noID, "", at); err == nil {
		t.Error("a receipt with no id must be rejected")
	}

	if _, err := s.ByArtifact(""); err == nil {
		t.Error("an empty artifact key must be rejected")
	}
}
