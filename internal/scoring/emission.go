package scoring

import (
	"fmt"
	"math"
	"time"

	"github.com/relayfirst/relayfirst/internal/epoch"
)

// Emission constants from MVP.md §6.2 and §6.3.
const (
	// EpochLength groups work into settleable windows.
	EpochLength = 24 * time.Hour

	// BaseBudget is B0: the points available across all agents in epoch 0.
	BaseBudget = 1_000_000.0

	// Decay is the per-epoch budget multiplier. B(n) = B0 * Decay^n.
	Decay = 0.99

	// PerAgentCapFraction caps one agent's share of an epoch's budget.
	// MVP.md §6.3: a single agent may earn at most B(n) * 5%.
	//
	// The cap exists to keep small participants viable. The narrative depends on
	// newcomers being able to earn something, and a whale taking the whole epoch
	// would end that immediately.
	PerAgentCapFraction = 0.05
)

// EpochBudget returns B(n) for an epoch index.
//
// Later epochs pay less, which is what makes early participation worth more and
// gives the "head start" narrative its teeth. The budget is a fixed pool rather
// than a per-receipt rate: agents share it in proportion to their work.
func EpochBudget(epoch uint64) float64 {
	return BaseBudget * math.Pow(Decay, float64(epoch))
}

// PerAgentCap returns the most one agent may earn in an epoch.
func PerAgentCap(epoch uint64) float64 {
	return EpochBudget(epoch) * PerAgentCapFraction
}

// EpochOf returns the epoch index for a timestamp.
//
// It is a pure function of the timestamp, so any verifier can recompute which
// epoch a receipt belongs to without trusting the relayer.
//
// The origin is epoch.GenesisValue, not the unix zero. Anchoring matters here:
// without it the index is ~20,729 and B(n) = B0 * decay^n collapses to 3.3e-85,
// which makes BudgetFactor zero for every agent — see internal/epoch and
// docs/notes/epoch-anchoring.md.
func EpochOf(t time.Time) uint64 {
	return epoch.Of(t, EpochLength)
}

// EpochBounds returns the half-open interval [start, end) of an epoch.
func EpochBounds(epochIndex uint64) (start, end time.Time) {
	return epoch.Bounds(epochIndex, EpochLength)
}

// BudgetFactor reports the remaining headroom for an agent that has already
// earned a certain amount this epoch.
//
// It returns a value in [0,1] suitable for Params.BudgetFactor. The shape is
// deliberately a hard ceiling rather than a smooth taper: once an agent reaches
// its cap, further work earns nothing, which is easy to reason about and easy to
// test.
func BudgetFactor(epoch uint64, alreadyEarned float64) float64 {
	cap := PerAgentCap(epoch)
	if cap <= 0 {
		return 0
	}
	if alreadyEarned <= 0 {
		return 1
	}
	if alreadyEarned >= cap {
		return 0
	}
	return 1 - (alreadyEarned / cap)
}

// Allocate distributes an epoch's budget in proportion to work.
//
// Per MVP.md §6.2: points_i = B(n) * (work_i / totalWork).
//
// # Why the per-agent cap is NOT applied here
//
// MVP.md §6.3 caps a single agent at B(n) * 5%. Applying that cap inside this
// function conflicts with proportionality: with two equally matched agents each
// "should" receive 50% of the pool, which is far above a 5% ceiling, so clamping
// flattens the distribution and destroys the very signal the allocation exists to
// carry. Worse, clamping one large contributor silently hands its surplus to
// whoever remains, paying them for work they did not do.
//
// The two rules therefore live at different layers, matching where the doc puts
// them:
//
//   - Every receipt is attenuated prospectively by Params.BudgetFactor before it
//     is credited (see score.go), which is where a per-epoch ceiling actually
//     bites.
//   - This function answers a narrower question: "given these work totals, what
//     share of the pool does each agent's work represent?"
//
// Apply CapAllocation to the result when a hard settlement-layer ceiling is
// wanted; that is an explicit, separate decision rather than a hidden side effect
// of division.
//
// work must be non-negative. Negative values are treated as an error rather
// than silently clamped, because they would indicate a bug upstream.
func Allocate(epoch uint64, work map[string]float64) (map[string]float64, error) {
	total := 0.0
	for agent, w := range work {
		if w < 0 {
			return nil, fmt.Errorf("scoring: agent %q has negative work %v", agent, w)
		}
		total += w
	}

	out := make(map[string]float64, len(work))
	if total <= 0 {
		// Nobody did measurable work; the epoch allocates nothing rather than
		// dividing by zero.
		for agent := range work {
			out[agent] = 0
		}
		return out, nil
	}

	budget := EpochBudget(epoch)
	for agent, w := range work {
		if w == 0 {
			out[agent] = 0
			continue
		}
		out[agent] = budget * (w / total)
	}
	return out, nil
}

// CapAllocation applies the per-agent ceiling of B(n) * 5% (MVP.md §6.3).
//
// This is a separate, explicit step because it changes the meaning of the
// numbers: an allocation describes shares of work, whereas a capped allocation
// describes what will actually be emitted. Surplus above the cap is simply not
// emitted, so the sum can fall below the epoch budget. That direction is safe —
// an under-emitted epoch is a smaller supply increase, never a larger one.
func CapAllocation(epoch uint64, allocation map[string]float64) map[string]float64 {
	cap := PerAgentCap(epoch)
	out := make(map[string]float64, len(allocation))

	for agent, v := range allocation {
		if v < 0 {
			v = 0
		}
		if v > cap {
			v = cap
		}
		out[agent] = v
	}
	return out
}

// TotalAllocated sums an allocation, for auditing.
//
// Note that the sum can be less than the epoch budget: the per-agent cap means
// surplus is simply not emitted. That is intentional — an under-emitted epoch is
// a smaller supply increase, never a larger one.
func TotalAllocated(allocation map[string]float64) float64 {
	var total float64
	for _, v := range allocation {
		total += v
	}
	return total
}
