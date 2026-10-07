package scoring

import (
	"fmt"

	"github.com/relayfirst/relayfirst/internal/tenure"
)

// This file is the two-pool settlement (MVP.md §6.2c, incentive.md §2): the epoch budget
// is split between a WORK pool (Layer 1) and a NODE pool (Layer 0) before either is
// shared out. It is a pure function so a settlement stays recomputable, and it is
// separate from Settle (the single-pool Layer 1 case) so existing callers are untouched.

// Inputs is everything a two-pool settlement needs. It is assembled by the caller from
// ledgers and passed in whole, so the settlement itself reads no clock and no store and
// stays a pure function of its inputs.
type Inputs struct {
	// Work is each agent's accumulated work this epoch.
	Work map[string]float64

	// NodeTenures is each node's tenure count, for the Layer 0 tier weight.
	NodeTenures map[string]int

	// Bindings maps an agent id to the node id it is bound to (the PoSR binding). An
	// agent absent here is unbound and gets no multiplier.
	Bindings map[string]string

	// ReceiptCounts is each agent's receipt count this epoch. It is what M1 gates the
	// multiplier on: an agent that produced no receipts earned no multiplier, however
	// alive its node was.
	ReceiptCounts map[string]int
}

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

// WorkMultiplier is the Layer 1 multiplier m_i for an agent (incentive.md §4, M1).
//
// It is 1.25 only when ALL three hold, and 1.0 otherwise:
//
//   - the agent is bound to a node (the PoSR binding);
//   - that node has reached the tenure floor (tenure.MinTenure);
//   - the agent produced at least one receipt this epoch.
//
// # Why "and receipts > 0", which is the point of M1
//
// The obvious reading -- bound plus a live node -- is a null test. A farm's machines are
// alive, and an independent verifier will sign that honestly, so "alive + bound" hands
// the bonus to a node that does no work at all. That routes around the whole liveness
// measurement: it would pay for uptime the protocol does not consider work. Gating on
// receipts ties the bonus to actual output, so an agent with zero receipts gets 1.0x no
// matter how reliable its node is.
func WorkMultiplier(agentID string, in Inputs) float64 {
	node, bound := in.Bindings[agentID]
	if !bound {
		return 1.0
	}
	if in.NodeTenures[node] < tenure.MinTenure {
		return 1.0
	}
	if in.ReceiptCounts[agentID] <= 0 {
		return 1.0
	}
	return 1.25
}

// SettleEpoch splits an epoch's budget into the work and node pools and allocates each.
//
// # The split
//
//   - The node pool is NodePoolFraction(epoch) of B(n), shared by tenure-tier weight
//     over NodeTenures. A node below the tenure floor has weight zero and takes nothing.
//   - The work pool is the rest, shared by WORK TIMES MULTIPLIER (m_i), then capped per
//     agent. Apply the multiplier before the cap so the cap is the ceiling on what is
//     actually emitted, which is what "5% of the budget" means.
//
// # Why the node pool can burn entirely
//
// If no node is eligible, the node pool emits nothing and is burned. It is NOT moved to
// the work pool: the phase ratios are a policy ("50% to nodes in Phase 1"), and a quiet
// node network changing where the money goes would make the ratio meaningless. Burning is
// the safe direction, and it matches the cap-surplus rule.
func SettleEpoch(epoch uint64, in Inputs) (EpochSettlement, error) {
	budget := EpochBudget(epoch)
	nodeFraction := NodePoolFraction(epoch)
	nodePool := budget * nodeFraction
	workPool := budget - nodePool

	// Layer 1: weight work by m_i, share the work pool, then cap.
	weighted := make(map[string]float64, len(in.Work))
	for agent, w := range in.Work {
		if w < 0 {
			return EpochSettlement{}, fmt.Errorf("scoring: agent %q has negative work %v", agent, w)
		}
		if w == 0 {
			weighted[agent] = 0
			continue
		}
		weighted[agent] = w * WorkMultiplier(agent, in)
	}
	workShares, err := allocateAmount(workPool, weighted)
	if err != nil {
		return EpochSettlement{}, err
	}
	capped := capAmount(workPool*PerAgentCapFraction, workShares)
	workBurned := workPool - TotalAllocated(capped)

	// Layer 0: share the node pool by tenure-tier weight.
	nodeShares := NodePoolShare(in.NodeTenures)
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
