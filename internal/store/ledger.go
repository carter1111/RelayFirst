package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/relayfirst/relayfirst/internal/scoring"
	"github.com/relayfirst/relayfirst/internal/sqlite"
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

// CumulativeMicro returns each agent's points summed over every epoch up to and
// including maxEpoch, in micro-points.
//
// # Why the balance root uses cumulative totals, not this epoch's
//
// A claim proves "this agent earned N by this epoch" and SETS the on-chain total to N
// (contracts/RelayPoints.sol). N must therefore be cumulative through maxEpoch, not one
// epoch's award: a per-epoch N would set the total to just that epoch's share and erase
// every earlier epoch the agent did not happen to claim. The contract's own comment
// makes this the reason a missed claim costs nothing.
//
// The result is keyed by agent_id and carries integers, because the leaf's total is a
// uint256 and a float would round differently here and on-chain.
func (l *SQLPointsLedger) CumulativeMicro(maxEpoch uint64) (map[string]int64, error) {
	rows, err := l.db.Handle().Query(
		`SELECT agent_id, SUM(micro_points) FROM point_entries WHERE epoch <= ? GROUP BY agent_id`,
		maxEpoch,
	)
	if err != nil {
		return nil, fmt.Errorf("store: cumulative points: %w", err)
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var (
			agent string
			micro sql.NullInt64
		)
		if err := rows.Scan(&agent, &micro); err != nil {
			return nil, fmt.Errorf("store: scan cumulative points: %w", err)
		}
		if micro.Valid && micro.Int64 > 0 {
			out[agent] = micro.Int64
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate cumulative points: %w", err)
	}
	return out, nil
}

// SQLWorkLedger is a durable scoring.WorkLedger.
//
// It is the settlement input and the audit trail (D1): an epoch's points are derived
// from these records, so they must survive a restart for the settlement to be
// recomputable. Keyed by receipt id, so recording the same receipt twice is a no-op and
// a retry cannot double a total.
type SQLWorkLedger struct {
	db *DB
}

// NewWorkLedger wraps db as a scoring.WorkLedger.
func NewWorkLedger(db *DB) *SQLWorkLedger { return &SQLWorkLedger{db: db} }

var _ scoring.WorkLedger = (*SQLWorkLedger)(nil)

// Record appends one record, idempotently per receipt id.
func (l *SQLWorkLedger) Record(rec scoring.WorkRecord) (bool, error) {
	if rec.ReceiptID == "" {
		return false, fmt.Errorf("store: work record has no receipt id")
	}
	if rec.AgentID == "" {
		return false, fmt.Errorf("store: work record for %s has no agent", rec.ReceiptID)
	}
	if rec.Work <= 0 {
		// Non-positive work is noise; the caller already knows the verdict was a miss.
		return false, nil
	}

	micro := int64(rec.Work*scoring.MicroPerPoint + 0.5)
	if micro <= 0 {
		return false, nil
	}

	res, err := l.db.Handle().Exec(`
		INSERT INTO work_records (receipt_id, agent_id, epoch, micro_work, artifact_key, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(receipt_id) DO NOTHING
	`, rec.ReceiptID, rec.AgentID, rec.Epoch, micro, rec.ArtifactKey, timeToUnix(rec.RecordedAt))
	if err != nil {
		return false, fmt.Errorf("store: record work: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: record work rows: %w", err)
	}
	return n > 0, nil
}

// Totals returns each agent's summed work for an epoch.
func (l *SQLWorkLedger) Totals(epoch uint64) (map[string]float64, error) {
	rows, err := l.db.Handle().Query(
		`SELECT agent_id, SUM(micro_work) FROM work_records WHERE epoch = ? GROUP BY agent_id`, epoch,
	)
	if err != nil {
		return nil, fmt.Errorf("store: work totals: %w", err)
	}
	defer rows.Close()

	out := map[string]float64{}
	for rows.Next() {
		var agent string
		var micro int64
		if err := rows.Scan(&agent, &micro); err != nil {
			return nil, fmt.Errorf("store: scan work total: %w", err)
		}
		out[agent] = float64(micro) / scoring.MicroPerPoint
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate work totals: %w", err)
	}
	return out, nil
}

// ForAgent returns one agent's records for an epoch, in the order recorded.
func (l *SQLWorkLedger) ForAgent(agentID string, epoch uint64) ([]scoring.WorkRecord, error) {
	rows, err := l.db.Handle().Query(`
		SELECT receipt_id, agent_id, epoch, micro_work, artifact_key, recorded_at
		FROM work_records WHERE agent_id = ? AND epoch = ? ORDER BY recorded_at, receipt_id
	`, agentID, epoch)
	if err != nil {
		return nil, fmt.Errorf("store: work for agent: %w", err)
	}
	defer rows.Close()

	var out []scoring.WorkRecord
	for rows.Next() {
		var (
			rec   scoring.WorkRecord
			micro int64
			at    int64
		)
		if err := rows.Scan(&rec.ReceiptID, &rec.AgentID, &rec.Epoch, &micro, &rec.ArtifactKey, &at); err != nil {
			return nil, fmt.Errorf("store: scan work record: %w", err)
		}
		rec.Work = float64(micro) / scoring.MicroPerPoint
		rec.RecordedAt = sqlite.UnixToTime(at)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate work records: %w", err)
	}
	return out, nil
}

// Count returns the number of work records.
func (l *SQLWorkLedger) Count() int {
	var n int
	err := l.db.Handle().QueryRow(`SELECT COUNT(*) FROM work_records`).Scan(&n)
	if err != nil {
		return 0
	}
	return n
}
