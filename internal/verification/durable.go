package verification

import (
	"errors"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// errNoLedger reports a durable adapter constructed without its backing store.
//
// It is an error rather than a silent no-op because a commitment that is not recorded
// means the consequence it carries does not exist, and a verdict that is not stored
// means scoring will read "unverified" for work that was actually checked.
func errNoLedger() error {
	return errors.New("verification: durable adapter has no backing store")
}

// This file bridges the verification interfaces to the durable store.
//
// # Why the adapters live here rather than in internal/sqlite
//
// The dependency direction is the reason. internal/verification imports internal/receipt
// (to re-execute a task) and therefore links eip712 and secp256k1. The relay node must
// not be able to link any of that (MVP.md §7.1, guarded by a test in internal/node), and
// internal/sqlite is shared with the node — so internal/sqlite must not import
// internal/verification.
//
// Putting the adapters here means the import runs one way, verification to sqlite, and
// the node's dependency graph stays clean. The cost is that internal/sqlite declares its
// own CommitmentState strings, which these adapters translate at the boundary. A test
// asserts the two vocabularies agree, so a divergence fails loudly instead of writing a
// state the reader cannot interpret.

// DurableStakes adapts sqlite.CommitmentLedger to StakeLedger.
type DurableStakes struct {
	ledger *sqlite.CommitmentLedger
}

// NewDurableStakes returns a StakeLedger backed by ledger.
func NewDurableStakes(ledger *sqlite.CommitmentLedger) *DurableStakes {
	return &DurableStakes{ledger: ledger}
}

// Commit records a commitment.
func (d *DurableStakes) Commit(id, agentID string, epoch uint64, amount uint64, at time.Time) (bool, error) {
	if d == nil || d.ledger == nil {
		return false, errNoLedger()
	}
	return d.ledger.Commit(id, agentID, epoch, amount, at)
}

// Release discharges a commitment.
func (d *DurableStakes) Release(id string, at time.Time) (bool, error) {
	if d == nil || d.ledger == nil {
		return false, errNoLedger()
	}
	return d.ledger.Release(id, at)
}

// Slash surrenders a commitment.
func (d *DurableStakes) Slash(id string, at time.Time) (bool, error) {
	if d == nil || d.ledger == nil {
		return false, errNoLedger()
	}
	return d.ledger.Slash(id, at)
}

// Get returns one commitment, translating the state vocabulary.
func (d *DurableStakes) Get(id string) (Commitment, bool) {
	if d == nil || d.ledger == nil {
		return Commitment{}, false
	}
	c, ok := d.ledger.Get(id)
	if !ok {
		return Commitment{}, false
	}
	return Commitment{
		ID:          c.ID,
		AgentID:     c.AgentID,
		Epoch:       c.Epoch,
		Amount:      c.Amount,
		State:       toStakeState(c.State),
		CommittedAt: c.CommittedAt,
		SettledAt:   c.SettledAt,
	}, true
}

// Standing reports an agent's totals.
func (d *DurableStakes) Standing(agentID string) (committed, released, slashed uint64) {
	if d == nil || d.ledger == nil {
		return 0, 0, 0
	}
	return d.ledger.Standing(agentID)
}

// Entries returns every commitment, translating each state.
func (d *DurableStakes) Entries() []Commitment {
	if d == nil || d.ledger == nil {
		return nil
	}

	rows := d.ledger.Entries()
	out := make([]Commitment, 0, len(rows))
	for _, c := range rows {
		out = append(out, Commitment{
			ID:          c.ID,
			AgentID:     c.AgentID,
			Epoch:       c.Epoch,
			Amount:      c.Amount,
			State:       toStakeState(c.State),
			CommittedAt: c.CommittedAt,
			SettledAt:   c.SettledAt,
		})
	}
	return out
}

// toStakeState translates the durable vocabulary into this package's.
//
// An unrecognised value becomes StakeCommitted rather than being dropped, because an
// unreadable state must not silently look like a settled one: treating an unknown state
// as released would let a corrupted record read as "the verifier was right".
func toStakeState(s sqlite.CommitmentState) StakeState {
	switch s {
	case sqlite.CommitmentReleased:
		return StakeReleased
	case sqlite.CommitmentSlashed:
		return StakeSlashed
	default:
		return StakeCommitted
	}
}

// toSQLiteState translates this package's vocabulary into the durable one.
func toSQLiteState(s StakeState) sqlite.CommitmentState {
	switch s {
	case StakeReleased:
		return sqlite.CommitmentReleased
	case StakeSlashed:
		return sqlite.CommitmentSlashed
	default:
		return sqlite.CommitmentCommitted
	}
}

// DurableVerdicts adapts sqlite.VerdictStore to a VerdictSource.
type DurableVerdicts struct {
	store *sqlite.VerdictStore
}

// NewDurableVerdicts returns a VerdictSource backed by store.
func NewDurableVerdicts(store *sqlite.VerdictStore) DurableVerdicts {
	return DurableVerdicts{store: store}
}

// Verified reports whether the stored verdict affirms the receipt.
//
// It is as strict as RecordedVerdicts: only an explicit verified status with a named
// verifier, a recomputed hash, and a verifier different from the producer counts.
// Anything else is false.
func (d DurableVerdicts) Verified(r *receipt.Receipt) bool {
	if r == nil || d.store == nil {
		return false
	}

	v, ok := d.store.Get(r.ReceiptID)
	if !ok {
		return false
	}
	if v.Status != string(receipt.VerificationVerified) {
		return false
	}
	if v.VerifierID == "" || v.RecomputedHash == "" {
		return false
	}
	if v.VerifierID == v.AgentID {
		return false
	}
	return true
}

// Describe implements the describer interface, so a CLI can report which source is
// active.
func (d DurableVerdicts) Describe() string {
	return "durable verifier verdict (read from the verification_verdicts table)"
}

// RecordingVerdicts writes verdicts into a durable store and reads them back.
//
// # Why this composes two things
//
// A verifier needs to *write* what it concluded; scoring needs to *read* it. Keeping
// both here means one object can be handed to both sides, so a caller cannot wire a
// writer and a reader to different stores and end up with verdicts that go nowhere.
type RecordingVerdicts struct {
	store *sqlite.VerdictStore
}

// NewRecordingVerdicts returns a store that both records and answers.
func NewRecordingVerdicts(store *sqlite.VerdictStore) *RecordingVerdicts {
	return &RecordingVerdicts{store: store}
}

// Record persists a verdict.
func (r *RecordingVerdicts) Record(v sqlite.Verdict) error {
	if r == nil || r.store == nil {
		return errNoLedger()
	}
	return r.store.Record(v)
}

// AsSource returns the read side.
func (r *RecordingVerdicts) AsSource() DurableVerdicts {
	if r == nil {
		return DurableVerdicts{}
	}
	return DurableVerdicts{store: r.store}
}
