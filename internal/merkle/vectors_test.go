package merkle_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/relayfirst/relayfirst/internal/merkle"
)

// vectorCorpus mirrors testdata/merkle-vectors.json.
type vectorCorpus struct {
	Hash            string          `json:"hash"`
	LeafRule        string          `json:"leafRule"`
	NodeRule        string          `json:"nodeRule"`
	EmptyRoot       string          `json:"emptyRoot"`
	Trees           []vectorTree    `json:"trees"`
	BalanceLeafRule string          `json:"balanceLeafRule"`
	BalanceLeaves   []balanceVector `json:"balanceLeaves"`
}

// balanceVector mirrors one claim-leaf vector (MVP.md §6.2b).
type balanceVector struct {
	AgentID    string `json:"agentId"`
	AgentHash  string `json:"agentHash"`
	TotalMicro int64  `json:"totalMicro"`
	Epoch      uint64 `json:"epoch"`
	Leaf       string `json:"leaf"`
}

type vectorTree struct {
	Name   string        `json:"name"`
	Leaves []string      `json:"leaves"`
	Width  int           `json:"width"`
	Root   string        `json:"root"`
	Proofs []vectorProof `json:"proofs"`
}

type vectorProof struct {
	Index    int      `json:"index"`
	Leaf     string   `json:"leaf"`
	Siblings []string `json:"siblings"`
}

func loadVectors(t *testing.T) vectorCorpus {
	t.Helper()

	path := filepath.Join("..", "..", "testdata", "merkle-vectors.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v\n\nIf the corpus is missing, regenerate it:\n  go run internal/devtools/gen_merkle_vectors.go", path, err)
	}

	var c vectorCorpus
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(c.Trees) == 0 {
		t.Fatal("the corpus holds no trees")
	}
	return c
}

func mustID(t *testing.T, hex string) merkle.Hash {
	t.Helper()
	id, err := merkle.IDFromHex(hex)
	if err != nil {
		t.Fatalf("IDFromHex(%q): %v", hex, err)
	}
	return id
}

// TestVectors_RootMatchesCommittedCorpus is the Go half of invariant A4 for S7.
//
// The same corpus is compiled into the Solidity test, so a change to either
// implementation that the other does not share fails on one side or the other. Two
// implementations agreeing only with themselves proves nothing.
func TestVectors_RootMatchesCommittedCorpus(t *testing.T) {
	c := loadVectors(t)

	for _, tree := range c.Trees {
		t.Run(tree.Name, func(t *testing.T) {
			leaves := make([]merkle.Hash, 0, len(tree.Leaves))
			for _, h := range tree.Leaves {
				leaves = append(leaves, mustID(t, h))
			}

			got := merkle.Root(leaves)
			want := mustID(t, tree.Root)
			if got != want {
				t.Errorf("root = %s, want %s from the corpus", got, want)
			}
		})
	}
}

// TestVectors_ProofsVerifyAgainstCommittedRoots checks that the committed proofs are
// genuinely valid for the committed roots, so the Solidity side is not being handed
// vectors that would fail there for an unrelated reason.
func TestVectors_ProofsVerifyAgainstCommittedRoots(t *testing.T) {
	c := loadVectors(t)

	for _, tree := range c.Trees {
		t.Run(tree.Name, func(t *testing.T) {
			leaves := make([]merkle.Hash, 0, len(tree.Leaves))
			for _, h := range tree.Leaves {
				leaves = append(leaves, mustID(t, h))
			}

			for _, vp := range tree.Proofs {
				siblings := make([]merkle.Hash, 0, len(vp.Siblings))
				for _, s := range vp.Siblings {
					siblings = append(siblings, mustID(t, s))
				}

				p := merkle.Proof{
					Leaf:     mustID(t, vp.Leaf),
					Index:    vp.Index,
					Siblings: siblings,
					Root:     mustID(t, tree.Root),
					Width:    tree.Width,
				}

				if !merkle.Verify(p) {
					t.Errorf("committed proof for index %d does not verify", vp.Index)
				}

				// The proof must also be independently reproducible, not merely
				// stored correctly.
				regenerated, err := merkle.Prove(leaves, vp.Index)
				if err != nil {
					t.Fatalf("Prove(%d): %v", vp.Index, err)
				}
				if regenerated.Root != p.Root {
					t.Errorf("regenerated root %s != committed %s", regenerated.Root, p.Root)
				}
				if len(regenerated.Siblings) != len(p.Siblings) {
					t.Fatalf("regenerated %d siblings, committed %d", len(regenerated.Siblings), len(p.Siblings))
				}
				for i := range regenerated.Siblings {
					if regenerated.Siblings[i] != p.Siblings[i] {
						t.Errorf("sibling %d differs: regenerated %s, committed %s",
							i, regenerated.Siblings[i], p.Siblings[i])
					}
				}
			}
		})
	}
}

