package scoring

import (
	"math"
	"testing"
)

// TestSettleEpoch_SplitsBudgetBetweenPools: the epoch budget must split into a work
// pool and a node pool at the phase ratio (MVP.md §6.2c), and the two must sum to no
// more than the budget.
func TestSettleEpoch_SplitsBudgetBetweenPools(t *testing.T) {
	const epoch = 0 // Phase 1: 50/50
	budget := EpochBudget(epoch)

	got, err := SettleEpoch(epoch,
		map[string]float64{"a": 1},
		map[string]int{"node-x": 5}, // tenure 5 -> tier 1.0
	)
	if err != nil {
		t.Fatalf("SettleEpoch: %v", err)
	}

	// One worker takes the whole work pool, but the 5% cap bites.
	if want := budget * NodePoolFraction(epoch) * PerAgentCapFraction; math.Abs(got.Work["a"]-want) > 1e-6 {
		t.Errorf("work a = %v, want the capped work pool %v", got.Work["a"], want)
	}
	// One eligible node takes the whole node pool.
	if want := budget * NodePoolFraction(epoch); math.Abs(got.Node["node-x"]-want) > 1e-6 {
		t.Errorf("node = %v, want the whole node pool %v", got.Node["node-x"], want)
	}

	// Nothing is over-emitted.
	total := TotalAllocated(got.Work) + TotalAllocated(got.Node)
	if total > budget*(1+1e-9) {
		t.Errorf("work+node = %v exceeds the budget %v", total, budget)
	}
}

// TestSettleEpoch_PhaseRatioIsByEpochHeight: the node pool shrinks as the phases advance.
func TestSettleEpoch_PhaseRatioIsByEpochHeight(t *testing.T) {
	work := map[string]float64{"a": 1}
	nodes := map[string]int{"n": 5}

	p1, _ := SettleEpoch(0, work, nodes)
	p2, _ := SettleEpoch(30, work, nodes)
	p3, _ := SettleEpoch(60, work, nodes)

	// Same work and one node each; the node pool must decrease phase by phase.
	if !(p1.Node["n"] > p2.Node["n"] && p2.Node["n"] > p3.Node["n"]) {
		t.Errorf("node pool must shrink by phase: p1 %v, p2 %v, p3 %v", p1.Node["n"], p2.Node["n"], p3.Node["n"])
	}
}

// TestSettleEpoch_NoEligibleNodeBurnsTheNodePool: with no node past the tenure floor the
// node pool emits nothing and is burned, not moved to the work pool.
func TestSettleEpoch_NoEligibleNodeBurnsTheNodePool(t *testing.T) {
	const epoch = 0
	budget := EpochBudget(epoch)

	got, err := SettleEpoch(epoch,
		map[string]float64{"a": 0.01, "b": 0.01, "c": 0.01, "d": 0.01}, // spread so no cap
		map[string]int{"fresh": 1},                                     // below the floor
	)
	if err != nil {
		t.Fatalf("SettleEpoch: %v", err)
	}

	if len(got.Node) != 0 {
		t.Errorf("an ineligible node must get nothing, got %v", got.Node)
	}
	wantBurned := budget * NodePoolFraction(epoch)
	if math.Abs(got.NodeBurned-wantBurned) > 1e-6 {
		t.Errorf("node burned = %v, want the whole node pool %v", got.NodeBurned, wantBurned)
	}

	// And the work pool was NOT inflated with the node pool: its total stays bounded by
	// the work pool size.
	workPool := budget * (1 - NodePoolFraction(epoch))
	if got := TotalAllocated(got.Work); got > workPool*(1+1e-9) {
		t.Errorf("work total %v exceeds the work pool %v -- the node pool leaked", got, workPool)
	}
}

// TestSettleEpoch_IsDeterministic: same inputs, same numbers, so a settlement stays
// recomputable and feeds a reproducible root.
func TestSettleEpoch_IsDeterministic(t *testing.T) {
	work := map[string]float64{"a": 3, "b": 7}
	nodes := map[string]int{"x": 4, "y": 20}
	first, err := SettleEpoch(7, work, nodes)
	if err != nil {
		t.Fatalf("SettleEpoch: %v", err)
	}
	second, _ := SettleEpoch(7, work, nodes)
	if math.Abs(TotalAllocated(first.Work)-TotalAllocated(second.Work)) > 1e-9 {
		t.Error("work totals are not deterministic")
	}
	if math.Abs(TotalAllocated(first.Node)-TotalAllocated(second.Node)) > 1e-9 {
		t.Error("node totals are not deterministic")
	}
}
