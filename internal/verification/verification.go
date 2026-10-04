// Package verification implements adversarial verification (MVP.md §5.5).
//
// # The problem it solves
//
// Mining tasks are self-generated, so "the agent did the work" is currently the
// agent's own claim. S4 introduces a second party who re-runs the task and reports
// whether the claimed result reproduces. Because task types are restricted to those
// with independent ground truth (invariant A2), that verdict is binary: the result
// either reproduces or it does not. There is no "somewhat correct", and therefore no
// dispute layer.
//
// # What verification is here
//
// Re-execution. internal/mining already runs a task deterministically from its spec,
// so a verifier re-runs the same spec and compares the result hash. The verifier
// does not need the producer's cooperation or trust.
//
// # The honest limitation, stated where it will be read
//
// A large operator running enough agents can still verify its own work. The cost
// rises linearly with the number of agents it must operate, and the payoff is
// suppressed by the global artifact dedup ledger (invariant A6), but the defence is
// economic rather than cryptographic. MVP.md §5.6 is explicit that this is an
// acceptable trade rather than a proof, and this package must not be described as
// unbreakable. The load-bearing anti-farming mechanism remains A6.
//
// # What this package deliberately does NOT do
//
// It does not move points. Invariant A5 makes points non-transferable, and the
// points ledger's method set is pinned by a test to exactly six methods with no
// debit. So a verifier's commitment is recorded as a separate magnitude in a
// separate ledger (see stake.go), never as a deduction from a balance. Slashing here
// means "this commitment was surrendered and recorded", never "points were
// subtracted from someone".
package verification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// ErrSelfVerification reports an attempt by a producer to verify its own receipt
// (MVP.md §5.6 rule 1).
var ErrSelfVerification = errors.New("verification: an agent may not verify its own receipt")

// ErrModeNotSupported reports that a receipt declares a verification mode this
// verifier does not implement.
//
// # Why this is its own error rather than a generic failure
//
// "I cannot check this" and "I checked and it failed" have different fixes, and
// collapsing them makes a mode mismatch look like a bad receipt. The same split as
// UnsupportedError versus ValidationError in the receipt layer, and for the same
// reason: an operator who sees a generic error will conclude the producer cheated,
// when the actual answer is that the wrong verifier was asked.
//
// A caller should route it: a receipt declaring `evaluator` needs an evaluator, and
// one declaring `dispute` is not checkable by this build at all.
var ErrModeNotSupported = errors.New("verification: unsupported verification mode")

// Recomputer re-runs a receipt's task and returns the result it observed.
//
// It is an interface rather than a direct call so the comparison logic can be tested
// without a network, and so a future verifier could run the task in a sandbox.
type Recomputer interface {
	Recompute(ctx context.Context, r *receipt.Receipt) (receipt.Result, error)
}

// ReceiptUpdater persists a receipt's verification outcome.
//
// It is narrow on purpose: a verifier needs to record its verdict and nothing else,
// so it should not hold a handle that can rewrite receipts.
type ReceiptUpdater interface {
	UpdateVerification(r *receipt.Receipt) error
}

// Policy decides who verifies what, and whether an agent may verify at all.
//
// # Why the assignment policy is an interface
//
// The production policy is the open decision BLK-3, and locking one in now would
// freeze a choice that has real trade-offs (determinism versus censorship
// resistance, candidate-set freshness, grindability of agent ids). Everything else
// in this package — re-execution, the binary verdict, the anti-collusion guards, the
// commitment record — is independent of that choice. So the policy is injected, and
// the mechanism is usable with any of the candidates.
//
// Note that a Policy cannot weaken the anti-collusion rules: Verify re-checks its
// output. A policy is a chooser, not an authority.
type Policy interface {
	// Assign returns the agent who should verify r, given the candidates offered.
	//
	// candidates is the caller's current view of eligible verifiers, which may be
	// empty when the caller has no registry. Returning an error means "no valid
	// assignment exists", which is a legitimate outcome rather than a failure: an
	// epoch with no eligible verifier should leave receipts pending, not verify them
	// by default.
	Assign(r *receipt.Receipt, candidates []string) (string, error)

	// AllowVerify reports whether agent may verify, given how much it has produced
	// and verified in this epoch (MVP.md §5.6 rule 2).
	//
	// The counts are supplied by the caller because this package does not own an
	// epoch's production statistics; a policy that needs a different measure can be
	// written without changing the mechanism.
	AllowVerify(epoch uint64, agent string, produced, verified int) error
}

