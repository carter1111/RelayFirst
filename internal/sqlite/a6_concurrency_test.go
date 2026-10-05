package sqlite_test

import (
	"fmt"
	"path/filepath"
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
//
// # Why the busy timeout is no longer set here
//
// It used to be applied with `db.Handle().Exec("PRAGMA busy_timeout = 5000")`, which reaches only the
// one connection that serves that call — every other pooled connection kept the driver default of 0,
// so concurrent writers failed with SQLITE_BUSY before reaching their statement. The timeout is now
// part of the DSN (internal/sqlite.withPragmas), so it applies to every connection the pool opens.
// Removing the manual pragma here is what lets these tests exercise the real configuration.
func openWithConns(t *testing.T, conns int) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "a6-msg.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	db.Handle().SetMaxOpenConns(conns)
	return db
}

// TestA6_MessageDedupHasExactlyOneWinner_OneConnection is the control: the property holds today.
func TestA6_MessageDedupHasExactlyOneWinner_OneConnection(t *testing.T) {
	assertOneMessageWinner(t, openWithConns(t, 1), 32)
}

// TestA6_MessageDedupHasExactlyOneWinner_ManyConnections is the message-store half of the question a
// wider write pool depends on.
//
// It was skipped while the busy timeout was per-connection and concurrent writers were rejected
// before their statement ran. With the timeout in the DSN that obstacle is gone, so this asserts the
// property directly: 32 concurrent deliveries of the SAME id, across 8 connections, and exactly one
// reports itself as stored. Two winners would make a redelivery indistinguishable from new traffic.
func TestA6_MessageDedupHasExactlyOneWinner_ManyConnections(t *testing.T) {
	assertOneMessageWinner(t, openWithConns(t, 8), 32)
}

// TestA6_ManyConnectionsCanWriteWithoutBusyErrors pins the fix that made the measurement above
// possible.
//
// # Why this is a success assertion now, when it asserted a failure before
//
// It previously recorded the OBSERVED OBSTACLE: with the timeout applied once through one connection,
// pooled writers failed with "database is locked". The DSN parameter removes that obstacle, so the
// honest test is the opposite one — distinct writes across many connections must all succeed. A
// regression in the DSN plumbing (a pragma that stops being applied per connection) fails here.
func TestA6_ManyConnectionsCanWriteWithoutBusyErrors(t *testing.T) {
	db := openWithConns(t, 8)
	store := sqlite.NewMessageStore(db)

	const writers = 16
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
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
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(w)
	}
	close(start)
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("%d of %d pooled writers errored; the busy timeout must apply to every connection "+
			"the pool opens, or a wider write pool is unusable: %v", len(errs), writers, errs[0])
	}
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
