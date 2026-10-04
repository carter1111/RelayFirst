// Package store provides the miner-side durable stores: receipts, the global
// artifact dedup ledger, and the points ledger.
//
// # Relationship to internal/sqlite and internal/protocol
//
// The database handle and the schema live in internal/sqlite, and the wire format
// lives in internal/protocol. Both were split out for the same reason: this package
// imports internal/receipt, which links eip712 and secp256k1, and the thin node must
// not be able to reach any of that (MVP.md §7.1).
//
// Re-exporting the handle here keeps the miner-side call sites unchanged while the
// node imports internal/sqlite directly. That way the dependency direction is
// enforced by the compiler rather than by a comment.
package store

import (
	"time"

	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// DB is the database handle (internal/sqlite).
//
// It is aliased rather than re-declared so the many existing call sites
// (`*store.DB`) keep working while the definition lives where the node can reach it.
type DB = sqlite.DB

// Open opens (or creates) a SQLite database at path and applies the schema.
//
// Pass ":memory:" for an ephemeral database, which is what tests use.
func Open(path string) (*DB, error) { return sqlite.Open(path) }

// unixToTime converts a stored unix second count to a time.
func unixToTime(sec int64) time.Time { return sqlite.UnixToTime(sec) }

// timeToUnix converts a time to stored unix seconds.
func timeToUnix(t time.Time) int64 { return sqlite.TimeToUnix(t) }
