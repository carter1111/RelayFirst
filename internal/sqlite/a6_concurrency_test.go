package sqlite_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// This file measures whether the MESSAGE store's dedup survives a wider connection pool.
//
// # Why the message store needs its own measurement
//
// The artifact ledger's dedup is a different table and a different statement. Concluding anything
// about one from the other is exactly the inference this exercise replaces with measurement, so the
// two paths are tested separately even though the shape looks the same.
//
// The message store's dedup is what stops a redelivery from being counted twice (S5-3): a sender
// whose acknowledgement was lost retries, and a retry must be acknowledged without being treated as
// new traffic.
//
// # Why the connection-limit manipulation uses Handle()
//
// So no production API is added for the sake of a test. If a later phase concludes a knob is needed,
// that is a separate change with its own justification.

// openWithConns opens a store and raises the connection limit.
func openWithConns(t *testing.T, conns int) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "a6-msg.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// A busy timeout matters: with several connections and a single writer, SQLite returns
	// SQLITE_BUSY immediately without one, and a busy error is NOT the property under test. Setting it
	// isolates the question to "can two writers both succeed".
	if _, err := db.Handle().Exec("PRAGMA busy_timeout = 5000"); err != nil {
		t.Fatalf("set busy_timeout: %v", err)
	}
	db.Handle().SetMaxOpenConns(conns)
	return db
}

// TestA6_MessageDedupHasExactlyOneWinner_OneConnection is the control: the property holds today.
func TestA6_MessageDedupHasExactlyOneWinner_OneConnection(t *testing.T) {
	assertOneMessageWinner(t, openWithConns(t, 1), 32)
}

// TestA6_MessageDedupHasExactlyOneWinner_ManyConnections is the measurement for this path, once the
// per-connection busy timeout obstacle is removed.
//
// # Why it is skipped rather than deleted
//
// It cannot reach the dedup question today, because concurrent writers are rejected before the
// statement runs. Keeping it as a skip is a better record of "not yet established" than a comment:
// the test exists, and enabling it is the step that a wider write pool depends on.
func TestA6_MessageDedupHasExactlyOneWinner_ManyConnections(t *testing.T) {
	t.Skip("blocked by the per-connection busy_timeout obstacle: see the ledger measurement in " +
		"internal/store. Enable this once that is fixed, because it is the measurement a wider write " +
		"pool depends on.")
}

// TestA6_ManyConnectionsAreBlockedByAMissingBusyTimeout records the measured obstacle for this path.
//
// # Why this asserts a FAILURE rather than a success
//
// The useful finding is that a wider write pool does not work today, and the reason is specific:
// `PRAGMA busy_timeout` is per-connection, and it is applied once through one connection. Asserting
// the observed obstacle pins it, so that applying the pragma per connection later makes this test
// fail and points the author at the measurements they now need to redo.
func TestA6_ManyConnectionsAreBlockedByAMissingBusyTimeout(t *testing.T) {
	db := openWithConns(t, 8)
	store := sqlite.NewMessageStore(db)

	const writers = 16
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		busy  int
		other []error
	)
	start := make(chan struct{})

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			_, err := store.Put(sqlite.Message{
				ID: fmt.Sprintf("0x%064x", w), AgentID: fmt.Sprintf("agent-%d", w),
				Kind: "receipt", Payload: []byte("{}"), ReceivedAt: time.Now(),
			})
			if err == nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if strings.Contains(err.Error(), "database is locked") {
				busy++
			} else {
				other = append(other, err)
			}
		}(w)
	}
	close(start)
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("unexpected errors, so the obstacle is not what this test records: %v", other[0])
	}
	if busy == 0 {
		t.Fatal("no writer reported a busy error, so a wider write pool may now work — re-run the " +
			"dedup measurement before concluding anything about widening it")
	}
	t.Logf("%d of %d concurrent writers hit SQLITE_BUSY with 8 connections", busy, writers)
}

func assertOneMessageWinner(t *testing.T, db *sqlite.DB, n int) {
	t.Helper()
	store := sqlite.NewMessageStore(db)

	const id = "0xab00000000000000000000000000000000000000000000000000000000000001"

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		stored int
		errs   []error
	)
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			wasStored, err := store.Put(sqlite.Message{
				ID: id, AgentID: fmt.Sprintf("agent-%d", i), Kind: "receipt",
				Payload: []byte("{}"), ReceivedAt: time.Now(),
			})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			if wasStored {
				stored++
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("%d of %d deliveries errored, so this measured contention rather than the property: %v",
			len(errs), n, errs[0])
	}
	if stored != 1 {
		t.Fatalf("%d of %d concurrent deliveries were stored; exactly 1 is correct, because a "+
			"duplicate that reports itself as stored is indistinguishable from new traffic", stored, n)
	}
}

// TestA6_SingleConnectionIsStillTheProductionDefault guards the boundary.
//
// This file tests what COULD happen with a wider pool, and must not itself change what production
// does. Asserting the configured limit rather than trusting a comment is what makes that a check.
func TestA6_SingleConnectionIsStillTheProductionDefault(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "default.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if got := db.Handle().Stats().MaxOpenConnections; got != 1 {
		t.Errorf("production default MaxOpenConnections = %d, want 1: this file must not change "+
			"production behaviour, only measure it", got)
	}
}
