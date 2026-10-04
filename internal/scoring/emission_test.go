package scoring

import (
	"testing"
	"time"
)

func TestEpochBudget_Decays(t *testing.T) {
	if got := EpochBudget(0); got != BaseBudget {
		t.Errorf("EpochBudget(0) = %v, want %v", got, BaseBudget)
	}

	// B(n) = B0 * decay^n
	for _, n := range []uint64{1, 10, 100} {
		want := BaseBudget
		for i := uint64(0); i < n; i++ {
			want *= Decay
		}
		if got := EpochBudget(n); !almostEqual(got, want, 1e-6) {
			t.Errorf("EpochBudget(%d) = %v, want %v", n, got, want)
		}
	}

	// Strictly decreasing, which is what makes early participation worth more.
	prev := EpochBudget(0)
	for n := uint64(1); n < 50; n++ {
		cur := EpochBudget(n)
		if cur >= prev {
			t.Fatalf("budget did not decrease at epoch %d: %v >= %v", n, cur, prev)
		}
		prev = cur
	}
}

func TestPerAgentCap(t *testing.T) {
	for _, epoch := range []uint64{0, 1, 25} {
		want := EpochBudget(epoch) * PerAgentCapFraction
		if got := PerAgentCap(epoch); !almostEqual(got, want, 1e-9) {
			t.Errorf("PerAgentCap(%d) = %v, want %v", epoch, got, want)
		}
	}
	if PerAgentCapFraction != 0.05 {
		t.Errorf("PerAgentCapFraction = %v, want 0.05 (MVP.md §6.3)", PerAgentCapFraction)
	}
}

func TestEpochOf_IsPureAndAligned(t *testing.T) {
	// Pick an epoch by its bounds rather than hard-coding an index, so this test
	// does not need updating if the genesis moves.
	const n = 497504
	start, end := EpochBounds(n)

	if got := EpochOf(start); got != n {
		t.Errorf("EpochOf(start) = %d, want %d", got, n)
	}
	// The interval is half-open: the instant before end is still the old epoch.
	if got := EpochOf(end.Add(-time.Second)); got != n {
		t.Errorf("EpochOf(end-1s) = %d, want %d", got, n)
	}
	if got := EpochOf(end); got != n+1 {
		t.Errorf("EpochOf(end) = %d, want %d", got, n+1)
	}

	// Same input, same answer: any verifier can recompute this.
	for i := 0; i < 10; i++ {
		if got := EpochOf(start); got != n {
			t.Fatalf("EpochOf is not deterministic: %d", got)
		}
	}
}

// TestEpochOf_IsAnchoredAtGenesis is the regression guard for the bug documented
// in docs/notes/epoch-anchoring.md.
//
// An unanchored clock puts the live epoch near 20,729, where B0 * decay^n is
// ~3.3e-85 and no agent can be credited more than once. The genesis anchor is
// what prevents that, so this asserts the live parameters are actually usable.
func TestEpochOf_IsAnchoredAtGenesis(t *testing.T) {
	now := time.Now()
	n := EpochOf(now)

	// Two and a half years of daily epochs is ~900. Anything remotely close to
	// the raw unix epoch count means the anchor has been lost.
	if n > 10_000 {
		t.Fatalf("live epoch is %d, which indicates the genesis anchor is missing", n)
	}

	// The live budget must be usable: an agent must be able to earn more than one
	// award before hitting its cap.
	if cap := PerAgentCap(n); cap <= BasePoints {
		t.Fatalf("live per-agent cap is %v (epoch %d); an agent could not earn even one award",
			cap, n)
	}

	// And the head-start curve must still be visible at the current epoch.
	if !(EpochBudget(0) > EpochBudget(n)) {
		t.Error("epoch 0 must pay more than the current epoch")
	}
}

func TestEpochBounds_LengthIsOneDay(t *testing.T) {
	start, end := EpochBounds(1000)

	if d := end.Sub(start); d != EpochLength {
		t.Errorf("epoch length = %v, want %v", d, EpochLength)
	}
	if EpochLength != 24*time.Hour {
		t.Errorf("EpochLength = %v, want 24h (MVP.md §6.2)", EpochLength)
	}
}

func TestBudgetFactor(t *testing.T) {
	const epoch = 0
	cap := PerAgentCap(epoch)

	cases := []struct {
		earned float64
		want   float64
	}{
		{0, 1.0},
		{cap / 2, 0.5},
		{cap, 0.0},
		{cap * 2, 0.0}, // overshoot still floors at 0
		{-10, 1.0},     // negative earned treats as no earnings
	}

	for _, c := range cases {
		got := BudgetFactor(epoch, c.earned)
		if !almostEqual(got, c.want, 1e-9) {
			t.Errorf("BudgetFactor(earned=%v) = %v, want %v", c.earned, got, c.want)
		}
		if got < 0 || got > 1 {
			t.Errorf("BudgetFactor(%v) = %v is outside [0,1]", c.earned, got)
		}
	}
}

