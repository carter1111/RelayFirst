package llm

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/relayfirst/relayfirst/internal/mining"
)

// FieldResolver adapts a Provider to mining.Resolver (ADR-0002).
//
// It is the seam where the model's answer enters the protocol, so it carries the
// most important rule in this package:
//
//	The model's output is NEVER trusted to be canonical.
//
// Every value the model returns is passed through mining.NormalizeValue before it
// is returned. A model that writes "approximately $1,234.50 (as of today)" and one
// that writes "$1,234.50" must land on the same stored string, or two honest runs
// over the same document would disagree and verification would stop being binary —
// which is the property MVP.md §5.0 rests on. Normalization is not cosmetic here;
// it is what keeps the receipt verifiable.
type FieldResolver struct {
	// Provider performs the inference. Required.
	Provider Provider

	// MaxBodyChars truncates the document before it is sent.
	//
	// This bounds cost per task, which matters because the whole point of semantic
	// tasks is that they consume budget: an unbounded prompt would let a single
	// task spend arbitrarily. Zero means DefaultMaxBodyChars.
	MaxBodyChars int

	// OnUsage, when set, receives the token usage of each completed call. Callers
	// use it to fill the receipt's work block, which is the auditable evidence
	// that the task cost something.
	OnUsage func(provider, model string, u Usage)

	// mu guards usage.
	mu sync.Mutex

	// usage accumulates what this resolver has consumed since the last TakeUsage.
	// The receiver is a pointer and TakeUsage has a pointer receiver, so a mutex
	// here is safe; copying a FieldResolver would be a mistake anyway.
	usage mining.Usage
}

// recordUsage folds one provider response into the accumulator.
func (r *FieldResolver) recordUsage(u Usage) {
	if r == nil {
		return
	}
	model := strings.TrimSpace(u.Model)
	if model == "" {
		model = r.Provider.Model()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage = r.usage.Add(mining.Usage{
		TokensIn:  uint64(max(u.InputTokens, 0)),
		TokensOut: uint64(max(u.OutputTokens, 0)),
		Model:     model,
		Provider:  r.Provider.Name(),
	})
}

// ResetUsage discards accumulated usage without reading it.
//
// Implemented alongside TakeUsage so a failed attempt cannot leak its cost into
// the receipt of the attempt that eventually succeeds.
func (r *FieldResolver) ResetUsage() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage = mining.Usage{}
}

// TakeUsage returns the usage accumulated since the last take, and resets the
// accumulator.
//
// # Why this exists
//
// A semantic extract can resolve several fields, so several provider calls happen
// within one mining iteration, and the receipt must report their sum. Cumulative
// counters alone cannot express that: they would either report only the last call
// or force the caller to take deltas against a baseline it does not know. A
// take-and-reset is the smallest interface that lets one iteration attribute
// exactly its own cost.
//
// Resetting is not a convenience — without it a long-running miner would attribute
// the whole run's token total to whichever receipt was built last.
func (r *FieldResolver) TakeUsage() mining.Usage {
	if r == nil {
		return mining.Usage{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	u := r.usage
	r.usage = mining.Usage{}
	return u
}

// DefaultMaxBodyChars bounds how much of a document is sent to the model.
//
// Sized for an HTML or JSON page's informative region rather than an entire file.
// Callers that need more should say so explicitly, because the default is what
// determines the worst-case cost of one task.
const DefaultMaxBodyChars = 16_000

// compile-time assertion that the resolver satisfies the mining interface.
var _ mining.Resolver = (*FieldResolver)(nil)

// NewFieldResolver returns a resolver over p.
func NewFieldResolver(p Provider) (*FieldResolver, error) {
	if p == nil {
		return nil, fmt.Errorf("llm: resolver requires a provider")
	}
	return &FieldResolver{Provider: p}, nil
}

func (r *FieldResolver) maxBody() int {
	if r.MaxBodyChars > 0 {
		return r.MaxBodyChars
	}
	return DefaultMaxBodyChars
}

// extractionSystemPrompt instructs the model to answer narrowly.
//
// The constraints are deliberately strict, and deliberately redundant with
// NormalizeValue: the prompt reduces the number of unparseable answers, and
// normalization guarantees the stored form is canonical regardless. A prompt alone
// would be a hope; the code is the guarantee.
const extractionSystemPrompt = `You extract a single field value from a document.

Rules:
- Reply with the value alone. No prose, no explanation, no quotes.
- If the value is a number, reply with digits only, without currency symbols or
  thousands separators.
- If the field is not present in the document, reply with exactly: NOT_FOUND
- Do not guess. Do not infer from context. Only report what the document states.`

// Resolve answers description from body using the configured provider.
//
// A model response of NOT_FOUND is reported as absent (found=false), which the
// mining layer records as null. Absence is a legitimate deterministic observation,
// not a failure, so it must not be turned into an error.
func (r *FieldResolver) Resolve(ctx context.Context, description string, body []byte) (string, bool, error) {
	if r.Provider == nil {
		return "", false, fmt.Errorf("llm: resolver has no provider")
	}
	desc := strings.TrimSpace(description)
	if desc == "" {
		return "", false, fmt.Errorf("llm: field description is empty")
	}

	document := string(body)
	if max := r.maxBody(); len(document) > max {
		// Truncate rather than error: a long page is normal, and the informative
		// region is near the top far more often than not.
		document = document[:max]
	}

	user := fmt.Sprintf("Field to extract:\n%s\n\nDocument:\n%s", desc, document)

	text, usage, err := r.Provider.Complete(ctx, extractionSystemPrompt, user)
	if err != nil {
		return "", false, fmt.Errorf("llm: complete: %w", err)
	}

	if r.OnUsage != nil {
		r.OnUsage(r.Provider.Name(), r.Provider.Model(), usage)
	}

	// Accumulate for the current iteration. The receipt's work block is built from
	// this, so a call that is not recorded here is a cost the protocol denies.
	r.recordUsage(usage)

	// The model is asked to say NOT_FOUND rather than to stay silent, because a
	// silent answer is indistinguishable from a truncated one.
	if isNotFound(text) {
		return "", false, nil
	}

	// Strip surrounding quotes a model may add despite the instruction. Only
	// matching pairs are removed, so a value that legitimately starts with a
	// quote is not damaged.
	cleaned := stripWrappingQuotes(text)
	if cleaned == "" {
		return "", false, nil
	}

	return cleaned, true, nil
}

// isNotFound reports whether a model response signals absence.
//
// Compared case-insensitively on the trimmed value, because models vary the
// casing of sentinel tokens even when told not to.
func isNotFound(text string) bool {
	s := strings.TrimSpace(text)
	if s == "" {
		return false // empty is handled by the caller as empty, not as absence
	}
	return strings.EqualFold(s, "NOT_FOUND") ||
		strings.EqualFold(s, "not found") ||
		strings.EqualFold(s, "N/A")
}

// stripWrappingQuotes removes one matching pair of surrounding quotes.
func stripWrappingQuotes(s string) string {
	t := strings.TrimSpace(s)
	pairs := [][2]string{
		{`"`, `"`},
		{`'`, `'`},
		{"`", "`"},
		{"“", "”"},
	}
	for _, p := range pairs {
		if len(t) >= 2 && strings.HasPrefix(t, p[0]) && strings.HasSuffix(t, p[1]) {
			inner := t[len(p[0]) : len(t)-len(p[1])]
			return strings.TrimSpace(inner)
		}
	}
	return t
}
