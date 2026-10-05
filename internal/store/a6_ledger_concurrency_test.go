package store_test

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/sqlite"
	"github.com/relayfirst/relayfirst/internal/store"
)

// This file establishes, by measurement, WHERE invariant A6's atomicity comes from.
//
// # Why this is a test and not an argument
//
// The justification previously recorded for `SetMaxOpenConns(1)` was that it prevents a
// check-then-insert race — two concurrent deliveries both seeing "absent" and both inserting. That
// justification was written from reading the connection limit's comment, not from reading the write
// paths, and reading them showed something else: both paths are SINGLE-STATEMENT atomic upserts.
//
// So the question "does A6 need one connection?" has two possible answers with opposite consequences
// for the next phase, and the difference is not visible in prose. It is visible in a concurrent test.
// The same reasoning as measuring throughput before optimizing it: measure, do not infer.
//
// # What is actually being tested
//
// A6 says the artifact dedup ledger must be global: the second agent to observe a piece of content
// must be told it was not first, or farming by re-submitting is profitable. The property is violated
// if two concurrent observers of the SAME artifact are both told they were first.
//
// # Why this lives in store_test and not sqlite_test
//
// The artifact ledger is in internal/store, which imports internal/sqlite. Putting the test here
// keeps it beside the code it measures, and keeps the sqlite package's tests about the sqlite
// package. The connection-limit manipulation uses the exported Handle(), so no production API is
// added for the sake of a test.

// openWithConns opens a store and raises the connection limit.
//
// It uses the exported Handle() rather than a new production API: the point is to test the current
// code under a different pool size, not to add a knob for it. If a later phase concludes a knob is
// needed, that is a separate change with its own justification.
// The busy timeout is no longer set with a manual `PRAGMA`, which reached only one connection in the
// pool. It is now part of the DSN (internal/sqlite.withPragmas) so every pooled connection inherits
// it, which is the change that makes the measurements below reachable. See
// TestA6_ManyConnectionsCanWriteWithoutBusyErrors in internal/sqlite for the property that pins it.
func openWithConns(t *testing.T, conns int) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "a6.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	db.Handle().SetMaxOpenConns(conns)
	return db
}

// # The measured result, and what it means
//
// A FIRST measurement found that with 8 connections, 24 of 32 concurrent observers failed with
// `database is locked (SQLITE_BUSY)` BEFORE reaching the question of how many were told they were
// first. The cause was not the SQL and not the dedup logic: `PRAGMA busy_timeout` is PER-CONNECTION,
// and it was applied once through `db.Handle().Exec(...)`, so only one connection in the pool had it
// (connection 0 reported `busy_timeout=5000`, connections 1-3 reported 0).
//
// That obstacle has since been FIXED — the timeout is now part of the DSN
// (internal/sqlite.withPragmas), so every connection the pool opens inherits it. The measurement was
// then re-run with the pragma applied correctly, which is what the old note said was required before
// concluding anything:
//
//   - TestA6_ArtifactDedupHasExactlyOneWinner_ManyConnections (8 conns, 32 observers) now PASSES:
//     exactly one winner, so A6's atomicity comes from the single-statement upsert rather than from
//     the connection limit. That is the reason a wider write pool is even a possibility.
//   - TestA6_ManyDistinctArtifactsUnderContention confirms it is not a ledger that collapses every
//     key into one winner: 64 distinct artifacts each get their own first observer.
//
// # What this does NOT establish
//
// It does not mean production should widen the pool. Production still uses
// `SetMaxOpenConns(1)` (TestA6_SingleConnectionIsStillTheProductionDefault pins it), because
// single-writer serialization is correct and sufficient — the ingest benchmark saturates well below
// a contended SQLite. The measurement establishes that widening would be SAFE for A6, which is a
// precondition, not a reason.

// TestA6_ArtifactDedupHasExactlyOneWinner_OneConnection is the control: the property holds today.
func TestA6_ArtifactDedupHasExactlyOneWinner_OneConnection(t *testing.T) {
	assertExactlyOneWinner(t, openWithConns(t, 1), 32)
}

