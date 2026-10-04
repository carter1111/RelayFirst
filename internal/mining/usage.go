package mining

import (
	"strings"
	"sync"
)

// UsageSource reports the inference one iteration consumed, and clears its
// accumulator so the next iteration starts from zero.
//
// It is a separate interface from Resolver on purpose. Only one resolver
// implementation actually calls a model; the offline stub used in tests does
// not, and widening Resolver would force every implementer to satisfy a concern
// that only some of them have.
//
// *llm.FieldResolver implements it, and Loop discovers it by type assertion, so
// no call site has to wire it explicitly.
type UsageSource interface {
	// TakeUsage returns what has been consumed since the previous call and
	// resets the accumulator. An implementation that did not reset would make a
	// long-running miner attribute its entire run's cost to one receipt.
	TakeUsage() Usage
}

// UsageReset clears a usage accumulator without reading it.
//
// It exists so a failed task attempt cannot leak its cost into the next attempt's
// receipt. TakeUsage alone would work, but reading and discarding is a confusing
// way to express "forget this", and the intent is worth a named method.
type UsageReset interface {
	ResetUsage()
}

// Usage reports the inference one task consumed.
//
// The field names mirror the receipt's work block (MVP.md §4) on purpose: this
// type exists to fill that block, and a second naming scheme would invite a
// silent mapping bug at exactly the seam that must not have one.
type Usage struct {
	TokensIn  uint64
	TokensOut uint64

	// Model and Provider identify what served the request. Empty means unknown
	// rather than "none" — the distinction matters, because "unknown" must not
	// overwrite a provider the spec already named.
	Model    string
	Provider string
}

// Add folds another report into u.
//
// Several semantic fields in one task are several provider calls, so their cost
// has to accumulate rather than overwrite: recording only the last call would
// understate a multi-field task, and the whole point of this number is to be the
// auditable evidence of what the task cost.
func (u Usage) Add(other Usage) Usage {
	u.TokensIn += other.TokensIn
	u.TokensOut += other.TokensOut
	if s := strings.TrimSpace(other.Model); s != "" {
		u.Model = s
	}
	if s := strings.TrimSpace(other.Provider); s != "" {
		u.Provider = s
	}
	return u
}

// IsZero reports whether nothing was recorded.
func (u Usage) IsZero() bool {
	return u.TokensIn == 0 && u.TokensOut == 0 && strings.TrimSpace(u.Model) == ""
}

// UsageRecorder accumulates the usage of one mining iteration.
//
// # Why this is a shared object rather than a return value
//
// The executor is the only layer that knows a provider was called, but the loop
// is the layer that builds the receipt, and the Resolver interface sits between
// them. Threading a value back up that path would mean widening Resolver — which
// every implementer, including the offline stub in tests, would then have to
// satisfy for a concern that only one of them has.
//
// A recorder is safe here because exactly one mining iteration runs at a time
// (Loop.MineOnce is not called concurrently), and the mutex makes even that
// assumption non-load-bearing.
//
// It must be reset per iteration: a running daemon would otherwise accumulate
// every task's cost and attribute the total to whichever receipt happened to be
// built last.
type UsageRecorder struct {
	mu  sync.Mutex
	cur Usage
}

// Record folds one report into the current total.
func (r *UsageRecorder) Record(u Usage) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cur = r.cur.Add(u)
}

// Total returns the accumulated usage without clearing it.
func (r *UsageRecorder) Total() Usage {
	if r == nil {
		return Usage{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cur
}

// Reset clears the accumulator. The loop calls it at the start of every
// iteration so one task's cost cannot be attributed to the next task's receipt.
func (r *UsageRecorder) Reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cur = Usage{}
}
