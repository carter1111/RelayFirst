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

// NodePoolSource supplies the Layer 0 settlement inputs for an epoch.
//
// It is an interface rather than the concrete store types so the mining package keeps no
// dependency on how tenure or bindings are persisted; store satisfies it.
type NodePoolSource interface {
	// NodeTenures is each node's tenure count as of the epoch.
	NodeTenures(epoch uint64) (map[string]int, error)

	// Bindings maps an agent id to the node id it is bound to, as of the epoch.
	Bindings(epoch uint64) (map[string]string, error)
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

	// Work accumulates a receipt's measured WORK, which is the settlement input
	// (MVP.md §5.3 -> §6.2). Required to score; when nil, receipts are stored but
	// nothing is recorded.
	//
	// This replaced a direct points Credit (D1): a receipt contributes work, and an
	// epoch's points come from settling the work totals, not from a per-receipt rate.
	Work scoring.WorkLedger

	// Points receives the SETTLED points when an epoch is finalized (see Finalize).
	// Required only for finalization; a miner that never finalizes (or has no points
	// ledger) still records work.
	Points scoring.PointsLedger

	// Nodes supplies the Layer 0 inputs at finalization: each node's tenure count and
	// the agent->node bindings. Optional; when nil, no node pool is paid and the whole
	// budget goes to the work pool (the pre-Layer-0 behaviour).
	Nodes NodePoolSource

	// Clock is injected for tests. Nil means time.Now.
	Clock func() time.Time

	// OnVerdict, when set, receives the scoring outcome of each receipt. A CLI
	// uses it to show points accruing; tests use it to assert behaviour.
	OnVerdict func(r *receipt.Receipt, v scoring.Verdict)

	// OnError, when set, is told about a scoring step that failed for one receipt,
	// so the failure is VISIBLE even though it is not returned.
	//
	// # Why an error is reported but not returned
	//
	// Save must not fail for a scoring problem: the receipt is already stored and can
	// be re-scored, and returning an error would make the runner treat a successful
	// mining iteration as a failed one and back off from it. So the failure is swallowed
	// — but swallowing it SILENTLY was wrong in the other direction: a receipt whose
	// points were never credited looked exactly like one that earned nothing, and the
	// miner had no way to tell "I earned nothing" from "the ledger rejected my credit".
	//
	// `stage` names which step failed ("emit" or "credit"), because the two mean
	// different things: an emit failure is a scoring-input problem, a credit failure is
	// a ledger problem, and a caller watching the numbers wants to know which.
	OnError func(r *receipt.Receipt, stage string, err error)
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
//
// But they are REPORTED through OnError, not discarded. A swallowed failure that
// nobody can see is indistinguishable from "this receipt earned nothing", and the
// miner cannot act on a problem it cannot observe.
func (s *ScoringSink) score(r *receipt.Receipt, at time.Time) {
	if s.Artifacts == nil || s.Work == nil {
		return
	}

	// RecordWork claims the artifact (dedup, A6) and records the work. It no longer
	// credits points: points come from Finalize.
	verdict, err := scoring.RecordWork(r, s.Artifacts, s.Work, s.paramsFor(r), at)
	if err != nil {
		s.reportError(r, "record-work", err)
		return
	}

	if s.OnVerdict != nil {
		s.OnVerdict(r, verdict)
	}
}

// Finalize settles an epoch: it turns the accumulated WORK into POINTS and writes
// them, once.
//
// # Who calls this, and when (condition 2, D1)
//
// Settlement is NOT automatic on the mining path, on purpose: it is an epoch-level,
// once-per-epoch step, and running it on every receipt would settle a moving total and
// produce a number that changes as more work arrives — the opposite of a settlement.
// The trigger is therefore explicit: an operator (or a scheduler) runs the finalize
// step once an epoch has closed, with `relayfirst settle`, and it is idempotent per
// epoch through the points ledger. See docs/notes/settlement-trigger.md.
//
// # What it does
//
//  1. Reads the epoch's work totals (the settlement input).
//  2. Runs scoring.Settle: share the fixed budget, then apply the per-agent cap.
//  3. Writes one points entry per agent for the epoch.
//
// Returning the settled map lets a caller build the epoch's Merkle root (S7) from the
// same numbers, so the published root and the credited points cannot disagree.
func (s *ScoringSink) Finalize(epoch uint64, at time.Time) (map[string]float64, error) {
	if s.Work == nil {
		return nil, fmt.Errorf("mining: no work ledger to settle")
	}
	if s.Points == nil {
		return nil, fmt.Errorf("mining: no points ledger to settle into")
	}

	totals, err := s.Work.Totals(epoch)
	if err != nil {
		return nil, fmt.Errorf("mining: read work totals for epoch %d: %w", epoch, err)
	}
	counts, err := s.Work.Counts(epoch)
	if err != nil {
		return nil, fmt.Errorf("mining: read work counts for epoch %d: %w", epoch, err)
	}

	in := scoring.Inputs{Work: totals, ReceiptCounts: counts}
	if s.Nodes != nil {
		if in.NodeTenures, err = s.Nodes.NodeTenures(epoch); err != nil {
			return nil, fmt.Errorf("mining: read node tenures for epoch %d: %w", epoch, err)
		}
		if in.Bindings, err = s.Nodes.Bindings(epoch); err != nil {
			return nil, fmt.Errorf("mining: read bindings for epoch %d: %w", epoch, err)
		}
	}

	settled, err := scoring.Settle(epoch, totals)
	if err != nil {
		return nil, fmt.Errorf("mining: settle epoch %d: %w", epoch, err)
	}

	// Two-pool split (MVP.md §6.2c): Layer 1 (work) and Layer 0 (nodes). Without a node
	// source there is no Layer 0, so the whole budget goes to work -- the single-pool
	// result, which is what `settled` already holds.
	layer0 := map[string]float64{}
	if s.Nodes != nil {
		split, err := scoring.SettleEpoch(epoch, in)
		if err != nil {
			return nil, fmt.Errorf("mining: settle epoch %d: %w", epoch, err)
		}
		settled = split.Work
		layer0 = split.Node
	}

	// Write one SCOPE-TAGGED entry per recipient. The entry id is derived from the
	// epoch, agent and scope -- settlement is per (agent, scope), and the points
	// ledger's idempotency key is what makes a re-run of Finalize a no-op rather than a
	// double credit. An agent can appear twice (once for work, once for a node); the
	// scope keeps the two entries from colliding on one id.
	for agent, points := range settled {
		if points <= 0 {
			continue
		}
		if _, err := s.Points.Credit(settlementEntryID(epoch, "work", agent), agent, epoch, points, at); err != nil {
			return settled, fmt.Errorf("mining: credit work points for %s epoch %d: %w", agent, epoch, err)
		}
	}
	for node, points := range layer0 {
		if points <= 0 {
			continue
		}
		if _, err := s.Points.Credit(settlementEntryID(epoch, "node", node), node, epoch, points, at); err != nil {
			return settled, fmt.Errorf("mining: credit node points for %s epoch %d: %w", node, epoch, err)
		}
	}
	return settled, nil
}

// settlementEntryID is the points-ledger idempotency key for an (epoch, scope, recipient)
// settlement.
//
// It is derived rather than random so a second Finalize of the same epoch collides with
// the first and is ignored, which is what makes finalization safe to re-run after a
// crash. The "settle:" prefix keeps it from ever colliding with a receipt id. The scope
// ("work" or "node") is what lets one identity hold both a work entry and a node entry in
// the same epoch without the two crowding each other out of the ledger.
func settlementEntryID(epoch uint64, scope, recipient string) string {
	return fmt.Sprintf("settle:%d:%s:%s", epoch, scope, recipient)
}

// reportError surfaces a swallowed scoring failure when a reporter is configured.
//
// It takes the nil check off every call site so the two reporting paths (emit and
// credit) cannot forget it.
func (s *ScoringSink) reportError(r *receipt.Receipt, stage string, err error) {
	if s.OnError != nil {
		s.OnError(r, stage, err)
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
// DescribeVerdict renders a verdict for a progress line.
//
// It reports WORK, not points, because a receipt no longer earns points directly
// (D1): the points appear when the epoch settles. Saying "+X points" here would be the
// model that was migrated away from.
func DescribeVerdict(v scoring.Verdict) string {
	if v.Work <= 0 {
		if v.Reason != "" {
			return "no work: " + v.Reason
		}
		return "no work"
	}
	return fmt.Sprintf("+%.4f work", v.Work)
}

// ShortArtifact trims an artifact key for display.
func ShortArtifact(key string) string {
	k := strings.TrimPrefix(key, "sha256:")
	if len(k) <= 12 {
		return k
	}
	return k[:12] + "..."
}
