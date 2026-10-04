package sqlite_test

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// These tests cover the batched write path (Phase 2).
//
// The property that matters is not "is it faster" — the benchmark measures that — but that batching
// does NOT change dedup behaviour. A6's atomicity comes from the SQL statement, so putting several
// of those in one transaction must change WHEN they commit and nothing else. If batching changed
// which duplicate was reported as stored, it would be a correctness regression hidden behind a
// speedup.

func openStore(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func batchMsg(id string) sqlite.Message {
	return sqlite.Message{
		ID: id, AgentID: "agent:eip155:8453:0x0000000000000000000000000000000000000001",
		Kind: "receipt", Payload: []byte("{}"), ReceivedAt: time.Now(),
	}
}

// TestBatch_MatchesSinglePathExactly is the equivalence check.
//
// The same sequence of deliveries, with the same duplicates, through both paths must produce the
// same stored/duplicate outcome for every message. Anything else means the batch path has different
// semantics, which the speedup would not justify.
func TestBatch_MatchesSinglePathExactly(t *testing.T) {
	// Interleaved duplicates: each id is delivered twice, which is what a retry looks like.
	ids := []string{
		"0x" + fmt.Sprintf("%064x", 1), "0x" + fmt.Sprintf("%064x", 2),
		"0x" + fmt.Sprintf("%064x", 1), "0x" + fmt.Sprintf("%064x", 3),
		"0x" + fmt.Sprintf("%064x", 2), "0x" + fmt.Sprintf("%064x", 3),
	}

	// Single path.
	singleDB := openStore(t)
	single := sqlite.NewMessageStore(singleDB)
	singleResults := make([]bool, 0, len(ids))
	for _, id := range ids {
		stored, err := single.Put(batchMsg(id))
		if err != nil {
			t.Fatalf("single put: %v", err)
		}
		singleResults = append(singleResults, stored)
	}

	// Batched path, with a small batch so the sequence spans several transactions.
	batchDB := openStore(t)
	batchStore := sqlite.NewMessageStore(batchDB)
	batcher := batchStore.NewBatcher(2)
	batchResults := make([]bool, 0, len(ids))
	for _, id := range ids {
		res, err := batcher.Add(batchMsg(id))
		if err != nil {
			t.Fatalf("batch add: %v", err)
		}
		batchResults = append(batchResults, res.Stored)
	}
	if err := batcher.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	for i := range ids {
		if singleResults[i] != batchResults[i] {
			t.Errorf("message %d (%s): single path stored=%v, batch path stored=%v; "+
				"batching must change when things commit, not which duplicate is reported stored",
				i, ids[i], singleResults[i], batchResults[i])
		}
	}

	// And the two stores must end up holding the same number of rows.
	if a, b := single.Count(), batchStore.Count(); a != b {
		t.Errorf("single path stored %d messages, batched path %d; they must agree", a, b)
	}
}

// TestBatch_StoresWhatItReports keeps the returned flags honest against the database.
func TestBatch_StoresWhatItReports(t *testing.T) {
	db := openStore(t)
	store := sqlite.NewMessageStore(db)
	batcher := store.NewBatcher(4)

	var storedCount int
	for i := 0; i < 10; i++ {
		res, err := batcher.Add(batchMsg("0x" + fmt.Sprintf("%064x", i)))
		if err != nil {
			t.Fatalf("add: %v", err)
		}
		if res.Stored {
			storedCount++
		}
	}
	if err := batcher.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if got := store.Count(); got != storedCount {
		t.Errorf("reported %d stored but the store holds %d", storedCount, got)
	}
}

// TestBatch_FlushIsIdempotent keeps a double flush from erroring, since a caller is likely to flush
// defensively at the end of a burst.
func TestBatch_FlushIsIdempotent(t *testing.T) {
	db := openStore(t)
	store := sqlite.NewMessageStore(db)
	batcher := store.NewBatcher(8)

	if _, err := batcher.Add(batchMsg("0x" + fmt.Sprintf("%064x", 1))); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := batcher.Flush(); err != nil {
		t.Fatalf("first flush: %v", err)
	}
	if err := batcher.Flush(); err != nil {
		t.Fatalf("a second flush with nothing pending must be a no-op: %v", err)
	}
	// A batcher with nothing ever added must also flush cleanly.
	fresh := store.NewBatcher(8)
	if err := fresh.Flush(); err != nil {
		t.Fatalf("flushing an unused batcher must be a no-op: %v", err)
	}
}

// TestBatch_MalformedMessageDoesNotPoisonTheBatch keeps one bad message from discarding the rest.
//
// This is the difference between a per-message failure and a batch failure: a bad id is that
// message's problem, and a caller that lost the good messages in the batch would be unable to tell
// which succeeded.
func TestBatch_MalformedMessageDoesNotPoisonTheBatch(t *testing.T) {
	db := openStore(t)
	store := sqlite.NewMessageStore(db)
	batcher := store.NewBatcher(8)

	good1, err := batcher.Add(batchMsg("0x" + fmt.Sprintf("%064x", 1)))
	if err != nil {
		t.Fatalf("good add: %v", err)
	}
	if !good1.Stored {
		t.Error("the first good message must be stored")
	}

	// An empty id is rejected per-message, and the rejection is BOTH in the result and returned, so
	// a caller that only checks the error cannot mistake the message for accepted.
	bad, err := batcher.Add(sqlite.Message{ID: "", AgentID: "a", Kind: "receipt", Payload: []byte("{}")})
	if err == nil {
		t.Fatal("a message with no id must be reported, or a caller reading only the error would " +
			"think it was accepted")
	}
	if bad.Err == nil {
		t.Error("the result must also carry the per-message error")
	}

	// And the batch must still be usable: this is the difference between a per-message failure and a
	// batch failure. A caller that lost the good messages would be unable to tell which succeeded.
	good2, err := batcher.Add(batchMsg("0x" + fmt.Sprintf("%064x", 2)))
	if err != nil {
		t.Fatalf("a bad message must not break later ones in the batch: %v", err)
	}
	if !good2.Stored {
		t.Error("the message after the bad one must still be stored")
	}

	if err := batcher.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := store.Count(); got != 2 {
		t.Errorf("store holds %d, want 2: the two good messages must survive", got)
	}
}

// TestBatch_ConcurrentBatchesStillDedup is the A6 property across batches.
//
// Two batchers racing on the same id must produce exactly one stored=true in total, or batching has
// opened a double-insert window that the single path does not have.
func TestBatch_ConcurrentBatchesStillDedup(t *testing.T) {
	db := openStore(t)
	store := sqlite.NewMessageStore(db)

	const batches = 8
	const id = "0x" + "cd00000000000000000000000000000000000000000000000000000000000001"

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		stored  int
		firstEr error
	)
	start := make(chan struct{})

	for b := 0; b < batches; b++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			batcher := store.NewBatcher(4)
			<-start
			res, err := batcher.Add(batchMsg(id))
			if err != nil {
				mu.Lock()
				if firstEr == nil {
					firstEr = err
				}
				mu.Unlock()
				return
			}
			if err := batcher.Flush(); err != nil {
				mu.Lock()
				if firstEr == nil {
					firstEr = err
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			if res.Stored {
				stored++
			}
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if firstEr != nil {
		// With one connection the batches serialize, so an error here means the batcher's own
		// transaction handling is wrong rather than that contention was measured.
		t.Fatalf("a concurrent batch errored, which the single connection should make impossible: %v", firstEr)
	}
	if stored != 1 {
		t.Fatalf("%d of %d concurrent batches reported storing the same id; A6 requires exactly 1, "+
			"because batching must not open a double-insert window", stored, batches)
	}
	if got := store.Count(); got != 1 {
		t.Errorf("store holds %d rows for one id, want 1", got)
	}
}

// TestBatch_AnUnflushedBatchHoldsTheWriteLock is the hazard this API introduces, pinned as a test.
//
// # Why this matters more than the speedup
//
// Measured: while a batch is open and unflushed, a separate `Put` BLOCKS. The open transaction holds
// the write lock, and with the production default of one connection there is no other connection to
// proceed on.
//
// So a caller that forgets to flush does not merely lose data — it STOPS THE NODE. That is a worse
// failure mode than the row-at-a-time path has, and it is the reason this test exists rather than a
// comment: a batch API that can stall ingest is a real hazard, and the mitigation (defer Flush, or a
// bounded batch size) needs to be visible to whoever uses it.
//
// The batch size default of 16 limits the damage rather than eliminating it: a burst of 15 messages
// followed by silence leaves the lock held until the next message or the flush. So the real
// mitigation is the caller flushing, which is why the doc comment on Batcher says a final Flush is
// the step a caller is most likely to forget.
func TestBatch_AnUnflushedBatchHoldsTheWriteLock(t *testing.T) {
	db := openStore(t)
	store := sqlite.NewMessageStore(db)
	// A large limit so the batch stays open: the point is to observe the unflushed window.
	batcher := store.NewBatcher(1000)

	if _, err := batcher.Add(batchMsg("0x" + fmt.Sprintf("%064x", 1))); err != nil {
		t.Fatalf("add: %v", err)
	}

	// A separate write, with a bounded wait so a blocked writer is an observation rather than a
	// hung test suite.
	done := make(chan error, 1)
	go func() {
		_, err := store.Put(batchMsg("0x" + fmt.Sprintf("%064x", 2)))
		done <- err
	}()

	select {
	case err := <-done:
		// If this ever stops blocking, the hazard this test documents has changed, and the person who
		// changed it should say why.
		t.Fatalf("a separate Put completed while a batch was unflushed (err=%v); this test records "+
			"that it BLOCKS, so a change here means the hazard and its mitigation need revisiting", err)
	case <-time.After(2 * time.Second):
		t.Log("confirmed: an unflushed batch blocks other writers for as long as the transaction is open")
	}

	// And flushing releases it, which is the mitigation the doc comment prescribes.
	if err := batcher.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("after the flush the blocked write must complete: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a flush must release the write lock, or a caller could never recover from holding it")
	}
}

// TestBatch_LimitIsRespected keeps the batch size from being decorative: a caller capping the
// uncommitted window must actually get that cap.
func TestBatch_LimitIsRespected(t *testing.T) {
	db := openStore(t)
	store := sqlite.NewMessageStore(db)

	// A limit of 1 means every add commits immediately, so a reader should see each message as soon
	// as it is added. That is observable as a count that grows one at a time.
	batcher := store.NewBatcher(1)
	for i := 0; i < 5; i++ {
		if _, err := batcher.Add(batchMsg("0x" + fmt.Sprintf("%064x", i))); err != nil {
			t.Fatalf("add: %v", err)
		}
		if got := store.Count(); got != i+1 {
			t.Errorf("with limit=1 the store should hold %d after %d adds, got %d", i+1, i+1, got)
		}
	}
}

// TestBatch_DefaultsToTheMeasuredSaturation documents the default and why it is not arbitrary.
func TestBatch_DefaultsToTheMeasuredSaturation(t *testing.T) {
	if sqlite.DefaultBatchSize != 16 {
		t.Errorf("DefaultBatchSize = %d, want 16: that is where throughput saturated in the "+
			"measurement, and a larger default would hold more uncommitted work for no measured gain",
			sqlite.DefaultBatchSize)
	}
	db := openStore(t)
	store := sqlite.NewMessageStore(db)

	// Zero and negative both fall back to the default rather than meaning "unbounded", because an
	// unbounded batch would accumulate every message before committing — the opposite of the point.
	for _, limit := range []int{0, -1} {
		b := store.NewBatcher(limit)
		if b == nil {
			t.Fatalf("NewBatcher(%d) returned nil", limit)
		}
	}
}
