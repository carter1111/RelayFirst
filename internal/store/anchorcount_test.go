package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// These tests cover the anchor counting query that replaced a per-iteration full scan in the
// mining CLI's live progress line.
//
// The count is user-facing: it answers "how much independently checkable evidence do I have".
// An inflated number is therefore worse than no number, which is why the synthetic anchors are
// excluded and asserted to be.

// anchorID returns a well-formed 32-byte receipt id for index n.
func anchorID(n int) string { return fmt.Sprintf("0x%064x", n) }

// anchorReceiptFor builds a signed receipt with explicit anchors.
func anchorReceiptFor(t *testing.T, key string, id string, anchors []receipt.Anchor) *receipt.Receipt {
	t.Helper()

	agent, err := receipt.DeriveAgentID(key, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	r := &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: id,
		AgentID:   agent,
		Epoch:     5,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": "https://example.com"},
			SpecHash:      expCh(1),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015860},
		Result:       receipt.Result{Value: "200", Hash: expCh(2)},
		Anchors:      anchors,
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	if err := r.Sign(key); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return r
}

func TestCountAnchorsByAgent_CountsDistinctRealAnchors(t *testing.T) {
	rs := openReceiptStore(t)

	shared := expCh(0x10)

	// Two receipts sharing one content hash, plus a third with its own.
	for i, a := range [][]receipt.Anchor{
		{{URL: "https://a.example", ContentHash: shared, Status: 200}},
		{{URL: "https://b.example", ContentHash: shared, Status: 200}},
		{{URL: "https://c.example", ContentHash: expCh(0x11), Status: 200}},
	} {
		r := anchorReceiptFor(t, storeTestKey1, anchorID(i), a)
		if err := rs.Save(r, "", time.Unix(1791015800, 0)); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	agent, err := receipt.DeriveAgentID(storeTestKey1, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	got, err := rs.CountAnchorsByAgent(agent)
	if err != nil {
		t.Fatalf("CountAnchorsByAgent: %v", err)
	}
	if got != 2 {
		t.Errorf("count = %d, want 2 (one shared hash counted once, plus one distinct)", got)
	}
}

// TestCountAnchorsByAgent_ExcludesSyntheticAnchors: compute tasks capture no external
// evidence, so counting them would inflate the number the miner reads.
func TestCountAnchorsByAgent_ExcludesSyntheticAnchors(t *testing.T) {
	rs := openReceiptStore(t)

	for i, a := range [][]receipt.Anchor{
		{{URL: "inline", ContentHash: expCh(0x20)}},
		{{URL: "inline", ContentHash: expCh(0x21)}},
		{{URL: "https://real.example", ContentHash: expCh(0x22), Status: 200}},
	} {
		r := anchorReceiptFor(t, storeTestKey1, anchorID(i), a)
		if err := rs.Save(r, "", time.Unix(1791015800, 0)); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	agent, err := receipt.DeriveAgentID(storeTestKey1, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	got, err := rs.CountAnchorsByAgent(agent)
	if err != nil {
		t.Fatalf("CountAnchorsByAgent: %v", err)
	}
	if got != 1 {
		t.Errorf("count = %d, want 1 — synthetic inline anchors carry no re-fetchable evidence", got)
	}
}

// TestCountAnchorsByAgent_AgreesWithTheLoop is the assertion that makes the query safe to swap
// in: it must return exactly what the in-memory count returned, on the same data.
func TestCountAnchorsByAgent_AgreesWithTheLoop(t *testing.T) {
	rs := openReceiptStore(t)

	for i, a := range [][]receipt.Anchor{
		{{URL: "https://a.example", ContentHash: expCh(0x30), Status: 200}},
		{{URL: "inline", ContentHash: expCh(0x31)}},
		{{URL: "https://b.example", ContentHash: expCh(0x32), Status: 200}, {URL: "https://c.example", ContentHash: expCh(0x33), Status: 200}},
		{{URL: "https://d.example", ContentHash: expCh(0x30), Status: 200}}, // repeats 0x30
	} {
		r := anchorReceiptFor(t, storeTestKey1, anchorID(i), a)
		if err := rs.Save(r, "", time.Unix(1791015800, 0)); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	agent, err := receipt.DeriveAgentID(storeTestKey1, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	// The loop-based count, which the query replaced.
	mine, err := rs.ByAgentAll(agent)
	if err != nil {
		t.Fatalf("ByAgentAll: %v", err)
	}
	want := rs.AnchorsFor(mine)

	got, err := rs.CountAnchorsByAgent(agent)
	if err != nil {
		t.Fatalf("CountAnchorsByAgent: %v", err)
	}

	if got != want {
		t.Errorf("query returned %d but the loop returned %d; the replacement must agree exactly", got, want)
	}
}

func TestCountAnchorsByAgent_UnknownAgentIsZero(t *testing.T) {
	rs := openReceiptStore(t)

	got, err := rs.CountAnchorsByAgent("agent:nobody")
	if err != nil {
		t.Fatalf("CountAnchorsByAgent: %v", err)
	}
	if got != 0 {
		t.Errorf("count = %d, want 0", got)
	}
}

func TestCountAnchorsByAgent_RejectsEmptyAgent(t *testing.T) {
	rs := openReceiptStore(t)
	if _, err := rs.CountAnchorsByAgent(""); err == nil {
		t.Error("an empty agent id must be rejected rather than counting every agent's anchors")
	}
}

// TestCountAnchorsByAgent_IsScopedToOneAgent: a query that leaked across agents would show a
// miner evidence they did not gather.
func TestCountAnchorsByAgent_IsScopedToOneAgent(t *testing.T) {
	rs := openReceiptStore(t)

	r1 := anchorReceiptFor(t, storeTestKey1, anchorID(1), []receipt.Anchor{{URL: "https://a.example", ContentHash: expCh(0x40), Status: 200}})
	r2 := anchorReceiptFor(t, storeTestKey2, anchorID(2), []receipt.Anchor{{URL: "https://b.example", ContentHash: expCh(0x41), Status: 200}})

	for _, r := range []*receipt.Receipt{r1, r2} {
		if err := rs.Save(r, "", time.Unix(1791015800, 0)); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	agent1, err := receipt.DeriveAgentID(storeTestKey1, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	got, err := rs.CountAnchorsByAgent(agent1)
	if err != nil {
		t.Fatalf("CountAnchorsByAgent: %v", err)
	}
	if got != 1 {
		t.Errorf("count = %d, want 1 — only this agent's anchors", got)
	}
}
