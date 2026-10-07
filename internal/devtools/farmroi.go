//go:build ignore

// farmroi models whether a VPS farm can profit from the Layer 0 node pool.
//
//	go run internal/devtools/farmroi.go
//
// # Why this exists
//
// incentive.md §10.3 marks this LAUNCH-BLOCKING: with 50% of the epoch budget going to
// the node pool in Phase 1, the design is not safe to launch until it is shown that a
// farm -- many nodes on cheap VPSes, earning Layer 0 without doing any agent work --
// cannot recoup its hosting cost. If it can, nodes are a subsidy farm and the pool is
// captured by whoever spins up the most machines, which is the opposite of the
// "reliable relay operators" the pool is meant to reward.
//
// # What it models, and what it does NOT
//
// It models the RATIO that matters: a farm's Layer 0 share of one epoch's node pool,
// against the per-node VPS cost, over the first year. The split is tenure-tier weighted
// and the pool halves as the phases go, so both a naive farm (all fresh nodes, lowest
// tier) and a patient farm (nodes held past 12 epochs, top tier) are computed.
//
// It does NOT price points. There is no dollar value for a point, deliberately (A5: a
// point has no price and no promised return). So the honest output is dimensionless:
// points per USD of hosting, and the break-even "a point must eventually be worth X for
// the farm to profit". X is the answer a decision-maker supplies; this tool makes the
// question exact instead of hand-waved.
//
// # The spread parameter, and why it is the crux
//
// Reward is per-ELIGIBLE-NODE, split by tier weight, so a farm's share rises with the
// number of nodes it runs. What keeps that from being unbounded is dilution: the more
// nodes there are in total, the smaller each one's slice. The farm's share therefore
// depends on how many OTHER eligible nodes exist, which is the one number nobody knows
// before launch. The tool sweeps it, so the answer is "profit is (not) possible when the
// network has fewer/more than N nodes", rather than a single misleading point estimate.
package main

import (
	"fmt"
	"os"

	"github.com/relayfirst/relayfirst/internal/scoring"
	"github.com/relayfirst/relayfirst/internal/tenure"
)

// Assumptions, all explicit so a reviewer can change them. Each is a knob, not a claim.
type assumptions struct {
	// vpsCostUSDPerMonth is the hosting cost of one node. DigitalOcean's smallest,
	// Hetzner's smallest and a Vultr nano are all within a few dollars of this.
	vpsCostUSDPerMonth float64

	// farmNodes is how many nodes the farm runs.
	farmNodes int

	// fillRate is the fraction of a month an epoch spans; epochs are weekly.
	epochsPerMonth float64

	// epochs is how many epochs we model (52 = one year).
	epochs int
}

func defaultAssumptions() assumptions {
	return assumptions{
		vpsCostUSDPerMonth: 5.0,
		farmNodes:          100,
		epochsPerMonth:     365.0 / 7.0 / 12.0, // ~4.348 epochs per month
		epochs:             52,
	}
}

// nodePoolFraction is the share of the epoch budget that goes to the node pool, by
// phase (incentive.md §2, MVP.md §6.2c). Phase 3 is a SUNSET: the node pool stops.
func nodePoolFraction(epoch uint64) float64 {
	switch {
	case epoch <= 25:
		return 0.50 // Phase 1
	case epoch <= 51:
		return 0.25 // Phase 2
	default:
		return 0.00 // Phase 3: sunset -- the node pool stops (2026-10-08)
	}
}

// farmShare returns the farm's share of one epoch's node pool.
//
// otherNodes is the number of ELIGIBLE nodes that are not the farm's. Every node in the
// pool is one unit of weight; a farm node at the top tier weighs 1.25, a fresh one 1.0.
// The share is the farm's total weight over the network's total weight.
func farmShare(a assumptions, otherNodes int, farmTierWeight float64) float64 {
	farmWeight := float64(a.farmNodes) * farmTierWeight
	otherWeight := float64(otherNodes) * 1.0
	total := farmWeight + otherWeight
	if total <= 0 {
		return 0
	}
	return farmWeight / total
}

