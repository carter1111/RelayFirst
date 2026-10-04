// Package merkle builds an epoch's receipts into a Merkle tree whose root is
// anchored on-chain (MVP.md §4.3, S7).
//
// # Why a standard construction, not a bespoke one
//
// Invariant A1 forbids hand-rolled cryptographic primitives, and the reasoning
// generalises to structures built from them: a Merkle tree with a subtly wrong
// shape still verifies its own proofs perfectly, so the mistake is invisible in
// every test you would naturally write. The only way to be confident is to follow
// a published construction.
//
// This follows RFC 6962's domain-separation scheme, with two deliberate
// substitutions documented below.
//
// # The construction
//
//	leaf(id)         = keccak256(0x00 ‖ id)          id is a 32-byte receipt id
//	node(left,right) = keccak256(0x01 ‖ left ‖ right)
//	root()           = keccak256("")                 the empty epoch
//
// The single prefix byte is the whole point. Without it, an internal node
// keccak256(l ‖ r) and a leaf keccak256(id) are drawn from the same output space,
// so an attacker could present a two-element subtree as if it were a single leaf
// and win a proof for a receipt that was never in the tree. RFC 6962 introduces
// 0x00/0x01 precisely to make the two cases structurally distinct.
//
// # Substitution 1: keccak256 instead of SHA-256
//
// RFC 6962 specifies SHA-256. Solidity's native hash is keccak256, and verifying a
// proof on-chain is the entire reason this tree exists — so the hash must be one the
// EVM can compute, or the on-chain verification becomes a hand-written SHA-256 in
// Solidity, which is exactly the kind of custom cryptography to avoid. Note that
// golang.org/x/crypto/sha3 (already a dependency for EIP-712) provides keccak256, so
// no new dependency is introduced.
//
// # Substitution 2: a perfect tree via power-of-two padding
//
// RFC 6962 balances an odd tree by splitting at the largest power of two below the
// leaf count. That is well specified, but it makes the tree shape depend on the
// count in a way that is easy to implement differently in two languages — and a
// shape mismatch between Go and Solidity is silent until a proof fails.
//
// Padding to a power of two instead yields one unambiguous shape in both languages:
// every internal node has two children, every proof has length log2(n), and
// verification is a single indexed loop. The padding leaf is the leaf of the
// all-zero receipt id, chosen because it can never collide with a real receipt
// (no receipt has an all-zero id).
//
// The cost is that the root commits to the padded width, so a verifier needs the
// epoch's receipt count. That is acceptable: the count is derivable from the same
// receipts the root is recomputed from.
//
// # What this package does NOT do
//
// It does not decide which receipts belong to an epoch — the caller does, from the
// store. Anchoring, deploying and submitting are human actions; see
// contracts/RelayAnchor.sol and docs/stages/S7-report.md.
package merkle

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/sha3"
)

// IDLen is the byte length of a receipt id.
const IDLen = 32

// Domain separation prefixes, following RFC 6962.
const (
	// leafPrefix marks a hash as coming from a leaf.
	leafPrefix = 0x00

	// nodePrefix marks a hash as coming from an internal node.
	nodePrefix = 0x01
)

// Hash is a 32-byte keccak256 digest.
type Hash [32]byte

// ZeroID is the all-zero receipt id.
//
// No real receipt has this id: a receipt id is a hash of the signed payload, so
// producing a zero one would require a preimage attack. That is what makes it safe
// as the padding marker rather than a value that could be confused with real work.
var ZeroID Hash

// EmptyRoot is the root of an epoch with no receipts.
//
// It follows RFC 6962, which defines the empty tree's hash as the hash of the empty
// string, so an empty epoch has a well-defined root rather than a special case at
// every call site. On-chain, an empty epoch should simply not be anchored.
func EmptyRoot() Hash { return keccak256(nil) }

// LeafHash returns the leaf hash for a receipt id.
func LeafHash(id Hash) Hash {
	buf := make([]byte, 0, 1+IDLen)
	buf = append(buf, leafPrefix)
	buf = append(buf, id[:]...)
	return keccak256(buf)
}

// nodeHash combines two child hashes.
//
// It is unexported on purpose: exposing it would invite callers to build trees by
// hand, and the ordering rule (which side is left) is exactly what must not be
// reimplemented. Callers use Root and Proof.
func nodeHash(left, right Hash) Hash {
	buf := make([]byte, 0, 1+2*32)
	buf = append(buf, nodePrefix)
	buf = append(buf, left[:]...)
	buf = append(buf, right[:]...)
	return keccak256(buf)
}

// Root returns the Merkle root over leaves, in order.
//
// An empty input yields EmptyRoot(). A single leaf yields its own leaf hash, so a
// one-receipt epoch needs no dummy sibling — which matters because a fabricated
// sibling would be indistinguishable from real data.
func Root(leaves []Hash) Hash {
	switch len(leaves) {
	case 0:
		return EmptyRoot()
	case 1:
		return LeafHash(leaves[0])
	}

	width := nextPow2(len(leaves))
	level := make([]Hash, 0, width)
	for _, id := range leaves {
		level = append(level, LeafHash(id))
	}
	// Pad to a power of two so the shape is unambiguous. The padding target is held
	// in its own variable rather than read from cap(level): cap is an allocation
	// detail, and a tree shape that depended on it would change if the slice were
	// ever built differently.
	for len(level) < width {
		level = append(level, LeafHash(ZeroID))
	}

	for len(level) > 1 {
		next := make([]Hash, 0, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			next = append(next, nodeHash(level[i], level[i+1]))
		}
		level = next
	}
	return level[0]
}

