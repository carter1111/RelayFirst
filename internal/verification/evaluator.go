package verification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// This file implements the evaluator mechanism (S9-9, MVP.md §5.0).
//
// # What this is, and the honest boundary around it
//
// An evaluator is a designated party — possibly another agent, possibly a model —
// that scores a result which cannot be recomputed. A summary, a translation, a piece
// of generated code: there is no second run that produces the same answer, so
// "re-run and compare" is not available.
//
// MVP.md §5.0 requires saying plainly that this is much weaker than recompute, and
// this implementation says it in the places that matter:
//
//   - An evaluator verdict is NOT a binary fact. It is an opinion with an author.
//   - An evaluator can be wrong, bribed, or colluding with the producer.
//   - A verdict is therefore always attributed, and the attribution is part of the
//     record rather than a debugging detail.
//
// # Why the mechanism is deliberately thin
//
// S9-9 is scoped to "mechanism only": designate an evaluator, collect a verdict,
// attribute it. No scoring formula, no reputation, no appeal. Those are policy, and
// building policy on top of an unproven mechanism is how a weak foundation becomes a
// load-bearing one by accident. The interface below is the seam they will attach to.

// Evaluation is an evaluator's verdict about a task's result.
//
// # Why the evaluator's identity is not optional
//
// An unattributed opinion cannot be weighed. If a verdict did not say who produced
// it, a consumer could not distinguish a designated expert from an anonymous
// stranger, and the whole mechanism would degrade to "someone said so". So the
// evaluator id is a required field, not metadata.
type Evaluation struct {
	// EvaluatorID is the agentId of whoever judged. Required.
	EvaluatorID string

	// Score is the evaluator's judgement. Its scale is the evaluator's business;
	// this layer does not impose one, because a scale invented here would be a
	// policy decision smuggled into a mechanism.
	Score float64

	// Passed is the evaluator's bottom-line call.
	//
	// # Why both a score and a pass/fail
	//
	// Consumers need one of the two, and which one varies: a market wants a
	// threshold, a reader wants a number. Recording both lets the evaluator express
	// the judgement once and consumers interpret it, rather than each consumer
	// inventing a threshold over a number the evaluator never meant as one.
	Passed bool

	// Rationale is the evaluator's stated reason.
	//
	// It is required. An opinion with no reason cannot be reviewed, and review is the
	// only thing that makes a weak verdict usable at all — a consumer deciding
	// whether to trust an evaluation has nothing else to go on.
	Rationale string

	// EvaluatedAt is when the judgement was made, in the evaluator's own account.
	EvaluatedAt time.Time
}

// Validate checks an evaluation's shape.
func (e Evaluation) Validate() error {
	if strings.TrimSpace(e.EvaluatorID) == "" {
		return fmt.Errorf("verification: evaluation has no evaluator id; " +
			"an unattributed opinion cannot be weighed")
	}
	if strings.TrimSpace(e.Rationale) == "" {
		return fmt.Errorf("verification: evaluation by %s has no rationale; "+
			"an opinion with no reason cannot be reviewed", e.EvaluatorID)
	}
	if e.EvaluatedAt.IsZero() {
		return fmt.Errorf("verification: evaluation by %s has no timestamp", e.EvaluatorID)
	}
	return nil
}

// Evaluator judges a result that cannot be recomputed.
//
// # Why this takes the whole receipt
//
// An evaluator needs the task, the result and the anchors to judge — the same
// material a recomputer needs, because the judgement is about the same work. Passing
// a narrower type would tempt a caller to construct one, and the fields it omitted
// would be exactly the ones a careful evaluator needed.
type Evaluator interface {
	// Evaluate judges r's result and returns a verdict.
	//
	// A returned error means the evaluator could not judge — not that the work
	// failed. The distinction matters as much here as it does for recompute: "no
	// opinion" and "a negative opinion" must not be collapsed, or an unreachable
	// evaluator would look like a bad result.
	Evaluate(ctx context.Context, r *receipt.Receipt) (Evaluation, error)
}

// EvaluationOutcome is the result of running the evaluator mechanism.
type EvaluationOutcome struct {
	// Evaluation is the verdict, attributed.
	Evaluation Evaluation

	// Recorded reports whether the verdict was written to the receipt.
	Recorded bool

	// Note explains anything a caller should know about how the verdict was handled.
	Note string
}

