package sqlite

import (
	"database/sql"
	"fmt"
	"time"
)

// CardStore holds published A2A agent cards for a node.
//
// # What it deliberately does not do
//
// It does not verify anything. The card and its proof are stored as opaque bytes
// and returned unchanged, because a node is not in a position to check a signature
// (MVP.md §7.1) and because a card's proof covers exact bytes — re-serializing one
// here would invalidate a proof that was perfectly good.
//
// It also does not decide whether a card is *honest*. A directory records what was
// published; a reader decides what to trust. Storing a card is not an endorsement,
// and the schema has no "verified" column precisely so that cannot creep in.
//
// It lives in this package rather than internal/store so the node never
// transitively links receipt, eip712 or publish — the same separation the messages
// store relies on.
type CardStore struct {
	db *DB
}

// NewCardStore wraps db.
func NewCardStore(db *DB) *CardStore { return &CardStore{db: db} }

// AgentCard is a published card and its proof, both verbatim.
type AgentCard struct {
	// AgentID is the identity the publisher declared. It is the primary key.
	AgentID string

	// Card is the exact A2A card JSON that was signed.
	Card []byte

	// Proof is the exact proof JSON that accompanies it.
	Proof []byte

	// UpdatedAt is when this version was published.
	UpdatedAt time.Time
}

// Put stores a card, replacing any previous version for the same agent.
//
// Replacing rather than rejecting is deliberate: a card is current state, not a
// log. An agent that rotates its endpoint must be able to publish the new card,
// and leaving the old one addressable would mean readers find a stale endpoint.
//
// The trade-off is that a publisher can overwrite its own card freely, so a reader
// cannot treat a stored card as immutable history. That is acceptable because the
// proof is what matters and it is over the bytes the reader receives — a reader
// verifies what it got, not what was published first.
func (s *CardStore) Put(c AgentCard) error {
	if c.AgentID == "" {
		return fmt.Errorf("store: card agent id is empty")
	}
	if len(c.Card) == 0 {
		return fmt.Errorf("store: card bytes are empty")
	}
	if len(c.Proof) == 0 {
		return fmt.Errorf("store: card proof is empty")
	}
	at := c.UpdatedAt
	if at.IsZero() {
		at = time.Now()
	}

	_, err := s.db.handle.Exec(`
		INSERT INTO agent_cards (agent_id, card, proof, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			card = excluded.card,
			proof = excluded.proof,
			updated_at = excluded.updated_at
	`, c.AgentID, c.Card, c.Proof, TimeToUnix(at))
	if err != nil {
		return fmt.Errorf("store: put agent card: %w", err)
	}
	return nil
}

// Get returns one agent's card.
func (s *CardStore) Get(agentID string) (AgentCard, bool, error) {
	if agentID == "" {
		return AgentCard{}, false, fmt.Errorf("store: agent id is empty")
	}
	var (
		card, proof []byte
		updatedAt   int64
	)
	err := s.db.handle.QueryRow(`
		SELECT card, proof, updated_at FROM agent_cards WHERE agent_id = ?
	`, agentID).Scan(&card, &proof, &updatedAt)
	if err == sql.ErrNoRows {
		return AgentCard{}, false, nil
	}
	if err != nil {
		return AgentCard{}, false, fmt.Errorf("store: get agent card: %w", err)
	}
	return AgentCard{
		AgentID:   agentID,
		Card:      card,
		Proof:     proof,
		UpdatedAt: UnixToTime(updatedAt),
	}, true, nil
}

// List returns published cards, most recently updated first.
//
// A limit of zero or less means "every card". The cap exists so a directory
// listing cannot be made to read an unbounded amount as agents accumulate.
func (s *CardStore) List(limit int) ([]AgentCard, error) {
	query := `SELECT agent_id, card, proof, updated_at FROM agent_cards
	          ORDER BY updated_at DESC, agent_id`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.handle.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list agent cards: %w", err)
	}
	defer rows.Close()

	var out []AgentCard
	for rows.Next() {
		var (
			c         AgentCard
			updatedAt int64
		)
		if err := rows.Scan(&c.AgentID, &c.Card, &c.Proof, &updatedAt); err != nil {
			return nil, fmt.Errorf("store: scan agent card: %w", err)
		}
		c.UpdatedAt = UnixToTime(updatedAt)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate agent cards: %w", err)
	}
	return out, nil
}

// Count returns how many cards are published.
func (s *CardStore) Count() int {
	var n int
	if err := s.db.handle.QueryRow(`SELECT COUNT(*) FROM agent_cards`).Scan(&n); err != nil {
		return 0
	}
	return n
}
