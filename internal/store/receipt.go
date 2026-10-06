package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/scoring"
)

// ReceiptStore persists receipts.
//
// # Why the whole canonical body is stored, not a shredded schema
//
// The signature covers a canonical serialization of the receipt. If the stored
// form were reassembled from columns, any reassembly discrepancy — field order,
// number formatting, a nullable column round-tripping differently — would
// invalidate the signature of a receipt that was perfectly valid when written.
// Keeping the canonical bytes verbatim means a receipt read back still verifies,
// which is the entire point of persisting it.
//
// The extra columns are indexes, not the source of truth.
type ReceiptStore struct {
	db *DB
}

// NewReceiptStore wraps db.
func NewReceiptStore(db *DB) *ReceiptStore {
	return &ReceiptStore{db: db}
}

// Save persists r.
//
// The artifact key is optional: receipts for tasks whose artifact cannot be
// identified are still worth storing, and a NULL column is more honest than a
// fabricated key.
func (s *ReceiptStore) Save(r *receipt.Receipt, artifactKey string, at time.Time) error {
	if r == nil {
		return fmt.Errorf("store: nil receipt")
	}
	if r.ReceiptID == "" {
		return fmt.Errorf("store: receipt id is empty")
	}

	body, err := r.MarshalCanonical()
	if err != nil {
		return fmt.Errorf("store: marshal receipt: %w", err)
	}

	var artifact any
	if artifactKey != "" {
		artifact = artifactKey
	}

	// Conflict is a no-op rather than an error: replaying a receipt must not be
	// able to alter a stored one. Receipts are immutable once signed.
	_, err = s.db.Handle().Exec(`
		INSERT INTO receipts (receipt_id, agent_id, epoch, task_type, artifact_key, body, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(receipt_id) DO NOTHING
	`, r.ReceiptID, r.AgentID, r.Epoch, string(r.Task.Type), artifact, string(body), timeToUnix(at))
	if err != nil {
		return fmt.Errorf("store: save receipt: %w", err)
	}
	return nil
}

// Load returns the receipt with the given id.
func (s *ReceiptStore) Load(receiptID string) (*receipt.Receipt, bool, error) {
	var body string
	err := s.db.Handle().QueryRow(
		`SELECT body FROM receipts WHERE receipt_id = ?`, receiptID,
	).Scan(&body)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: load receipt: %w", err)
	}

	r, err := receipt.Unmarshal([]byte(body))
	if err != nil {
		// A stored receipt that no longer parses means the schema changed under
		// it. Surfacing the error is right: silently skipping it would hide data
		// loss.
		return nil, false, fmt.Errorf("store: parse stored receipt %s: %w", receiptID, err)
	}
	return r, true, nil
}

// ByAgent returns an agent's receipts for an epoch, newest first.
func (s *ReceiptStore) ByAgent(agentID string, epoch uint64) ([]*receipt.Receipt, error) {
	return s.query(`SELECT body FROM receipts WHERE agent_id = ? AND epoch = ? ORDER BY created_at DESC`,
		agentID, epoch)
}

// ByArtifact returns every receipt that observed an artifact key.
func (s *ReceiptStore) ByArtifact(artifactKey string) ([]*receipt.Receipt, error) {
	if artifactKey == "" {
		return nil, fmt.Errorf("store: artifact key is empty")
	}
	return s.query(`SELECT body FROM receipts WHERE artifact_key = ? ORDER BY created_at DESC`, artifactKey)
}

