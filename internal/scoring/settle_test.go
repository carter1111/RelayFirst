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

// TestSettle_CapSurplusIsBurnedNotRedistributed is the "cap remainder is destroyed"
// decision (incentive.md v0.21 §10.4; MVP.md §6.3).
//
// When the per-agent cap bites, the withheld surplus must vanish: a fixed budget is a
// ceiling on issuance, not a pool to be re-shared. If the surplus were redistributed,
// the total emitted would climb back to the budget -- every epoch would mint the full
// amount, and the emission curve would be a rate rather than a bound.
//
// The witness is a two-agent epoch where one agent's proportional share is above the
// cap. Its surplus is large enough that any redistribution into the other agent would
// be visible, so the other agent's amount staying at its proportional share is the
// proof there was none.
func TestSettle_CapSurplusIsBurnedNotRedistributed(t *testing.T) {
	const epoch = 0
	budget := EpochBudget(epoch)

	// 99 : 1. The whale's proportional share is 0.99*budget = 19.8% of the budget,
	// well above the 5% cap, so a large surplus is withheld.
	settled, err := Settle(epoch, map[string]float64{"whale": 99, "honest": 1})
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}

	// The whale lands exactly on the cap.
	if math.Abs(settled["whale"]-PerAgentCap(epoch)) > 1e-6 {
		t.Errorf("whale = %v, want the cap %v", settled["whale"], PerAgentCap(epoch))
	}

	// The honest agent keeps its proportional share: 1% of the budget, NOT a larger
	// amount that would appear if the withheld surplus were handed back.
	wantHonest := budget * 0.01
	if math.Abs(settled["honest"]-wantHonest) > 1e-6 {
		t.Errorf("honest = %v, want its proportional %v (surplus must not be redistributed)",
			settled["honest"], wantHonest)
	}

	// And the epoch as a whole under-emits: the burned surplus is the difference.
	total := TotalAllocated(settled)
	if total >= budget {
		t.Errorf("emitted total %v is not below the budget %v — the surplus was not burned",
			total, budget)
	}
	// The gap is exactly the withheld surplus (0.99 - 0.05 of the budget). Asserting
	// the size, not merely "less than", pins that nothing else was paid out.
	wantBurned := budget * (0.99 - PerAgentCapFraction)
	if math.Abs((budget-total)-wantBurned) > 1e-6 {
		t.Errorf("burned %v, want %v", budget-total, wantBurned)
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
	// The budget steps every DecayPeriod epochs, so 365 epochs is that many steps.
	want := math.Pow(Decay, float64(365/DecayPeriod))
	if ratio := late["a"] / early["a"]; math.Abs(ratio-want) > 1e-9 {
		t.Errorf("late/early = %v, want decay^(365/%d) = %v", ratio, DecayPeriod, want)
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
