package sqlite

import (
	"database/sql"
	"fmt"
)

// PutBatch stores messages inside one transaction.
//
// # The measured reason this exists
//
// Row-at-a-time ingest measured about 14188 writes/s on the baseline machine. Wrapping inserts in a
// transaction moved it to 34453 at a batch of 16, and larger batches did not improve on that:
//
//	batch=1     14188 writes/s   (70.5 us/write)
//	batch=16    34453 writes/s   (29.0 us/write)   <- saturated here
//	batch=64    32961 writes/s   (30.3 us/write)
//	batch=256   35378 writes/s   (28.3 us/write)
//
// So the win is real and roughly 2.4x, and it arrives at 16 rather than at some larger number. The
// default reflects that measurement rather than a guess, and it is deliberately NOT the 64 the task
// proposed: a bigger batch buys nothing here while holding more work in memory before a commit.
//
// # Why this does NOT conflict with A6
//
// A6's atomicity comes from the SQL, not from the connection limit: both write paths are
// single-statement atomic upserts, and `putOne` is the statement `Put` has always used. Putting
// several of those in one transaction changes WHEN they commit, not whether each is atomic against a
// concurrent observer of the same id.
//
// A concurrent duplicate is still resolved by the primary-key constraint on `messages`, so a second
// delivery sees RowsAffected 0 and reports stored=false whether it ran in this batch, another batch,
// or outside any batch. The A6 concurrency tests pin that, and the batch tests below pin the same
// property held across batches.
//
// # Why the results are per-message rather than one error
//
// A caller needs to know which messages were stored, because a duplicate is a normal outcome rather
// than a failure. One error for the batch would force a re-query to learn what happened, and that
// re-query would read state that had already moved on.
type Batcher struct {
	store *MessageStore
	limit int

	tx      *sql.Tx
	pending int
	results []BatchResult
}

// BatchResult reports one message's outcome within a batch.
type BatchResult struct {
	// ID is the message id this result is for.
	ID string
	// Stored is false when the id was already present, exactly as in Put.
	Stored bool
	// Err is set when this message could not be attempted.
	Err error
}

// DefaultBatchSize is the measured saturation point for this workload.
//
// It is 16 because batching past that produced no further throughput on the baseline machine. A
// larger default would hold more uncommitted work in memory for no measured benefit.
const DefaultBatchSize = 16

// NewBatcher returns a batcher over the store.
//
// A limit of zero or less uses DefaultBatchSize. The limit exists so a caller can cap how much work
// accumulates before a commit, which matters when a node receives a burst: committing at 16 keeps the
// uncommitted window small when the process is killed.
func (s *MessageStore) NewBatcher(limit int) *Batcher {
	if limit <= 0 {
		limit = DefaultBatchSize
	}
	return &Batcher{store: s, limit: limit}
}

// Add queues a message, committing when the batch fills.
//
// # Why a per-message failure is returned as an error
//
// The implementation records the failure in BatchResult.Err, and returning nil alongside it would
// make the failure easy to lose: a caller that uses the returned error and ignores the result would
// treat a rejected message as accepted. So the per-message error is ALSO returned here, and the
// distinction that matters is scope: this error is about THIS message only, while a returned error
// from a filled batch's flush is about the transaction.
//
// That means the two can be told apart by whether the failure names a message. It is deliberately
// not a separate return value: two error channels would be easier to get wrong than one error whose
// scope a caller can check.
//
// # Why a commit or begin failure is different
//
// Those affect every message in the batch, so they are returned without a partial BatchResult that
// would imply the rest succeeded.
func (b *Batcher) Add(m Message) (BatchResult, error) {
	if b.tx == nil {
		if err := b.begin(); err != nil {
			return BatchResult{ID: m.ID, Err: err}, err
		}
	}

	stored, err := b.store.putOne(b.tx, m)
	res := BatchResult{ID: m.ID, Stored: stored, Err: err}
	b.pending++
	b.results = append(b.results, res)

	if b.pending >= b.limit {
		if cerr := b.Flush(); cerr != nil {
			return res, cerr
		}
	}
	// The per-message error is returned so it cannot be dropped by a caller that only reads the
	// error. Returning nil here would report a rejected message as fine.
	return res, err
}

// Flush commits whatever is pending.
//
// It is a no-op when nothing is pending, so a caller can call it unconditionally at the end of a
// burst without tracking whether anything was added. That matters because forgetting the final flush
// would silently discard the last partial batch — the failure a caller is most likely to make.
func (b *Batcher) Flush() error {
	if b.tx == nil {
		return nil
	}
	tx := b.tx
	b.tx = nil
	b.pending = 0
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit batch: %w", err)
	}
	return nil
}

// Results returns the per-message outcomes accumulated so far.
//
// It is a copy so a caller cannot mutate the batcher's record of what it did.
func (b *Batcher) Results() []BatchResult {
	return append([]BatchResult(nil), b.results...)
}

// begin starts a transaction.
//
// # Why BEGIN is default rather than IMMEDIATE, stated because it is a choice
//
// database/sql begins a DEFERRED transaction, which takes the write lock on the first write rather
// than at BEGIN. With the production default of one connection that is harmless either way. It is
// written down here because it WOULD matter if the pool were widened, and widening the pool is a
// decision with its own measurement (see the A6 concurrency tests) rather than a free change.
func (b *Batcher) begin() error {
	tx, err := b.store.db.handle.Begin()
	if err != nil {
		return fmt.Errorf("store: begin batch: %w", err)
	}
	b.tx = tx
	return nil
}