// SaveVerification updates a stored receipt's verification block.
//
// # Why this is separate from Save
//
// Save writes a receipt once and ignores a conflicting id, because a signed receipt is
// immutable. The verification block is the opposite: it is NOT part of the signed
// payload (MVP.md §4.2), it is filled in after the fact by a verifier, and it is
// expected to change from pending to verified or rejected. So it needs its own update
// path, and it must not be able to touch the signed bytes.
//
// The body is re-serialized and rewritten, which is safe precisely because the signature
// covers only the fields before verification. A test asserts that a re-serialized receipt
// still verifies, so a future change to the canonical writer cannot silently invalidate
// stored receipts through this path.
func (s *ReceiptStore) SaveVerification(r *receipt.Receipt) error {
	if r == nil {
		return fmt.Errorf("store: nil receipt")
	}
	if r.ReceiptID == "" {
		return fmt.Errorf("store: receipt id is empty")
	}

	body, err := r.MarshalCanonical()
	if err != nil {
		return fmt.Errorf("store: marshal receipt: %w", err)
	}

	res, err := s.db.Handle().Exec(
		`UPDATE receipts SET body = ? WHERE receipt_id = ?`, string(body), r.ReceiptID)
	if err != nil {
		return fmt.Errorf("store: save verification: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: save verification rows: %w", err)
	}
	if n == 0 {
		// Updating a receipt that is not stored would silently discard the verdict, so it
		// is reported instead.
		return fmt.Errorf("store: receipt %s is not stored, so its verification cannot be recorded", r.ReceiptID)
	}
	return nil
}

// ByEpoch returns every receipt recorded in one epoch, oldest first.
//
// It exists for the anchoring path, which previously read every receipt and filtered
// in memory. That is fine at development scale and wasteful at real scale, and — more
// importantly — the filtering happened in Go, so a mistake there would silently change
// which receipts belong to a root. Pushing it into SQL means the selection is one
// query that can be reasoned about on its own.
func (s *ReceiptStore) ByEpoch(epoch uint64) ([]*receipt.Receipt, error) {
	return s.query(`SELECT body FROM receipts WHERE epoch = ? ORDER BY receipt_id`, epoch)
}

// CountByEpochAndAgent returns how many receipts one agent recorded in one epoch.
//
// It answers the anti-collusion ratio question ("how much did this agent produce?")
// with a COUNT rather than by loading bodies, because the caller only needs the number
// and never the receipts.
func (s *ReceiptStore) CountByEpochAndAgent(epoch uint64, agentID string) (int, error) {
	var n int
	err := s.db.Handle().QueryRow(
		`SELECT COUNT(*) FROM receipts WHERE epoch = ? AND agent_id = ?`, epoch, agentID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count receipts: %w", err)
	}
	return n, nil
}

// CountVerifiedByEpochAndAgent returns how many of an agent's receipts in one epoch
// carry a given verification status.
//
// # Why this reads the stored JSON
//
// The verification block is not part of the signed payload, so it is not sharded into
// columns — it lives inside the stored body. SQLite can read it with json_extract,
// which keeps the query in SQL rather than loading every body into Go to count them.
// If the schema ever changes, this expression and the canonical writer have to move
// together, which is why the test asserts the agreed value rather than a merely
// plausible one.
func (s *ReceiptStore) CountVerifiedByEpochAndAgent(epoch uint64, agentID, status string) (int, error) {
	if status == "" {
		return 0, fmt.Errorf("store: verification status is empty")
	}
	var n int
	err := s.db.Handle().QueryRow(
		`SELECT COUNT(*) FROM receipts
		 WHERE epoch = ? AND agent_id = ? AND json_extract(body, '$.verification.status') = ?`,
		epoch, agentID, status,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count verified receipts: %w", err)
	}
	return n, nil
}

// CountAnchorsByAgent returns the number of distinct, externally re-fetchable anchor
// content hashes across one agent's receipts.
//
// # Why this is a query rather than a loop
//
// The mining CLI displays an anchor count on every iteration. Computing it by loading every
// stored receipt and counting in Go means the cost grows with the agent's entire history, on
// a loop that runs every few seconds — so a long-running miner would get steadily slower for
// no reason. This answers the same question in one query.
//
// # Which anchors count
//
// Synthetic "inline" anchors are excluded: compute tasks capture no external evidence, so
// counting them would inflate a number whose whole purpose is "how much independently
// checkable evidence do I have". An inflated count is worse than no count.
func (s *ReceiptStore) CountAnchorsByAgent(agentID string) (int, error) {
	if agentID == "" {
		return 0, fmt.Errorf("store: agent id is empty")
	}

	// json_each expands the anchors array so each anchor becomes a row, which is what lets
	// DISTINCT work on the content hash. The alternative — one row per receipt with a
	// JSON-encoded array — cannot be de-duplicated in SQL.
	var n int
	err := s.db.Handle().QueryRow(`
		SELECT COUNT(DISTINCT json_extract(a.value, '$.contentHash'))
		FROM receipts r, json_each(r.body, '$.anchors') a
		WHERE r.agent_id = ?
		  AND json_extract(a.value, '$.url') IS NOT NULL
		  AND json_extract(a.value, '$.url') != 'inline'
		  AND json_extract(a.value, '$.contentHash') IS NOT NULL
	`, agentID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: count anchors: %w", err)
	}
	return n, nil
}

