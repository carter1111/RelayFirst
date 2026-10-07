package scoring

import (
	"math"
	"testing"

	"github.com/relayfirst/relayfirst/internal/tenure"
)

// inputs is a small helper to keep the tests readable.
func inputs(work map[string]float64, tenures map[string]int, binds map[string]string, receipts map[string]int) Inputs {
	if tenures == nil {
		tenures = map[string]int{}
	}
	if binds == nil {
		binds = map[string]string{}
	}
	if receipts == nil {
		receipts = map[string]int{}
	}
	return Inputs{Work: work, NodeTenures: tenures, Bindings: binds, ReceiptCounts: receipts}
}

// TestWorkMultiplier_M1_RequiresReceipts is the M1 falsification test the decision-maker
// asked for: a node past the tenure floor, bound to the agent, pays 1.25x ONLY if the
// agent actually produced receipts. "Alive" alone must not be enough, because a farm's
// machines are alive and an independent verifier signs that honestly -- paying for that
// would route around the entire liveness measurement.
func TestWorkMultiplier_M1_RequiresReceipts(t *testing.T) {
	const agent = "agent:a"
	in := inputs(
		map[string]float64{agent: 1},
		map[string]int{"node-1": 5},        // eligible tenure (>= 3)
		map[string]string{agent: "node-1"}, // bound
		map[string]int{agent: 0},           // NO receipts
	)

	if got := WorkMultiplier(agent, in); got != 1.0 {
		t.Fatalf("tenure-eligible but zero-receipt agent got m=%.2f, want 1.0 -- "+
			"'alive' must not pay the bonus on its own", got)
	}

	// With a receipt, the same agent earns the bonus.
	in.ReceiptCounts[agent] = 1
	if got := WorkMultiplier(agent, in); got != 1.25 {
		t.Fatalf("bound + eligible + receipts>0 got m=%.2f, want 1.25", got)
	}

	// And each of the three conditions, missing, drops it back to 1.0.
	in.ReceiptCounts[agent] = 1
	in.NodeTenures["node-1"] = tenure.MinTenure - 1 // below floor
	if got := WorkMultiplier(agent, in); got != 1.0 {
		t.Errorf("below tenure floor got m=%.2f, want 1.0", got)
	}
	in.NodeTenures["node-1"] = 5
	in.Bindings = map[string]string{} // unbound
	if got := WorkMultiplier(agent, in); got != 1.0 {
		t.Errorf("unbound got m=%.2f, want 1.0", got)
	}
}

// TestSettleEpoch_M1_ZeroReceiptPaysBaseNotBonus is the end-to-end version of the same
// property: two agents with equal work, bound to equally-live nodes, must NOT both get
// the bonus -- the one with no receipts stays at the base and so earns less.
func TestSettleEpoch_M1_ZeroReceiptPaysBaseNotBonus(t *testing.T) {
	withReceipts := "agent:eip155:8453:0x00000000000000000000000000000000000000a1"
	noReceipts := "agent:eip155:8453:0x00000000000000000000000000000000000000a2"

	work := map[string]float64{withReceipts: 1, noReceipts: 1}
	// Background agents keep each of the two BELOW the 5% cap, so the cap does not mask
	// the multiplier difference (both would otherwise clamp to the same ceiling).
	for i := 0; i < 100; i++ {
		work[fmtAgentID(0xb0+i)] = 1
	}

	got, err := SettleEpoch(0, Inputs{
		Work:          work,
		NodeTenures:   map[string]int{"n1": 12, "n2": 12},
		Bindings:      map[string]string{withReceipts: "n1", noReceipts: "n2"},
		ReceiptCounts: map[string]int{withReceipts: 5, noReceipts: 0},
	})
	if err != nil {
		t.Fatalf("SettleEpoch: %v", err)
	}

	if !(got.Work[withReceipts] > got.Work[noReceipts]) {
		t.Errorf("the agent with receipts (%v) must out-earn the zero-receipt one (%v); "+
			"M1 pays the bonus only for work",
			got.Work[withReceipts], got.Work[noReceipts])
	}
	// The ratio must be exactly the multiplier, and the zero-receipt side must be the
	// base: 1.25 / 1.0.
	if ratio := got.Work[withReceipts] / got.Work[noReceipts]; math.Abs(ratio-1.25) > 1e-6 {
		t.Errorf("earnings ratio = %v, want 1.25 (the multiplier, and only for the worker)", ratio)
	}
}

func fmtAgentID(n int) string {
	const hex = "0123456789abcdef"
	// A 40-hex-char address from a small integer, just distinct stand-ins.
	out := make([]byte, 40)
	for i := range out {
		out[i] = '0'
	}
	out[38] = hex[(n>>4)&0xf]
	out[39] = hex[n&0xf]
	return "agent:eip155:8453:0x" + string(out)
}

