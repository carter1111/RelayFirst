package mining

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/eip712"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// Loop ties generation, execution and signing together: it is the "mine"
// operation of MVP.md §9.1, minus the CLI shell and the durable store.
//
// It deliberately holds no state and does no I/O of its own beyond calling the
// generator and the executors, so the mining core stays testable without a
// network or a database.
type Loop struct {
	// AgentID is the signer identity, e.g. "agent:eip155:8453:0x...".
	AgentID string

	// PrivKeyHex signs each receipt. Never logged (CODING_RULES.md §8).
	PrivKeyHex string

	// Generator produces tasks.
	Generator *Generator

	// Deps are the executor collaborators.
	Deps Deps

	// EpochLength groups receipts into settlement epochs (MVP.md §6.2).
	EpochLength time.Duration

	// Now is injected for deterministic tests; nil means time.Now.
	Now func() time.Time

	// MaxAttempts bounds retries when an individual task fails. A single
	// unreachable URL must not stall the loop.
	MaxAttempts int

	// Ctx optionally bounds a mining pass. Nil means context.Background().
	Ctx context.Context
}

// MineResult reports one mining pass.
type MineResult struct {
	Receipt *receipt.Receipt
	Task    receipt.TaskType
	// Attempts is how many tasks were tried before one succeeded.
	Attempts int
	// Err is set when every attempt failed; Receipt is then nil.
	Err error
}

// Context is the context used for a mining pass. It exists so MineOnce stays
// callable without threading a context through every test, while still allowing
// a daemon to cancel cleanly.
func (l *Loop) context() context.Context {
	if l.Ctx != nil {
		return l.Ctx
	}
	return context.Background()
}

func (l *Loop) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Loop) epochLength() time.Duration {
	if l.EpochLength > 0 {
		return l.EpochLength
	}
	return 24 * time.Hour
}

func (l *Loop) maxAttempts() int {
	if l.MaxAttempts > 0 {
		return l.MaxAttempts
	}
	return 3
}

// MineOnce runs one task and returns a signed receipt.
//
// Failures are retried up to MaxAttempts because individual tasks legitimately
// fail (a dead URL, a timeout). Only when every attempt fails does it return an
// error, so a caller can distinguish "this task was unlucky" from "the mining
// loop is broken".
func (l *Loop) MineOnce() MineResult {
	if l.Generator == nil {
		return MineResult{Err: fmt.Errorf("mining: loop has no generator")}
	}
	if strings.TrimSpace(l.AgentID) == "" {
		return MineResult{Err: fmt.Errorf("mining: loop has no agent id")}
	}

	var lastErr error
	for attempt := 0; attempt < l.maxAttempts(); attempt++ {
		// Clear any accumulated inference before each attempt, or a failed
		// attempt's cost would be attributed to the receipt that eventually
		// succeeds — overstating one task with another's spend.
		if src, ok := l.Deps.Resolver.(UsageReset); ok {
			src.ResetUsage()
		}

		taskType, spec, err := l.Generator.Next()
		if err != nil {
			return MineResult{Attempts: attempt + 1, Err: err}
		}

		res, anchors, err := Run(l.context(), l.Deps, taskType, spec)
		if err != nil {
			lastErr = err
			continue
		}

		r, err := l.buildReceipt(taskType, spec, res, anchors)
		if err != nil {
			lastErr = err
			continue
		}

		if err := r.Sign(l.PrivKeyHex); err != nil {
			return MineResult{Attempts: attempt + 1, Err: fmt.Errorf("mining: sign: %w", err)}
		}

		// Self-check: never emit a receipt that fails our own validation. This
		// catches a malformed spec or a signature bug at the source rather than
		// downstream at the verifier.
		if err := r.Validate(nil); err != nil {
			return MineResult{Attempts: attempt + 1, Err: fmt.Errorf("mining: produced invalid receipt: %w", err)}
		}

		return MineResult{Receipt: r, Task: taskType, Attempts: attempt + 1}
	}

	return MineResult{Attempts: l.maxAttempts(), Err: lastErr}
}

// buildReceipt assembles an unsigned receipt from a completed task.
func (l *Loop) buildReceipt(
	taskType receipt.TaskType,
	spec map[string]any,
	res receipt.Result,
	anchors []receipt.Anchor,
) (*receipt.Receipt, error) {
	specHash, err := canonicalSpecHash(spec)
	if err != nil {
		return nil, fmt.Errorf("mining: hash spec: %w", err)
	}

	now := l.now()
	work := l.workFor(spec)

	r := &receipt.Receipt{
		Schema:  receipt.Schema,
		AgentID: l.AgentID,
		Epoch:   receipt.NewEpoch(now, l.epochLength()),
		Task: receipt.Task{
			Type:          taskType,
			Spec:          spec,
			SpecHash:      specHash,
			SelfGenerated: true,
			// A2ATaskID stays nil for the whole MVP (MVP.md §16.4).
		},
		Work:         work,
		Result:       res,
		Anchors:      anchors,
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}

	r.ReceiptID = deriveReceiptID(r, specHash)
	return r, nil
}

