// Package tenure tracks a node's consecutive qualifying epochs and maps that to a
// reward weight (incentive.md §3, Stage 3 decision PRE-1(a)).
//
// # What this package deliberately does NOT do
//
// It does not decide whether an epoch qualified. It takes that as a plain bool. The
// measurement that produces the bool -- slot heartbeats, a 95% threshold, a liveness
// challenge -- is the liveness protocol, which is not designed yet and is P1. Keeping
// the input a bool is what lets this state machine be written, tested and relied on
// now, without pretending a protocol exists.
//
// # Why it is its own package (no crypto, no scoring)
//
// Node tenure is reward-adjacent but carries no keys and no scoring. A leaf like this
// can be imported wherever it is needed without dragging crypto into a binary the CI
// import-graph gates keep clean.
package tenure

// MinTenure is the eligibility floor: a node needs this many consecutive qualifying
// epochs before it earns any Layer 0 weight at all (incentive.md §3: "tenure >= 3
// epochs").
const MinTenure = 3

// Tier boundaries (incentive.md §3, the 2026-10-07 compression to a 25% spread).
const (
	// silverFrom is the tenure at which the weight steps from 1.0x to 1.1x.
	silverFrom = 6
	// goldFrom is the tenure at which it steps from 1.1x to 1.25x.
	goldFrom = 12

	weightEligible = 1.0
	weightSilver   = 1.1
	weightGold     = 1.25
)

// State is a node's tenure position after some epoch.
//
// It carries MissStreak because "one miss steps down, two in a row resets" cannot be
// decided from the tenure count alone: after a single miss the next miss means
// something different than it would after a qualifying epoch.
type State struct {
	// Tenure is the count of consecutive qualifying epochs.
	Tenure int

	// MissStreak is how many epochs in a row have missed. It resets to 0 on any
	// qualifying epoch, and reaching 2 zeroes Tenure.
	MissStreak int
}

// Advance returns the state after one more epoch.
//
// The rule (incentive.md §3, 2026-10-07):
//
//   - a qualifying epoch increments tenure and clears the miss streak;
//   - a single miss steps tenure down by one, without zeroing it, so an operator's
//     one-off outage is not treated as abandonment;
//   - two misses in a row reset tenure to zero.
//
// # One reading this makes explicit
//
// "单 epoch 不达标 → tenure 降一档" is read here as "tenure decreases by one epoch",
// not "the weight drops a whole tier". The steps are the same on the way up (one
// qualifying epoch is +1), so the symmetric reading is +-1. If the intent was a tier
// step instead, this function is where it changes.
func Advance(prev State, qualified bool) State {
	if qualified {
		return State{Tenure: prev.Tenure + 1, MissStreak: 0}
	}
	streak := prev.MissStreak + 1
	if streak >= 2 {
		// Two in a row: reset. The streak is kept so a third miss stays a reset
		// rather than looking like a fresh single miss.
		return State{Tenure: 0, MissStreak: streak}
	}
	tenure := prev.Tenure - 1
	if tenure < 0 {
		tenure = 0
	}
	return State{Tenure: tenure, MissStreak: streak}
}

// Tenure folds a chronological sequence of qualifying-epoch flags into a State.
//
// The sequence must be ordered oldest first, with no gaps: each entry is one epoch.
func Tenure(qualified []bool) State {
	var s State
	for _, q := range qualified {
		s = Advance(s, q)
	}
	return s
}

// Tier returns the Layer 0 reward weight for a tenure count.
//
// A node below MinTenure is not eligible and weighs zero: it earns no Layer 0 share.
// That is the natural reading of "资格: tenure >= 3 epochs", and it is deliberately
// not a small positive weight -- an ineligible node must not dilute the pool.
func Tier(tenureEpochs int) float64 {
	switch {
	case tenureEpochs < MinTenure:
		return 0
	case tenureEpochs < silverFrom:
		return weightEligible
	case tenureEpochs < goldFrom:
		return weightSilver
	default:
		return weightGold
	}
}

// PoolShare returns each node's fraction of a Layer 0 pool from its tenure count,
// with ineligible nodes getting nothing.
//
// The pool is split by tenure-tier weight, so the shares sum to 1 across eligible
// nodes. When no node is eligible the result is empty rather than a division by zero:
// that epoch pays out nothing.
func PoolShare(tenures map[string]int) map[string]float64 {
	weights := make(map[string]float64, len(tenures))
	var total float64
	for node, t := range tenures {
		w := Tier(t)
		if w <= 0 {
			continue
		}
		weights[node] = w
		total += w
	}

	out := make(map[string]float64, len(weights))
	if total <= 0 {
		return out
	}
	for node, w := range weights {
		out[node] = w / total
	}
	return out
}
