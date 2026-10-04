package sqlite

import (
	"encoding/json"
	"fmt"
	"time"
)

// TaskStore is the node's task board (S10-3).
//
// # What it stores, and what it deliberately does not decide
//
// It records offers and the agents who expressed interest. It does not record who WON,
// because it cannot know: the node has no authority, and giving it one would make it a
// market operator rather than a relay. What resolves a competition is the protocol — the
// requester accepts one offer, and the others' work has no receipt the requester
// acknowledges.
//
// So a claim is stored as interest, and the API says "granted: false" so a caller cannot
// read an acknowledgement as a win.
//
// # Why expiry is recorded but not enforced
//
// ARCHITECTURE.md §4.5 makes expiry a protocol transition decided from signed data, because
// two relays with different clocks would otherwise disagree about whether an offer was
// still open. A relay that filtered its listing by its own clock would be inventing an
// authority it does not have, so it returns lapsed offers too and lets the client decide.
type TaskStore struct {
	db *DB
}

// NewTaskStore wraps db.
func NewTaskStore(db *DB) *TaskStore { return &TaskStore{db: db} }

// TaskOffer is a published task announcement.
type TaskOffer struct {
	TaskID    string
	Requester string
	Subject   string
	Spec      json.RawMessage
	ExpiresAt *time.Time
	OfferedAt time.Time
	Claims    int
}

// Put records an offer.
//
// # Why a repeat is an update rather than a duplicate
//
// A requester that re-posts the same task (a retry, or a corrected spec) means "this is
// the current offer", not "there are two". Two rows would make a task appear twice in a
// listing, and a client counting open tasks would be wrong.
func (s *TaskStore) Put(o TaskOffer) error {
	if o.TaskID == "" {
		return fmt.Errorf("store: task offer has no id")
	}
	if o.Requester == "" {
		return fmt.Errorf("store: task offer %s has no requester", o.TaskID)
	}
	if o.Subject == "" {
		return fmt.Errorf("store: task offer %s has no subject", o.TaskID)
	}
	at := o.OfferedAt
	if at.IsZero() {
		at = time.Now()
	}

	var expires any
	if o.ExpiresAt != nil {
		expires = TimeToUnix(*o.ExpiresAt)
	}

	_, err := s.db.handle.Exec(`
		INSERT INTO task_offers (task_id, requester, subject, spec, expires_at, offered_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET
			requester = excluded.requester,
			subject = excluded.subject,
			spec = excluded.spec,
			expires_at = excluded.expires_at,
			offered_at = excluded.offered_at
	`, o.TaskID, o.Requester, o.Subject, []byte(o.Spec), expires, TimeToUnix(at))
	if err != nil {
		return fmt.Errorf("store: put task offer: %w", err)
	}
	return nil
}

// ByID returns one offer with its claim count.
func (s *TaskStore) ByID(taskID string) (TaskOffer, bool, error) {
	if taskID == "" {
		return TaskOffer{}, false, fmt.Errorf("store: task id is empty")
	}
	offers, err := s.query(`
		SELECT o.task_id, o.requester, o.subject, o.spec, o.expires_at, o.offered_at,
		       (SELECT COUNT(*) FROM task_claims c WHERE c.task_id = o.task_id)
		FROM task_offers o WHERE o.task_id = ?`, taskID)
	if err != nil {
		return TaskOffer{}, false, err
	}
	if len(offers) == 0 {
		return TaskOffer{}, false, nil
	}
	return offers[0], true, nil
}

// List returns offers, newest first, optionally filtered by subject.
//
// A limit of zero or less means "every offer".
//
// # Why expired offers are included
//
// Because expiry is the protocol's decision, not this node's (ARCHITECTURE.md §4.5). The
// response carries ExpiresAt so a client can filter with its own clock; the node filtering
// would mean a relay silently hiding work it merely believed was late.
func (s *TaskStore) List(subject string, limit int) ([]TaskOffer, error) {
	query := `SELECT o.task_id, o.requester, o.subject, o.spec, o.expires_at, o.offered_at,
	                 (SELECT COUNT(*) FROM task_claims c WHERE c.task_id = o.task_id)
	          FROM task_offers o`
	args := []any{}
	if subject != "" {
		query += ` WHERE o.subject = ?`
		args = append(args, subject)
	}
	query += ` ORDER BY o.offered_at DESC, o.task_id`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	return s.query(query, args...)
}

// AddClaim records an agent's interest in an offer.
//
// # Why this is idempotent
//
// The same agent claiming twice is a retry, not two claims. Counting it twice would inflate
// the interest a task appears to have, which is the number a requester looks at when
// deciding whether the board is alive.
func (s *TaskStore) AddClaim(taskID, claimant string, at time.Time) error {
	if taskID == "" || claimant == "" {
		return fmt.Errorf("store: a claim needs a task id and a claimant")
	}
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.db.handle.Exec(`
		INSERT INTO task_claims (task_id, claimant, claimed_at)
		VALUES (?, ?, ?)
		ON CONFLICT(task_id, claimant) DO NOTHING
	`, taskID, claimant, TimeToUnix(at))
	if err != nil {
		return fmt.Errorf("store: add claim: %w", err)
	}
	return nil
}

// Claimants returns the agents who claimed a task, in claim order.
func (s *TaskStore) Claimants(taskID string) ([]string, error) {
	rows, err := s.db.handle.Query(
		`SELECT claimant FROM task_claims WHERE task_id = ? ORDER BY claimed_at, claimant`, taskID)
	if err != nil {
		return nil, fmt.Errorf("store: list claimants: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("store: scan claimant: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate claimants: %w", err)
	}
	return out, nil
}

// Count returns how many offers are on the board.
func (s *TaskStore) Count() int {
	var n int
	if err := s.db.handle.QueryRow(`SELECT COUNT(*) FROM task_offers`).Scan(&n); err != nil {
		return 0
	}
	return n
}

func (s *TaskStore) query(query string, args ...any) ([]TaskOffer, error) {
	rows, err := s.db.handle.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query task offers: %w", err)
	}
	defer rows.Close()

	var out []TaskOffer
	for rows.Next() {
		var (
			o         TaskOffer
			spec      []byte
			expiresAt *int64
			offeredAt int64
		)
		if err := rows.Scan(&o.TaskID, &o.Requester, &o.Subject, &spec,
			&expiresAt, &offeredAt, &o.Claims); err != nil {
			return nil, fmt.Errorf("store: scan task offer: %w", err)
		}
		o.Spec = json.RawMessage(spec)
		o.OfferedAt = UnixToTime(offeredAt)
		if expiresAt != nil {
			t := UnixToTime(*expiresAt)
			o.ExpiresAt = &t
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate task offers: %w", err)
	}
	return out, nil
}
