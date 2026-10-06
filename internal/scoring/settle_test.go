package scoring

import (
	"fmt"
	"math"
	"testing"
)

// These tests lock the two properties the head-start narrative rests on, now that
// model B is the single emission model (D1, 2026-10-07):
//
//   - decay: a later epoch has a strictly smaller budget, so early work is worth more
//     in absolute terms;
//   - the per-agent cap: one agent cannot take more than B(n) * 5%, so a whale cannot
//     crowd out newcomers.
//
// They are written against Settle, the step that turns work into emitted points, so
// they fail if either property is lost anywhere along that path.

// TestSettle_SharesTheFixedBudget: the sum of emitted points equals the budget when
// no one is capped. That is what "fixed budget" means -- the total is an INPUT, not
// an output that grows with the number of receipts.
func TestSettle_SharesTheFixedBudget(t *testing.T) {
	const epoch = 0

	// 25 equal agents, so each share is 4% — BELOW the 5% cap. The cap only bites
	// above 5%, and 5% x 20 = 100%, so at most 20 agents can be paid their
	// proportional share before the ceiling flattens the tail. A two-agent case would
	// cap both and the sum would fall short of the budget for a reason that is the cap
	// working, not the share failing.
	work := map[string]float64{}
	for i := 0; i < 25; i++ {
		work[fmt.Sprintf("agent-%02d", i)] = 10
	}

	got, err := Settle(epoch, work)
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	var sum float64
	for _, v := range got {
		sum += v
	}
	if math.Abs(sum-EpochBudget(epoch)) > 1e-6 {
		t.Errorf("settled sum = %v, want the full budget %v when nobody is capped",
			sum, EpochBudget(epoch))
	}
	// Equal work means equal shares.
	want := EpochBudget(epoch) / 25
	for agent, v := range got {
		if math.Abs(v-want) > 1e-6 {
			t.Errorf("agent %s settled %v, want an equal share %v", agent, v, want)
		}
	}
}

// TestSettle_AppliesThePerAgentCap: one agent's emitted points never exceed B(n) * 5%.
//
// The cap only has meaning against a budget -- "5% of what?" -- which is exactly why
// model B was chosen. This is that meaning made executable.
func TestSettle_AppliesThePerAgentCap(t *testing.T) {
	const epoch = 0
	// One agent does all the work: a proportional share would be the whole budget, far
	// above the cap.
	got, err := Settle(epoch, map[string]float64{"whale": 1_000_000})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	cap := PerAgentCap(epoch)
	if got["whale"] > cap+1e-9 {
		t.Errorf("whale emitted %v, above the per-agent cap %v", got["whale"], cap)
	}
	if math.Abs(got["whale"]-cap) > 1e-6 {
		t.Errorf("whale emitted %v, want exactly the cap %v", got["whale"], cap)
	}
	// And the sum can fall BELOW the budget, because surplus is not emitted. That is
	// the safe direction: a smaller supply increase, never a larger one.
	if got["whale"] > EpochBudget(epoch) {
		t.Errorf("emitted %v exceeds the budget %v", got["whale"], EpochBudget(epoch))
	}
}

// TestSettle_DecayMakesEarlyEpochsWorthMore is the head-start property: the same work
// settles to fewer points in a later epoch.
func TestSettle_DecayMakesEarlyEpochsWorthMore(t *testing.T) {
	work := map[string]float64{"a": 100}

	early, err := Settle(0, work)
	if err != nil {
		t.Fatalf("Settle(0): %v", err)
	}
	late, err := Settle(365, work)
	if err != nil {
		t.Fatalf("Settle(365): %v", err)
	}
	if !(early["a"] > late["a"]) {
		t.Errorf("epoch 0 settled %v, epoch 365 settled %v -- later must be less",
			early["a"], late["a"])
	}
	// And the ratio is the decay the doc names, so the curve cannot silently change.
	want := math.Pow(Decay, 365)
	if ratio := late["a"] / early["a"]; math.Abs(ratio-want) > 1e-9 {
		t.Errorf("late/early = %v, want decay^365 = %v", ratio, want)
	}
}

// TestSettle_NoWorkEmitsNothing: an epoch where nobody worked allocates nothing rather
// than dividing by zero or minting a budget into the void.
func TestSettle_NoWorkEmitsNothing(t *testing.T) {
	got, err := Settle(0, map[string]float64{"a": 0, "b": 0})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	for agent, v := range got {
		if v != 0 {
			t.Errorf("agent %q emitted %v with no work", agent, v)
		}
	}
}

// TestSettle_RejectsNegativeWork keeps an upstream bug from becoming a negative share.
func TestSettle_RejectsNegativeWork(t *testing.T) {
	if _, err := Settle(0, map[string]float64{"a": -1}); err == nil {
		t.Fatal("negative work must be an error, not a silent clamp")
	}
}

// TestSettle_IsDeterministic: the same inputs settle to the same output, which is what
// lets the result feed a Merkle root a client verifies offline.
func TestSettle_IsDeterministic(t *testing.T) {
	work := map[string]float64{"a": 12.5, "b": 3, "c": 99}
	first, _ := Settle(7, work)
	for i := 0; i < 20; i++ {
		again, _ := Settle(7, work)
		for agent, v := range first {
			if again[agent] != v {
				t.Fatalf("settlement is not deterministic for %q: %v vs %v", agent, v, again[agent])
			}
		}
	}
}
