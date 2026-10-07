package scoring

import "fmt"

// This file is the two-pool settlement (MVP.md §6.2c, incentive.md §2): the epoch budget
// is split between a WORK pool (Layer 1) and a NODE pool (Layer 0) before either is
// shared out. It is a pure function so a settlement stays recomputable, and it is
// separate from Settle (which is the single-pool Layer 1 case) so the Layer 1 callers and
// tests are untouched.

// EpochSettlement is the outcome of one epoch's two-pool split.
type EpochSettlement struct {
	// Work is the Layer 1 points per agent.
	Work map[string]float64

	// Node is the Layer 0 points per node, keyed by the node's agent id.
	Node map[string]float64

	// WorkBurned and NodeBurned are the parts of each pool that were not emitted: the
	// work pool's per-agent cap surplus, and a node pool with no eligible node. Both are
	// burned rather than rolled over or redistributed (incentive.md §10.4), so total
	// issuance is a ceiling, not a target.
	WorkBurned float64
	NodeBurned float64
}

// SettleEpoch splits an epoch's budget into the work and node pools and allocates each.
//
// # The split
//
//   - The node pool is NodePoolFraction(epoch) of B(n), shared by tenure-tier weight
//     over nodeTenures. A node below the tenure floor has weight zero and takes nothing.
//   - The work pool is the rest, shared by work share and then capped per agent.
//
// # Why the node pool can burn entirely
//
// If no node is eligible, the node pool emits nothing and is burned. It is NOT moved to
// the work pool: the phase ratios are a policy ("50% to nodes in Phase 1"), and a quiet
// node network changing where the money goes would make the ratio meaningless. Burning is
// the safe direction, and it matches the cap-surplus rule.
func SettleEpoch(epoch uint64, work map[string]float64, nodeTenures map[string]int) (EpochSettlement, error) {
	budget := EpochBudget(epoch)
	nodeFraction := NodePoolFraction(epoch)
	nodePool := budget * nodeFraction
	workPool := budget - nodePool

	// Layer 1: share the work pool by work, then apply the per-agent cap.
	workShares, err := allocateAmount(workPool, work)
	if err != nil {
		return EpochSettlement{}, err
	}
	capped := capAmount(workPool*PerAgentCapFraction, workShares)
	workBurned := workPool - TotalAllocated(capped)

	// Layer 0: share the node pool by tenure-tier weight.
	nodeShares := NodePoolShare(nodeTenures)
	nodeOut := make(map[string]float64, len(nodeShares))
	var nodePaid float64
	for node, frac := range nodeShares {
		v := nodePool * frac
		nodeOut[node] = v
		nodePaid += v
	}

	return EpochSettlement{
		Work:       capped,
		Node:       nodeOut,
		WorkBurned: workBurned,
		NodeBurned: nodePool - nodePaid,
	}, nil
}

// allocateAmount distributes a fixed amount in proportion to weights.
//
// It is Allocate with the budget supplied rather than read from EpochBudget, because the
// two-pool split hands the work pool a subset of the budget. Negative weight is an error
// (an upstream bug), and a zero total allocates nothing rather than dividing by zero.
func allocateAmount(amount float64, weights map[string]float64) (map[string]float64, error) {
	total := 0.0
	for id, w := range weights {
		if w < 0 {
			return nil, fmt.Errorf("scoring: agent %q has negative work %v", id, w)
		}
		total += w
	}

	out := make(map[string]float64, len(weights))
	if total <= 0 {
		for id := range weights {
			out[id] = 0
		}
		return out, nil
	}
	for id, w := range weights {
		if w == 0 {
			out[id] = 0
			continue
		}
		out[id] = amount * (w / total)
	}
	return out, nil
}

// capAmount clamps every entry to ceil, dropping any surplus (which is burned).
func capAmount(ceil float64, allocation map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(allocation))
	for id, v := range allocation {
		if v < 0 {
			v = 0
		}
		if v > ceil {
			v = ceil
		}
		out[id] = v
	}
	return out
}