// Activity reports an agent's epoch activity, which the ratio cap is measured against.
//
// # Why this is injected rather than computed here
//
// This package deliberately does not own an epoch's production statistics — the
// mining and store layers do, and reading them here would give verification a second,
// divergent view of the same facts. An Activity source lets a caller supply the
// authoritative counts without verification growing its own bookkeeping.
//
// When no Activity is configured, the counts are (0, 0). With a policy that refuses
// a pure verifier, that means verification is *refused* rather than silently
// permitted — the fail-closed direction, since permitting it would disable the very
// rule the ratio exists to enforce.
type Activity interface {
	// Produced returns how many receipts the agent produced in the epoch.
	Produced(epoch uint64, agent string) (int, error)

	// Verified returns how many receipts the agent has already verified in the epoch.
	Verified(epoch uint64, agent string) (int, error)
}

// AnchorChecker decides whether a receipt's anchor evidence still matches what its
// declared source returns.
//
// # Why this is separate from re-running the task
//
// Running a task answers "what does the task produce now". That is not the same question as
// "does the evidence this receipt recorded still describe the same content". A probe receipt
// records a status code and a content hash; if a task produced the same status from
// completely different bytes, re-running alone would call it agreement.
//
// The content hash is also inside the *signed* payload (MVP.md §4.2), and invariant A2
// defines valid work as work with a re-fetchable anchor. So a verifier that ignores it is
// accepting a signed claim it never checked — which is precisely the class of gap §5.5's
// "re-fetch the anchor" step exists to close.
type AnchorChecker interface {
	// Check reports whether r's anchors still describe the content the source returns.
	// A nil error means the anchors are consistent.
	Check(ctx context.Context, r *receipt.Receipt) error
}

// Config configures a Verifier.
type Config struct {
	// Policy decides assignment and eligibility. Required: without one there is no
	// basis for choosing a verifier, and defaulting to "anyone" would be exactly the
	// self-verification hole this package exists to close.
	Policy Policy

	// Stakes records commitments. Required.
	Stakes StakeLedger

	// Receipts records the verdict onto the receipt. Required.
	Receipts ReceiptUpdater

	// Recomputer re-runs the task. Required.
	Recomputer Recomputer

	// Activity supplies the epoch counts the ratio cap is measured against.
	//
	// Optional, but leaving it nil means a policy with a ratio will refuse every
	// verification (counts are 0/0, and 0 production is refused). That is deliberate:
	// failing closed is better than enforcing a rule against fabricated numbers.
	Activity Activity

	// Anchors checks that the receipt's anchor evidence still matches its source
	// (MVP.md §5.5). Optional.
	//
	// When nil, anchor consistency is not checked and a receipt is judged on reproduction
	// alone. That is weaker than §5.5 describes, and the reason it is not the default is
	// that the check needs a fetcher: a verifier with no network cannot perform it. A
	// deployment should configure it; a caller that cannot should know it is skipping a
	// documented step, which is why Describe-style labels exist on the other injectables.
	Anchors AnchorChecker

	// Window decides whether a receipt may still be verified (MVP.md §5.4).
	//
	// When nil, AlwaysOpen is used: verification is permitted at any time, and honest
	// work may be falsely rejected once the anchor's content has changed. That is the
	// accuracy-versus-refusal trade documented on AlwaysOpen, and a CLI reports which
	// window is active rather than leaving it implicit.
	Window Window

	// StakePoints is the commitment magnitude recorded per verification, in points.
	//
	// It is a *magnitude for deterrence*, not a deposit: nothing is deducted from any
	// balance, and invariant A5 forbids moving points between agents. Zero means
	// DefaultStakePoints.
	StakePoints uint64

	// Candidates is the caller's view of eligible verifiers, passed to the policy.
	// Empty is allowed for a policy that does not need a registry.
	Candidates []string

	// Now is injected for deterministic tests. Nil means time.Now.
	Now func() time.Time
}

// DefaultStakePoints is the commitment magnitude from MVP.md §5.5 (`stake: 50`).
const DefaultStakePoints = 50

// Outcome reports one verification.
type Outcome struct {
	// Verified is the binary verdict. There is no partial credit: the task types
	// have independent ground truth, so a result either reproduces or it does not.
	Verified bool

	// VerifierID is who performed the verification.
	VerifierID string

	// RecomputedHash is the hash the verifier observed, stored as the evidence for
	// the verdict so a later reader can tell "wrong result" from "wrong anchor".
	RecomputedHash string

	// CommittedID identifies the commitment record for this verification, so a
	// settlement can be traced back to the verdict that caused it.
	CommittedID string

	// Reason explains a rejection, or an error outcome.
	Reason string
}

// String renders an outcome for logs and CLI output.
func (o Outcome) String() string {
	verdict := "rejected"
	if o.Verified {
		verdict = "verified"
	}
	if o.Reason != "" {
		return fmt.Sprintf("%s (%s)", verdict, o.Reason)
	}
	return verdict
}