// workFor builds the receipt's work block for one executed task.
//
// # Why this is not just "read the spec"
//
// The work block is the auditable evidence of what the task cost, and the MVP's
// economics rest on that cost being real. Recording zeros for a task that actually
// called a model would understate it. So the inference-reported numbers, when a
// resolver can supply them, take precedence over the spec's declarations.
//
// # The honesty boundary
//
// Anything not covered by a UsageSource is reported as consumed nothing, which is
// correct for probe, compute and positional extract: they genuinely do. A task
// that spent inference without recording it here would be a receipt that lies
// about its own cost, so any new inference-consuming task type must wire a
// source too.
func (l *Loop) workFor(spec map[string]any) receipt.Work {
	started := l.now().Unix()

	w := receipt.Work{
		Provider:   providerOf(spec),
		Model:      modelOf(spec),
		StartedAt:  started,
		FinishedAt: started,
	}

	// A UsageSource is discovered by type assertion rather than by threading a
	// value back up the call chain, so no executor or call site has to change.
	if src, ok := l.Deps.Resolver.(UsageSource); ok {
		u := src.TakeUsage()
		w.TokensIn = u.TokensIn
		w.TokensOut = u.TokensOut
		if s := strings.TrimSpace(u.Model); s != "" {
			w.Model = s
		}
		if s := strings.TrimSpace(u.Provider); s != "" {
			// The provider that actually served the call is stronger evidence
			// than the spec's declaration, so it wins when present.
			w.Provider = s
		}
	}

	return w
}

// deriveReceiptID builds a deterministic receipt id from the signed content.
//
// It is derived from the payload rather than random so that the same work
// submitted twice yields the same id — which is what the global dedup ledger in
// S3 will key on for idempotency.
func deriveReceiptID(r *receipt.Receipt, specHash string) string {
	payload, err := r.SignedPayload()
	if err != nil {
		// SignedPayload only fails on an unsupported type, which cannot happen
		// for the fixed receipt shape. Fall back to the spec hash.
		payload = []byte(specHash)
	}
	sum := sha256.Sum256(payload)
	return "0x" + hex.EncodeToString(sum[:])
}

// providerOf reports the inference provider declared in the spec, defaulting to
// "local" for tasks that need no inference at all (compute).
//
// Honesty matters here: the work block records what actually happened, and
// compute tasks genuinely consume no inference budget.
func providerOf(spec map[string]any) string {
	if s, ok := spec["provider"].(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	if op, ok := spec["op"].(string); ok && op != "" {
		return "local"
	}
	return "none"
}

// modelOf reports the model declared in the spec, if any.
func modelOf(spec map[string]any) string {
	if s, ok := spec["model"].(string); ok {
		return s
	}
	return ""
}

// VerifyReceipt re-checks a receipt produced by this loop, mirroring what the
// standalone verifier does. Used by tests and by the daemon's self-check before
// reporting work.
//
// A receipt from a version this build cannot check is reported as such rather
// than as an invalid receipt (H2, S9-0j): the two mean different things to
// whoever reads the error, and the fix differs.
func VerifyReceipt(r *receipt.Receipt) error {
	if err := r.ValidateStructure(); err != nil {
		return classifyVerifyError(err)
	}
	return classifyVerifyError(r.Validate(nil))
}

// classifyVerifyError labels an unsupported version explicitly.
func classifyVerifyError(err error) error {
	if err == nil {
		return nil
	}
	if receipt.IsUnsupported(err) {
		return fmt.Errorf(
			"this receipt's schema is newer or older than this build can check — upgrade the verifier; "+
				"it is not being reported as invalid: %w", err)
	}
	return err
}

// SignerAddressHex returns the address implied by the loop's agent id, for
// display and for cross-checking against the recovered signer.
func (l *Loop) SignerAddressHex() (string, error) {
	addr, err := eip712.HexToAddress(addressFromAgentID(l.AgentID))
	if err != nil {
		return "", err
	}
	return eip712.AddressToHex(addr), nil
}

// addressFromAgentID extracts the address portion of an agent id.
func addressFromAgentID(agentID string) string {
	parts := strings.Split(agentID, ":")
	if len(parts) < 4 {
		return agentID
	}
	return parts[len(parts)-1]
}

// HashPayload exposes the content hash used for anchor integrity, so callers
// can compare against an independently computed hash.
func HashPayload(b []byte) string { return anchor.HashBytes(b) }
