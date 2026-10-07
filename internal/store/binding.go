package store

import (
	"fmt"
	"time"
)

// SQLBindingLedger stores PoSR bindings: which agent is bound to which node
// (incentive.md §4, one-binding-per-identity).
//
// It satisfies mining.NodePoolSource alongside SQLTenureLedger, so the settlement can
// read both the node tenures and the agent->node bindings through one interface.
type SQLBindingLedger struct {
	db *DB
}

// NewBindingLedger wraps db as a binding ledger.
func NewBindingLedger(db *DB) *SQLBindingLedger { return &SQLBindingLedger{db: db} }

// Bind records an agent's binding to a node, once.
//
// It returns an error if the agent already has a binding, rather than replacing it: the
// binding is what the 1.25x multiplier rests on, and silently rebinding would let an
// identity switch nodes to wherever the bonus is easiest, which is the opposite of the
// one-binding-per-identity rule.
func (l *SQLBindingLedger) Bind(agentID, nodeID string, epoch uint64, at time.Time) error {
	if agentID == "" || nodeID == "" {
		return fmt.Errorf("store: a binding needs both an agent and a node")
	}

	res, err := l.db.Handle().Exec(`
		INSERT INTO node_bindings (agent_id, node_id, epoch, recorded_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(agent_id) DO NOTHING
	`, agentID, nodeID, epoch, timeToUnix(at))
	if err != nil {
		return fmt.Errorf("store: bind: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: bind rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("store: agent %s is already bound; a binding is one-per-identity", agentID)
	}
	return nil
}

// Bindings returns each agent's bound node, for bindings made at or before maxEpoch.
//
// The epoch filter matters: a settlement for epoch N must not see a binding made in a
// later epoch, or a settlement could be recomputed differently depending on when it ran.
func (l *SQLBindingLedger) Bindings(maxEpoch uint64) (map[string]string, error) {
	rows, err := l.db.Handle().Query(
		`SELECT agent_id, node_id FROM node_bindings WHERE epoch <= ?`, maxEpoch,
	)
	if err != nil {
		return nil, fmt.Errorf("store: bindings: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var agent, node string
		if err := rows.Scan(&agent, &node); err != nil {
			return nil, fmt.Errorf("store: scan binding: %w", err)
		}
		out[agent] = node
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate bindings: %w", err)
	}
	return out, nil
}
