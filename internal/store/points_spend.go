package store

import (
	"fmt"
	"time"

	"github.com/relayfirst/relayfirst/internal/scoring"
)

// SQLPointsSpend is the burn side of the points ledger: spending points, which is burning
// them (incentive.md "积分怎么花" -- points cannot be transferred, so the only way to
// spend is to destroy).
//
// # Why it is separate from SQLPointsLedger
//
// A credit is an award and a burn is a spend; they happen for different reasons and are
// examined differently, so they live in different tables. The balance is the difference:
// usable = credited - burned. Payout follows the same rule (decided 2026-10-08), so a
// burned point is gone from the TGE claim too.
type SQLPointsSpend struct {
	db *DB
}

// NewPointsSpend wraps db as a burn ledger.
func NewPointsSpend(db *DB) *SQLPointsSpend { return &SQLPointsSpend{db: db} }

// burnReasons is the closed set of things points may be spent on.
//
// It is a closed set for the same reason the delegation scope is (ADR-0007): an open
// reason field would let any caller invent a sink, and a sink nobody defined is a way to
// move points that the design never agreed to. Adding a reason is a deliberate change.
var burnReasons = map[string]bool{
	"namespace": true, // register a Relay ID / namespace (S10-0)
	"discount":  true, // a fee discount voucher
	"priority":  true, // relay priority / API quota
}

// Burn records a spend of micro points, atomically, only if the balance covers it.
//
// # Why the check and the decrement are one statement
//
// Reading the balance and then inserting would be a race: two burns could each see enough
// balance and together overdraw. The insert-select does both in one statement, so the
// balance cannot be spent twice. SQLite serialises the write, which is what makes it safe.
//
// The reason must be in the closed set; a burn_id that already exists is a no-op (a retry
// must not double-spend).
func (l *SQLPointsSpend) Burn(burnID, agentID string, epoch uint64, microPoints int64, reason string, at time.Time) error {
	if burnID == "" {
		return fmt.Errorf("store: burn has no id")
	}
	if agentID == "" {
		return fmt.Errorf("store: burn has no agent")
	}
	if microPoints <= 0 {
		return fmt.Errorf("store: burn amount must be positive, got %d", microPoints)
	}
	if !burnReasons[reason] {
		return fmt.Errorf("store: %q is not a defined burn reason (want namespace, discount or priority)", reason)
	}

	res, err := l.db.Handle().Exec(`
		INSERT INTO point_burns (burn_id, agent_id, epoch, micro_points, reason, burned_at)
		SELECT ?, ?, ?, ?, ?, ?
		WHERE (SELECT COALESCE(SUM(micro_points), 0) FROM point_entries WHERE agent_id = ?)
		    - (SELECT COALESCE(SUM(micro_points), 0) FROM point_burns   WHERE agent_id = ?) >= ?
		ON CONFLICT(burn_id) DO NOTHING
	`, burnID, agentID, epoch, microPoints, reason, timeToUnix(at), agentID, agentID, microPoints)
	if err != nil {
		return fmt.Errorf("store: burn: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: burn rows: %w", err)
	}
	if n == 0 {
		// Either the id already existed (a retry) or the balance was short. Tell them
		// apart, because "already burned" is success and "insufficient" is not.
		if l.burnExists(burnID) {
			return nil
		}
		return fmt.Errorf("store: insufficient balance to burn %d micro points for %s", microPoints, agentID)
	}
	return nil
}

// burnExists reports whether a burn id is already recorded.
func (l *SQLPointsSpend) burnExists(burnID string) bool {
	var one int
	err := l.db.Handle().QueryRow(`SELECT 1 FROM point_burns WHERE burn_id = ?`, burnID).Scan(&one)
	return err == nil
}

// UsableMicro returns an agent's spendable balance in micro points: credited minus burned.
func (l *SQLPointsSpend) UsableMicro(agentID string) (int64, error) {
	var usable int64
	err := l.db.Handle().QueryRow(`
		SELECT (SELECT COALESCE(SUM(micro_points), 0) FROM point_entries WHERE agent_id = ?)
		     - (SELECT COALESCE(SUM(micro_points), 0) FROM point_burns   WHERE agent_id = ?)
	`, agentID, agentID).Scan(&usable)
	if err != nil {
		return 0, fmt.Errorf("store: usable balance: %w", err)
	}
	return usable, nil
}

// UsableMicroThroughEpoch returns the usable balance in micro points, counting only
// credits and burns that happened at or before maxEpoch.
//
// It exists so a claim for an epoch proves the balance AS OF that epoch, not the current
// one: the merkle leaf carries a total, and a burn after the epoch must not silently
// change what the epoch's root says that total was.
func (l *SQLPointsSpend) UsableMicroThroughEpoch(agentID string, maxEpoch uint64) (int64, error) {
	var usable int64
	err := l.db.Handle().QueryRow(`
		SELECT (SELECT COALESCE(SUM(micro_points), 0) FROM point_entries WHERE agent_id = ? AND epoch <= ?)
		     - (SELECT COALESCE(SUM(micro_points), 0) FROM point_burns   WHERE agent_id = ? AND epoch <= ?)
	`, agentID, maxEpoch, agentID, maxEpoch).Scan(&usable)
	if err != nil {
		return 0, fmt.Errorf("store: usable balance through epoch: %w", err)
	}
	return usable, nil
}

// CumulativeUsableMicro returns each agent's usable balance through maxEpoch, in micro
// points, for building the balance root that a claim is verified against.
//
// It is CumulativeMicro with the burns subtracted, so the root reflects what an agent can
// actually claim rather than what they grossed.
func (l *SQLPointsSpend) CumulativeUsableMicro(maxEpoch uint64) (map[string]int64, error) {
	rows, err := l.db.Handle().Query(`
		SELECT agent_id, SUM(net) FROM (
		    SELECT agent_id,  micro_points AS net FROM point_entries WHERE epoch <= ?
		    UNION ALL
		    SELECT agent_id, -micro_points AS net FROM point_burns   WHERE epoch <= ?
		) GROUP BY agent_id
	`, maxEpoch, maxEpoch)
	if err != nil {
		return nil, fmt.Errorf("store: cumulative usable points: %w", err)
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var agent string
		var net int64
		if err := rows.Scan(&agent, &net); err != nil {
			return nil, fmt.Errorf("store: scan cumulative usable: %w", err)
		}
		if net > 0 {
			out[agent] = net
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate cumulative usable: %w", err)
	}
	return out, nil
}

// MicroFromPoints converts a float points amount to integer micro points, using the same
// rounding the credits use, so a burn and a credit of the "same" number are the same
// integer.
func MicroFromPoints(points float64) int64 {
	return int64(points*scoring.MicroPerPoint + 0.5)
}
