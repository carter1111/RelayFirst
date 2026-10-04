package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Message is one store-and-forward envelope held by a node.
//
// It is deliberately payload-agnostic. The node does not parse, verify or
// interpret what it carries (MVP.md §7.1: "the node is dumb"); it only needs
// enough indexing to hand messages back to their addressee. That is also why
// Kind exists rather than a table per message shape — a future message type can
// reuse this storage without a migration.
type Message struct {
	// ID is the envelope's unique id (the receipt id for receipt messages). It is
	// the idempotency key: storing the same ID twice is a no-op (S5-3).
	ID string

	// AgentID is the addressee. Indexing on it is what makes the pull path O(1)
	// in the number of other agents' messages.
	AgentID string

	// Kind names the payload's type, e.g. "receipt". Opaque to the node.
	Kind string

	// Payload is the opaque body, stored byte-for-byte.
	//
	// Stored verbatim on purpose: a receipt's signature covers an exact byte
	// sequence, so a node that re-serialized it would silently invalidate every
	// receipt it touched. The node must be able to forward what it received.
	Payload []byte

	// ReceivedAt is when the node accepted it.
	ReceivedAt time.Time
}

// MessageStore stores and retrieves envelopes for a node.
//
// The primary key is Message.ID, so dedup is enforced by the database rather than
// by a check-then-insert in the handler. That distinction is load-bearing: a
// read-then-write would let two concurrent deliveries of the same receipt both
// see "absent" and both insert.
//
// It lives in this package rather than in internal/store so the node never
// transitively links receipt or eip712 — see the package comment.
type MessageStore struct {
	db *DB
}

// NewMessageStore wraps db.
func NewMessageStore(db *DB) *MessageStore { return &MessageStore{db: db} }

// Put stores m, reporting whether it was newly stored.
//
// stored is false when the ID was already present, which is a normal and
// expected outcome rather than an error: senders retry, and a retry must not be
// treated as a failure.
func (s *MessageStore) Put(m Message) (stored bool, err error) {
	if m.ID == "" {
		return false, fmt.Errorf("store: message id is empty")
	}
	if m.AgentID == "" {
		return false, fmt.Errorf("store: message agent id is empty")
	}
	if m.Kind == "" {
		return false, fmt.Errorf("store: message kind is empty")
	}
	if len(m.Payload) == 0 {
		return false, fmt.Errorf("store: message payload is empty")
	}

	at := m.ReceivedAt
	if at.IsZero() {
		at = time.Now()
	}

	res, err := s.db.handle.Exec(`
		INSERT INTO messages (receipt_id, agent_id, kind, payload, received_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(receipt_id) DO NOTHING
	`, m.ID, m.AgentID, m.Kind, m.Payload, TimeToUnix(at))
	if err != nil {
		return false, fmt.Errorf("store: put message: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: put message rows: %w", err)
	}
	return n > 0, nil
}

// ByAgent returns an agent's messages, newest first.
//
// A limit of zero or less means "every message". The cap exists so a pull cannot
// be made to read an unbounded amount by an agent that has accumulated years of
// traffic.
func (s *MessageStore) ByAgent(agentID string, limit int) ([]Message, error) {
	if agentID == "" {
		return nil, fmt.Errorf("store: agent id is empty")
	}

	query := `SELECT receipt_id, agent_id, kind, payload, received_at
	          FROM messages WHERE agent_id = ? ORDER BY received_at DESC, receipt_id`
	args := []any{agentID}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.handle.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query messages: %w", err)
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		var (
			m  Message
			at int64
		)
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Kind, &m.Payload, &at); err != nil {
			return nil, fmt.Errorf("store: scan message: %w", err)
		}
		m.ReceivedAt = UnixToTime(at)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate messages: %w", err)
	}
	return out, nil
}

// Get returns one message by id.
func (s *MessageStore) Get(id string) (Message, bool) {
	var (
		m  Message
		at int64
	)
	err := s.db.handle.QueryRow(`
		SELECT receipt_id, agent_id, kind, payload, received_at FROM messages WHERE receipt_id = ?
	`, id).Scan(&m.ID, &m.AgentID, &m.Kind, &m.Payload, &at)

	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return Message{}, false
	}
	m.ReceivedAt = UnixToTime(at)
	return m, true
}

// Count returns the number of stored messages.
func (s *MessageStore) Count() int {
	var n int
	if err := s.db.handle.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// AgentCount returns the number of distinct agents with stored messages.
//
// It feeds the node's well-known document, which reports how many agents a node
// is serving so an operator can tell an empty node from a busy one.
func (s *MessageStore) AgentCount() int {
	var n int
	if err := s.db.handle.QueryRow(`SELECT COUNT(DISTINCT agent_id) FROM messages`).Scan(&n); err != nil {
		return 0
	}
	return n
}
