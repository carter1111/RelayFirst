package scoring

import (
	"fmt"
	"math"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// Scoring constants from MVP.md §5.3.
const (
	// BasePoints is the fixed multiplier every valid unit of work starts from.
	BasePoints = 10.0

	// DiversityHalving is the penalty step applied per repeat within a domain.
	// diversity = 1 / (1 + sameDomainRepeats * DiversityHalving)
	DiversityHalving = 0.5
)

// Verdict is the outcome of evaluating one receipt.
type Verdict struct {
	// ArtifactKey identifies the observed artifact.
	ArtifactKey string

	// Novelty is 1.0 for a first sighting and 0.0 otherwise (MVP.md §5.2).
	Novelty float64

	// Verified is 0 or 1. It is binary by design: a verifier either reproduces
	// the result or clears it, and a cleared receipt is void rather than
	// partially credited (MVP.md §5.3).
	Verified float64

	// Diversity shrinks as one agent repeatedly observes the same domain.
	Diversity float64

	// BudgetFactor scales descending work so a single agent cannot dominate an
	// epoch (MVP.md §6.3).
	BudgetFactor float64

	// Points is the final credited amount: BASE x Verified x Novelty x
	// Diversity x BudgetFactor.
	Points float64

	// Reason explains a zero score, so operators can tell farming apart from a
	// genuine mistake.
	Reason string
}

// Params are the tunable inputs to one scoring evaluation.
type Params struct {
	// Verified reports whether the receipt's result was reproduced. Only true
	// earns credit.
	Verified bool

	// SameDomainRepeats counts how many times this agent has already been
	// credited for this domain within the epoch, excluding the current one.
	SameDomainRepeats int

	// BudgetFactor is the remaining per-agent budget headroom for the epoch,
	// in [0,1]. 1 means no cap pressure, 0 means the agent is at its ceiling.
	BudgetFactor float64
}

// Score evaluates one receipt against the ledger and the epoch parameters.
//
// The order of checks is deliberate: novelty is consulted first, because a
// repeat sighting must be rejected regardless of how well everything else
// lines up. That is what makes farming unprofitable (MVP.md §5.2).
func Score(r *receipt.Receipt, ledger Ledger, p Params) (Verdict, error) {
	key, err := ArtifactKey(r)
	if err != nil {
		return Verdict{}, err
	}

	v := Verdict{
		ArtifactKey:  key,
		Verified:     0,
		Novelty:      0,
		Diversity:    1,
		BudgetFactor: clamp01(p.BudgetFactor),
	}

	// A receipt that cannot be reproduced is void, not partly credited.
	if !p.Verified {
		v.Reason = "result not reproduced by verifier"
		v.Points = 0
		return v, nil
	}
	v.Verified = 1

	// Novelty is the gate. The ledger is consulted for the *current* state, so
	// the caller must Observe only after a successful Score.
	if ledger != nil {
		if _, seen := ledger.Lookup(key); seen {
			v.Reason = fmt.Sprintf("artifact %s was already observed; a repeat sighting contributes no new information", short(key))
			v.Points = 0
			return v, nil
		}
	}
	v.Novelty = 1

	// Diversity attenuates repeated work within one domain.
	repeats := p.SameDomainRepeats
	if repeats < 0 {
		repeats = 0
	}
	v.Diversity = 1.0 / (1.0 + float64(repeats)*DiversityHalving)

	// Budget headroom. At zero, further work earns nothing this epoch.
	if v.BudgetFactor <= 0 {
		v.Reason = "per-agent epoch budget exhausted"
		v.Points = 0
		return v, nil
	}

	v.Points = BasePoints * v.Verified * v.Novelty * v.Diversity * v.BudgetFactor
	return v, nil
}

// Emit scores a receipt and, when it earns credit, records the sighting.
//
// Splitting Score and Emit matters: scoring must stay side-effect free so it can
// be dry-run for auditing, while Emit is the only place the ledger advances.
func Emit(r *receipt.Receipt, ledger Ledger, p Params, at time.Time) (Verdict, error) {
	v, err := Score(r, ledger, p)
	if err != nil {
		return Verdict{}, err
	}
	if v.Points <= 0 {
		return v, nil
	}
	if ledger == nil {
		return Verdict{}, fmt.Errorf("scoring: cannot emit without a ledger")
	}
	if _, err := ledger.Observe(v.ArtifactKey, r.AgentID, at); err != nil {
		return Verdict{}, err
	}
	return v, nil
}

func clamp01(f float64) float64 {
	if math.IsNaN(f) || f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// short renders a key for log lines without spilling the whole 71-char string.
func short(s string) string {
	const keep = 18
	if len(s) <= keep {
		return s
	}
	return s[:keep] + "..."
}
