package store

import (
	"fmt"
	"time"

	"github.com/relayfirst/relayfirst/internal/tenure"
)

// SQLTenureLedger stores per-epoch node liveness flags durably (incentive.md §3).
//
// It records only the raw flag; the tenure count and tier are derived on read by
// folding the flags through internal/tenure. Storing the derived state as well would
// let it disagree with its inputs, and the fold is pure, so there is nothing to cache.
type SQLTenureLedger struct {
	db *DB
}

// NewTenureLedger wraps db as a tenure flag ledger.
func NewTenureLedger(db *DB) *SQLTenureLedger { return &SQLTenureLedger{db: db} }

// Record stores one epoch's flag for a node, idempotently per (node, epoch).
//
// The boolean is stored as 0/1 because that is what SQLite has; the meaning stays a
// bool at every boundary. Re-recording the same (node, epoch) is a no-op, matching the
// other ledgers: a retry must not change a number.
func (l *SQLTenureLedger) Record(nodeID string, epoch uint64, qualified bool, at time.Time) (bool, error) {
	if nodeID == "" {
		return false, fmt.Errorf("store: tenure flag has no node id")
	}
	q := 0
	if qualified {
		q = 1
	}

	res, err := l.db.Handle().Exec(`
		INSERT INTO node_tenure (node_id, epoch, qualified, recorded_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(node_id, epoch) DO NOTHING
	`, nodeID, epoch, q, timeToUnix(at))
	if err != nil {
		return false, fmt.Errorf("store: record tenure flag: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: record tenure rows: %w", err)
	}
	return n > 0, nil
}

// Tenure returns a node's tenure state, folding every recorded flag in epoch order.
//
// The fold is over the full history up to and including maxEpoch, not a stored
// counter: a single missed epoch steps the count down and two in a row reset it, so the
// count depends on the order and contiguity of the flags, which only the raw rows
// preserve.
func (l *SQLTenureLedger) Tenure(nodeID string, maxEpoch uint64) (tenure.State, error) {
	flags, err := l.flags(nodeID, maxEpoch)
	if err != nil {
		return tenure.State{}, err
	}
	return tenure.Tenure(flags), nil
}

// Tier returns the node's Layer 0 weight as of maxEpoch.
func (l *SQLTenureLedger) Tier(nodeID string, maxEpoch uint64) (float64, error) {
	s, err := l.Tenure(nodeID, maxEpoch)
	if err != nil {
		return 0, err
	}
	return tenure.Tier(s.Tenure), nil
}

// AllTenures returns each node's tenure count as of maxEpoch, for building a pool split.
//
// A node with no rows is simply absent, not present with a zero: absence and "recorded
// zero epochs" are the same to the reward (both ineligible), so collapsing them avoids
// a distinction the pool would have to handle.
func (l *SQLTenureLedger) AllTenures(maxEpoch uint64) (map[string]int, error) {
	rows, err := l.db.Handle().Query(
		`SELECT DISTINCT node_id FROM node_tenure WHERE epoch <= ?`, maxEpoch,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list tenure nodes: %w", err)
	}
	var nodes []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan tenure node: %w", err)
		}
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: iterate tenure nodes: %w", err)
	}
	rows.Close()

	out := make(map[string]int, len(nodes))
	for _, n := range nodes {
		s, err := l.Tenure(n, maxEpoch)
		if err != nil {
			return nil, err
		}
		out[n] = s.Tenure
	}
	return out, nil
}

// flags reads a node's qualified flags in ascending epoch order.
//
// It selects the whole history rather than a window: the reset rule makes the count
// depend on consecutive misses, and a window that began mid-streak would compute a
// different tenure than the node actually has.
func (l *SQLTenureLedger) flags(nodeID string, maxEpoch uint64) ([]bool, error) {
	rows, err := l.db.Handle().Query(
		`SELECT qualified FROM node_tenure WHERE node_id = ? AND epoch <= ? ORDER BY epoch ASC`,
		nodeID, maxEpoch,
	)
	if err != nil {
		return nil, fmt.Errorf("store: read tenure flags: %w", err)
	}
	defer rows.Close()

	var flags []bool
	for rows.Next() {
		var q int
		if err := rows.Scan(&q); err != nil {
			return nil, fmt.Errorf("store: scan tenure flag: %w", err)
		}
		flags = append(flags, q != 0)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate tenure flags: %w", err)
	}
	return flags, nil
}
