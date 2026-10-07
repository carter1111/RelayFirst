package merkle

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// BalanceRoot is the Merkle root over an epoch's SETTLED points (MVP.md §6.2b).
//
// # It is a different root from the epoch's receipt root
//
// MVP.md §6.2b names two roots that must not be conflated:
//
//   - the RECEIPT root is built from receipt ids (the tree this package already
//     builds) and proves a receipt is included in an epoch;
//   - the BALANCE root is built from each agent's settled total and is what a
//     points claim is verified against.
//
// This file is the balance half. It reuses the same tree shape (Root, Prove,
// Verify) so an epoch's balance root can be produced, anchored and proven exactly
// like a receipt root; only the leaf preimage differs.
//
// # The leaf must match contracts/RelayPoints.sol byte for byte
//
// The claim contract computes
//
//	keccak256(abi.encodePacked(bytes32 agentId, uint256 total, uint64 epoch))
//
// and invariant A4 requires the two implementations to agree, so the preimage here
// is the concatenation of those three values in that order: 32 bytes, then a
// big-endian 32-byte total, then a big-endian 8-byte epoch. Both integers are
// unsigned and exact -- points are carried as an integer micro-count, not a float
// -- because a float would round differently here and on-chain.

// BalanceLeaf returns the claim leaf for one agent's settled total at an epoch.
//
// agentID is the 32-byte value RelayPoints pays: keccak256(chainId ‖ address),
// which AgentID produces from the "agent:eip155:<chain>0x<addr>" form. epoch is
// inside the leaf so a proof for one epoch cannot be replayed into another.
//
// totalMicro is the agent's settled total in micro-points (integer). It must be
// non-negative: a negative total describes no claim.
func BalanceLeaf(agentID Hash, totalMicro int64, epoch uint64) (Hash, error) {
	if totalMicro < 0 {
		return Hash{}, fmt.Errorf("merkle: balance total %d is negative", totalMicro)
	}

	pre := make([]byte, 0, IDLen+32+8)
	pre = append(pre, agentID[:]...)

	var total [32]byte
	binary.BigEndian.PutUint64(total[24:], uint64(totalMicro)) // uint256, high bits zero
	pre = append(pre, total[:]...)

	var ep [8]byte
	binary.BigEndian.PutUint64(ep[:], epoch)
	pre = append(pre, ep[:]...)

	return keccak256(pre), nil
}

// AgentID returns the 32-byte agent identifier RelayPoints uses as a claim key,
// from the canonical "agent:eip155:<chainId>:0x<address>" form.
//
// It is keccak256(abi.encodePacked(uint64 chainId, address)), 8 then 20 bytes.
// This mirrors RelayPoints._agentId. Parsing lives here rather than depending on
// internal/agentid so the encoding is self-contained: only the leaf builder needs
// it, and a leaf is the one place it must be byte-exact.
func AgentID(id string) (Hash, error) {
	s := strings.TrimPrefix(id, "agent:eip155:")
	chainStr, addrStr, ok := strings.Cut(s, ":")
	if !ok {
		return Hash{}, fmt.Errorf("merkle: agent id %q is not agent:eip155:<chainId>:<address>", id)
	}

	chainID, err := parseUint64(chainStr)
	if err != nil {
		return Hash{}, fmt.Errorf("merkle: agent id chainId %q: %w", chainStr, err)
	}

	addrHex := strings.TrimPrefix(strings.ToLower(addrStr), "0x")
	if len(addrHex) != 40 {
		return Hash{}, fmt.Errorf("merkle: agent id address %q must be 20 bytes", addrStr)
	}
	addr, err := hex.DecodeString(addrHex)
	if err != nil {
		return Hash{}, fmt.Errorf("merkle: agent id address %q is not hex: %w", addrStr, err)
	}

	pre := make([]byte, 0, 8+20)
	var c [8]byte
	binary.BigEndian.PutUint64(c[:], chainID)
	pre = append(pre, c[:]...)
	pre = append(pre, addr...)

	return keccak256(pre), nil
}

// BalanceRootFromTotals builds the epoch's balance root from each agent's settled
// total in micro-points.
//
// Leaves are placed in SORTED agent order. The tree shape does not sort, and a
// caller passing a map would otherwise get a root that depends on iteration order
// -- a different root for the same settlement, which no verifier could reproduce.
// The order here is the canonical one.
func BalanceRootFromTotals(epoch uint64, totalsMicro map[Hash]int64) (Hash, error) {
	agents := make([]Hash, 0, len(totalsMicro))
	for a := range totalsMicro {
		agents = append(agents, a)
	}
	sortHashes(agents)

	leaves := make([]Hash, 0, len(agents))
	for _, a := range agents {
		leaf, err := BalanceLeaf(a, totalsMicro[a], epoch)
		if err != nil {
			return Hash{}, err
		}
		leaves = append(leaves, leaf)
	}
	return Root(leaves), nil
}

// BalanceProof builds an inclusion proof for one agent's leaf in an epoch's balance
// tree. The leaves are ordered exactly as BalanceRootFromTotals orders them.
func BalanceProof(epoch uint64, totalsMicro map[Hash]int64, agent Hash) (Proof, error) {
	agents := make([]Hash, 0, len(totalsMicro))
	for a := range totalsMicro {
		agents = append(agents, a)
	}
	sortHashes(agents)

	leaves := make([]Hash, 0, len(agents))
	index := -1
	for i, a := range agents {
		total, ok := totalsMicro[a]
		if !ok {
			continue
		}
		leaf, err := BalanceLeaf(a, total, epoch)
		if err != nil {
			return Proof{}, err
		}
		if a == agent {
			index = i
		}
		leaves = append(leaves, leaf)
	}
	if index < 0 {
		return Proof{}, fmt.Errorf("merkle: agent %s has no balance in epoch %d", agent.Hex(), epoch)
	}
	return Prove(leaves, index)
}

// sortHashes sorts hashes in ascending byte order (the canonical leaf order).
func sortHashes(h []Hash) {
	// A tiny insertion sort keeps this file dependency-free of sort.Slice's
	// closure allocation; n is the number of agents in one epoch.
	for i := 1; i < len(h); i++ {
		for j := i; j > 0 && less(h[j], h[j-1]); j-- {
			h[j], h[j-1] = h[j-1], h[j]
		}
	}
}

func less(a, b Hash) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// parseUint64 parses a decimal uint64 without pulling strconv into every path.
func parseUint64(s string) (uint64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a decimal integer")
		}
		next := n*10 + uint64(c-'0')
		if next < n {
			return 0, fmt.Errorf("overflows uint64")
		}
		n = next
	}
	return n, nil
}