// TestVectors_DeclaredRulesAreAccurate: the corpus states the construction in prose
// for other implementers. If those strings drift from what the code does, a reader
// following them would build a different tree.
func TestVectors_DeclaredRulesAreAccurate(t *testing.T) {
	c := loadVectors(t)

	if c.Hash != "keccak256" {
		t.Errorf("corpus declares hash %q; the implementation uses keccak256", c.Hash)
	}
	if c.LeafRule != "keccak256(0x00 || id)" {
		t.Errorf("corpus leaf rule = %q", c.LeafRule)
	}
	if c.NodeRule != "keccak256(0x01 || left || right)" {
		t.Errorf("corpus node rule = %q", c.NodeRule)
	}
	if c.BalanceLeafRule == "" {
		t.Error("corpus must declare the balance leaf rule (MVP.md §6.2b)")
	}

	// And the declared empty root must be the real one.
	if got, want := merkle.EmptyRoot().Hex(), c.EmptyRoot; got != want {
		t.Errorf("emptyRoot = %s, corpus says %s", got, want)
	}
}

// TestVectors_BalanceLeavesMatchCommittedCorpus is the Go half of invariant A4 for
// the points claim leaf (MVP.md §6.2b). The same vectors are compiled into the
// Solidity test and recomputed by the viem script; a leaf that is wrong by a byte
// would still verify against itself, so only cross-language agreement catches it.
func TestVectors_BalanceLeavesMatchCommittedCorpus(t *testing.T) {
	c := loadVectors(t)
	if len(c.BalanceLeaves) == 0 {
		t.Fatal("the corpus holds no balance vectors")
	}

	for _, b := range c.BalanceLeaves {
		agentHash, err := merkle.AgentID(b.AgentID)
		if err != nil {
			t.Fatalf("AgentID(%q): %v", b.AgentID, err)
		}
		if got := agentHash.Hex(); got != b.AgentHash {
			t.Errorf("AgentID(%q) = %s, corpus says %s", b.AgentID, got, b.AgentHash)
		}

		leaf, err := merkle.BalanceLeaf(agentHash, b.TotalMicro, b.Epoch)
		if err != nil {
			t.Fatalf("BalanceLeaf(%q): %v", b.AgentID, err)
		}
		if got := leaf.Hex(); got != b.Leaf {
			t.Errorf("BalanceLeaf(%q, %d, %d) = %s, corpus says %s",
				b.AgentID, b.TotalMicro, b.Epoch, got, b.Leaf)
		}
	}
}

// TestVectors_CoverPaddingBoundaries: the corpus must exercise the sizes where the
// power-of-two padding actually applies, since that is where two implementations are
// most likely to differ.
func TestVectors_CoverPaddingBoundaries(t *testing.T) {
	c := loadVectors(t)

	sizes := map[int]bool{}
	for _, tree := range c.Trees {
		sizes[len(tree.Leaves)] = true
	}

	// 1 (no padding at all) and at least one size one past a power of two (padding
	// kicks in at the first sibling level).
	if !sizes[1] {
		t.Error("the corpus must include a single-leaf tree")
	}

	foundPadded := false
	for n := 2; n <= 64; n <<= 1 {
		if sizes[n+1] {
			foundPadded = true
		}
	}
	if !foundPadded {
		t.Error("the corpus must include a size one past a power of two")
	}
}
