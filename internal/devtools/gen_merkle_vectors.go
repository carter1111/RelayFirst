//go:build ignore

// gen_merkle_vectors emits the cross-language Merkle test vectors.
//
//	go run internal/devtools/gen_merkle_vectors.go
//
// It writes two files from one computation, so they cannot disagree:
//
//	testdata/merkle-vectors.json        canonical, for Go and any other consumer
//	contracts/test/MerkleVectors.sol    the same data as Solidity constants
//
// # Why two outputs instead of one shared JSON
//
// A Solidity test could read the JSON through a cheatcode, but that needs a
// cheatcode interface and a JSON parser in the test, which is more moving parts
// than the thing being tested. Emitting Solidity constants from the same Go run
// removes all of it: the two languages are then compared through data that provably
// came from a single source.
//
// # Why this exists at all
//
// Invariant A4: cross-language agreement must be gated in CI. A Merkle verifier that
// agrees only with itself proves nothing — the whole value of the anchor is that an
// independent implementation reaches the same root. Two implementations sharing no
// code and agreeing on committed vectors is the strongest available evidence.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/sha3"

	"github.com/relayfirst/relayfirst/internal/merkle"
)

// vecLeafCounts are the tree sizes exercised. They deliberately include 1 (the
// no-padding case), the powers of two, and one past each power of two (the first
// size that needs padding at each level).
var vecLeafCounts = []int{1, 2, 3, 4, 5, 7, 8, 9, 16, 17}

type proofVector struct {
	Index    int      `json:"index"`
	Leaf     string   `json:"leaf"`
	Siblings []string `json:"siblings"`
}

type treeVector struct {
	Name   string        `json:"name"`
	Leaves []string      `json:"leaves"`
	Width  int           `json:"width"`
	Root   string        `json:"root"`
	Proofs []proofVector `json:"proofs"`
}

type corpus struct {
	Description string       `json:"description"`
	Hash        string       `json:"hash"`
	LeafRule    string       `json:"leafRule"`
	NodeRule    string       `json:"nodeRule"`
	EmptyRoot   string       `json:"emptyRoot"`
	Trees       []treeVector `json:"trees"`

	// BalanceLeaves pin the claim-leaf preimage against contracts/RelayPoints.sol,
	// which is a DIFFERENT leaf from the receipt tree above (MVP.md §6.2b). They are
	// here because invariant A4 requires cross-language agreement to be gated, and a
	// leaf that is wrong by one byte still verifies against itself.
	BalanceLeafRule string          `json:"balanceLeafRule"`
	BalanceLeaves   []balanceVector `json:"balanceLeaves"`
}

// balanceVector is one settled (agent, total, epoch) with its claim leaf.
type balanceVector struct {
	AgentID    string `json:"agentId"`    // canonical agent:eip155:<chain>:<addr>
	AgentHash  string `json:"agentHash"`  // keccak256(chainId || address)
	TotalMicro int64  `json:"totalMicro"` // settled total in micro-points
	Epoch      uint64 `json:"epoch"`
	Leaf       string `json:"leaf"` // keccak256(agentHash || total || epoch)
}

// balanceCases are deterministic stand-ins for real claims: two addresses on one
// chain, and a second chain for the same address, so the chainId is exercised.
var balanceCases = []struct {
	agent      string
	totalMicro int64
	epoch      uint64
}{
	{"agent:eip155:8453:0x70997970c51812dc3a010c7d01b50e0d17dc79c8", 140_000_000_000, 0},
	{"agent:eip155:8453:0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc", 42_000_000_000, 7},
	{"agent:eip155:1:0x70997970c51812dc3a010c7d01b50e0d17dc79c8", 1_000_000, 12},
}

