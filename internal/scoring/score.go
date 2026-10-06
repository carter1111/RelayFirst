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
//
// # Why this reports WORK and not points (D1, 2026-10-07)
//
// Under the settled emission model (MVP.md §6.2) a receipt does not earn a fixed
// number of points. It contributes WORK, and an epoch's points are the fixed budget
// shared by the work totals at settlement. So this type reports the work a receipt is
// worth; points appear only from Settle, over an epoch's accumulated work.
//
// There is deliberately no Points field and no BudgetFactor here. A per-receipt points
// value would be the model this was migrated away from, and a per-receipt budget factor
// has no meaning once the ceiling is applied to an allocation rather than to each
// receipt.
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

	// Work is the measured output: BASE x Verified x Novelty x Diversity. It is a
	// work UNIT recorded for settlement, not points.
	Work float64

	// Reason explains a zero-work outcome, so operators can tell farming apart from a
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

	// There is deliberately no BudgetFactor here (D1). The per-agent ceiling is applied
	// at SETTLEMENT, over an epoch's allocation, not per receipt — a per-receipt budget
	// fraction belonged to the model this was migrated away from.
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
		ArtifactKey: key,
		Verified:    0,
		Novelty:     0,
		Diversity:   1,
	}

	// A receipt that cannot be reproduced is void, not partly credited.
	if !p.Verified {
		v.Reason = "result not reproduced by verifier"
		return v, nil
	}
	v.Verified = 1

	// Novelty is the gate. The ledger is consulted for the *current* state, so
	// the caller must Observe only after a successful Score.
	if ledger != nil {
		if _, seen := ledger.Lookup(key); seen {
			v.Reason = fmt.Sprintf("artifact %s was already observed; a repeat sighting contributes no new information", short(key))
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

	// Work, not points. The per-agent ceiling is applied at settlement (MVP.md §6.3),
	// over an allocation, not here over a single receipt.
	v.Work = BasePoints * v.Verified * v.Novelty * v.Diversity
	return v, nil
}

// RecordWork scores a receipt and, when it has novel work, records the sighting
// (dedup) and the work (settlement input).
//
// # Why this replaced Emit (D1)
//
// Emit used to write a receipt's points directly into a points ledger — the per-receipt
// emission model. Under the settled model a receipt contributes WORK, so this records
// the work and lets the epoch settlement turn totals into points. The dedup sighting is
// still recorded here, because novelty is a per-receipt gate and the artifact must be
// claimed exactly once, at the moment the work is accepted.
//
// Splitting Score and RecordWork matters as before: Score is side-effect free so it can
// be dry-run, and RecordWork is the only place the ledgers advance.
func RecordWork(r *receipt.Receipt, ledger Ledger, work WorkLedger, p Params, at time.Time) (Verdict, error) {
	v, err := Score(r, ledger, p)
	if err != nil {
		return Verdict{}, err
	}
	if v.Work <= 0 {
		return v, nil
	}
	if ledger == nil {
		return Verdict{}, fmt.Errorf("scoring: cannot record work without a dedup ledger")
	}
	// Claim the artifact first. If this fails, nothing is recorded, so a retry can
	// still claim it; recording work first would let a receipt earn work while its
	// artifact stayed unclaimed and farmable.
	if _, err := ledger.Observe(v.ArtifactKey, r.AgentID, at); err != nil {
		return Verdict{}, err
	}
	if work != nil {
		if _, err := work.Record(WorkRecord{
			ReceiptID:   r.ReceiptID,
			AgentID:     r.AgentID,
			Epoch:       r.Epoch,
			Work:        v.Work,
			ArtifactKey: v.ArtifactKey,
			RecordedAt:  at,
		}); err != nil {
			return Verdict{}, err
		}
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
