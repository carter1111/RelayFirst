package epoch

import (
	"testing"
	"time"
)

// TestGenesis_IsTheEpochZeroInstant pins the anchor. The value is provisional and
// may change before mainnet, but it must remain a *fixed constant*: a receipt's
// epoch is inside its signed payload, so two honest verifiers using different
// origins would compute different epochs and reject each other's signatures.
func TestGenesis_IsTheEpochZeroInstant(t *testing.T) {
	if got := Genesis().Unix(); got != GenesisValue {
		t.Errorf("Genesis().Unix() = %d, want %d", got, GenesisValue)
	}
	if got := Of(Genesis(), time.Hour); got != 0 {
		t.Errorf("genesis must be epoch 0, got %d", got)
	}
}

// TestReleaseGuard_RefusesAProvisionalMainnetBuild is the non-vacuity proof for the
// mainnet genesis guard (BLK-4).
//
// The guard's whole value is that a release cannot silently ship the development
// placeholder: the genesis is inside a receipt's signed payload, so receipts minted
// under the wrong origin cannot be re-signed afterwards. This test drives the same
// function the guard runs, so the reasoning is checkable without a tagged binary.
func TestReleaseGuard_RefusesAProvisionalMainnetBuild(t *testing.T) {
	// Provisional genesis must be refused.
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("a provisional genesis must refuse a mainnet build; the guard is vacuous")
			}
		}()
		assertReleaseGenesis(placeholderGenesis)
	}()

	// A pinned genesis (the real launch date) must NOT be refused, or the guard
	// would make every legitimate release impossible.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("a pinned genesis must be accepted, got panic: %v", r)
			}
		}()
		assertReleaseGenesis(placeholderGenesis + 86400)
	}()

	// And the current value is still provisional, which is what makes the guard
	// meaningful today rather than a no-op.
	if !IsProvisional() {
		t.Fatal("this test assumes the genesis is still provisional; if the launch " +
			"date has been pinned, update this test to assert the opposite")
	}
}

func TestOf_CountsFromGenesis(t *testing.T) {
	const hour = time.Hour

	cases := []struct {
		name   string
		offset time.Duration
		want   uint64
	}{
		{"genesis", 0, 0},
		{"just before the next epoch", 3599 * time.Second, 0},
		{"exactly one epoch", hour, 1},
		{"two epochs", 2 * hour, 2},
		{"just after two epochs", 2*hour + time.Second, 2},
		{"one day", 24 * hour, 24},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Of(Genesis().Add(c.offset), hour)
			if got != c.want {
				t.Errorf("Of(genesis%+v) = %d, want %d", c.offset, got, c.want)
			}
		})
	}
}

// TestOf_ClampsPreGenesisToZero is the regression guard for the underflow.
//
// A pre-genesis timestamp yields a negative delta; converting that to uint64
// directly would wrap to an astronomically large index instead of 0. That is not
// a hypothetical: an enormous epoch index drives the emission curve
// B0 * decay^n to exactly zero, which is the same failure the genesis anchor
// exists to prevent, arrived at by a different route.
func TestOf_ClampsPreGenesisToZero(t *testing.T) {
	const hour = time.Hour

	preGenesis := []time.Duration{
		-time.Second,
		-time.Hour,
		-24 * time.Hour,
		-365 * 24 * time.Hour,
		-10 * 365 * 24 * time.Hour,
	}

	for _, d := range preGenesis {
		got := Of(Genesis().Add(d), hour)
		if got != 0 {
			t.Errorf("Of(genesis%+v) = %d, want 0 — a pre-genesis time must clamp, not underflow", d, got)
		}
	}
}

func TestOf_HandlesNonPositiveLength(t *testing.T) {
	for _, length := range []time.Duration{0, -time.Hour, -time.Second} {
		if got := Of(Genesis().Add(100*time.Hour), length); got != 0 {
			t.Errorf("Of(length=%v) = %d, want 0", length, got)
		}
	}
}

// TestOf_IsPure: the same instant must always land in the same epoch, because a
// verifier recomputes this without trusting any server.
func TestOf_IsPure(t *testing.T) {
	t0 := Genesis().Add(500 * time.Hour)
	first := Of(t0, time.Hour)

	for i := 0; i < 100; i++ {
		if got := Of(t0, time.Hour); got != first {
			t.Fatalf("Of is not deterministic: %d != %d", got, first)
		}
	}
}

func TestBounds_IntervalIsHalfOpen(t *testing.T) {
	const n = 42
	start, end := Bounds(n, time.Hour)

	if d := end.Sub(start); d != time.Hour {
		t.Errorf("interval = %v, want 1h", d)
	}
	if got := Of(start, time.Hour); got != n {
		t.Errorf("Of(start) = %d, want %d", got, n)
	}
	// The instant before the end is still in epoch n.
	if got := Of(end.Add(-time.Second), time.Hour); got != n {
		t.Errorf("Of(end-1s) = %d, want %d", got, n)
	}
	// The end itself belongs to the next epoch.
	if got := Of(end, time.Hour); got != n+1 {
		t.Errorf("Of(end) = %d, want %d", got, n+1)
	}
}

func TestBounds_ZeroEpochStartsAtGenesis(t *testing.T) {
	start, _ := Bounds(0, time.Hour)
	if !start.Equal(Genesis()) {
		t.Errorf("Bounds(0).start = %v, want the genesis %v", start, Genesis())
	}
}

func TestBounds_HandlesNonPositiveLength(t *testing.T) {
	// Must not panic or produce a non-genesis interval.
	start, end := Bounds(10, 0)
	if !start.Equal(Genesis()) || !end.Equal(Genesis()) {
		t.Errorf("Bounds with zero length = (%v, %v), want the genesis for both", start, end)
	}
}

// TestBounds_RoundTripsAcrossManyEpochs: a receipt's epoch and its interval must
// agree, or an operator reading a window would look at the wrong one.
func TestBounds_RoundTripsAcrossManyEpochs(t *testing.T) {
	const hour = time.Hour

	for _, n := range []uint64{0, 1, 23, 24, 365, 1000, 100_000, 1_000_000} {
		start, _ := Bounds(n, hour)
		if got := Of(start, hour); got != n {
			t.Errorf("Of(Bounds(%d).start) = %d, want %d", n, got, n)
		}
	}
}
