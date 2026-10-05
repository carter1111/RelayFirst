// Package epoch defines when the RelayFirst epoch clock starts.
//
// # Why this is its own package
//
// The epoch index is part of a receipt's signed payload (receipt.NewEpoch) and
// also drives emission (scoring.EpochOf). Both must agree exactly, but scoring
// imports receipt, so the formula cannot live in either without creating a
// cycle. It lives here instead, as a leaf both can depend on.
//
// # The bug this package exists to fix
//
// The formula was originally `t.Unix() / epochLength`, with no origin. That has
// no genesis: it makes the current epoch ~20,729, and because the epoch budget
// is B(n) = B0 * decay^n, the live budget computes to 3.3e-85 points. Through
// BudgetFactor = 1 - earned/cap, with a cap of ~1.7e-86, an agent was credited
// exactly once and then blocked forever — the points economy could not move.
// See docs/notes/epoch-anchoring.md.
//
// # Why the genesis is a constant rather than a parameter
//
// The epoch index is a pure function of a timestamp. Every verifier must derive
// the same value from the same receipt without trusting a server, so the origin
// has to be a fixed, agreed constant — not configuration that could differ
// between two honest nodes. A receipt signed under one genesis and verified
// under another would silently change epochs, and the epoch is inside the signed
// payload, so the signature would fail rather than degrade quietly. That is the
// safe direction, but it means the value below cannot be changed casually.
package epoch

import "time"

// GenesisValue is the unix timestamp of epoch 0.
//
// # PROVISIONAL — pin this before mainnet
//
// This is a placeholder chosen to be in the recent past, so that pre-launch
// development exercises a normal, non-degenerate budget (epoch ~2, B ~980,000)
// rather than the astronomically decayed one an unanchored clock produces.
//
// Before mainnet, set it to the actual launch date. The consequences of getting
// it wrong are asymmetric:
//
//   - Set too EARLY: launch begins at a high epoch, so B(n) is already decayed
//     and early participants are underpaid relative to the intended curve. The
//     "head start" narrative is weakened but nothing breaks.
//   - Set too LATE: launch begins at epoch 0 with the full B0 budget, which is
//     the intended behaviour. Being late is therefore the safe direction.
//
// Because a receipt's epoch is inside its signed payload, changing this value
// invalidates every receipt signed under the old one. Do it before opening the
// protocol to real mining, not after.
//
// # The decision (2026-10-05): genesis == the launch date
//
// The value below is DECIDED to be the public launch date's 00:00:00Z (BLK-4,
// option A; see docs/notes/blk-4-genesis-decision.md). Launching at epoch 0 with
// the full B0 budget is the intended behaviour, and it is the late/safe half of
// the asymmetry above.
//
// It is NOT YET SET, because the launch date is a human decision that has not
// been made. Until it is, this stays the development placeholder and
// IsProvisional reports true. ReleaseGuardError refuses a `-tags mainnet` build
// while that is the case, so a shipped binary cannot carry the placeholder by
// accident.
//
// 1790841600 == 2026-10-01T00:00:00Z.
const GenesisValue int64 = 1790841600

// placeholderGenesis is the development value GenesisValue carries until the
// launch date is pinned. It is a separate name so the guard can compare against
// it without repeating the literal, and so "is this still provisional?" is one
// question in one place.
const placeholderGenesis int64 = 1790841600

// IsProvisional reports whether GenesisValue is still the development placeholder.
//
// It is true in development and must be false in a release. It exists so callers
// (the -tags mainnet guard, and tests) ask the question rather than comparing the
// literal themselves, which is how a second spelling of the placeholder appears.
func IsProvisional() bool { return GenesisValue == placeholderGenesis }

// Genesis returns epoch 0's start as a UTC time.
func Genesis() time.Time { return time.Unix(GenesisValue, 0).UTC() }

// Of returns the epoch index containing t, for a given epoch length.
//
// A timestamp before the genesis belongs to epoch 0 rather than to a negative,
// underflowed index. Clamping is deliberate: `uint64` of a negative division
// would wrap to an enormous number, which would then drive emission to exactly
// zero for every pre-launch receipt — the same failure this package exists to
// prevent, arrived at by a different route.
func Of(t time.Time, length time.Duration) uint64 {
	if length <= 0 {
		return 0
	}
	secs := int64(length.Seconds())
	if secs <= 0 {
		return 0
	}

	delta := t.Unix() - GenesisValue
	if delta < 0 {
		return 0
	}
	return uint64(delta / secs)
}

// Bounds returns the half-open interval [start, end) of an epoch.
func Bounds(n uint64, length time.Duration) (start, end time.Time) {
	secs := int64(length.Seconds())
	if secs <= 0 {
		return Genesis(), Genesis()
	}
	startUnix := GenesisValue + int64(n)*secs
	return time.Unix(startUnix, 0).UTC(), time.Unix(startUnix+secs, 0).UTC()
}