// TestSettleEpoch_SplitsBudgetBetweenPools: the epoch budget must split into a work pool
// and a node pool at the phase ratio, and the two together must not exceed the budget.
func TestSettleEpoch_SplitsBudgetBetweenPools(t *testing.T) {
	const epoch = 0 // Phase 1: 50/50
	budget := EpochBudget(epoch)

	got, err := SettleEpoch(epoch,
		inputs(map[string]float64{"a": 1}, map[string]int{"node-x": 5}, nil, nil),
	)
	if err != nil {
		t.Fatalf("SettleEpoch: %v", err)
	}

	if want := budget * NodePoolFraction(epoch) * PerAgentCapFraction; math.Abs(got.Work["a"]-want) > 1e-6 {
		t.Errorf("work a = %v, want the capped work pool %v", got.Work["a"], want)
	}
	if want := budget * NodePoolFraction(epoch); math.Abs(got.Node["node-x"]-want) > 1e-6 {
		t.Errorf("node = %v, want the whole node pool %v", got.Node["node-x"], want)
	}

	total := TotalAllocated(got.Work) + TotalAllocated(got.Node)
	if total > budget*(1+1e-9) {
		t.Errorf("work+node = %v exceeds the budget %v", total, budget)
	}
}

// TestSettleEpoch_PhaseRatioIsByEpochHeight: the node pool shrinks as the phases advance.
func TestSettleEpoch_PhaseRatioIsByEpochHeight(t *testing.T) {
	work := map[string]float64{"a": 1}
	nodes := map[string]int{"n": 5}

	p1, _ := SettleEpoch(0, inputs(work, nodes, nil, nil))
	p2, _ := SettleEpoch(30, inputs(work, nodes, nil, nil))
	p3, _ := SettleEpoch(60, inputs(work, nodes, nil, nil))

	if !(p1.Node["n"] > p2.Node["n"] && p2.Node["n"] > p3.Node["n"]) {
		t.Errorf("node pool must shrink by phase: p1 %v, p2 %v, p3 %v", p1.Node["n"], p2.Node["n"], p3.Node["n"])
	}
}

// TestSettleEpoch_NoEligibleNodeBurnsTheNodePool: with no node past the tenure floor the
// node pool emits nothing, is burned, and is NOT moved to the work pool.
func TestSettleEpoch_NoEligibleNodeBurnsTheNodePool(t *testing.T) {
	const epoch = 0
	budget := EpochBudget(epoch)

	got, err := SettleEpoch(epoch,
		inputs(map[string]float64{"a": 0.01, "b": 0.01, "c": 0.01, "d": 0.01},
			map[string]int{"fresh": 1}, nil, nil),
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
	workPool := budget * (1 - NodePoolFraction(epoch))
	if got := TotalAllocated(got.Work); got > workPool*(1+1e-9) {
		t.Errorf("work total %v exceeds the work pool %v -- the node pool leaked", got, workPool)
	}
}

// TestSettleEpoch_IsDeterministic: same inputs, same numbers.
func TestSettleEpoch_IsDeterministic(t *testing.T) {
	in := inputs(map[string]float64{"a": 3, "b": 7}, map[string]int{"x": 4, "y": 20}, nil, nil)
	first, err := SettleEpoch(7, in)
	if err != nil {
		t.Fatalf("SettleEpoch: %v", err)
	}
	second, _ := SettleEpoch(7, in)
	if math.Abs(TotalAllocated(first.Work)-TotalAllocated(second.Work)) > 1e-9 {
		t.Error("work totals are not deterministic")
	}
	if math.Abs(TotalAllocated(first.Node)-TotalAllocated(second.Node)) > 1e-9 {
		t.Error("node totals are not deterministic")
	}
}

// TestNodePoolFraction_SunsetAt52: Phase 3 is a sunset, not a smaller subsidy. From epoch
// 52 the node pool is ZERO -- Layer 0 stops, and the whole budget is the work pool.
func TestNodePoolFraction_SunsetAt52(t *testing.T) {
	if got := NodePoolFraction(51); got != 0.25 {
		t.Errorf("epoch 51 node pool = %v, want 0.25 (Phase 2)", got)
	}
	if got := NodePoolFraction(52); got != 0 {
		t.Errorf("epoch 52 node pool = %v, want 0 (sunset) -- Layer 0 must stop", got)
	}
	if got := NodePoolFraction(200); got != 0 {
		t.Errorf("epoch 200 node pool = %v, want 0 (sunset persists)", got)
	}

	// And a sunset epoch pays the node pool nothing, even to an eligible node.
	got, err := SettleEpoch(52, inputs(map[string]float64{"a": 1}, map[string]int{"n": 12}, nil, nil))
	if err != nil {
		t.Fatalf("SettleEpoch: %v", err)
	}
	if len(got.Node) != 0 {
		t.Errorf("a sunset epoch must pay no node points, got %v", got.Node)
	}
}
