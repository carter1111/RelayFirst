package tenure

import (
	"math"
	"testing"
)

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestAdvance_QualifyingIncrements(t *testing.T) {
	var s State
	for i := 1; i <= 5; i++ {
		s = Advance(s, true)
		if s.Tenure != i {
			t.Fatalf("after %d qualifying epochs tenure = %d, want %d", i, s.Tenure, i)
		}
		if s.MissStreak != 0 {
			t.Fatalf("a qualifying epoch must clear the miss streak, got %d", s.MissStreak)
		}
	}
}

func TestAdvance_SingleMissStepsDownNotZero(t *testing.T) {
	s := State{Tenure: 5}
	s = Advance(s, false)
	if s.Tenure != 4 {
		t.Errorf("one miss from tenure 5 = %d, want 4 (step down, not reset)", s.Tenure)
	}
	if s.MissStreak != 1 {
		t.Errorf("miss streak = %d, want 1", s.MissStreak)
	}

	// And a qualifying epoch after that single miss still clears the streak and
	// resumes the climb from the stepped-down value.
	s = Advance(s, true)
	if s.Tenure != 5 || s.MissStreak != 0 {
		t.Errorf("after recovering: tenure %d streak %d, want 5 and 0", s.Tenure, s.MissStreak)
	}
}

func TestAdvance_TwoMissesReset(t *testing.T) {
	s := State{Tenure: 10}
	s = Advance(s, false) // 9, streak 1
	s = Advance(s, false) // reset
	if s.Tenure != 0 {
		t.Errorf("two consecutive misses from tenure 10 = %d, want a reset to 0", s.Tenure)
	}
	if s.MissStreak < 2 {
		t.Errorf("miss streak = %d, want >= 2", s.MissStreak)
	}

	// A third miss must still be a reset, not a fresh single miss that would look
	// like a step down from zero.
	s = Advance(s, false)
	if s.Tenure != 0 {
		t.Errorf("a third consecutive miss = %d, want 0", s.Tenure)
	}
}

func TestAdvance_DoesNotGoNegative(t *testing.T) {
	s := Advance(State{}, false) // from zero, a single miss
	if s.Tenure < 0 {
		t.Errorf("tenure = %d, want 0 (never negative)", s.Tenure)
	}
}

func TestTenure_FoldsSequence(t *testing.T) {
	// Three qualifying, a miss, two qualifying: 1,2,3, then 2, then 3,4.
	got := Tenure([]bool{true, true, true, false, true, true})
	if got.Tenure != 4 {
		t.Errorf("tenure = %d, want 4", got.Tenure)
	}

	// Two consecutive misses zero it and the climb restarts.
	got = Tenure([]bool{true, true, true, true, false, false, true})
	if got.Tenure != 1 {
		t.Errorf("tenure = %d, want 1 after a reset and one recovery", got.Tenure)
	}
}

func TestTier_Boundaries(t *testing.T) {
	cases := []struct {
		tenure int
		want   float64
	}{
		{0, 0}, {2, 0}, // below the eligibility floor
		{3, 1.0}, {5, 1.0},
		{6, 1.1}, {11, 1.1},
		{12, 1.25}, {100, 1.25},
	}
	for _, c := range cases {
		if got := Tier(c.tenure); !almostEqual(got, c.want) {
			t.Errorf("Tier(%d) = %v, want %v", c.tenure, got, c.want)
		}
	}
}

func TestPoolShare_IneligibleGetsNothingAndDoesNotDilute(t *testing.T) {
	shares := PoolShare(map[string]int{
		"short":  1,  // ineligible
		"bronze": 3,  // 1.0
		"silver": 7,  // 1.1
		"gold":   20, // 1.25
	})

	if _, ok := shares["short"]; ok {
		t.Error("an ineligible node must receive no share, and must not dilute the pool")
	}

	// The pool is split over the three eligible weights only.
	total := 1.0 + 1.1 + 1.25
	if !almostEqual(shares["bronze"], 1.0/total) {
		t.Errorf("bronze share = %v, want %v", shares["bronze"], 1.0/total)
	}
	if !almostEqual(shares["gold"], 1.25/total) {
		t.Errorf("gold share = %v, want %v", shares["gold"], 1.25/total)
	}

	var sum float64
	for _, s := range shares {
		sum += s
	}
	if !almostEqual(sum, 1) {
		t.Errorf("shares sum to %v, want 1", sum)
	}
}

func TestPoolShare_NoEligibleNodesPaysNothing(t *testing.T) {
	shares := PoolShare(map[string]int{"a": 1, "b": 2})
	if len(shares) != 0 {
		t.Errorf("no eligible nodes must split nothing, got %v", shares)
	}
}
