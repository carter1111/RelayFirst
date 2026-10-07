package scoring

import (
	"fmt"
	"math"
	"time"

	"github.com/relayfirst/relayfirst/internal/epoch"
	"github.com/relayfirst/relayfirst/internal/tenure"
)

// Emission constants from MVP.md §6.2 and §6.3.
const (
	// EpochLength groups work into settleable windows.
	//
	// MVP.md §6.2 (2026-10-08): one week. The earlier 24-hour value was a
	// placeholder.
	EpochLength = 7 * 24 * time.Hour

	// BaseBudget is B0: the points available across all agents in epoch 0.
	BaseBudget = 7_000_000.0

	// Decay is the budget step applied every DecayPeriod epochs:
	// B(n) = B0 * Decay^floor(n / DecayPeriod).
	//
	// MVP.md §6.2 (2026-10-08): 0.85, i.e. -15% roughly every four weeks. This
	// replaced a per-epoch 0.99 decay; the shape is now a stepwise decline, so
	// EpochBudget steps down every fourth epoch rather than every epoch.
	Decay = 0.85

	// DecayPeriod is how many epochs share one decay step. With a 7-day epoch
	// this is about a month.
	DecayPeriod = 4

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
// gives the "head start" narrative its teeth. The decline is a step, not a
// smooth curve: the budget holds for DecayPeriod epochs and then drops, so it
// only changes about monthly. The budget is a fixed pool rather than a
// per-receipt rate: agents share it in proportion to their work.
func EpochBudget(epoch uint64) float64 {
	return BaseBudget * math.Pow(Decay, float64(epoch/DecayPeriod))
}

// PerAgentCap returns the most one agent may earn in an epoch.
func PerAgentCap(epoch uint64) float64 {
	return EpochBudget(epoch) * PerAgentCapFraction
}

// NodePoolFraction is the share of an epoch's budget that goes to the NODE pool
// (Layer 0), by phase (MVP.md §6.2c, incentive.md §2). The rest is the work pool
// (Layer 1). Phases switch on epoch height, pre-locked, with no vote.
//
// # Phase 3 is a SUNSET, not a smaller subsidy
//
// At Phase 3 the node pool goes to ZERO, not 10%. Layer 0 is a coldstart subsidy for
// bringing a node network up, and it is meant to end: after ~12 months (epoch 52) nodes
// are paid nothing here, and the intent is to replace the subsidy with token mining. An
// earlier draft kept paying 10% indefinitely, which contradicted incentive.md §3's
// sunset; the sustained-10% version was the wrong one.
func NodePoolFraction(epoch uint64) float64 {
	switch {
	case epoch <= 25:
		return 0.50 // Phase 1
	case epoch <= 51:
		return 0.25 // Phase 2
	default:
		return 0.00 // Phase 3: sunset -- the node pool stops
	}
}

// NodePoolShare returns each agent-node's fraction of the Layer 0 pool this epoch.
//
// # It is not a points function; it is a split of a pool defined elsewhere
//
// The pool's SIZE is handled by the caller (the settle step); this only answers "how is
// the node pool divided". It defers to tenure.PoolShare, which weights by tier and gives
// an ineligible node nothing -- so a node below the tenure floor dilutes nobody.
//
// # Why it lives here and not in tenure
//
// tenure owns the tier rule; emission owns the pool. Keeping the split's SHAPE in one
// place and its WEIGHTS in another means the tier table can change without touching
// settlement, and the pool ratio can change without touching tenure.
func NodePoolShare(nodeTenures map[string]int) map[string]float64 {
	return tenure.PoolShare(nodeTenures)
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

// Settle turns an epoch's accumulated WORK into the POINTS actually emitted.
//
// # This is the settlement step model B requires (D1, 2026-10-07)
//
// MVP.md §5.3 defines `work` (a measure of useful output). MVP.md §6.2 defines the
// single emission model: a fixed per-epoch budget shared by work. Settling is the
// step between them, and it is deliberately ONE function so a caller cannot apply
// the share without the cap, or the cap without the share:
//
//	points_i = CapAllocation( Allocate(work) )
//
// # Why it is a pure function of (epoch, work)
//
// The result must be reproducible by anyone from the same inputs, because it feeds a
// Merkle root (S7) that a client later verifies offline. Reading a clock or a store
// here would make the number depend on who computed it, which is the fork the whole
// protocol avoids. So it takes the work totals and nothing else.
//
// # What it does not do
//
// It does not read receipts or a ledger; the caller supplies the per-agent work
// totals it accumulated. Keeping it a pure function is what lets a settlement be
// recomputed and checked rather than trusted.
func Settle(epoch uint64, work map[string]float64) (map[string]float64, error) {
	allocation, err := Allocate(epoch, work)
	if err != nil {
		return nil, err
	}
	return CapAllocation(epoch, allocation), nil
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
