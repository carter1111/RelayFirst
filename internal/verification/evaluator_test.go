package verification_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/verification"
)

// These tests cover S9-9: the evaluator mechanism.
//
// The properties that matter are not "does it collect a verdict" — that is
// mechanical — but the boundaries around it:
//
//   - a producer may not evaluate its own work, and that matters MORE here than for
//     recompute, because an opinion has no reproduction to constrain it;
//   - a verdict is always attributed, because an unattributed opinion cannot be
//     weighed;
//   - the mechanism refuses to serve a mode it is not for, in both directions;
//   - and "no opinion" is distinguishable from "a negative opinion".

// fakeEvaluator returns a canned verdict.
type fakeEvaluator struct {
	eval  verification.Evaluation
	err   error
	calls int
}

func (f *fakeEvaluator) Evaluate(context.Context, *receipt.Receipt) (verification.Evaluation, error) {
	f.calls++
	return f.eval, f.err
}

func evaluatorReceipt(t *testing.T) *receipt.Receipt {
	t.Helper()
	return modeReceipt(t, receipt.VerificationEvaluator)
}

func goodEvaluation(id string) verification.Evaluation {
	return verification.Evaluation{
		EvaluatorID: id,
		Score:       0.9,
		Passed:      true,
		Rationale:   "the summary covers the source material accurately",
		EvaluatedAt: time.Unix(1791015900, 0),
	}
}

// TestEvaluate_CollectsAndAttributesVerdict is the control.
func TestEvaluate_CollectsAndAttributesVerdict(t *testing.T) {
	r := evaluatorReceipt(t)
	v, _ := newVerifier(t, r, &fakeRecomputer{result: r.Result},
		eligible{who: "agent:eip155:8453:0x00000000000000000000000000000000000000f1"})

	ev := &fakeEvaluator{eval: goodEvaluation("agent:eip155:8453:0x00000000000000000000000000000000000000e1")}
	out, err := v.Evaluate(context.Background(), r, ev)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if out.Evaluation.EvaluatorID == "" {
		t.Error("the verdict must be attributed to its author")
	}
	if !out.Evaluation.Passed {
		t.Error("the verdict must be preserved")
	}
	// The receipt must carry the attribution, so a later reader can weigh it.
	if r.Verification.VerifierID == nil || *r.Verification.VerifierID != out.Evaluation.EvaluatorID {
		t.Error("the receipt must record who evaluated it")
	}
	if r.Verification.Status != receipt.VerificationVerified {
		t.Errorf("a passing verdict must be recorded as verified, got %s", r.Verification.Status)
	}
}

// TestEvaluate_RejectsSelfEvaluation is the rule that matters most here.
//
// Recompute at least requires the numbers to agree, so a cheating self-verifier
// still has to do the work. An opinion has no such constraint: a producer evaluating
// its own summary can simply say it is good. So this is checked even though the
// recompute path checks the analogous thing.
func TestEvaluate_RejectsSelfEvaluation(t *testing.T) {
	r := evaluatorReceipt(t)
	v, _ := newVerifier(t, r, &fakeRecomputer{result: r.Result},
		eligible{who: "agent:eip155:8453:0x00000000000000000000000000000000000000f1"})

	// The producer tries to evaluate its own work.
	ev := &fakeEvaluator{eval: goodEvaluation(r.AgentID)}
	_, err := v.Evaluate(context.Background(), r, ev)
	if err == nil {
		t.Fatal("a producer must not be able to evaluate its own work")
	}
	if !errors.Is(err, verification.ErrSelfVerification) {
		t.Errorf("want ErrSelfVerification, got: %v", err)
	}
}