// Proof is a Merkle inclusion proof for one leaf.
type Proof struct {
	// Leaf is the receipt id the proof is about.
	Leaf Hash `json:"leaf"`

	// Index is the leaf's position in the epoch's ordering. Verification needs it
	// to know which side each sibling belongs on, so it is part of the proof rather
	// than inferred.
	Index int `json:"index"`

	// Siblings are the hashes from the leaf level upward to the root.
	Siblings []Hash `json:"siblings"`

	// Root is the root the proof should reproduce.
	Root Hash `json:"root"`

	// Width is the padded leaf count, which is what makes the tree shape
	// reproducible. It is recorded so a verifier can reconstruct the same shape
	// without guessing.
	Width int `json:"width"`
}

// Prove returns an inclusion proof for the leaf at index.
//
// It is an error for index to be out of range: an out-of-range index would
// otherwise produce a proof that silently cannot verify, which is worse than a
// clear failure at generation time.
func Prove(leaves []Hash, index int) (Proof, error) {
	if len(leaves) == 0 {
		return Proof{}, errors.New("merkle: cannot prove against an empty epoch")
	}
	if index < 0 || index >= len(leaves) {
		return Proof{}, fmt.Errorf("merkle: index %d is out of range for %d leaf/leaves", index, len(leaves))
	}

	width := nextPow2(len(leaves))
	level := make([]Hash, 0, width)
	for _, id := range leaves {
		level = append(level, LeafHash(id))
	}
	for len(level) < width {
		level = append(level, LeafHash(ZeroID))
	}

	siblings := make([]Hash, 0, log2(width))
	pos := index
	for len(level) > 1 {
		sibling := pos ^ 1
		siblings = append(siblings, level[sibling])

		next := make([]Hash, 0, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			next = append(next, nodeHash(level[i], level[i+1]))
		}
		level = next
		pos /= 2
	}

	return Proof{
		Leaf:     leaves[index],
		Index:    index,
		Siblings: siblings,
		Root:     level[0],
		Width:    width,
	}, nil
}

// Verify reports whether the proof shows its leaf is in its root.
//
// It is the same algorithm the Solidity contract runs, and the two are checked
// against shared vectors in CI (invariant A4), because a verifier that agrees with
// itself proves nothing.
func Verify(p Proof) bool {
	if !p.IsWellFormed() {
		return false
	}

	h := LeafHash(p.Leaf)
	pos := p.Index
	for _, sibling := range p.Siblings {
		if pos&1 == 0 {
			h = nodeHash(h, sibling)
		} else {
			h = nodeHash(sibling, h)
		}
		pos /= 2
	}
	return h == p.Root
}

// IsWellFormed reports whether the proof's shape is internally consistent.
//
// # Why this check exists separately
//
// A proof carrying a short sibling list would still "verify" if the caller supplied
// a root it happens to reproduce, and a proof whose index lies outside its declared
// width describes a position the tree never had. Rejecting those up front means
// Verify answers a question about a well-defined tree, rather than accepting any
// sequence of hashes that happens to fold to the expected value.
func (p Proof) IsWellFormed() bool {
	if p.Width <= 0 {
		return false
	}
	if p.Width&(p.Width-1) != 0 {
		// Width must be a power of two; anything else is not this construction.
		return false
	}
	if p.Index < 0 || p.Index >= p.Width {
		return false
	}
	if len(p.Siblings) != log2(p.Width) {
		return false
	}
	return true
}

// ---------------------------------------------------------------- helpers

// nextPow2 returns the smallest power of two >= n, with a floor of 1.
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

// log2 returns the base-2 logarithm of a power of two.
func log2(n int) int {
	k := 0
	for n > 1 {
		n >>= 1
		k++
	}
	return k
}

// keccak256 returns the Keccak-256 digest of b.
//
// It uses the same library as the EIP-712 encoder, so the project has exactly one
// keccak implementation and the receipt signature and the Merkle tree cannot drift
// apart.
func keccak256(b []byte) Hash {
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write(b)
	var out Hash
	copy(out[:], h.Sum(nil))
	return out
}

// IDFromHex parses a receipt id such as "0x<64 hex chars>" into a Hash.
//
// It accepts the 0x prefix optionally, because a receipt id appears with it in
// receipts and without it in some tooling output, and rejecting one spelling would
// be a papercut with no security benefit.
func IDFromHex(s string) (Hash, error) {
	var out Hash

	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(t, "0x")
	t = strings.TrimPrefix(t, "0X")
	if len(t) != IDLen*2 {
		return out, fmt.Errorf("merkle: id must be %d hex characters, got %d", IDLen*2, len(t))
	}

	raw, err := hex.DecodeString(t)
	if err != nil {
		return out, fmt.Errorf("merkle: id is not valid hex: %w", err)
	}
	copy(out[:], raw)
	return out, nil
}

// Hex renders a hash as "0x<64 hex chars>", matching how the contract and the CLI
// report values.
func (h Hash) Hex() string { return "0x" + hex.EncodeToString(h[:]) }

// String implements fmt.Stringer.
func (h Hash) String() string { return h.Hex() }
