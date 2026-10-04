package verification

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"
)

// StakeState is the lifecycle state of a commitment.
type StakeState string

const (
	// StakeCommitted means the commitment is open: the verdict was made and has not
	// yet settled.
	StakeCommitted StakeState = "committed"

	// StakeReleased means the verdict was sound and the commitment is discharged.
	StakeReleased StakeState = "released"

	// StakeSlashed means the verdict was disputable and the commitment was
	// surrendered.
	StakeSlashed StakeState = "slashed"
)

// Commitment is one recorded commitment.
//
// # What this is NOT
//
// It is not a deposit and not a balance. Nothing is deducted from anywhere to create
// it, and settling it moves no points. It exists as a *recorded magnitude with a
// consequence attached*, so that making a verification claim is not free. Invariant
// A5 forbids points moving between agents, and the points ledger's method set is
// pinned by a test to exactly six methods with no debit — so expressing the
// consequence as a balance change is not available even if it were desirable.
type Commitment struct {
	// ID is stable per receipt, so re-verifying cannot create a second commitment.
	ID string

	// AgentID is whoever made the claim.
	AgentID string

	// Epoch groups commitments for per-epoch reporting.
	Epoch uint64

	// Amount is the committed magnitude in points. It is never subtracted from a
	// balance; see the note above.
	Amount uint64

	// State is the lifecycle state.
	State StakeState

	// CommittedAt and SettledAt bound the commitment's life.
	CommittedAt time.Time
	SettledAt   time.Time
}

// StakeLedger records commitments.
//
// # The method set is deliberately tiny, and pinned by a test
//
// There is no method that moves points between agents, no balance to redeem, and no
// withdrawal. Commit, Release and Slash each change only the *state of a record*.
// TestStakeLedgerExposesNoPointMovement pins this, so adding a points-moving verb is
// a deliberate, reviewed act rather than a drive-by change.
//
// The verb "slash" is used because TASKS.md and MVP.md §5.5 use it. Read it as
// "surrender a recorded commitment", never as "deduct a balance".
type StakeLedger interface {
	// Commit records a commitment. It is idempotent per id: committing an existing
	// id reports wrote=false rather than creating a second record.
	Commit(id, agentID string, epoch uint64, amount uint64, at time.Time) (wrote bool, err error)

	// Release discharges a commitment whose verdict was sound.
	Release(id string, at time.Time) (released bool, err error)

	// Slash surrenders a commitment whose verdict was disputable.
	Slash(id string, at time.Time) (slashed bool, err error)

	// Get returns one commitment.
	Get(id string) (Commitment, bool)

	// Standing reports an agent's totals: committed, released and slashed magnitudes.
	//
	// These are three sums of magnitudes, not a balance: there is no operation that
	// turns them into points, and no operation that moves points between agents.
	Standing(agentID string) (committed, released, slashed uint64)

	// Entries returns every commitment, for auditing.
	Entries() []Commitment
}

// StakeLedgerMethods returns the StakeLedger interface's method names.
//
// It exists so the invariant A5 guard can assert the method set without importing
// reflect into a test that would otherwise be all behaviour. A guard that cannot see
// the interface cannot prove the interface is safe.
func StakeLedgerMethods() []string {
	var iface *StakeLedger
	typ := reflect.TypeOf(iface).Elem()

	out := make([]string, 0, typ.NumMethod())
	for i := 0; i < typ.NumMethod(); i++ {
		out = append(out, typ.Method(i).Name)
	}
	return out
}

// MemStakeLedger is an in-memory StakeLedger.
//
// Like the other in-memory ledgers in this project it makes no durability claim; a
// persistent implementation can be added behind the same interface without changing
// callers.
type MemStakeLedger struct {
	mu    sync.RWMutex
	order []string
	byID  map[string]int
	items []Commitment
}

// NewMemStakeLedger returns an empty ledger.
func NewMemStakeLedger() *MemStakeLedger {
	return &MemStakeLedger{byID: map[string]int{}}
}

// Commit records a commitment, idempotently per id.
func (l *MemStakeLedger) Commit(id, agentID string, epoch uint64, amount uint64, at time.Time) (bool, error) {
	if id == "" {
		return false, errors.New("verification: commitment id is empty")
	}
	if agentID == "" {
		return false, errors.New("verification: commitment agent id is empty")
	}
	if amount == 0 {
		// A zero commitment is not a commitment: it would impose no consequence, so
		// recording it would imply a deterrence that is not there.
		return false, errors.New("verification: commitment amount must be positive")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.byID[id]; exists {
		return false, nil
	}

	l.byID[id] = len(l.items)
	l.items = append(l.items, Commitment{
		ID:          id,
		AgentID:     agentID,
		Epoch:       epoch,
		Amount:      amount,
		State:       StakeCommitted,
		CommittedAt: at,
	})
	return true, nil
}

// Release discharges a commitment.
func (l *MemStakeLedger) Release(id string, at time.Time) (bool, error) {
	return l.settle(id, StakeReleased, at)
}

// Slash surrenders a commitment.
func (l *MemStakeLedger) Slash(id string, at time.Time) (bool, error) {
	return l.settle(id, StakeSlashed, at)
}

// settle moves a commitment to its terminal state.
//
// A commitment settles once. Re-settling is refused rather than treated as a no-op,
// because a second verdict on the same work would otherwise be able to change the
// consequence it carries — which is the accountability the record exists to provide.
func (l *MemStakeLedger) settle(id string, to StakeState, at time.Time) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	i, ok := l.byID[id]
	if !ok {
		return false, fmt.Errorf("verification: unknown commitment %q", id)
	}
	if l.items[i].State != StakeCommitted {
		return false, fmt.Errorf("verification: commitment %q already %s", id, l.items[i].State)
	}

	l.items[i].State = to
	l.items[i].SettledAt = at
	return true, nil
}

// Get returns one commitment.
func (l *MemStakeLedger) Get(id string) (Commitment, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	i, ok := l.byID[id]
	if !ok {
		return Commitment{}, false
	}
	return l.items[i], true
}

// Standing reports an agent's committed, released and slashed magnitudes.
//
// Note that "committed" includes both settled and open commitments: it answers "how
// much has this agent put forward", which is the number a deterrence argument needs.
func (l *MemStakeLedger) Standing(agentID string) (committed, released, slashed uint64) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, c := range l.items {
		if c.AgentID != agentID {
			continue
		}
		committed += c.Amount
		switch c.State {
		case StakeReleased:
			released += c.Amount
		case StakeSlashed:
			slashed += c.Amount
		}
	}
	return committed, released, slashed
}

// Entries returns every commitment in write order.
func (l *MemStakeLedger) Entries() []Commitment {
	l.mu.RLock()
	defer l.mu.RUnlock()

	out := make([]Commitment, len(l.items))
	copy(out, l.items)
	return out
}