// TestEvaluate_RequiresRationale keeps a verdict reviewable. An opinion with no
// reason cannot be weighed, and review is the only thing that makes a weak verdict
// usable at all.
func TestEvaluate_RequiresRationale(t *testing.T) {
	r := evaluatorReceipt(t)
	v, _ := newVerifier(t, r, &fakeRecomputer{result: r.Result},
		eligible{who: "agent:eip155:8453:0x00000000000000000000000000000000000000f1"})

	ev := &fakeEvaluator{eval: verification.Evaluation{
		EvaluatorID: "agent:eip155:8453:0x00000000000000000000000000000000000000e1",
		Passed:      true,
		EvaluatedAt: time.Unix(1791015900, 0),
		// Rationale deliberately absent.
	}}
	if _, err := v.Evaluate(context.Background(), r, ev); err == nil {
		t.Fatal("an evaluation with no rationale must be rejected")
	}
}

// TestEvaluate_RequiresAttribution is the other half of reviewability.
func TestEvaluate_RequiresAttribution(t *testing.T) {
	r := evaluatorReceipt(t)
	v, _ := newVerifier(t, r, &fakeRecomputer{result: r.Result},
		eligible{who: "agent:eip155:8453:0x00000000000000000000000000000000000000f1"})

	ev := &fakeEvaluator{eval: verification.Evaluation{
		Passed:      true,
		Rationale:   "looks fine",
		EvaluatedAt: time.Unix(1791015900, 0),
		// EvaluatorID deliberately absent.
	}}
	if _, err := v.Evaluate(context.Background(), r, ev); err == nil {
		t.Fatal("an unattributed opinion cannot be weighed and must be rejected")
	}
}

// TestEvaluate_RefusesRecomputeTask is the symmetric gate to S9-8.
//
// A recompute task has a binary answer and does not need an opinion. Accepting one
// would let a weaker method be substituted for a stronger one — the same failure as
// the recompute verifier accepting an evaluator task, in the other direction.
func TestEvaluate_RefusesRecomputeTask(t *testing.T) {
	r := modeReceipt(t, receipt.VerificationRecompute)
	v, _ := newVerifier(t, r, &fakeRecomputer{result: r.Result},
		eligible{who: "agent:eip155:8453:0x00000000000000000000000000000000000000f1"})

	ev := &fakeEvaluator{eval: goodEvaluation("agent:eip155:8453:0x00000000000000000000000000000000000000e1")}
	_, err := v.Evaluate(context.Background(), r, ev)
	if err == nil {
		t.Fatal("the evaluator mechanism must not serve a recompute task")
	}
	if !errors.Is(err, verification.ErrModeNotSupported) {
		t.Errorf("want ErrModeNotSupported, got: %v", err)
	}
	if ev.calls != 0 {
		t.Errorf("the evaluator was called %d times for a task it must not serve", ev.calls)
	}
}

// TestEvaluate_NoOpinionIsNotANegativeOpinion is the distinction that keeps an
// unreachable evaluator from looking like a bad result.
func TestEvaluate_NoOpinionIsNotANegativeOpinion(t *testing.T) {
	r := evaluatorReceipt(t)
	v, _ := newVerifier(t, r, &fakeRecomputer{result: r.Result},
		eligible{who: "agent:eip155:8453:0x00000000000000000000000000000000000000f1"})

	// The evaluator could not judge.
	ev := &fakeEvaluator{err: errors.New("evaluator unreachable")}
	_, err := v.Evaluate(context.Background(), r, ev)
	if err == nil {
		t.Fatal("an evaluator that could not judge must produce an error, not a verdict")
	}
	// And it must not have been recorded as a rejection.
	if r.Verification.Status == receipt.VerificationRejected {
		t.Error("an unreachable evaluator must not be recorded as a negative verdict")
	}

	// Contrast: a negative opinion IS a verdict.
	r2 := evaluatorReceipt(t)
	ev2 := &fakeEvaluator{eval: verification.Evaluation{
		EvaluatorID: "agent:eip155:8453:0x00000000000000000000000000000000000000e1",
		Score:       0.1,
		Passed:      false,
		Rationale:   "the summary omits the main conclusion",
		EvaluatedAt: time.Unix(1791015900, 0),
	}}
	out, err := v.Evaluate(context.Background(), r2, ev2)
	if err != nil {
		t.Fatalf("a negative verdict is still a verdict, not an error: %v", err)
	}
	if out.Evaluation.Passed {
		t.Error("the negative verdict must be preserved")
	}
	if r2.Verification.Status != receipt.VerificationRejected {
		t.Errorf("a failing verdict must be recorded as rejected, got %s", r2.Verification.Status)
	}
}

