package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/relayfirst/relayfirst/internal/scoring"
)

// SQLArtifactLedger is a durable scoring.Ledger.
//
// It satisfies the same interface as scoring.MemLedger, so callers do not change
// when durability is turned on. The global-ness lives in the schema: the primary
// key is the artifact key alone (invariant A6).
type SQLArtifactLedger struct {
	db *DB
}

// NewArtifactLedger wraps db as a scoring.Ledger.
func NewArtifactLedger(db *DB) *SQLArtifactLedger {
	return &SQLArtifactLedger{db: db}
}

// compile-time assertion that the interface is satisfied.
var _ scoring.Ledger = (*SQLArtifactLedger)(nil)

// Observe records a submission, reporting whether it was the first.
//
// The insert and the update are expressed as one statement so that two concurrent
// submissions of the same artifact cannot both be told they were first: SQLite
// applies the primary-key constraint, and the RETURNING clause reports which path
// was taken.
func (l *SQLArtifactLedger) Observe(artifactKey, agentID string, at time.Time) (bool, error) {
	if artifactKey == "" {
		return false, fmt.Errorf("store: artifact key is empty")
	}
	if agentID == "" {
		return false, fmt.Errorf("store: agent id is empty")
	}

	// Attempt the insert. On conflict, bump the count and return seen=false.
	var inserted int
	err := l.db.Handle().QueryRow(`
		INSERT INTO artifact_sightings (artifact_key, first_seen_at, first_agent_id, seen_count)
		VALUES (?, ?, ?, 1)
		ON CONFLICT(artifact_key) DO UPDATE SET seen_count = seen_count + 1
		RETURNING seen_count
	`, artifactKey, timeToUnix(at), agentID).Scan(&inserted)
	if err != nil {
		return false, fmt.Errorf("store: observe: %w", err)
	}

	// seen_count == 1 means this call created the row, so it was the first sighting.
	return inserted == 1, nil
}

// Lookup returns the sighting for an artifact.
func (l *SQLArtifactLedger) Lookup(artifactKey string) (scoring.Sighting, bool) {
	var (
		key       string
		seenAt    int64
		agentID   string
		seenCount int64
	)

	err := l.db.Handle().QueryRow(`
		SELECT artifact_key, first_seen_at, first_agent_id, seen_count
		FROM artifact_sightings WHERE artifact_key = ?
	`, artifactKey).Scan(&key, &seenAt, &agentID, &seenCount)

	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return scoring.Sighting{}, false
	}

	return scoring.Sighting{
		ArtifactKey:  key,
		FirstSeenAt:  unixToTime(seenAt),
		FirstAgentID: agentID,
		SeenCount:    uint64(seenCount),
	}, true
}

// Len returns the number of distinct artifacts.
func (l *SQLArtifactLedger) Len() int {
	var n int
	if err := l.db.Handle().QueryRow(`SELECT COUNT(*) FROM artifact_sightings`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// SQLPointsLedger is a durable scoring.PointsLedger.
//
// It exposes no transfer capability, matching the in-memory implementation and
// invariant A5. The schema has no balance column to move between rows, and the
// only write path appends a credit keyed by receipt id.
type SQLPointsLedger struct {
	db *DB
}

// NewPointsLedger wraps db as a scoring.PointsLedger.
func NewPointsLedger(db *DB) *SQLPointsLedger {
	return &SQLPointsLedger{db: db}
}

var _ scoring.PointsLedger = (*SQLPointsLedger)(nil)

// Credit records points for a receipt, idempotently per receipt id.
func (l *SQLPointsLedger) Credit(receiptID, agentID string, epoch uint64, points float64, at time.Time) (bool, error) {
	if receiptID == "" {
		return false, fmt.Errorf("store: receipt id is empty")
	}
	if agentID == "" {
		return false, fmt.Errorf("store: agent id is empty")
	}
	if points <= 0 {
		// A zero award is noise; the caller already knows the verdict was a miss.
		return false, nil
	}

	micro := int64(points*scoring.MicroPerPoint + 0.5)
	if micro <= 0 {
		return false, nil
	}

	res, err := l.db.Handle().Exec(`
		INSERT INTO point_entries (receipt_id, agent_id, epoch, micro_points, credited_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(receipt_id) DO NOTHING
	`, receiptID, agentID, epoch, micro, timeToUnix(at))
	if err != nil {
		return false, fmt.Errorf("store: credit: %w", err)
	}

	// RowsAffected is 0 when the conflict clause skipped a duplicate, which is
	// exactly the idempotency signal the interface asks for.
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: credit rows: %w", err)
	}
	return n > 0, nil
}

// Balance returns an agent's lifetime points.
func (l *SQLPointsLedger) Balance(agentID string) float64 {
	var micro sql.NullInt64
	err := l.db.Handle().QueryRow(
		`SELECT SUM(micro_points) FROM point_entries WHERE agent_id = ?`, agentID,
	).Scan(&micro)
	if err != nil || !micro.Valid {
		return 0
	}
	return float64(micro.Int64) / scoring.MicroPerPoint
}

// EpochBalance returns an agent's points within one epoch.
func (l *SQLPointsLedger) EpochBalance(agentID string, epoch uint64) float64 {
	var micro sql.NullInt64
	err := l.db.Handle().QueryRow(
		`SELECT SUM(micro_points) FROM point_entries WHERE agent_id = ? AND epoch = ?`,
		agentID, epoch,
	).Scan(&micro)
	if err != nil || !micro.Valid {
		return 0
	}
	return float64(micro.Int64) / scoring.MicroPerPoint
}

// Entry returns the entry for a receipt.
func (l *SQLPointsLedger) Entry(receiptID string) (scoring.Entry, bool) {
	var (
		id       string
		agentID  string
		epoch    int64
		micro    int64
		credited int64
	)

	err := l.db.Handle().QueryRow(`
		SELECT receipt_id, agent_id, epoch, micro_points, credited_at
		FROM point_entries WHERE receipt_id = ?
	`, receiptID).Scan(&id, &agentID, &epoch, &micro, &credited)

	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return scoring.Entry{}, false
	}

	return scoring.Entry{
		ReceiptID:  id,
		AgentID:    agentID,
		Epoch:      uint64(epoch),
		Points:     float64(micro) / scoring.MicroPerPoint,
		CreditedAt: unixToTime(credited),
	}, true
}

// Entries returns every entry in credit order.
func (l *SQLPointsLedger) Entries() []scoring.Entry {
	rows, err := l.db.Handle().Query(`
		SELECT receipt_id, agent_id, epoch, micro_points, credited_at
		FROM point_entries ORDER BY credited_at, receipt_id
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []scoring.Entry
	for rows.Next() {
		var (
			id       string
			agentID  string
			epoch    int64
			micro    int64
			credited int64
		)
		if err := rows.Scan(&id, &agentID, &epoch, &micro, &credited); err != nil {
			return nil
		}
		out = append(out, scoring.Entry{
			ReceiptID:  id,
			AgentID:    agentID,
			Epoch:      uint64(epoch),
			Points:     float64(micro) / scoring.MicroPerPoint,
			CreditedAt: unixToTime(credited),
		})
	}
	return out
}

// AgentCount returns the number of distinct credited agents.
func (l *SQLPointsLedger) AgentCount() int {
	var n int
	if err := l.db.Handle().QueryRow(
		`SELECT COUNT(DISTINCT agent_id) FROM point_entries`,
	).Scan(&n); err != nil {
		return 0
	}
	return n
}