func TestAllocate_ProportionalAndCapped(t *testing.T) {
	const epoch = 0

	t.Run("proportional", func(t *testing.T) {
		got, err := Allocate(epoch, map[string]float64{
			"a": 1,
			"b": 3,
		})
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		wantA := EpochBudget(epoch) * 0.25
		wantB := EpochBudget(epoch) * 0.75

		if !almostEqual(got["a"], wantA, 1e-6) {
			t.Errorf("a = %v, want %v", got["a"], wantA)
		}
		if !almostEqual(got["b"], wantB, 1e-6) {
			t.Errorf("b = %v, want %v", got["b"], wantB)
		}
	})

	t.Run("single agent receives its full proportional share", func(t *testing.T) {
		// Allocate describes shares of work, not emitted amounts, so a lone
		// contributor receives the entire pool. Capping is a separate step.
		got, err := Allocate(epoch, map[string]float64{"solo": 1_000_000})
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		if !almostEqual(got["solo"], EpochBudget(epoch), 1e-6) {
			t.Errorf("solo = %v, want the whole budget %v", got["solo"], EpochBudget(epoch))
		}
	})

	t.Run("cap is applied by CapAllocation, not Allocate", func(t *testing.T) {
		allocation, err := Allocate(epoch, map[string]float64{"whale": 1_000_000})
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		capped := CapAllocation(epoch, allocation)

		if !almostEqual(capped["whale"], PerAgentCap(epoch), 1e-6) {
			t.Errorf("capped whale = %v, want the cap %v", capped["whale"], PerAgentCap(epoch))
		}
		// Surplus is simply not emitted.
		if TotalAllocated(capped) > EpochBudget(epoch) {
			t.Error("capped allocation must never exceed the epoch budget")
		}
		if TotalAllocated(capped) >= TotalAllocated(allocation) {
			t.Error("capping should reduce the emitted total, not raise it")
		}
	})
}

// TestAllocate_SumNeverExceedsBudget: an epoch can under-emit, never over-emit.
//
// Allocate itself is proportional, so the budget bound is checked after the
// settlement-layer cap is applied — that is the pair of steps a real epoch
// settlement performs.
func TestAllocate_SumNeverExceedsBudget(t *testing.T) {
	const epoch = 7

	for _, n := range []int{1, 2, 5, 50, 500} {
		work := make(map[string]float64, n)
		for i := 0; i < n; i++ {
			work[itoa(i)] = float64(i + 1)
		}

		allocation, err := Allocate(epoch, work)
		if err != nil {
			t.Fatalf("Allocate(n=%d): %v", n, err)
		}

		// Proportional shares can be large when few agents compete; the cap is
		// what bounds emission.
		capped := CapAllocation(epoch, allocation)

		// Comparing summed floats to an analytic budget needs a tolerance:
		// summing N shares can exceed the budget by a few ULPs without any
		// real over-emission. Tolerance is relative to the budget, not absolute,
		// so it stays meaningful at every epoch's scale.
		limit := EpochBudget(epoch) * (1 + 1e-9)
		if total := TotalAllocated(capped); total > limit {
			t.Errorf("n=%d: capped total %.12f exceeds budget %.12f", n, total, EpochBudget(epoch))
		}
		for agent, v := range capped {
			if v > PerAgentCap(epoch) {
				t.Errorf("n=%d: agent %s got %v, above cap %v", n, agent, v, PerAgentCap(epoch))
			}
		}

		// Capping must never increase what anyone receives.
		for agent, v := range capped {
			if v > allocation[agent] {
				t.Errorf("n=%d: capping raised agent %s from %v to %v", n, agent, allocation[agent], v)
			}
		}
	}
}

func TestAllocate_HandlesZeroAndEmpty(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := Allocate(0, map[string]float64{})
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d entries, want 0", len(got))
		}
	})

	t.Run("all zero", func(t *testing.T) {
		got, err := Allocate(0, map[string]float64{"a": 0, "b": 0})
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		// Must not divide by zero, and must not invent emission.
		if TotalAllocated(got) != 0 {
			t.Errorf("total = %v, want 0", TotalAllocated(got))
		}
		if len(got) != 2 {
			t.Errorf("got %d entries, want 2 (each agent present, zero-valued)", len(got))
		}
	})
}

func TestAllocate_RejectsNegativeWork(t *testing.T) {
	if _, err := Allocate(0, map[string]float64{"a": -1}); err == nil {
		t.Error("negative work must be rejected rather than clamped silently")
	}
}

// TestEmission_HeadStartNarrative: the same relative work pays less in a later
// epoch. This is the mechanical basis of "early participation is worth more".
func TestEmission_HeadStartNarrative(t *testing.T) {
	work := map[string]float64{"a": 1, "b": 1}

	early, err := Allocate(0, work)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	late, err := Allocate(365, work)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	if !(early["a"] > late["a"]) {
		t.Errorf("epoch 0 paid %v and epoch 365 paid %v; earlier must pay more",
			early["a"], late["a"])
	}
}
