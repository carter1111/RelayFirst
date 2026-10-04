package verification

import (
	"reflect"
	"strings"
	"sync"

	"github.com/relayfirst/relayfirst/internal/mining"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// RecordedVerdicts answers "was this receipt verified?" from what a verifier wrote
// onto the receipt.
//
// # Why this is the production source
//
// mining.ScoringSink needs to know whether a receipt's work was reproduced. The only
// answer that means anything comes from a verifier, and a verifier's answer lives in
// the receipt's Verification block. So the source reads that block.
//
// This is what closes the S4-0 gap: the score no longer takes the submitter's word
// for its own validity.
//
// # The consequence, which callers must expect
//
// A freshly mined receipt is `pending`, so it is NOT verified and earns nothing until
// a verifier has looked at it. That is the intended end state — verification is what
// makes points mean something — but it means a miner running entirely alone accrues
// no points. Wiring this source into a miner without also running a verifier produces
// a miner that does work and earns nothing, which is a confusing state to debug, so
// the CLI reports which source is in use.
//
// # Why it is conservative
//
// Only an explicit `verified` status counts. A pending receipt, a rejected one, or an
// unreadable status all yield false. Defaulting the other way would make the entire
// verification mechanism advisory.
type RecordedVerdicts struct {
	// Lookup optionally returns the authoritative current state of a receipt.
	//
	// It exists because a caller may hold a receipt object that predates the
	// verifier's write — the miner's in-memory copy, for instance. When Lookup is
	// nil, the receipt passed in is used as-is, which is correct for a caller that
	// just read it from the store.
	Lookup func(receiptID string) (*receipt.Receipt, bool)
}

// Verified implements mining.VerdictSource.
func (v RecordedVerdicts) Verified(r *receipt.Receipt) bool {
	if r == nil {
		return false
	}

	subject := r
	if v.Lookup != nil {
		if fresh, ok := v.Lookup(r.ReceiptID); ok && fresh != nil {
			subject = fresh
		}
	}

	if subject.Verification.Status != receipt.VerificationVerified {
		return false
	}

	// A status alone is not enough. A `verified` receipt must also name its verifier
	// and carry the hash that was reproduced, or the claim is unsupported. Without
	// this check a malformed or hand-edited receipt could claim verification with no
	// evidence behind it.
	if subject.Verification.VerifierID == nil || strings.TrimSpace(*subject.Verification.VerifierID) == "" {
		return false
	}
	if subject.Verification.RecomputedHash == nil || strings.TrimSpace(*subject.Verification.RecomputedHash) == "" {
		return false
	}

	// And the verifier must not be the producer, which is the one anti-collusion rule
	// that can be checked from the receipt alone. The others need epoch statistics.
	if *subject.Verification.VerifierID == subject.AgentID {
		return false
	}

	return true
}

// Describe implements the optional describer mining's CLI uses, so an operator can
// see which source is active.
func (v RecordedVerdicts) Describe() string {
	return "verifier verdict (read from the receipt's verification block)"
}

// MemVerdicts is an in-memory verdict store.
//
// It exists for a deployment that verifies in-process and then scores: the verifier
// writes here, the sink reads here. It is deliberately not a ledger of its own —
// it holds no amounts and moves nothing, only the verdict and its evidence, so it
// cannot become a second source of truth about points.
type MemVerdicts struct {
	mu      sync.RWMutex
	records map[string]receipt.Verification
}

// NewMemVerdicts returns an empty store.
func NewMemVerdicts() *MemVerdicts {
	return &MemVerdicts{records: map[string]receipt.Verification{}}
}

// Record stores a verification outcome for a receipt id.
func (m *MemVerdicts) Record(receiptID string, v receipt.Verification) {
	if m == nil || receiptID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[receiptID] = v
}

// Lookup returns a receipt carrying the recorded verification.
//
// It returns a minimal receipt: only the fields RecordedVerdicts inspects are
// populated, which is enough for a verdict and deliberately not enough to be mistaken
// for the stored receipt. A caller needing the full receipt should read it from the
// store instead.
func (m *MemVerdicts) Lookup(receiptID string) (*receipt.Receipt, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	v, ok := m.records[receiptID]
	if !ok {
		return nil, false
	}
	return &receipt.Receipt{
		ReceiptID:    receiptID,
		AgentID:      "", // filled by the caller's own receipt; only the verdict is here
		Verification: v,
	}, true
}

// MemVerdictsMethods returns MemVerdicts' method names.
//
// It exists so the invariant A5 guard can inspect the type without the test importing
// reflect itself. A guard that cannot see the method set cannot prove the method set
// is safe.
func MemVerdictsMethods() []string {
	typ := reflect.TypeOf(&MemVerdicts{})

	out := make([]string, 0, typ.NumMethod())
	for i := 0; i < typ.NumMethod(); i++ {
		out = append(out, typ.Method(i).Name)
	}
	return out
}

// AsSource returns a VerdictSource backed by this store.
func (m *MemVerdicts) AsSource() RecordedVerdicts {
	return RecordedVerdicts{Lookup: m.Lookup}
}

// compile-time assertions that the adapters satisfy the interfaces they exist for.
var (
	_ mining.VerdictSource = RecordedVerdicts{}
	_ mining.VerdictSource = SelfCheckAdapter{}
)

// SelfCheckAdapter exposes mining's self-check as a VerdictSource.
//
// It exists so a caller can name the weak source explicitly rather than relying on a
// nil default. Naming it at a call site is the point: an operator reading the wiring
// sees "self-check" and knows verification is not happening.
type SelfCheckAdapter struct{}

// Verified implements mining.VerdictSource.
func (SelfCheckAdapter) Verified(r *receipt.Receipt) bool {
	return mining.SelfCheckVerdicts{}.Verified(r)
}

// Describe implements the describer interface.
func (SelfCheckAdapter) Describe() string { return mining.SelfCheckVerdicts{}.Describe() }
