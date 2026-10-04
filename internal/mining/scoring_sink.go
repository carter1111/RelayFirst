package mining

import (
	"fmt"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/scoring"
)

// ReceiptLister reads back an agent's receipts for an epoch.
//
// It exists so the scoring sink can compute the diversity factor from *stored*
// receipts rather than tracking a parallel counter. store.ReceiptStore already
// satisfies it, so no changes are needed there.
type ReceiptLister interface {
	ByAgent(agentID string, epoch uint64) ([]*receipt.Receipt, error)
}

// ScoringSink persists receipts *and* credits the points they earn.
//
// # Why this exists
//
// Producing a signed receipt is not the same as being paid for it. Without this
// sink the miner writes work to disk that no ledger ever recognises, so the
// economy never moves and "can mine" is only half true. This type closes that
// loop: persist, then evaluate, then credit.
//
// # Ordering, and why it matters
//
// The receipt is persisted BEFORE it is scored. If scoring fails, the work is
// still recorded and can be re-scored later; crediting first would risk paying
// for a receipt that was never stored. Losing points is recoverable, paying for
// nothing is not.
//
// # What is never credited
//
// A receipt that fails our own validation earns nothing. That is decided before
// the dedup ledger is consulted, so an invalid receipt cannot even claim an
// artifact — which matters, because claiming one would deny novelty to the honest
// agent who later does the real work (see the note on Sighting.SeenCount).
type ScoringSink struct {
	// Inner persists receipts. Required.
	Inner ReceiptSink

	// Receipts reads an agent's receipts back for a given epoch. It is used to
	// compute the diversity factor. Optional: without it, diversity is treated as
	// 1 (no penalty), which is the permissive direction and never inflates a score.
	Receipts ReceiptLister

	// Artifacts is the global dedup ledger (invariant A6). Required to score;
	// when nil, receipts are stored but nothing is credited.
	Artifacts scoring.Ledger

	// Verdicts decides whether a receipt counts as verified (S4-0).
	//
	// When nil, SelfCheckVerdicts is used, which reproduces the miner's pre-S4
	// behaviour. Injecting a verifier-derived source is what makes the score stop
	// trusting the submitter; see VerdictSource.
	Verdicts VerdictSource

	// Points is the append-only credit ledger (invariant A5). Required to credit;
	// when nil, receipts are stored but nothing is credited.
	Points scoring.PointsLedger

	// Clock is injected for tests. Nil means time.Now.
	Clock func() time.Time

	// OnVerdict, when set, receives the scoring outcome of each receipt. A CLI
	// uses it to show points accruing; tests use it to assert behaviour.
	OnVerdict func(r *receipt.Receipt, v scoring.Verdict)
}

// compile-time assertion.
var _ ReceiptSink = (*ScoringSink)(nil)

func (s *ScoringSink) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

// Save persists r and credits whatever it earned.
//
// Idempotency is layered: the inner store ignores a duplicate receipt id, the
// points ledger ignores a duplicate receipt id, and the dedup ledger reports
// novelty only once. A replay therefore cannot pay twice at any layer.
func (s *ScoringSink) Save(r *receipt.Receipt, artifactKey string, at time.Time) error {
	if s.Inner == nil {
		return fmt.Errorf("mining: scoring sink has no inner sink")
	}

	// Persist first, so work is never lost to a scoring failure.
	if err := s.Inner.Save(r, artifactKey, at); err != nil {
		return err
	}

	s.score(r, at)
	return nil
}