// Evaluate runs the evaluator mechanism for a receipt.
//
// # The mode check, and why it is the same shape as the recompute verifier's
//
// This mechanism serves `evaluator` tasks. Asking it to judge a `recompute` task
// would be wrong in both directions: the task has a binary answer that does not need
// an opinion, and accepting an opinion about it would let a weaker method be
// substituted for a stronger one. So the gate is symmetric with S9-8, and a caller
// routes on the error rather than guessing.
//
// # What it does not do
//
// It does not decide whether the verdict is correct, does not weight it, and does
// not settle anything. It collects an attributed opinion and records it. Anything
// more would be policy, and policy belongs where the trust model is decided.
func (v *Verifier) Evaluate(ctx context.Context, r *receipt.Receipt, ev Evaluator) (EvaluationOutcome, error) {
	if r == nil {
		return EvaluationOutcome{}, errors.New("verification: nil receipt")
	}
	if ev == nil {
		return EvaluationOutcome{}, errors.New("verification: nil evaluator")
	}

	mode := r.Task.VerificationOrDefault()
	if mode != receipt.VerificationEvaluator {
		return EvaluationOutcome{}, fmt.Errorf("%w: the evaluator mechanism serves %s, but this receipt declares %s",
			ErrModeNotSupported, receipt.VerificationEvaluator, mode)
	}

	eval, err := ev.Evaluate(ctx, r)
	if err != nil {
		return EvaluationOutcome{}, fmt.Errorf("verification: evaluate: %w", err)
	}
	if err := eval.Validate(); err != nil {
		return EvaluationOutcome{}, err
	}

	// Rule 1, applied to opinions. A producer judging its own work is not a verdict.
	if r.AgentID != "" && eval.EvaluatorID == r.AgentID {
		return EvaluationOutcome{}, fmt.Errorf(
			"%w: %s evaluated its own work; an opinion about one's own result is not evidence",
			ErrSelfVerification, eval.EvaluatorID)
	}

	out := EvaluationOutcome{Evaluation: eval}
	if v.cfg.Receipts != nil {
		if err := v.recordEvaluation(r, eval); err != nil {
			return EvaluationOutcome{}, err
		}
		out.Recorded = true
	}
	out.Note = "an evaluator verdict is an attributed opinion, not a binary fact; " +
		"it is weaker than recompute and must not be presented as equivalent"
	return out, nil
}

// recordEvaluation writes the verdict onto the receipt.
//
// # The gap this reveals, stated rather than papered over
//
// The receipt's Verification block has fixed fields — status, verifier, stake,
// recomputedHash — designed for recompute, where the evidence IS a hash. An
// evaluator's evidence is a rationale, and there is no field for it.
//
// Adding one would change the signed payload's field set. That is a minor change by
// the A9 §④ rule (tolerated by older readers), but it is still a change to a frozen
// structure, and S9-9 is scoped to "mechanism only". So the mechanism does NOT
// pretend to store the rationale: it stores the attributed verdict (who judged, and
// the call), and the rationale stays with the caller.
//
// The consequence is real and worth stating: a recorded evaluation is a smaller
// record than a recompute verdict. A consumer reading the receipt learns that a
// named evaluator passed or failed the work, and nothing about why. That is
// genuinely weaker, which is consistent with evaluator being the weaker mode — but a
// future schema should add the field rather than leave consumers guessing.
func (v *Verifier) recordEvaluation(r *receipt.Receipt, eval Evaluation) error {
	status := receipt.VerificationRejected
	if eval.Passed {
		status = receipt.VerificationVerified
	}

	evaluatorID := eval.EvaluatorID
	at := eval.EvaluatedAt.Unix()

	r.Verification = receipt.Verification{
		Status:     status,
		VerifierID: &evaluatorID,
		VerifiedAt: &at,
	}
	return v.cfg.Receipts.UpdateVerification(r)
}

// ErrNoEvaluator reports that no evaluator was supplied for an evaluator task.
var ErrNoEvaluator = errors.New("verification: no evaluator supplied")

// DesignateEvaluator picks an evaluator for a receipt from a candidate list.
//
// # Why this is a function and not a policy object
//
// The full assignment policy (reputation, rotation, eligibility) is deliberately out
// of scope for S9-9. What is in scope is the mechanism's one hard rule: the
// evaluator must not be the producer. A caller that wants more can wrap this.
//
// # Why it refuses rather than returning an empty choice
//
// Returning "" would let a caller treat "nobody eligible" as "proceed without an
// evaluator", and an unevaluated result marked verified is the worst outcome this
// mechanism can produce. It returns an error so that state cannot be reached by
// accident.
func DesignateEvaluator(r *receipt.Receipt, candidates []string) (string, error) {
	if r == nil {
		return "", errors.New("verification: nil receipt")
	}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if c == r.AgentID {
			// Skipping the producer rather than failing lets a caller pass an
			// unfiltered candidate list, which is the common case.
			continue
		}
		return c, nil
	}
	return "", fmt.Errorf("%w: no eligible evaluator among %d candidates for %s",
		ErrNoEvaluator, len(candidates), r.AgentID)
}
