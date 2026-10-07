package merkle_test

import (
	"testing"

	"github.com/relayfirst/relayfirst/internal/merkle"
)

func mustAgent(t *testing.T, id string) merkle.Hash {
	t.Helper()
	a, err := merkle.AgentID(id)
	if err != nil {
		t.Fatalf("AgentID(%q): %v", id, err)
	}
	return a
}

// TestBalance_LeafIsDeterministicAndEpochBound: the leaf must be a pure function of
// its inputs, and the epoch inside it must actually change the result -- otherwise a
// proof for one epoch would replay into another.
func TestBalance_LeafIsDeterministicAndEpochBound(t *testing.T) {
	a := mustAgent(t, "agent:eip155:8453:0x70997970c51812dc3a010c7d01b50e0d17dc79c8")

	l1, err := merkle.BalanceLeaf(a, 140_000_000_000, 10)
	if err != nil {
		t.Fatalf("BalanceLeaf: %v", err)
	}
	l2, _ := merkle.BalanceLeaf(a, 140_000_000_000, 10)
	if l1 != l2 {
		t.Error("the same inputs must give the same leaf")
	}

	l3, _ := merkle.BalanceLeaf(a, 140_000_000_000, 20)
	if l1 == l3 {
		t.Error("the epoch is inside the leaf, so a different epoch must change it")
	}

	l4, _ := merkle.BalanceLeaf(a, 140_000_000_001, 10)
	if l1 == l4 {
		t.Error("a different total must change the leaf")
	}
}

func TestBalance_RejectsNegativeTotal(t *testing.T) {
	a := mustAgent(t, "agent:eip155:8453:0x70997970c51812dc3a010c7d01b50e0d17dc79c8")
	if _, err := merkle.BalanceLeaf(a, -1, 0); err == nil {
		t.Error("a negative total describes no claim and must be an error")
	}
}

func TestAgentID_DistinguishesChainAndAddress(t *testing.T) {
	const addr = "0x70997970c51812dc3a010c7d01b50e0d17dc79c8"
	base := mustAgent(t, "agent:eip155:8453:"+addr)

	// Same address, different chain: must differ, or a claim would be cross-chain.
	if other := mustAgent(t, "agent:eip155:1:"+addr); other == base {
		t.Error("the chain id is part of the agent id")
	}
	// Case-insensitive address, same identity.
	if upper := mustAgent(t, "agent:eip155:8453:0x70997970C51812DC3A010C7D01B50E0D17DC79C8"); upper != base {
		t.Error("an address's case must not change the identity")
	}
}

func TestBalance_RootFromTotalsIsOrderIndependentAndProves(t *testing.T) {
	epoch := uint64(7)
	a := mustAgent(t, "agent:eip155:8453:0x70997970c51812dc3a010c7d01b50e0d17dc79c8")
	b := mustAgent(t, "agent:eip155:8453:0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc")

	m := map[merkle.Hash]int64{a: 140_000_000_000, b: 42_000_000_000}

	r1, err := merkle.BalanceRootFromTotals(epoch, m)
	if err != nil {
		t.Fatalf("BalanceRootFromTotals: %v", err)
	}

	// A map built in a different order must give the same root: the canonical leaf
	// order is the point. Rebuild with the keys inserted the other way.
	m2 := map[merkle.Hash]int64{}
	m2[b] = 42_000_000_000
	m2[a] = 140_000_000_000
	r2, err := merkle.BalanceRootFromTotals(epoch, m2)
	if err != nil {
		t.Fatalf("BalanceRootFromTotals (2): %v", err)
	}
	if r1 != r2 {
		t.Errorf("root depends on map order: %s vs %s", r1, r2)
	}

	// And an inclusion proof for one agent must verify against that root.
	p, err := merkle.BalanceProof(epoch, m, a)
	if err != nil {
		t.Fatalf("BalanceProof: %v", err)
	}
	if p.Root != r1 {
		t.Errorf("proof root %s != balance root %s", p.Root, r1)
	}
	if !merkle.Verify(p) {
		t.Error("the balance proof must verify against the balance root")
	}
}
