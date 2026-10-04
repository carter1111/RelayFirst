// Package sqlite provides the database handle and schema shared by every
// RelayFirst binary.
//
// # Why this is separate from internal/store
//
// internal/store holds the miner-side stores (receipts, the dedup ledger, the
// points ledger), and those necessarily import internal/receipt — a receipt's
// signature is verified with eip712, which links secp256k1.
//
// The node must not be able to verify a signature (MVP.md §7.1), and that has to
// be a property of the import graph rather than of reviewer attention. As long as
// "open the database" lived in a package that imports receipt, every node binary
// pulled in the whole signing stack transitively. Moving the handle and schema
// here breaks that link: this package depends only on database/sql and the pure-Go
// SQLite driver, so anything importing it — the node included — cannot reach
// verification code.
//
// # Introducing no CGO
//
// modernc.org/sqlite is the pure-Go translation, not the CGO binding. That is
// forced by invariant A3, and it is what lets the node ship as a static binary
// behind one `docker run`.
package sqlite

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Open opens (or creates) a SQLite database at path and applies the schema.
//
// Pass ":memory:" for an ephemeral database, which is what tests use. The schema
// is applied idempotently, so opening an existing database is safe.
func Open(path string) (*DB, error) {
	handle, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	// SQLite is single-writer. Serializing on one connection avoids "database is
	// locked" failures without needing retry loops, and the workloads here are
	// append-heavy rather than high-concurrency.
	handle.SetMaxOpenConns(1)

	if err := handle.Ping(); err != nil {
		handle.Close()
		return nil, fmt.Errorf("store: ping %s: %w", path, err)
	}

	db := &DB{handle: handle}
	if err := db.migrate(); err != nil {
		handle.Close()
		return nil, err
	}
	return db, nil
}

// DB is a handle to the underlying store.
type DB struct {
	handle *sql.DB
}

// Close releases the database.
func (db *DB) Close() error {
	if db == nil || db.handle == nil {
		return nil
	}
	return db.handle.Close()
}

// Handle exposes the raw handle for callers that need it (for example the mining
// package persisting receipts). It is intentionally narrow: prefer a typed method.
func (db *DB) Handle() *sql.DB { return db.handle }

// schema is applied on every Open. Each statement is idempotent.
//
// Every table any binary needs lives here rather than in the package that uses it.
// One file, one schema, one place to reason about — and it means a node and a miner
// can share a single database file without either needing the other's code.
//
// Note the deliberate absence of any points-transfer table or column. Points are
// append-only credits keyed by receipt id (invariant A5). There is nowhere to move
// them, by construction rather than by policy.
const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

-- Global artifact dedup ledger (invariant A6).
--
-- The primary key is the artifact key ALONE. There is no agent column in the key,
-- which is what makes the ledger global: the second agent to observe a given piece
-- of content collides with the first and earns nothing.
CREATE TABLE IF NOT EXISTS artifact_sightings (
    artifact_key   TEXT PRIMARY KEY,
    first_seen_at  INTEGER NOT NULL,
    first_agent_id TEXT    NOT NULL,
    seen_count     INTEGER NOT NULL DEFAULT 1
);

