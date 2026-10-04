package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CommitmentState mirrors verification.StakeState.
//
// It is redeclared here rather than imported so this package keeps its property of
// depending on nothing but database/sql. internal/verification imports internal/receipt
// (to re-execute tasks), and internal/receipt links eip712 and secp256k1 — so importing
// verification from here would let a relay node pull the whole signing stack into its
// dependency graph, which MVP.md §7.1 forbids and internal/node has a test guarding.
//
// The cost is a duplicated string set. The mitigation is that the caller converts at
// the boundary and a test asserts the two agree, so a divergence fails loudly rather
// than corrupting a record.
type CommitmentState string

const (
	// CommitmentCommitted means the commitment is open.
	CommitmentCommitted CommitmentState = "committed"

	// CommitmentReleased means the verdict was sound.
	CommitmentReleased CommitmentState = "released"

	// CommitmentSlashed means the verdict was disputable and the commitment surrendered.
	CommitmentSlashed CommitmentState = "slashed"
)

// Commitment is one durable commitment record.
//
// It holds no balance. See the schema comment: a commitment is a recorded magnitude
// with a lifecycle, and there is no operation that turns it into points.
type Commitment struct {
	ID          string
	AgentID     string
	Epoch       uint64
	Amount      uint64
	State       CommitmentState
	CommittedAt time.Time
	SettledAt   time.Time
}

// CommitmentLedger is a durable commitment store.
type CommitmentLedger struct {
	db *DB
}

// NewCommitmentLedger wraps db.
func NewCommitmentLedger(db *DB) *CommitmentLedger { return &CommitmentLedger{db: db} }