// score evaluates r and credits it when it earns something.
//
// Scoring failures are swallowed deliberately: the receipt is already stored, and
// a transient ledger problem must not turn a successful mining iteration into a
// failure that the runner then backs off from.
func (s *ScoringSink) score(r *receipt.Receipt, at time.Time) {
	if s.Artifacts == nil || s.Points == nil {
		return
	}

	verdict, err := scoring.Emit(r, s.Artifacts, s.paramsFor(r), at)
	if err != nil {
		return
	}

	if verdict.Points > 0 {
		// Emit already recorded the sighting; crediting is separate because the
		// points ledger is the thing that must be idempotent per receipt.
		_, _ = s.Points.Credit(r.ReceiptID, r.AgentID, r.Epoch, verdict.Points, at)
	}

	if s.OnVerdict != nil {
		s.OnVerdict(r, verdict)
	}
}

// paramsFor derives the scoring parameters for r.
//
// # Why Verified comes from a source rather than from a local check (S4-0)
//
// This used to ask the submitter whether the submitter's own work was good, by
// validating the receipt here. That was a recorded stopgap: it kept the miner able to
// earn something before S4 existed, at the cost of making the score trust the party
// it was meant to be checking.
//
// A VerdictSource is now injected. The production source reads the verdict a verifier
// wrote onto the receipt; a caller with no verifier can inject SelfCheckVerdicts,
// which is named so its limitation is visible at the call site.
//
// The default when no source is configured is SelfCheckVerdicts, matching the
// behaviour the miner had before S4 rather than silently refusing to credit anything.
// That is a deliberate compatibility choice and not a claim that a self-check is
// verification.
func (s *ScoringSink) paramsFor(r *receipt.Receipt) scoring.Params {
	source := s.Verdicts
	if source == nil {
		source = SelfCheckVerdicts{}
	}
	verified := source.Verified(r)

	epoch := r.Epoch
	if epoch == 0 {
		epoch = scoring.EpochOf(s.now())
	}

	return scoring.Params{
		Verified:          verified,
		SameDomainRepeats: s.sameDomainRepeats(r, epoch),
		BudgetFactor:      scoring.BudgetFactor(epoch, s.Points.EpochBalance(r.AgentID, epoch)),
	}
}

// sameDomainRepeats counts how many times this agent has already been credited for
// this receipt's domain within the epoch, which drives diversity (MVP.md §5.3).
//
// # Why this reads stored receipts instead of counting credits
//
// An earlier attempt counted every credit the agent had in the epoch and treated
// them all as same-domain. That is not merely imprecise, it is inverted: an agent
// who worked across many domains would be penalised more heavily than one who
// hammered a single domain, which is the opposite of what diversity is for. The
// domain therefore has to come from the actual URLs, and receipts are where those
// live.
//
// A count that cannot be computed yields zero repeats, i.e. no penalty. That
// direction is deliberate: it can only ever be generous, never inflate a score
// above what the formula allows.
func (s *ScoringSink) sameDomainRepeats(r *receipt.Receipt, epoch uint64) int {
	if s.Receipts == nil {
		return 0
	}

	domain := scoring.Domain(r)
	if domain == "" {
		// Compute tasks carry an inline anchor and have no domain, so diversity
		// cannot apply.
		return 0
	}

	prior, err := s.Receipts.ByAgent(r.AgentID, epoch)
	if err != nil {
		return 0
	}

	repeats := 0
	for _, other := range prior {
		// This receipt is already persisted by the time scoring runs, so it
		// appears in the list; a receipt must not count itself as a repeat.
		if other.ReceiptID == r.ReceiptID {
			continue
		}
		if scoring.Domain(other) == domain {
			repeats++
		}
	}
	return repeats
}

// DescribeVerdict renders a verdict for a progress line.
func DescribeVerdict(v scoring.Verdict) string {
	if v.Points <= 0 {
		if v.Reason != "" {
			return "no credit: " + v.Reason
		}
		return "no credit"
	}
	return fmt.Sprintf("+%.4f points", v.Points)
}

// ShortArtifact trims an artifact key for display.
func ShortArtifact(key string) string {
	k := strings.TrimPrefix(key, "sha256:")
	if len(k) <= 12 {
		return k
	}
	return k[:12] + "..."
}
