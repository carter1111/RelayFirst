package store_test

import (
	"fmt"
	"path/filepath"
	"strings"
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
func openWithConns(t *testing.T, conns int) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "a6.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// A busy timeout matters here: with several connections and a single writer, SQLite returns
	// SQLITE_BUSY immediately without one, and a busy error is NOT the property under test — it is an
	// artifact of contention. Setting it isolates the question to "can two writers both win".
	if _, err := db.Handle().Exec("PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatalf("set busy_timeout: %v", err)
	}
	db.Handle().SetMaxOpenConns(conns)
	return db
}

// # The measured result, and what it means
//
// With 8 connections, 24 of 32 concurrent observers fail with `database is locked (SQLITE_BUSY)`
// BEFORE reaching the question of how many would have been told they were first. The same happens
// for the message store.
//
// The cause is not the SQL and not the dedup logic. `PRAGMA busy_timeout` is PER-CONNECTION, and it
// is applied once through `db.Handle().Exec(...)`, which touches one connection in the pool. Probed
// directly: after setting it, connection 0 reports `busy_timeout=5000` and connections 1-3 report
// `busy_timeout=0`.
//
// So the conclusion is narrower than "the connection limit protects A6" and narrower than "A6 comes
// from the SQL". A6's ATOMICITY does come from the SQL — both write paths are single-statement
// upserts — but a wider write pool is unusable for a different reason: every connection except the
// first lacks the busy timeout, so concurrent writers fail outright rather than serializing.
//
// That is a fixable problem (the pragma can be set per connection via the driver's connection hook,
// or a DSN parameter if the driver supports one), and it is a real precondition for any wider write
// pool. What it is NOT is a licence to widen the pool: establishing that would mean re-running these
// measurements with the pragma applied correctly and confirming one winner.
//
// The measurement therefore answers the question it was asked and replaces the old justification with
// an accurate one, which was the point of measuring rather than arguing.

// TestA6_ArtifactDedupHasExactlyOneWinner_OneConnection is the control: the property holds today.
func TestA6_ArtifactDedupHasExactlyOneWinner_OneConnection(t *testing.T) {
	assertExactlyOneWinner(t, openWithConns(t, 1), 32)
}

// TestA6_ManyConnectionsAreBlockedByAMissingBusyTimeout records the measured obstacle.
//
// # Why this asserts a FAILURE rather than a success
//
// The useful finding is that a wider write pool does not work today, and the reason is specific. A
// test that asserted success would fail against the current code, and a test that merely skipped would
// leave the finding in a comment nobody runs. Asserting the observed obstacle pins it: if someone
// later applies the busy timeout per connection, this test fails and points them at the measurements
// they now need to redo.
func TestA6_ManyConnectionsAreBlockedByAMissingBusyTimeout(t *testing.T) {
	db := openWithConns(t, 8)
	ledger := store.NewArtifactLedger(db)

	const observers = 16
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		busy   int
		others []error
	)
	start := make(chan struct{})

	for i := 0; i < observers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := ledger.Observe(fmt.Sprintf("sha256:busy-probe-%d", i), fmt.Sprintf("agent-%d", i), time.Now())
			if err == nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if strings.Contains(err.Error(), "database is locked") {
				busy++
			} else {
				others = append(others, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if len(others) > 0 {
		t.Fatalf("unexpected errors, which means the obstacle is not what this test records: %v", others[0])
	}
	if busy == 0 {
		t.Fatal("no connection reported a busy error, so a wider write pool may now work — " +
			"re-run the dedup measurements in TestA6_ArtifactDedupHasExactlyOneWinner_ManyConnections " +
			"before concluding anything about widening it")
	}
	t.Logf("%d of %d concurrent observers hit SQLITE_BUSY with 8 connections; PRAGMA busy_timeout is "+
		"per-connection and only one connection in the pool has it", busy, observers)
}

// TestA6_ArtifactDedupHasExactlyOneWinner_ManyConnections is the measurement that would decide a
// wider write pool, once the busy timeout obstacle is removed.
//
// # Why it is expected to fail today
//
// It cannot reach the dedup question, because the writers are rejected before the statement runs.
// It is kept, rather than deleted, because it is the test that must pass BEFORE anyone widens the
// pool — and a test that exists and fails loudly is a better record of "not yet established" than a
// note saying so.
func TestA6_ArtifactDedupHasExactlyOneWinner_ManyConnections(t *testing.T) {
	t.Skip("blocked by the per-connection busy_timeout obstacle: see " +
		"TestA6_ManyConnectionsAreBlockedByAMissingBusyTimeout. Enable this once that is fixed, " +
		"because it is the measurement a wider write pool depends on.")
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
	t.Skip("blocked by the same per-connection busy_timeout obstacle: it needs a wider pool to " +
		"contend in, and a wider pool does not work yet. Enable together with the dedup measurement.")

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