// TestEvaluate_SaysItIsWeakerThanRecompute is the honesty requirement from MVP.md
// §5.0, enforced rather than merely documented.
//
// The spec is explicit that evaluator must not be presented as equally reliable. A
// mechanism that returned a verdict silently would leave every caller free to assume
// it is as good as recompute, so the note is part of the result.
func TestEvaluate_SaysItIsWeakerThanRecompute(t *testing.T) {
	r := evaluatorReceipt(t)
	v, _ := newVerifier(t, r, &fakeRecomputer{result: r.Result},
		eligible{who: "agent:eip155:8453:0x00000000000000000000000000000000000000f1"})

	ev := &fakeEvaluator{eval: goodEvaluation("agent:eip155:8453:0x00000000000000000000000000000000000000e1")}
	out, err := v.Evaluate(context.Background(), r, ev)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !strings.Contains(out.Note, "not a binary fact") {
		t.Errorf("the outcome must state that a verdict is an opinion, got: %q", out.Note)
	}
	if !strings.Contains(out.Note, "weaker than recompute") {
		t.Errorf("the outcome must state that it is weaker than recompute, got: %q", out.Note)
	}
}

func TestEvaluate_RejectsNilInputs(t *testing.T) {
	r := evaluatorReceipt(t)
	v, _ := newVerifier(t, r, &fakeRecomputer{result: r.Result},
		eligible{who: "agent:eip155:8453:0x00000000000000000000000000000000000000f1"})

	if _, err := v.Evaluate(context.Background(), nil, &fakeEvaluator{}); err == nil {
		t.Error("a nil receipt must be rejected")
	}
	if _, err := v.Evaluate(context.Background(), r, nil); err == nil {
		t.Error("a nil evaluator must be rejected")
	}
}

// TestDesignateEvaluator_SkipsTheProducer is the one hard rule in assignment.
func TestDesignateEvaluator_SkipsTheProducer(t *testing.T) {
	r := evaluatorReceipt(t)

	// The producer is first in the list, as it would be if a caller passed an
	// unfiltered candidate set.
	got, err := verification.DesignateEvaluator(r, []string{
		r.AgentID,
		"agent:eip155:8453:0x00000000000000000000000000000000000000e1",
	})
	if err != nil {
		t.Fatalf("DesignateEvaluator: %v", err)
	}
	if got == r.AgentID {
		t.Fatal("the designated evaluator must not be the producer")
	}
}

// TestDesignateEvaluator_RefusesWhenNobodyIsEligible is the failure that must not be
// silent: an unevaluated result marked verified is the worst outcome this mechanism
// can produce, so "nobody eligible" is an error rather than an empty choice.
func TestDesignateEvaluator_RefusesWhenNobodyIsEligible(t *testing.T) {
	r := evaluatorReceipt(t)

	if _, err := verification.DesignateEvaluator(r, []string{r.AgentID}); err == nil {
		t.Fatal("when only the producer is available there is no eligible evaluator")
	} else if !errors.Is(err, verification.ErrNoEvaluator) {
		t.Errorf("want ErrNoEvaluator, got: %v", err)
	}

	if _, err := verification.DesignateEvaluator(r, nil); err == nil {
		t.Fatal("an empty candidate list must fail rather than return nothing")
	}
	if _, err := verification.DesignateEvaluator(nil, []string{"x"}); err == nil {
		t.Error("a nil receipt must be rejected")
	}
}
