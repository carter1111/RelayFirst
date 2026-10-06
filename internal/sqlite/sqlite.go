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
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// DefaultBusyTimeoutMillis is how long SQLite waits for a lock before returning
// SQLITE_BUSY.
//
// # Why the value is applied per connection rather than once
//
// `PRAGMA busy_timeout` is PER-CONNECTION, and the pool creates connections lazily.
// Setting it once through `db.Exec` therefore reaches only the one connection that
// happened to serve that call; every other pooled connection keeps the driver
// default of 0. That was measured: connection 0 reported `busy_timeout=5000` while
// connections 1-3 reported 0, and under a wider pool concurrent writers then failed
// with "database is locked" before ever reaching their statement.
//
// The DSN parameter is how the modernc driver applies a pragma to EVERY connection
// it opens, so this is set at open time rather than by a post-open Exec.
const DefaultBusyTimeoutMillis = 5000

// Open opens (or creates) a SQLite database at path and applies the schema.
//
// Pass ":memory:" for an ephemeral database, which is what tests use. The schema
// is applied idempotently, so opening an existing database is safe.
func Open(path string) (*DB, error) {
	handle, err := sql.Open("sqlite", withPragmas(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	// SQLite allows one writer at a time. Production serializes writes on a single
	// connection, which avoids lock contention without retry loops; the busy timeout
	// above is set anyway so that a caller which raises the limit (a measurement, or
	// a future wider write pool) does not silently lose it per connection.
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

// withPragmas returns the DSN for path with the per-connection pragmas applied.
//
// # Why a DSN rather than a post-open Exec
//
// A `PRAGMA` sent through `db.Exec` runs on ONE pooled connection, so any pragma set
// that way is lost for connections opened later. The DSN form is applied by the
// driver to every connection it opens, which is what makes the setting a property of
// the database handle rather than of whichever connection served one call.
//
// # The forms of `path` that must be preserved
//
// The driver accepts a plain filename, a `:memory:` database, and a `file:` URI with
// its own parameters. The pragmas are added in the form each expects:
//
//   - `:memory:` is left untouched. It needs no lock wait (nothing else can see it),
//     and appending `?` would change which database the driver opens.
//   - a `file:` URI gets the parameter appended with `&` (or `?` if it has none).
//   - a plain path is wrapped as `file:<path>?...`, which is the URI form the driver
//     recognises for parameters.
//
// The escaping is deliberate: a `?` or `#` in a filename would otherwise be read as
// the start of the parameter list, so the path is percent-encoded first.
func withPragmas(path string) string {
	pragmas := fmt.Sprintf("_pragma=busy_timeout(%d)", DefaultBusyTimeoutMillis)

	if path == ":memory:" {
		return path
	}
	if strings.HasPrefix(path, "file:") {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		return path + sep + pragmas
	}
	return "file:" + escapeDSNPath(path) + "?" + pragmas
}

// escapeDSNPath percent-encodes the characters that would otherwise be read as DSN
// syntax, so a filename containing `?`, `#` or `%` opens the file it names.
func escapeDSNPath(path string) string {
	return strings.NewReplacer(
		"%", "%25",
		"?", "%3F",
		"#", "%23",
	).Replace(path)
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

-- ACCUMULATED WORK, before settlement (MVP.md §5.3 -> §6.2; D1).
--
-- This is the settlement INPUT and the audit trail: an epoch's points are the budget
-- shared by these work totals, so the totals must be recomputable from durable records
-- and "why did this agent get X" must be answerable. Points are derived from this
-- table; keeping only points would discard the inputs.
--
-- Keyed by receipt_id so recording the same receipt twice is a no-op (the dedup ledger
-- already gates novelty, but idempotency here means a retry cannot double a total).
-- micro_work is fixed-point for the same reason micro_points is: accumulating floats
-- drifts.
CREATE TABLE IF NOT EXISTS work_records (
    receipt_id   TEXT    PRIMARY KEY,
    agent_id     TEXT    NOT NULL,
    epoch        INTEGER NOT NULL,
    micro_work   INTEGER NOT NULL,
    artifact_key TEXT    NOT NULL,
    recorded_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS work_records_agent_idx ON work_records (agent_id, epoch);
CREATE INDEX IF NOT EXISTS work_records_epoch_idx ON work_records (epoch);

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

-- Observation index (S10-1).
--
-- # Why the node may parse a receipt for THIS table
--
-- MVP.md §7.1 says the node does not verify signatures, and that stands. Indexing is a
-- different act: it reads a few fields to make the work findable, and it makes no claim
-- about whether the work is real. §7.3 puts it plainly — indexing and querying do not
-- break security, because the data can be re-verified by whoever retrieves it.
--
-- The fields are extracted by JSON path, never by linking internal/receipt, which the
-- import-graph gate enforces. That is the structural expression of "index, do not verify":
-- the node cannot check a signature because it cannot reach the code that would.
--
-- # Why there is no validity column
--
-- A boolean here would be a judgement the node is in no position to make, and a consumer
-- reading it would be trusting the node's opinion. A forged receipt is indexed like any
-- other; the consumer's signature check is what discards it.
--
-- The primary key is the receipt id, so the same receipt arriving twice (a retry, or a
-- second path) indexes once. Two rows for one receipt would make the cross-verification
-- count wrong, and that count is the number a consumer actually reads.
CREATE TABLE IF NOT EXISTS observations (
    receipt_id   TEXT    PRIMARY KEY,
    subject      TEXT    NOT NULL,
    content_hash TEXT    NOT NULL,
    result_hash  TEXT    NOT NULL,
    agent_id     TEXT    NOT NULL,
    task_type    TEXT    NOT NULL,
    epoch        INTEGER NOT NULL,
    indexed_at   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS observations_subject_idx ON observations (subject, epoch DESC);
CREATE INDEX IF NOT EXISTS observations_agent_idx ON observations (agent_id);

-- Task board (S10-3).
--
-- # Why the node records offers without judging them
--
-- It stores a task announcement so executors can find it. It does not verify the requester,
-- does not grant exclusivity, and does not enforce expiry — MVP.md §7.3 puts the task relay
-- in the "does not break security" column precisely because the two agents verify each
-- other and the node's opinion is not load-bearing.
--
-- # Why expiry is stored but never filtered on
--
-- ARCHITECTURE.md §4.5 makes expiry a protocol transition decided from signed data. A relay
-- filtering by its own clock would be inventing an authority it does not have, and two
-- relays with skewed clocks would disagree about whether an offer was open. The column is
-- returned so a client can apply its own clock.
CREATE TABLE IF NOT EXISTS task_offers (
    task_id    TEXT    PRIMARY KEY,
    requester  TEXT    NOT NULL,
    subject    TEXT    NOT NULL,
    spec       BLOB,
    expires_at INTEGER,
    offered_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS task_offers_subject_idx ON task_offers (subject, offered_at DESC);

-- Claims are recorded as INTEREST, not as grants.
--
-- # Why there is no "winner" column
--
-- The node cannot know who won: it has no authority to grant a task, and giving it one
-- would make it a market operator rather than a relay. What resolves a competition is the
-- protocol — the requester accepts one offer, and the others' work has no receipt the
-- requester acknowledges.
--
-- The primary key is (task_id, claimant), so one agent claiming twice is a retry rather
-- than two claims. Counting it twice would inflate the interest a task appears to have,
-- which is the number a requester reads when deciding whether the board is alive.
CREATE TABLE IF NOT EXISTS task_claims (
    task_id    TEXT    NOT NULL,
    claimant   TEXT    NOT NULL,
    claimed_at INTEGER NOT NULL,
    PRIMARY KEY (task_id, claimant)
);

CREATE INDEX IF NOT EXISTS task_claims_task_idx ON task_claims (task_id, claimed_at);
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
