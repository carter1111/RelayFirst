package merkle

import (
	"fmt"
	"strings"
	"testing"
)

// ids builds n distinct, well-formed receipt ids.
func ids(n int) []Hash {
	out := make([]Hash, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, keccak256([]byte(fmt.Sprintf("receipt-%d", i))))
	}
	return out
}

// TestRoot_IsDeterministic: an independent party recomputing the root from the same
// receipts must arrive at the same value, or the anchor proves nothing.
func TestRoot_IsDeterministic(t *testing.T) {
	leaves := ids(7)

	first := Root(leaves)
	for i := 0; i < 100; i++ {
		if got := Root(leaves); got != first {
			t.Fatalf("Root is not deterministic at iteration %d", i)
		}
	}
}

// TestRoot_IsOrderSensitive: reordering the leaves must change the root. If it did
// not, the epoch's ordering would carry no information and a proof would not pin
// which position a receipt occupied.
func TestRoot_IsOrderSensitive(t *testing.T) {
	leaves := ids(4)

	shuffled := []Hash{leaves[1], leaves[0], leaves[3], leaves[2]}
	if Root(leaves) == Root(shuffled) {
		t.Error("Root must depend on leaf order")
	}
}

// TestRoot_DifferentEpochsDiffer: receipts from different epochs must produce
// different roots, or an anchor would not distinguish one epoch from another.
func TestRoot_DifferentEpochsDiffer(t *testing.T) {
	epochA := ids(4)
	epochB := ids(4)
	// Make the two sets disjoint, as two real epochs would be.
	for i := range epochB {
		epochB[i] = keccak256([]byte(fmt.Sprintf("other-%d", i)))
	}

	if Root(epochA) == Root(epochB) {
		t.Error("two disjoint epochs produced the same root")
	}
}

// TestRoot_ChangesIfAnyLeafChanges: a single altered id must change the root. This
// is what makes tampering detectable.
func TestRoot_ChangesIfAnyLeafChanges(t *testing.T) {
	leaves := ids(5)
	base := Root(leaves)

	for i := range leaves {
		mutated := append([]Hash(nil), leaves...)
		mutated[i] = keccak256([]byte(fmt.Sprintf("tampered-%d", i)))

		if Root(mutated) == base {
			t.Errorf("changing leaf %d did not change the root", i)
		}
	}
}

func TestRoot_EmptyAndSingle(t *testing.T) {
	if got := Root(nil); got != EmptyRoot() {
		t.Errorf("Root(nil) = %s, want EmptyRoot %s", got, EmptyRoot())
	}
	if got := Root([]Hash{}); got != EmptyRoot() {
		t.Errorf("Root(empty) = %s, want EmptyRoot", got)
	}

	one := ids(1)
	if got := Root(one); got != LeafHash(one[0]) {
		t.Errorf("a single-leaf root must be the leaf hash, got %s", got)
	}
}

// TestLeafAndNodeHashesAreStructurallyDistinct is the reason for the 0x00/0x01
// prefixes.
//
// Without domain separation, an internal node and a leaf are both "some 32-byte
// hash", so a verifier cannot tell them apart. The consequence is a real attack: an
// attacker picks two ids l and r, computes h = keccak(l ‖ r), and presents h as a
// *leaf*. A tree containing that internal node would then yield a valid proof for a
// receipt that was never in it.
//
// With the prefixes, keccak(0x00 ‖ h) can never equal a node hash keccak(0x01 ‖ ...),
// so a leaf cannot be reinterpreted as an internal node.
func TestLeafAndNodeHashesAreStructurallyDistinct(t *testing.T) {
	l := keccak256([]byte("l"))
	r := keccak256([]byte("r"))

	node := nodeHash(l, r)

	// The classic second-preimage attempt: treat the concatenation as a leaf.
	if LeafHash(node) == node {
		t.Error("a node hash must not be usable as a leaf hash")
	}

	// And the prefixes must actually differ, not merely produce different outputs
	// by accident of these particular inputs.
	if leafPrefix == nodePrefix {
		t.Fatal("leaf and node prefixes must differ")
	}
}

// TestVerify_AcceptsEveryLeafInEverySizedTree covers every leaf of trees from 1 to
// 33 leaves, so the padding path is exercised at each width boundary.
func TestVerify_AcceptsEveryLeafInEverySizedTree(t *testing.T) {
	for n := 1; n <= 33; n++ {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			leaves := ids(n)
			root := Root(leaves)

			for i := 0; i < n; i++ {
				proof, err := Prove(leaves, i)
				if err != nil {
					t.Fatalf("Prove(%d): %v", i, err)
				}
				if proof.Root != root {
					t.Fatalf("proof root %s != tree root %s", proof.Root, root)
				}
				if !Verify(proof) {
					t.Fatalf("proof for leaf %d did not verify", i)
				}
			}
		})
	}
}

// TestVerify_RejectsTamperedLeaf: the whole point of the anchor is that altering a
// receipt breaks its proof.
func TestVerify_RejectsTamperedLeaf(t *testing.T) {
	leaves := ids(8)
	proof, err := Prove(leaves, 3)
	if err != nil {
		t.Fatalf("Prove: %v", err)
	}

	if !Verify(proof) {
		t.Fatal("the untampered proof must verify first")
	}

	proof.Leaf = keccak256([]byte("forged"))
	if Verify(proof) {
		t.Error("a tampered leaf must not verify")
	}
}