func main() {
	a := defaultAssumptions()

	fmt.Println("farmroi -- can a VPS farm profit from the Layer 0 node pool?")
	fmt.Println("(incentive.md §10.3, launch-blocking; no point is priced -- see the file comment)")
	fmt.Println()
	fmt.Printf("assumptions: %d nodes @ $%.2f/mo, %.1f epochs/yr, Phase 1 node pool 50%%\n",
		a.farmNodes, a.vpsCostUSDPerMonth, float64(a.epochs))
	fmt.Println()

	// Two farms: a naive one (all nodes fresh -> tier weight 1.0 until they age, but a
	// farm is always buying new machines, so its steady-state average stays low) and a
	// patient one (nodes held a year -> top tier 1.25).
	farms := []struct {
		name string
		tier float64
	}{
		{"fresh farm (all tenure < 3, INELIGIBLE)", 0.0},
		{"mature farm (tenure 3-5, 1.0x)", tenure.Tier(3)},
		{"patient farm (tenure 12+, 1.25x)", tenure.Tier(12)},
	}

	for _, f := range farms {
		fmt.Printf("== %s ==\n", f.name)
		if f.tier == 0 {
			fmt.Println("   weight 0: a node below tenure 3 earns NOTHING. A farm of brand-new")
			fmt.Println("   nodes is paid zero for its first 3 epochs, by design.")
			fmt.Println()
			continue
		}

		fmt.Printf("   %-14s %-12s %-14s %-16s %s\n", "other nodes", "farm share", "pts-equiv/epoch", "pts/yr per node", "pts/yr per $")
		for _, other := range []int{0, 100, 1_000, 10_000} {
			share := farmShare(a, other, f.tier)

			var totalPoints float64
			for e := 0; e < a.epochs; e++ {
				budget := scoring.EpochBudget(uint64(e))
				totalPoints += budget * nodePoolFraction(uint64(e)) * share
			}

			costPerYear := a.vpsCostUSDPerMonth * 12 * float64(a.farmNodes)
			perNode := totalPoints / float64(a.farmNodes)
			perUSD := totalPoints / costPerYear

			fmt.Printf("   %-14d %-12.3f %-14.0f %-16.1f %.1f\n",
				other, share, scoring.EpochBudget(0)*nodePoolFraction(0)*share, perNode, perUSD)
		}
		fmt.Println()
	}

	// The break-even question, stated exactly.
	fmt.Println("== break-even, stated exactly ==")
	fmt.Println("   A farm recoups hosting when (points earned) x (future value per point) >= (hosting cost).")
	fmt.Println("   With no point price (by design), the honest form is:")
	fmt.Println("       required point value ($/point) = hosting cost / points earned")
	fmt.Println()
	for _, f := range farms {
		if f.tier == 0 {
			continue
		}
		for _, other := range []int{100, 1_000, 10_000} {
			var totalPoints float64
			for e := 0; e < a.epochs; e++ {
				totalPoints += scoring.EpochBudget(uint64(e)) * nodePoolFraction(uint64(e)) * farmShare(a, other, f.tier)
			}
			costPerYear := a.vpsCostUSDPerMonth * 12 * float64(a.farmNodes)
			req := costPerYear / totalPoints
			fmt.Printf("   %-30s others=%-6d -> a point must be worth $%.8f for break-even\n", f.name, other, req)
		}
	}
	fmt.Println()
	fmt.Println("   If the launch parameters make a point worth less than the smallest figure above,")
	fmt.Println("   a farm cannot recoup hosting, which is the property §10.3 asks to be shown.")
	fmt.Println("   Flipping it: the LARGEST figure is the worst case for the protocol -- if even that")
	fmt.Println("   is cheap enough to be implausible, the pool is not farmable.")

	os.Exit(0)
}