// vectorID derives a deterministic, distinct receipt id.
//
// Deriving from a label rather than embedding literals keeps the corpus readable and
// makes it obvious that the ids are arbitrary stand-ins for real receipt ids.
func vectorID(label string) merkle.Hash {
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write([]byte("relayfirst-merkle-vector:" + label))
	var out merkle.Hash
	copy(out[:], h.Sum(nil))
	return out
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gen_merkle_vectors: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	c := corpus{
		Description: "Cross-language Merkle vectors for RelayFirst epoch anchoring (S7). " +
			"internal/merkle and contracts/RelayAnchor.sol are both checked against this file.",
		Hash:      "keccak256",
		LeafRule:  "keccak256(0x00 || id)",
		NodeRule:  "keccak256(0x01 || left || right)",
		EmptyRoot: merkle.EmptyRoot().Hex(),
		BalanceLeafRule: "keccak256(agentHash || uint256 total || uint64 epoch), " +
			"agentHash = keccak256(uint64 chainId || 20-byte address)",
	}

	for _, bc := range balanceCases {
		agentHash, err := merkle.AgentID(bc.agent)
		if err != nil {
			return fmt.Errorf("agent id %q: %w", bc.agent, err)
		}
		leaf, err := merkle.BalanceLeaf(agentHash, bc.totalMicro, bc.epoch)
		if err != nil {
			return fmt.Errorf("balance leaf %q: %w", bc.agent, err)
		}
		c.BalanceLeaves = append(c.BalanceLeaves, balanceVector{
			AgentID:    bc.agent,
			AgentHash:  agentHash.Hex(),
			TotalMicro: bc.totalMicro,
			Epoch:      bc.epoch,
			Leaf:       leaf.Hex(),
		})
	}

	for _, n := range vecLeafCounts {
		leaves := make([]merkle.Hash, 0, n)
		labels := make([]string, 0, n)
		for i := 0; i < n; i++ {
			label := fmt.Sprintf("n%d-i%d", n, i)
			leaves = append(leaves, vectorID(label))
			labels = append(labels, label)
		}

		root := merkle.Root(leaves)

		tv := treeVector{
			Name:   fmt.Sprintf("n=%d", n),
			Leaves: make([]string, 0, n),
			Root:   root.Hex(),
		}
		for _, id := range leaves {
			tv.Leaves = append(tv.Leaves, id.Hex())
		}

		// Every leaf gets a proof, so the padding path and every index position are
		// covered rather than a single convenient sample.
		for i := 0; i < n; i++ {
			p, err := merkle.Prove(leaves, i)
			if err != nil {
				return fmt.Errorf("prove %s index %d: %w", tv.Name, i, err)
			}
			pv := proofVector{Index: i, Leaf: p.Leaf.Hex()}
			// An empty slice rather than nil, so the field serializes as [] instead of null.
			// A single-leaf tree legitimately has zero siblings, and null would force every
			// consumer — the Solidity generator, the JS cross-check, anything downstream — to
			// special-case it. One canonical representation is worth the two extra characters.
			pv.Siblings = make([]string, 0, len(p.Siblings))
			for _, s := range p.Siblings {
				pv.Siblings = append(pv.Siblings, s.Hex())
			}
			tv.Proofs = append(tv.Proofs, pv)
		}
		tv.Width = nextPow2(n)
		c.Trees = append(c.Trees, tv)
	}

	if err := writeJSON(c); err != nil {
		return err
	}
	return writeSolidity(c)
}

func writeJSON(c corpus) error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal corpus: %w", err)
	}
	raw = append(raw, '\n')

	path := filepath.Join("testdata", "merkle-vectors.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d trees)\n", path, len(c.Trees))
	return nil
}