func TestVerify_RejectsTamperedSibling(t *testing.T) {
	leaves := ids(8)
	proof, err := Prove(leaves, 3)
	if err != nil {
		t.Fatalf("Prove: %v", err)
	}

	for i := range proof.Siblings {
		mutated := proof
		mutated.Siblings = append([]Hash(nil), proof.Siblings...)
		mutated.Siblings[i] = keccak256([]byte(fmt.Sprintf("sibling-%d", i)))

		if Verify(mutated) {
			t.Errorf("a tampered sibling at index %d must not verify", i)
		}
	}
}

func TestVerify_RejectsTamperedRoot(t *testing.T) {
	leaves := ids(4)
	proof, _ := Prove(leaves, 1)

	proof.Root = keccak256([]byte("wrong root"))
	if Verify(proof) {
		t.Error("a proof against the wrong root must not verify")
	}
}

// TestVerify_RejectsWrongIndex: the index decides which side each sibling sits on,
// so a proof is only meaningful for the position it was generated for.
func TestVerify_RejectsWrongIndex(t *testing.T) {
	leaves := ids(8)
	proof, err := Prove(leaves, 3)
	if err != nil {
		t.Fatalf("Prove: %v", err)
	}

	proof.Index = 4
	if Verify(proof) {
		t.Error("a proof presented for the wrong index must not verify")
	}
}

// TestVerify_RejectsMalformedProofs: a proof whose shape is inconsistent describes
// no real tree, so it must be refused rather than interpreted generously.
func TestVerify_RejectsMalformedProofs(t *testing.T) {
	leaves := ids(4)
	good, err := Prove(leaves, 0)
	if err != nil {
		t.Fatalf("Prove: %v", err)
	}

	cases := map[string]func(Proof) Proof{
		"zero width":         func(p Proof) Proof { p.Width = 0; return p },
		"non-power-of-two":   func(p Proof) Proof { p.Width = 6; return p },
		"negative index":     func(p Proof) Proof { p.Index = -1; return p },
		"index beyond width": func(p Proof) Proof { p.Index = p.Width; return p },
		"short sibling list": func(p Proof) Proof { p.Siblings = p.Siblings[:len(p.Siblings)-1]; return p },
		"inflated sibling list": func(p Proof) Proof {
			p.Siblings = append(p.Siblings, Hash{})
			return p
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if Verify(mutate(good)) {
				t.Errorf("a %s proof must not verify", name)
			}
		})
	}
}

// TestProve_RejectsOutOfRange: generating a proof for a non-existent position must
// fail loudly rather than produce one that silently cannot verify.
func TestProve_RejectsOutOfRange(t *testing.T) {
	leaves := ids(3)

	if _, err := Prove(leaves, -1); err == nil {
		t.Error("a negative index must be rejected")
	}
	if _, err := Prove(leaves, len(leaves)); err == nil {
		t.Error("an index past the end must be rejected")
	}
	if _, err := Prove(nil, 0); err == nil {
		t.Error("proving against an empty epoch must be rejected")
	}
}

// TestProve_PaddingLeavesAreNotProvableAsRealWork: padding exists to fix the tree
// shape, so an index in the padded region must not be proposable as a receipt.
func TestProve_PaddingLeavesAreNotProvableAsRealWork(t *testing.T) {
	leaves := ids(3) // pads to width 4

	if _, err := Prove(leaves, 3); err == nil {
		t.Error("index 3 is padding and must not be provable as a receipt")
	}
}

// TestIDFromHex_AcceptsBothSpellings: a receipt id appears with and without the 0x
// prefix depending on the tool, and rejecting one spelling would be a papercut with
// no security benefit.
func TestIDFromHex_AcceptsBothSpellings(t *testing.T) {
	hex42 := "0x" + fmt.Sprintf("%064x", 42)
	bare := fmt.Sprintf("%064x", 42)

	a, err := IDFromHex(hex42)
	if err != nil {
		t.Fatalf("IDFromHex(0x...): %v", err)
	}
	b, err := IDFromHex(bare)
	if err != nil {
		t.Fatalf("IDFromHex(bare): %v", err)
	}
	if a != b {
		t.Error("both spellings must parse to the same hash")
	}
	if a.Hex() != hex42 {
		t.Errorf("Hex() = %s, want %s", a.Hex(), hex42)
	}
}

func TestIDFromHex_RejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"0x",
		"abcd",                         // too short
		"0x" + fmt.Sprintf("%066x", 1), // too long
		"0x" + strings.Repeat("z", 64), // non-hex
	}
	for _, c := range cases {
		if _, err := IDFromHex(c); err == nil {
			t.Errorf("IDFromHex(%q) must be rejected", c)
		}
	}
}

// TestProof_SiblingsLengthIsLog2Width: the on-chain verifier loops over the sibling
// array, so its length is part of the interface between the two implementations.
func TestProof_SiblingsLengthIsLog2Width(t *testing.T) {
	for n := 1; n <= 17; n++ {
		leaves := ids(n)
		proof, err := Prove(leaves, 0)
		if err != nil {
			t.Fatalf("n=%d Prove: %v", n, err)
		}
		if got, want := len(proof.Siblings), log2(proof.Width); got != want {
			t.Errorf("n=%d: %d siblings, want log2(%d)=%d", n, got, proof.Width, want)
		}
	}
}

func TestNextPow2(t *testing.T) {
	cases := map[int]int{
		-5: 1, 0: 1, 1: 1, 2: 2, 3: 4, 4: 4, 5: 8, 8: 8, 9: 16, 16: 16,
	}
	for in, want := range cases {
		if got := nextPow2(in); got != want {
			t.Errorf("nextPow2(%d) = %d, want %d", in, got, want)
		}
	}
}
