package scoring

import (
	"fmt"
	"sync"
	"time"
)

// WorkRecord is one line of accumulated WORK, before settlement.
//
// # Why work is recorded separately from points (condition 1, D1)
//
// Under the settled model (MVP.md §6.2) a receipt does not earn points directly.
// It contributes WORK, and an epoch's points are the fixed budget shared by the work
// totals. Two reasons this must be its own durable record:
//
//   - It is the SETTLEMENT INPUT. Settlement is a function of (epoch, work totals); if
//     the individual work records are lost, the totals cannot be recomputed and a
//     settled epoch can no longer be audited.
//   - It is the AUDIT TRAIL. "Why did this agent receive X?" is answered by the work
//     records and the settlement, not by a points total that hides them.
//
// So the work record is the source and the points entry is derived. Keeping only the
// points would be keeping a derived value and discarding the inputs.
type WorkRecord struct {
	// ReceiptID is the receipt that generated the work. It is the idempotency key:
	// recording the same receipt twice is a no-op.
	ReceiptID string

	// AgentID is the agent whose work this is.
	AgentID string

	// Epoch groups records for settlement.
	Epoch uint64

	// Work is the measured output: BASE x Verified x Novelty x Diversity. It is a
	// work UNIT, not points; points come from the epoch settlement.
	Work float64

	// ArtifactKey is the dedup key (invariant A6), kept so a settled epoch can be
	// audited against the artifacts it paid for.
	ArtifactKey string

	// RecordedAt is when the record was written.
	RecordedAt time.Time
}

// WorkLedger accumulates work records and exposes the per-epoch totals settlement
// consumes.
//
// Nothing here moves value; there is no Transfer (invariant A5). Work is recorded,
// never spent.
type WorkLedger interface {
	// Record appends one receipt's work. Idempotent per receiptId: recording a
	// duplicate returns written=false and does not double the total.
	Record(rec WorkRecord) (written bool, err error)

	// Totals returns each agent's summed work for an epoch, the input to Settle.
	Totals(epoch uint64) (map[string]float64, error)

	// ForAgent returns one agent's work records for an epoch, in record order, for audit.
	ForAgent(agentID string, epoch uint64) ([]WorkRecord, error)

	// Counts returns the number of work records per agent in an epoch.
	//
	// It is distinct from Totals because the M1 multiplier gates on whether an agent
	// produced ANY receipt, not on how much work: an agent could in principle have work
	// recorded with a zero total, and "did they work at all" is a count question.
	Counts(epoch uint64) (map[string]int, error)

	// Count returns the number of work records, for tests and reporting.
	Count() int
}

// MemWorkLedger is an in-memory WorkLedger.
//
// Like MemLedger and MemPointsLedger it makes no durability claim; the durable
// implementation lives with the other SQL ledgers.
type MemWorkLedger struct {
	mu    sync.RWMutex
	byID  map[string]WorkRecord
	order []string
}

// NewMemWorkLedger returns an empty in-memory work ledger.
func NewMemWorkLedger() *MemWorkLedger {
	return &MemWorkLedger{byID: map[string]WorkRecord{}}
}

// Record appends one record, ignoring a duplicate receipt id.
func (l *MemWorkLedger) Record(rec WorkRecord) (bool, error) {
	if rec.ReceiptID == "" {
		return false, fmt.Errorf("scoring: work record has no receipt id")
	}
	if rec.AgentID == "" {
		return false, fmt.Errorf("scoring: work record for %s has no agent", rec.ReceiptID)
	}
	if rec.Work < 0 {
		return false, fmt.Errorf("scoring: work record for %s has negative work %v", rec.ReceiptID, rec.Work)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.byID[rec.ReceiptID]; exists {
		return false, nil
	}
	l.byID[rec.ReceiptID] = rec
	l.order = append(l.order, rec.ReceiptID)
	return true, nil
}

// Totals sums work per agent for an epoch.
func (l *MemWorkLedger) Totals(epoch uint64) (map[string]float64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := map[string]float64{}
	for _, id := range l.order {
		r := l.byID[id]
		if r.Epoch != epoch {
			continue
		}
		out[r.AgentID] += r.Work
	}
	return out, nil
}

// Counts returns records per agent for an epoch.
func (l *MemWorkLedger) Counts(epoch uint64) (map[string]int, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := map[string]int{}
	for _, id := range l.order {
		r := l.byID[id]
		if r.Epoch != epoch {
			continue
		}
		out[r.AgentID]++
	}
	return out, nil
}

// ForAgent returns one agent's records for an epoch, in record order.
func (l *MemWorkLedger) ForAgent(agentID string, epoch uint64) ([]WorkRecord, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []WorkRecord
	for _, id := range l.order {
		r := l.byID[id]
		if r.AgentID == agentID && r.Epoch == epoch {
			out = append(out, r)
		}
	}
	return out, nil
}

// Count returns the number of records.
func (l *MemWorkLedger) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.order)
}