// Commit records a commitment, idempotently per id.
func (l *CommitmentLedger) Commit(id, agentID string, epoch uint64, amount uint64, at time.Time) (bool, error) {
	if id == "" {
		return false, fmt.Errorf("sqlite: commitment id is empty")
	}
	if agentID == "" {
		return false, fmt.Errorf("sqlite: commitment agent id is empty")
	}
	if amount == 0 {
		return false, fmt.Errorf("sqlite: commitment amount must be positive")
	}

	res, err := l.db.handle.Exec(`
		INSERT INTO verification_commitments (id, agent_id, epoch, amount, state, committed_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, id, agentID, epoch, amount, string(CommitmentCommitted), TimeToUnix(at))
	if err != nil {
		return false, fmt.Errorf("sqlite: commit: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite: commit rows: %w", err)
	}
	return n > 0, nil
}

// Release discharges a commitment.
func (l *CommitmentLedger) Release(id string, at time.Time) (bool, error) {
	return l.settle(id, CommitmentReleased, at)
}

// Slash surrenders a commitment.
func (l *CommitmentLedger) Slash(id string, at time.Time) (bool, error) {
	return l.settle(id, CommitmentSlashed, at)
}

// settle moves a commitment to its terminal state exactly once.
//
// The state guard lives in the WHERE clause rather than in a read-then-write, so two
// concurrent settlements cannot both observe "committed" and both proceed. A second
// settlement of the same commitment is refused rather than silently ignored, because a
// verdict that could change its own consequence twice would not be accountability.
func (l *CommitmentLedger) settle(id string, to CommitmentState, at time.Time) (bool, error) {
	res, err := l.db.handle.Exec(`
		UPDATE verification_commitments
		SET state = ?, settled_at = ?
		WHERE id = ? AND state = ?
	`, string(to), TimeToUnix(at), id, string(CommitmentCommitted))
	if err != nil {
		return false, fmt.Errorf("sqlite: settle: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite: settle rows: %w", err)
	}
	if n == 0 {
		// Either the id is unknown or it already settled; distinguish so the caller's
		// error is actionable.
		if _, found := l.Get(id); !found {
			return false, fmt.Errorf("sqlite: unknown commitment %q", id)
		}
		return false, fmt.Errorf("sqlite: commitment %q is already settled", id)
	}
	return true, nil
}

// Get returns one commitment.
func (l *CommitmentLedger) Get(id string) (Commitment, bool) {
	var (
		c           Commitment
		state       string
		committedAt int64
		settledAt   sql.NullInt64
	)

	err := l.db.handle.QueryRow(`
		SELECT id, agent_id, epoch, amount, state, committed_at, settled_at
		FROM verification_commitments WHERE id = ?
	`, id).Scan(&c.ID, &c.AgentID, &c.Epoch, &c.Amount, &state, &committedAt, &settledAt)

	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return Commitment{}, false
	}

	c.State = CommitmentState(state)
	c.CommittedAt = UnixToTime(committedAt)
	if settledAt.Valid {
		c.SettledAt = UnixToTime(settledAt.Int64)
	}
	return c, true
}

// Standing reports an agent's committed, released and slashed magnitudes.
//
// These are three sums, not a balance: nothing converts them into points, and there is
// no operation that moves points between agents.
func (l *CommitmentLedger) Standing(agentID string) (committed, released, slashed uint64) {
	rows, err := l.db.handle.Query(`
		SELECT amount, state FROM verification_commitments WHERE agent_id = ?
	`, agentID)
	if err != nil {
		return 0, 0, 0
	}
	defer rows.Close()

	for rows.Next() {
		var (
			amount uint64
			state  string
		)
		if err := rows.Scan(&amount, &state); err != nil {
			return committed, released, slashed
		}
		committed += amount
		switch CommitmentState(state) {
		case CommitmentReleased:
			released += amount
		case CommitmentSlashed:
			slashed += amount
		}
	}
	return committed, released, slashed
}

// Entries returns every commitment in insertion order.
func (l *CommitmentLedger) Entries() []Commitment {
	rows, err := l.db.handle.Query(`
		SELECT id, agent_id, epoch, amount, state, committed_at, settled_at
		FROM verification_commitments ORDER BY committed_at, id
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []Commitment
	for rows.Next() {
		var (
			c           Commitment
			state       string
			committedAt int64
			settledAt   sql.NullInt64
		)
		if err := rows.Scan(&c.ID, &c.AgentID, &c.Epoch, &c.Amount, &state, &committedAt, &settledAt); err != nil {
			return nil
		}
		c.State = CommitmentState(state)
		c.CommittedAt = UnixToTime(committedAt)
		if settledAt.Valid {
			c.SettledAt = UnixToTime(settledAt.Int64)
		}
		out = append(out, c)
	}
	return out
}

// Verdict is one durable verification verdict.
//
// It holds no amount: a verdict is an opinion about work, not a quantity of value.
type Verdict struct {
	ReceiptID      string
	AgentID        string
	VerifierID     string
	Status         string
	RecomputedHash string
	VerifiedAt     time.Time
	RecordedAt     time.Time
}

// VerdictStore is a durable verdict store.
type VerdictStore struct {
	db *DB
}

// NewVerdictStore wraps db.
func NewVerdictStore(db *DB) *VerdictStore { return &VerdictStore{db: db} }

// Record stores a verdict, overwriting an earlier one for the same receipt.
//
// Overwriting is deliberate here, unlike for commitments: re-verification legitimately
// produces a fresh verdict, and the *settlement* is what must not change twice. Keeping
// the latest verdict means a re-check after an outage can correct an earlier
// inconclusive result.
func (s *VerdictStore) Record(v Verdict) error {
	if v.ReceiptID == "" {
		return fmt.Errorf("sqlite: verdict receipt id is empty")
	}
	if v.VerifierID == "" {
		return fmt.Errorf("sqlite: verdict verifier id is empty")
	}
	if v.Status == "" {
		return fmt.Errorf("sqlite: verdict status is empty")
	}

	recordedAt := v.RecordedAt
	if recordedAt.IsZero() {
		recordedAt = time.Now()
	}

	var verifiedAt any
	if !v.VerifiedAt.IsZero() {
		verifiedAt = TimeToUnix(v.VerifiedAt)
	}

	_, err := s.db.handle.Exec(`
		INSERT INTO verification_verdicts
			(receipt_id, agent_id, verifier_id, status, recomputed_hash, verified_at, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(receipt_id) DO UPDATE SET
			agent_id        = excluded.agent_id,
			verifier_id     = excluded.verifier_id,
			status          = excluded.status,
			recomputed_hash = excluded.recomputed_hash,
			verified_at     = excluded.verified_at,
			recorded_at     = excluded.recorded_at
	`, v.ReceiptID, v.AgentID, v.VerifierID, v.Status, v.RecomputedHash, verifiedAt, TimeToUnix(recordedAt))
	if err != nil {
		return fmt.Errorf("sqlite: record verdict: %w", err)
	}
	return nil
}

// Get returns the verdict for a receipt.
func (s *VerdictStore) Get(receiptID string) (Verdict, bool) {
	var (
		v          Verdict
		hash       sql.NullString
		verifiedAt sql.NullInt64
		recordedAt int64
	)

	err := s.db.handle.QueryRow(`
		SELECT receipt_id, agent_id, verifier_id, status, recomputed_hash, verified_at, recorded_at
		FROM verification_verdicts WHERE receipt_id = ?
	`, receiptID).Scan(&v.ReceiptID, &v.AgentID, &v.VerifierID, &v.Status, &hash, &verifiedAt, &recordedAt)

	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return Verdict{}, false
	}

	if hash.Valid {
		v.RecomputedHash = hash.String
	}
	if verifiedAt.Valid {
		v.VerifiedAt = UnixToTime(verifiedAt.Int64)
	}
	v.RecordedAt = UnixToTime(recordedAt)
	return v, true
}

// Count returns the number of stored verdicts.
func (s *VerdictStore) Count() int {
	var n int
	if err := s.db.handle.QueryRow(`SELECT COUNT(*) FROM verification_verdicts`).Scan(&n); err != nil {
		return 0
	}
	return n
}