// Verifier performs adversarial verification.
type Verifier struct {
	cfg Config

	// recordMu serializes the verdict-recording step.
	//
	// # Why this lock exists, and why it is this narrow
	//
	// Verify writes the outcome into the receipt it was handed — `r.Verification = ...` — which
	// is a mutation of caller-owned memory. Two goroutines verifying the same receipt would
	// therefore write the same struct field concurrently, and that is a genuine data race: it
	// was found by the race-detector gate as soon as a test exercised it.
	//
	// The lock covers only the recording step, not the re-execution. Re-execution is the
	// expensive part (a network fetch plus a task run), so serializing it would destroy the
	// parallelism that makes verification practical; recording is a struct assignment and a
	// ledger write, so serializing it costs nothing measurable.
	//
	// A single lock rather than one per receipt is deliberate: the critical section is
	// microseconds, and a per-receipt map of mutexes would add allocation and lifetime
	// management to protect something that is not contended at that granularity.
	recordMu sync.Mutex
}

// New validates cfg and returns a verifier.
func New(cfg Config) (*Verifier, error) {
	switch {
	case cfg.Policy == nil:
		return nil, errors.New("verification: a policy is required; there is no safe default assignment")
	case cfg.Stakes == nil:
		return nil, errors.New("verification: a stake ledger is required")
	case cfg.Receipts == nil:
		return nil, errors.New("verification: a receipt updater is required")
	case cfg.Recomputer == nil:
		return nil, errors.New("verification: a recomputer is required")
	}
	if cfg.StakePoints == 0 {
		cfg.StakePoints = DefaultStakePoints
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Verifier{cfg: cfg}, nil
}

// Verify re-runs r's task, records the verdict, and settles the commitment.
//
// # Ordering, and what each step protects
//
//  1. Assign a verifier and reject a self-assignment. A policy is not trusted to get
//     this right, so the result is re-checked here.
//  2. Check eligibility (the ratio cap). A pure verifier with no production has no
//     stake in being truthful.
//  3. Re-execute and compare. A failed re-execution is an error, never agreement:
//     treating an unreachable source as a pass would make the whole check gameable by
//     taking the source offline.
//  4. Record the verdict on the receipt, then settle the commitment.
//
// The commitment is recorded at a stable id derived from the receipt, so a repeated
// verification cannot accumulate commitments.
func (v *Verifier) Verify(ctx context.Context, r *receipt.Receipt) (Outcome, error) {
	if r == nil {
		return Outcome{}, errors.New("verification: nil receipt")
	}
	if strings.TrimSpace(r.ReceiptID) == "" {
		return Outcome{}, errors.New("verification: receipt has no id")
	}

	// The declared verification mode decides whether this verifier can act at all
	// (S9-8, MVP.md §5.0).
	//
	// # Why this is a refusal and not a downgrade
	//
	// This verifier implements `recompute`: re-run the task and compare. A receipt
	// declaring `evaluator` cannot be checked that way — an evaluator's judgement is
	// not a reproducible computation, so running the task again would produce a
	// result that need not match and the comparison would be meaningless.
	//
	// Refusing is the only honest answer. Silently falling back to recompute would
	// report a binary verdict for a task that was never eligible for one, which is
	// worse than no verdict: it would launder a weak claim into a strong one. The
	// error is distinguishable so a caller routes it to the right verifier rather
	// than logging it as a failure.
	if mode := r.Task.VerificationOrDefault(); mode != receipt.VerificationRecompute {
		return Outcome{}, fmt.Errorf("%w: this verifier implements %s, not %s",
			ErrModeNotSupported, receipt.VerificationRecompute, mode)
	}

	verifier, err := v.cfg.Policy.Assign(r, v.cfg.Candidates)
	if err != nil {
		return Outcome{}, fmt.Errorf("verification: assign: %w", err)
	}
	if strings.TrimSpace(verifier) == "" {
		return Outcome{}, errors.New("verification: the policy assigned no verifier")
	}

	// The window is checked before anything else expensive happens. It protects the
	// producer from a false rejection: content changes, so re-checking an old receipt
	// could fail for a reason unrelated to whether the work was honest.
	window := v.cfg.Window
	if window == nil {
		window = AlwaysOpen{}
	}
	if err := window.Open(r, v.cfg.Now()); err != nil {
		return Outcome{VerifierID: verifier, Reason: err.Error()}, fmt.Errorf("verification: %w", err)
	}

	// Rule 1, re-checked. A policy is a chooser, not an authority: trusting its output
	// would mean a buggy or hostile Assigner could enable self-verification.
	if verifier == r.AgentID {
		return Outcome{}, fmt.Errorf("%w: %s", ErrSelfVerification, verifier)
	}

	// Rule 2. The counts come from an injected Activity source, or (0, 0) when none is
	// configured — which fails *closed*: a policy that refuses a pure verifier will
	// refuse, rather than the rule being silently skipped.
	produced, verifiedCount := 0, 0
	if v.cfg.Activity != nil {
		var err error
		if produced, err = v.cfg.Activity.Produced(r.Epoch, verifier); err != nil {
			return Outcome{}, fmt.Errorf("verification: read produced count: %w", err)
		}
		if verifiedCount, err = v.cfg.Activity.Verified(r.Epoch, verifier); err != nil {
			return Outcome{}, fmt.Errorf("verification: read verified count: %w", err)
		}
	}
	if err := v.cfg.Policy.AllowVerify(r.Epoch, verifier, produced, verifiedCount); err != nil {
		// The policy's message is used as-is: wrapping it again would produce a doubled
		// "verification:" prefix, and the policy's own wording is the useful part.
		return Outcome{}, err
	}

	recomputed, err := v.cfg.Recomputer.Recompute(ctx, r)
	if err != nil {
		// Not agreement. See the note on step 3 above.
		return Outcome{VerifierID: verifier, Reason: "re-execution failed: " + err.Error()}, fmt.Errorf("verification: recompute: %w", err)
	}

	verified := recomputed.Hash == r.Result.Hash && recomputed.Value == r.Result.Value

	var anchorErr error

	// Anchor consistency is checked only when reproduction succeeded, so the common case —
	// a result that does not reproduce — reports the clearest reason rather than a
	// secondary one. A receipt whose anchor no longer matches is rejected even when its
	// result reproduces, because the evidence supporting that result is then unverifiable:
	// it describes content nobody served.
	if verified && v.cfg.Anchors != nil {
		if err := v.cfg.Anchors.Check(ctx, r); err != nil {
			verified = false
			anchorErr = err
		}
	}

	now := v.cfg.Now()

	// The recording step is serialized: it mutates the caller's receipt in place and settles a
	// shared commitment. Re-execution above deliberately happens outside the lock, because that
	// is the expensive part and holding a lock across a network fetch would serialize all
	// verification.
	v.recordMu.Lock()
	defer v.recordMu.Unlock()

	verifiedAt := now.Unix()
	r.Verification = receipt.Verification{
		Status:         statusFor(verified),
		VerifierID:     &verifier,
		VerifiedAt:     &verifiedAt,
		Stake:          v.cfg.StakePoints,
		RecomputedHash: &recomputed.Hash,
	}

	if err := v.cfg.Receipts.UpdateVerification(r); err != nil {
		return Outcome{}, fmt.Errorf("verification: record verdict: %w", err)
	}

	// The commitment id is derived from the receipt, so re-verifying cannot create a
	// second commitment for the same work.
	committedID := commitmentID(r.ReceiptID)
	wrote, err := v.cfg.Stakes.Commit(committedID, verifier, r.Epoch, v.cfg.StakePoints, now)
	if err != nil {
		return Outcome{}, fmt.Errorf("verification: commit: %w", err)
	}

	out := Outcome{
		Verified:       verified,
		VerifierID:     verifier,
		RecomputedHash: recomputed.Hash,
		CommittedID:    committedID,
	}
	if !verified {
		if anchorErr != nil {
			out.Reason = "the anchor evidence does not match the source: " + anchorErr.Error()
		} else {
			out.Reason = "the recomputed result does not match the claimed result"
		}
	}

	// Re-verification is a no-op on the commitment.
	//
	// A commitment settles once, and a repeated verdict for the same receipt must not
	// be able to re-settle it — otherwise a second look could change the consequence
	// the first one recorded, which is exactly the accountability the record exists to
	// provide. The verdict above is still recomputed and written, so re-verifying
	// remains useful as a consistency check; only the settlement is skipped.
	if !wrote {
		return out, nil
	}

	// Settlement is deliberately asymmetric in what it *records*, not in what it pays:
	//   agreement  -> the commitment is released (the verifier's claim was sound)
	//   rejection  -> the commitment is slashed (the verifier made a disputable claim)
	//
	// Neither branch touches a point balance. Slashing here means the commitment was
	// surrendered and recorded, which is a deterrence ledger entry and nothing more.
	if verified {
		_, err = v.cfg.Stakes.Release(committedID, now)
	} else {
		_, err = v.cfg.Stakes.Slash(committedID, now)
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("verification: settle: %w", err)
	}

	return out, nil
}

// statusFor maps a verdict to the receipt's recorded status.
func statusFor(verified bool) receipt.VerificationStatus {
	if verified {
		return receipt.VerificationVerified
	}
	return receipt.VerificationRejected
}

// commitmentID derives a stable commitment id from a receipt id.
//
// Stability is what makes a commitment idempotent: the same receipt verified twice
// must refer to one record, not two, or a repeated verdict could double-count.
func commitmentID(receiptID string) string {
	return "stake:" + strings.TrimPrefix(receiptID, "0x")
}
