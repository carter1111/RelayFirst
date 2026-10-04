// Package llm provides the inference access that makes semantic-extract tasks
// cost something real.
//
// # Why this package exists
//
// ADR-0002 records the gap: probe/extract/compute were purely deterministic, so
// mining consumed no inference budget at all, which made the MVP's central claim
// ("your agent subscription is a mineable asset") false and removed the
// self-limiting property the sybil argument relied on.
//
// Semantic fields fix that only if resolving them actually calls a model. This
// package is that call.
//
// # The honesty caveat
//
// The cost is economically induced, not protocol-enforced. Someone willing to
// hand-write per-site parsing rules can still bypass the model entirely. Semantic
// tasks raise the price of farming; they do not prove it. The primary anti-farming
// load remains the global artifact dedup ledger (invariant A6). Do not describe
// this package as making farming impossible.
//
// # Secret handling
//
// An API key is supplied by the caller at construction time and is never logged,
// never embedded in an error, and never written into a receipt (CODING_RULES.md
// §8). Providers keep only what they need to sign a request.
package llm

import (
	"context"
	"errors"
	"fmt"
)

// Usage reports the tokens a call consumed.
//
// These numbers land in the receipt's work block, which is why they are returned
// rather than discarded: they are the auditable evidence that the task cost
// something.
type Usage struct {
	InputTokens  int
	OutputTokens int

	// Model identifies what served the call, so the receipt can name it. Empty
	// means the provider did not report one.
	Model string
}

// Total returns the sum of input and output tokens.
func (u Usage) Total() int { return u.InputTokens + u.OutputTokens }

// Provider completes a prompt and reports token usage.
//
// Implementations MUST NOT log, print, or include their credentials in returned
// errors. A provider whose key is unset should fail construction rather than
// failing at call time, so a misconfiguration surfaces before any work begins.
type Provider interface {
	// Name identifies the provider, for reporting (e.g. "openai").
	Name() string

	// Model identifies the model, for reporting and for the receipt's work block.
	Model() string

	// Complete sends a system instruction and a user prompt, returning the
	// model's text response and the tokens consumed.
	Complete(ctx context.Context, system, user string) (text string, usage Usage, err error)
}

// ErrNotConfigured is returned when a provider is constructed without the
// credentials it needs.
//
// It is deliberately distinct from a call failure: a missing key is a
// configuration mistake the operator must fix, not an inference error to retry.
var ErrNotConfigured = errors.New("llm: provider is not configured")

// ErrEmptyResponse is returned when a model yields no usable text.
var ErrEmptyResponse = errors.New("llm: model returned no text")

// redactKey renders a key's presence without revealing it.
//
// Used for diagnostics only. It reports length and a short fingerprint, which is
// enough for an operator to tell two keys apart without either being recoverable
// from a log.
func redactKey(key string) string {
	if key == "" {
		return "(unset)"
	}
	return fmt.Sprintf("(set, %d chars)", len(key))
}