// Count returns the number of stored receipts.
func (s *ReceiptStore) Count() int {
	var n int
	if err := s.db.Handle().QueryRow(`SELECT COUNT(*) FROM receipts`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// All returns every stored receipt, oldest first.
//
// Oldest-first is deliberate for the export path: a user taking their data away
// should get it in the order it was earned, so the file reads as a chronological
// record rather than an arbitrary shuffle.
//
// A limit of zero or less means every receipt.
func (s *ReceiptStore) All(limit int) ([]*receipt.Receipt, error) {
	q := `SELECT body FROM receipts ORDER BY created_at, receipt_id`
	var args []any
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	return s.query(q, args...)
}

// ByAgentAll returns all of one agent's receipts, oldest first.
//
// It is separate from ByAgent (which is epoch-scoped) because the export and
// status paths need lifetime totals, and passing a zero epoch there would silently
// select only epoch 0.
func (s *ReceiptStore) ByAgentAll(agentID string) ([]*receipt.Receipt, error) {
	if agentID == "" {
		return nil, fmt.Errorf("store: agent id is empty")
	}
	return s.query(`SELECT body FROM receipts WHERE agent_id = ? ORDER BY created_at, receipt_id`, agentID)
}

// AnchorsFor counts the distinct anchor content hashes across receipts.
//
// It answers "how many pieces of external evidence did this work rest on", which
// is the number a miner cares about: one receipt can carry several anchors, and a
// receipt with none is not valid work at all.
func (s *ReceiptStore) AnchorsFor(receipts []*receipt.Receipt) int {
	seen := map[string]struct{}{}
	for _, r := range receipts {
		if r == nil {
			continue
		}
		for _, a := range r.Anchors {
			// Compute anchors use a synthetic "inline" URL with no external
			// content, so they carry no independently re-fetchable evidence and
			// are not counted. Counting them would inflate the number a farmer
			// reads without adding any real verification value.
			if a.URL == "inline" {
				continue
			}
			if a.ContentHash == "" {
				continue
			}
			seen[a.ContentHash] = struct{}{}
		}
	}
	return len(seen)
}

func (s *ReceiptStore) query(q string, args ...any) ([]*receipt.Receipt, error) {
	rows, err := s.db.Handle().Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query receipts: %w", err)
	}
	defer rows.Close()

	var out []*receipt.Receipt
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, fmt.Errorf("store: scan receipt: %w", err)
		}
		r, err := receipt.Unmarshal([]byte(body))
		if err != nil {
			return nil, fmt.Errorf("store: parse stored receipt: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate receipts: %w", err)
	}
	return out, nil
}

// ScoringLedgers bundles the two ledgers a scoring run needs.
//
// It exists so a caller cannot accidentally pair an in-memory dedup ledger with a
// durable points ledger, which would let a restart re-credit work that the
// in-memory side forgot.
type ScoringLedgers struct {
	Artifacts scoring.Ledger
	// Work accumulates the settlement input (D1). A receipt contributes work, and an
	// epoch's points come from settling the work totals.
	Work   scoring.WorkLedger
	Points scoring.PointsLedger
}

// NewScoringLedgers returns durable ledgers backed by db.
func NewScoringLedgers(db *DB) ScoringLedgers {
	return ScoringLedgers{
		Artifacts: NewArtifactLedger(db),
		Work:      NewWorkLedger(db),
		Points:    NewPointsLedger(db),
	}
}
