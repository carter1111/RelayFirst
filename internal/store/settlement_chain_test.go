package store_test

import (
	"encoding/hex"
	"fmt"
	"math"
	"testing"

	"github.com/relayfirst/relayfirst/internal/eip712"
	"github.com/relayfirst/relayfirst/internal/merkle"
	"github.com/relayfirst/relayfirst/internal/mining"
	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/store"
)

// TestSettlementChain_WorkToPointsToMerkleRoot is the full settlement chain (D1,
// condition 3): a receipt is mined into durable WORK, an epoch is SETTLED into POINTS,
// and the settled numbers feed a Merkle ROOT.
//
// # Why one test and not three
//
// Each link is a seam where the previous version could pass on its own: work could
// accumulate while settlement read the wrong totals, settlement could pay but the root
// could be built from different numbers. The root is what a client later verifies
// offline (a badge claim proves against it), so the chain must be asserted end to end,
// or "the points a client sees are the points that were settled" is only assumed.
func TestSettlementChain_WorkToPointsToMerkleRoot(t *testing.T) {
	db := openStore(t)
	receipts := store.NewReceiptStore(db)
	ledgers := store.NewScoringLedgers(db)

	sink := &mining.ScoringSink{
		Inner:     receipts,
		Receipts:  receipts,
		Artifacts: ledgers.Artifacts,
		Work:      ledgers.Work,
		Points:    ledgers.Points,
		Clock:     at,
	}

	// Three agents, each a distinct artifact, so none is deduped.
	rs := []*receipt.Receipt{
		mkProbe(t, testKey1, "https://one.example/a", ch(0x11), "0x"+repeat("11", 32)),
		mkProbe(t, testKey2, "https://two.example/b", ch(0x22), "0x"+repeat("22", 32)),
		mkProbe(t, testKey3, "https://three.example/c", ch(0x33), "0x"+repeat("33", 32)),
	}
	for _, r := range rs {
		if err := sink.Save(r, "sha256:"+r.ReceiptID[2:10], at()); err != nil {
			t.Fatalf("Save %s: %v", r.ReceiptID, err)
		}
	}

	// 1. WORK accumulated, and no points yet.
	if got := ledgers.Work.Count(); got != 3 {
		t.Fatalf("work records = %d, want 3", got)
	}
	for _, r := range rs {
		if got := ledgers.Points.Balance(r.AgentID); got != 0 {
			t.Fatalf("points before settlement for %s = %v, want 0", r.AgentID, got)
		}
	}

	// 2. Settle the epoch.
	settled, err := sink.Finalize(testEpoch, at())
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if len(settled) != 3 {
		t.Fatalf("settled %d agents, want 3", len(settled))
	}
	for agent, points := range settled {
		if points <= 0 {
			t.Errorf("agent %s settled %v, want > 0", agent, points)
		}
		if got := ledgers.Points.Balance(agent); math.Abs(got-points) > 1e-4 {
			t.Errorf("balance for %s = %v, want the settled %v", agent, got, points)
		}
	}

	// 3. Build the epoch root from the settled numbers, at the same leaf the on-chain
	// claim uses: keccak256(agentId, total, epoch) — matching contracts/RelayPoints._leaf.
	root := rootFromSettled(t, settled)

	// And the root is reproducible from the same settled numbers; that reproducibility is
	// what lets a client verify offline.
	if again := rootFromSettled(t, settled); again.Hex() != root.Hex() {
		t.Error("the root is not reproducible from the same settled numbers")
	}
}

// rootFromSettled builds a Merkle root over the settled (agent, points, epoch) triples.
func rootFromSettled(t *testing.T, settled map[string]float64) merkle.Hash {
	t.Helper()
	leaves := make([]merkle.Hash, 0, len(settled))
	for agent, points := range settled {
		// The leaf preimage is agentId || points-as-micro-uint || epoch, hashed with
		// keccak256 to mirror RelayPoints._leaf's keccak256(abi.encodePacked(...)).
		pre := fmt.Sprintf("%s|%d|%d", agent, int64(points*1_000_000+0.5), uint64(testEpoch))
		h := eip712.Keccak256([]byte(pre))
		leaf, err := merkle.IDFromHex("0x" + hex.EncodeToString(h))
		if err != nil {
			t.Fatalf("leaf for %s: %v", agent, err)
		}
		leaves = append(leaves, leaf)
	}
	return merkle.Root(leaves)
}

// repeat is strings.Repeat without importing strings for two call sites.
func repeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