-- Append-only points credits (invariant A5).
--
-- receipt_id is the primary key, so crediting the same receipt twice is a no-op
-- at the storage layer, not merely at the caller.
CREATE TABLE IF NOT EXISTS point_entries (
    receipt_id   TEXT    PRIMARY KEY,
    agent_id     TEXT    NOT NULL,
    epoch        INTEGER NOT NULL,
    micro_points INTEGER NOT NULL,
    credited_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS point_entries_agent_idx ON point_entries (agent_id);
CREATE INDEX IF NOT EXISTS point_entries_epoch_idx ON point_entries (agent_id, epoch);

-- Receipts produced by mining (S2-8).
--
-- The canonical JSON is stored verbatim alongside a few indexed columns. Storing
-- the bytes rather than a shredded schema keeps the signed payload byte-exact,
-- which is what lets a stored receipt still verify later.
CREATE TABLE IF NOT EXISTS receipts (
    receipt_id   TEXT    PRIMARY KEY,
    agent_id     TEXT    NOT NULL,
    epoch        INTEGER NOT NULL,
    task_type    TEXT    NOT NULL,
    artifact_key TEXT,
    body         TEXT    NOT NULL,
    created_at   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS receipts_agent_idx ON receipts (agent_id, epoch);
CREATE INDEX IF NOT EXISTS receipts_artifact_idx ON receipts (artifact_key);

-- Store-and-forward messages held by a thin node (S5, MVP.md §7.1).
--
-- The node is deliberately DUMB: it never parses, verifies or interprets the
-- payload. It only needs two keys to index on, and both are supplied by the
-- sender in the envelope rather than extracted by reading the receipt. That is
-- what lets the same table hold future non-receipt message kinds without change.
--
-- receipt_id (the envelope's id) is the primary key, so a redelivery is a no-op
-- at the storage layer rather than merely at the handler (S5-3).
CREATE TABLE IF NOT EXISTS messages (
    receipt_id  TEXT    PRIMARY KEY,
    agent_id    TEXT    NOT NULL,
    kind        TEXT    NOT NULL,
    payload     BLOB    NOT NULL,
    received_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS messages_agent_idx ON messages (agent_id, received_at DESC);

-- Verification commitments (S4).
--
-- # What this table is NOT
--
-- It holds no balance and moves no points. Invariant A5 makes points
-- non-transferable, and a test pins the points ledger's method set to exactly six
-- methods with no debit verb. So a commitment is recorded here as a *magnitude with a
-- lifecycle state*, never as a deduction. The verb "slashed" means the commitment was
-- surrendered and recorded, not that points were taken from anyone.
--
-- Note the absence of any operation that could turn the amount column into points:
-- there is no path from this table to point_entries, and adding one would require a
-- points-moving method that the A5 guard forbids.
CREATE TABLE IF NOT EXISTS verification_commitments (
    id           TEXT    PRIMARY KEY,
    agent_id     TEXT    NOT NULL,
    epoch        INTEGER NOT NULL,
    amount       INTEGER NOT NULL,
    state        TEXT    NOT NULL,
    committed_at INTEGER NOT NULL,
    settled_at   INTEGER
);

CREATE INDEX IF NOT EXISTS commitments_agent_idx ON verification_commitments (agent_id, epoch);

-- Verification verdicts (S4).
--
-- Records what a verifier concluded about a receipt, so scoring can read the verdict
-- instead of trusting the submitter. It deliberately stores no amount: a verdict is an
-- opinion about work, not a quantity of value.
CREATE TABLE IF NOT EXISTS verification_verdicts (
    receipt_id      TEXT    PRIMARY KEY,
    agent_id        TEXT    NOT NULL,
    verifier_id     TEXT    NOT NULL,
    status          TEXT    NOT NULL,
    recomputed_hash TEXT,
    verified_at     INTEGER,
    recorded_at     INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS verdicts_verifier_idx ON verification_verdicts (verifier_id);

-- Published A2A agent cards (S9-3).
--
-- # Why the node stores the card as opaque bytes
--
-- The card is a signed document: its proof covers an exact byte sequence
-- (internal/publish/card_proof.go). A node that parsed and re-serialized a card
-- would silently invalidate that proof — the card would still look correct and
-- would fail verification somewhere else, later, which is the worst failure mode.
-- So the bytes are stored and returned verbatim, and the two indexed columns the
-- node needs are supplied by the publisher rather than extracted by reading the
-- card.
--
-- # Why this table holds no verification state
--
-- The node does not verify card proofs (MVP.md §7.1: a node cannot check
-- signatures). Nothing here records "this card is valid" because the node is in no
-- position to know that. A client fetches the card and its proof and decides for
-- itself; the node is a directory, not an authority.
--
-- agent_id is the primary key, so re-publishing a card for the same agent replaces
-- it. That is the intended behaviour: a card is current state, not a log. An agent
-- that rotates its endpoint should not leave a stale card for readers to find.
CREATE TABLE IF NOT EXISTS agent_cards (
    agent_id     TEXT    PRIMARY KEY,
    card         BLOB    NOT NULL,
    proof        BLOB    NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS agent_cards_updated_idx ON agent_cards (updated_at DESC);
`

func (db *DB) migrate() error {
	if _, err := db.handle.Exec(schema); err != nil {
		return fmt.Errorf("store: apply schema: %w", err)
	}
	return nil
}

// UnixToTime converts a stored unix second count to a time.
func UnixToTime(sec int64) time.Time { return time.Unix(sec, 0).UTC() }

// TimeToUnix converts a time to stored unix seconds.
func TimeToUnix(t time.Time) int64 { return t.Unix() }
