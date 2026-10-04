package scoring

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// MicroPerPoint is the fixed-point scale used to store points internally.
//
// Points are computed as floating-point products (BASE x verified x novelty x
// ...), but they are *stored* as integers. Accumulating floats across thousands
// of receipts drifts, and an economy whose ledger cannot be reconciled exactly is
// worse than one with a coarser unit. One point is one million micro-points,
// which is finer than any emission rate the MVP produces.
const MicroPerPoint = 1_000_000

// Entry is one immutable line in the points ledger.
type Entry struct {
	// ReceiptID is the receipt that earned this entry. It is the idempotency
	// key: crediting the same receipt twice is a no-op.
	ReceiptID string

	// AgentID is the credited agent.
	AgentID string

	// Epoch groups entries for settlement (MVP.md §6.2).
	Epoch uint64

	// Points is the credited amount at full precision.
	Points float64

	// CreditedAt is when the entry was written.
	CreditedAt time.Time
}

// PointsLedger records earned points.
//
// # Why there is no Transfer method (invariant A5)
//
// MVP.md §6.1 states that points are non-transferable, unpriced, and carry no
// promised return. That is a legal and trust boundary, not a UI preference: the
// moment points can move between accounts they start to look like a bearer
// instrument, and the "we may never issue a token" escape hatch closes.
//
// This interface therefore exposes *no* way to move points. There is no
// Transfer, Debit, Spend, Withdraw or Send. Points only ever appear, attached to
// the receipt that earned them. TestNoTransferCapability asserts this against
// the method set, so an accidental addition fails the build rather than shipping.
//
// If a future stage genuinely needs to move value, that belongs in the optional
// on-chain settlement layer (ARCHITECTURE.md §8), not here.
type PointsLedger interface {
	// Credit records points for a receipt. It is idempotent per receiptId:
	// crediting an already-credited receipt returns written=false and nil error.
	Credit(receiptID, agentID string, epoch uint64, points float64, at time.Time) (written bool, err error)

	// Balance returns an agent's lifetime points.
	Balance(agentID string) float64

	// EpochBalance returns an agent's points within one epoch.
	EpochBalance(agentID string, epoch uint64) float64

	// Entry returns the entry for a receipt, if it was credited.
	Entry(receiptID string) (Entry, bool)

	// Entries returns every entry in write order, for auditing.
	Entries() []Entry

	// AgentCount returns the number of distinct credited agents.
	AgentCount() int
}

// MemPointsLedger is an in-memory PointsLedger.
//
// Like MemLedger it makes no durability claim: S2-8 will supply the persistent
// implementation behind the same interface.
type MemPointsLedger struct {
	mu sync.RWMutex

	// entries preserves write order, which matters for auditability.
	entries []Entry

	// byReceipt is the idempotency index.
	byReceipt map[string]int

	// microByAgent and microByEpochKey are the running totals.
	microByAgent map[string]int64
	microByEpoch map[epochKey]int64
}

type epochKey struct {
	agentID string
	epoch   uint64
}

// NewMemPointsLedger returns an empty points ledger.
func NewMemPointsLedger() *MemPointsLedger {
	return &MemPointsLedger{
		byReceipt:    make(map[string]int),
		microByAgent: make(map[string]int64),
		microByEpoch: make(map[epochKey]int64),
	}
}

// Credit records points for a receipt.
//
// A zero or negative award writes nothing and reports written=false: an entry
// worth nothing is noise in the audit trail, and the caller already knows the
// verdict was a miss.
func (l *MemPointsLedger) Credit(receiptID, agentID string, epoch uint64, points float64, at time.Time) (bool, error) {
	if receiptID == "" {
		return false, fmt.Errorf("scoring: receipt id is empty")
	}
	if agentID == "" {
		return false, fmt.Errorf("scoring: agent id is empty")
	}
	if math.IsNaN(points) || math.IsInf(points, 0) {
		return false, fmt.Errorf("scoring: award must be finite, got %v", points)
	}
	if points <= 0 {
		return false, nil
	}

	micro := int64(math.Round(points * MicroPerPoint))
	if micro <= 0 {
		// Rounds to nothing at our resolution; treat as a miss rather than
		// recording a zero-valued entry.
		return false, nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.byReceipt[receiptID]; exists {
		// Idempotent: a replayed receipt does not double-pay.
		return false, nil
	}

	entry := Entry{
		ReceiptID:  receiptID,
		AgentID:    agentID,
		Epoch:      epoch,
		Points:     points,
		CreditedAt: at,
	}
	l.byReceipt[receiptID] = len(l.entries)
	l.entries = append(l.entries, entry)

	l.microByAgent[agentID] += micro
	l.microByEpoch[epochKey{agentID: agentID, epoch: epoch}] += micro

	return true, nil
}

// Balance returns an agent's lifetime points.
func (l *MemPointsLedger) Balance(agentID string) float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return float64(l.microByAgent[agentID]) / MicroPerPoint
}

// EpochBalance returns an agent's points within one epoch.
func (l *MemPointsLedger) EpochBalance(agentID string, epoch uint64) float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return float64(l.microByEpoch[epochKey{agentID: agentID, epoch: epoch}]) / MicroPerPoint
}

// Entry returns the entry for a receipt.
func (l *MemPointsLedger) Entry(receiptID string) (Entry, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	i, ok := l.byReceipt[receiptID]
	if !ok {
		return Entry{}, false
	}
	return l.entries[i], true
}

// Entries returns a copy of every entry in write order.
func (l *MemPointsLedger) Entries() []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// AgentCount returns the number of distinct credited agents.
func (l *MemPointsLedger) AgentCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return len(l.microByAgent)
}

// TotalPoints returns the sum of all credited points, for auditing.
//
// This is the number that must never be improvable by moving points around,
// because there is nowhere to move them to.
func (l *MemPointsLedger) TotalPoints() float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var micro int64
	for _, v := range l.microByAgent {
		micro += v
	}
	return float64(micro) / MicroPerPoint
}