// writeSolidity emits the same corpus as Solidity constants.
//
// The values are embedded rather than read at runtime so the Solidity test needs no
// cheatcodes, no JSON parser, and no external test library — which also means the
// test can run without network access to fetch forge-std.
//
// The trees are exposed through one indexed getter rather than one function per
// tree, so the Solidity side is a plain loop over the corpus instead of ten
// near-identical test functions.
func writeSolidity(c corpus) error {
	var b strings.Builder

	b.WriteString("// SPDX-License-Identifier: MIT\n")
	b.WriteString("pragma solidity ^0.8.20;\n\n")
	b.WriteString("/// @title MerkleVectors — GENERATED FILE, DO NOT EDIT BY HAND.\n")
	b.WriteString("///\n")
	b.WriteString("/// @notice The same vectors as testdata/merkle-vectors.json, emitted by\n")
	b.WriteString("///         internal/devtools/gen_merkle_vectors.go so both languages are compared\n")
	b.WriteString("///         against data that provably came from one computation.\n")
	b.WriteString("///\n")
	b.WriteString("/// @dev Regenerate with:\n")
	b.WriteString("///        go run internal/devtools/gen_merkle_vectors.go\n")
	b.WriteString("///      CI fails if this file is out of date.\n")
	b.WriteString("library MerkleVectors {\n")

	// One tree per generated helper, so the body of each is small and readable.
	for i, tv := range c.Trees {
		fmt.Fprintf(&b, "    // ---- %s: %d leaf/leaves, width %d, %d proof(s) ----\n",
			tv.Name, len(tv.Leaves), tv.Width, len(tv.Proofs))
		fmt.Fprintf(&b, "    function _tree%d() private pure returns (\n", i)
		b.WriteString("        bytes32[] memory leaves,\n")
		b.WriteString("        uint256 width,\n")
		b.WriteString("        bytes32 root,\n")
		b.WriteString("        uint256[] memory indices,\n")
		b.WriteString("        bytes32[][] memory siblings\n")
		b.WriteString("    ) {\n")

		fmt.Fprintf(&b, "        leaves = new bytes32[](%d);\n", len(tv.Leaves))
		for j, leaf := range tv.Leaves {
			fmt.Fprintf(&b, "        leaves[%d] = %s;\n", j, leaf)
		}
		fmt.Fprintf(&b, "        width = %d;\n", tv.Width)
		fmt.Fprintf(&b, "        root = %s;\n", tv.Root)

		fmt.Fprintf(&b, "        indices = new uint256[](%d);\n", len(tv.Proofs))
		fmt.Fprintf(&b, "        siblings = new bytes32[][](%d);\n", len(tv.Proofs))
		for j, pv := range tv.Proofs {
			fmt.Fprintf(&b, "        indices[%d] = %d;\n", j, pv.Index)
			fmt.Fprintf(&b, "        siblings[%d] = new bytes32[](%d);\n", j, len(pv.Siblings))
			for k, s := range pv.Siblings {
				fmt.Fprintf(&b, "        siblings[%d][%d] = %s;\n", j, k, s)
			}
		}
		b.WriteString("    }\n\n")
	}

	// The indexed getter the test loops over.
	fmt.Fprintf(&b, "    uint256 internal constant TREE_COUNT = %d;\n", len(c.Trees))
	fmt.Fprintf(&b, "    bytes32 internal constant EMPTY_ROOT = %s;\n\n", c.EmptyRoot)

	// Balance-leaf vectors (MVP.md §6.2b): a different leaf from the trees above,
	// pinned so the Go encoder and RelayPoints.sol agree.
	fmt.Fprintf(&b, "    uint256 internal constant BALANCE_COUNT = %d;\n\n", len(c.BalanceLeaves))
	b.WriteString("    /// @notice One settled claim leaf vector.\n")
	b.WriteString("    struct BalanceVector {\n")
	b.WriteString("        bytes32 agentHash;\n")
	b.WriteString("        uint256 totalMicro;\n")
	b.WriteString("        uint64 epoch;\n")
	b.WriteString("        bytes32 leaf;\n")
	b.WriteString("    }\n\n")
	b.WriteString("    /// @notice Returns the balance-leaf vectors.\n")
	b.WriteString("    function balanceVectors() internal pure returns (BalanceVector[] memory v) {\n")
	fmt.Fprintf(&b, "        v = new BalanceVector[](%d);\n", len(c.BalanceLeaves))
	for i, bl := range c.BalanceLeaves {
		fmt.Fprintf(&b, "        v[%d] = BalanceVector(%s, %d, %d, %s);\n",
			i, bl.AgentHash, bl.TotalMicro, bl.Epoch, bl.Leaf)
	}
	b.WriteString("    }\n")

	b.WriteString("    /// @notice Returns one tree's vectors by index.\n")
	b.WriteString("    function tree(uint256 i) internal pure returns (\n")
	b.WriteString("        bytes32[] memory leaves,\n")
	b.WriteString("        uint256 width,\n")
	b.WriteString("        bytes32 root,\n")
	b.WriteString("        uint256[] memory indices,\n")
	b.WriteString("        bytes32[][] memory siblings\n")
	b.WriteString("    ) {\n")
	for i := range c.Trees {
		fmt.Fprintf(&b, "        if (i == %d) return _tree%d();\n", i, i)
	}
	b.WriteString("        revert(\"MerkleVectors: tree index out of range\");\n")
	b.WriteString("    }\n")

	b.WriteString("}\n")

	path := filepath.Join("contracts", "test", "MerkleVectors.sol")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", path)
	return nil
}

func nextPow2(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