// TestA6_ManyConnectionsCanWriteWithoutBusyErrors pins the fix that made the measurement below
// reachable.
//
// # Why this asserts SUCCESS, when it used to assert the opposite
//
// It previously recorded the OBSERVED OBSTACLE: with `PRAGMA busy_timeout` applied once through one
// connection, 24 of 32 concurrent observers failed with "database is locked" before their statement
// ran. Moving the pragma into the DSN removes that obstacle, so the honest test is now that distinct
// writes across many connections all succeed. A regression in the DSN plumbing fails here.
func TestA6_ManyConnectionsCanWriteWithoutBusyErrors(t *testing.T) {
	db := openWithConns(t, 8)
	ledger := store.NewArtifactLedger(db)

	const observers = 16
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	start := make(chan struct{})

	for i := 0; i < observers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := ledger.Observe(fmt.Sprintf("sha256:busy-probe-%d", i), fmt.Sprintf("agent-%d", i), time.Now())
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("%d of %d concurrent observers errored; the busy timeout must apply to every "+
			"connection the pool opens, or A6 cannot be measured under a wider pool: %v",
			len(errs), observers, errs[0])
	}
}

// TestA6_ArtifactDedupHasExactlyOneWinner_ManyConnections is the measurement a wider write pool
// depends on: does the dedup still have exactly one winner across 8 connections?
//
// It was skipped while the busy timeout obstacle rejected writers before their statement ran. With
// that fixed it runs, and it is the evidence that A6's atomicity comes from the single-statement
// upsert rather than from the connection limit — the reason a wider pool is even a possibility.
func TestA6_ArtifactDedupHasExactlyOneWinner_ManyConnections(t *testing.T) {
	assertExactlyOneWinner(t, openWithConns(t, 8), 32)
}

// assertExactlyOneWinner drives n goroutines at one artifact key and counts how many were told they
// were first.
func assertExactlyOneWinner(t *testing.T, db *sqlite.DB, n int) {
	t.Helper()

	ledger := store.NewArtifactLedger(db)
	const artifactKey = "sha256:a6-concurrency-fixture"

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners int
		errs    []error
	)

	// A start barrier so the goroutines contend rather than running one after another. Without it the
	// first would finish before the second began, and the test would pass while proving nothing.
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			first, err := ledger.Observe(artifactKey, fmt.Sprintf("agent-%d", i), time.Now())

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			if first {
				winners++
			}
		}(i)
	}

	close(start)
	wg.Wait()

	// A busy error would mean the test measured contention rather than the property, so it is
	// reported separately from a duplicate winner.
	if len(errs) > 0 {
		t.Fatalf("%d of %d observers errored, which means this measured lock contention rather than "+
			"A6; the first error was: %v", len(errs), n, errs[0])
	}
	if winners != 1 {
		t.Fatalf("%d of %d concurrent observers were told they were first for one artifact; "+
			"A6 requires exactly 1, because a second winner means re-submitting the same content earns "+
			"points and the play is broken", winners, n)
	}

	// And the ledger must agree: one sighting, with a count equal to the number of observers.
	got, ok := ledger.Lookup(artifactKey)
	if !ok {
		t.Fatal("the artifact must be recorded")
	}
	if got.SeenCount != uint64(n) {
		t.Errorf("seenCount = %d, want %d: every observer must have been counted even though only "+
			"one was first", got.SeenCount, n)
	}
}

// TestA6_ManyDistinctArtifactsUnderContention keeps the test above from passing for a ledger that
// simply serializes everything into one winner regardless of the key.
func TestA6_ManyDistinctArtifactsUnderContention(t *testing.T) {
	db := openWithConns(t, 8)
	ledger := store.NewArtifactLedger(db)

	const keys = 64
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners int
	)
	start := make(chan struct{})

	for i := 0; i < keys; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			first, err := ledger.Observe(fmt.Sprintf("sha256:distinct-%d", i), fmt.Sprintf("agent-%d", i), time.Now())
			if err != nil {
				t.Errorf("observe %d: %v", i, err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if first {
				winners++
			}
		}(i)
	}
	close(start)
	wg.Wait()

	// Every distinct artifact must have exactly one winner, so the total is the number of keys.
	if winners != keys {
		t.Errorf("winners = %d, want %d: distinct artifacts must each have their own first observer, "+
			"or the ledger is collapsing unrelated content", winners, keys)
	}
}
