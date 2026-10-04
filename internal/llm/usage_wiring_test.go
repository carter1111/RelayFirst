package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/mining"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// These tests encode REQ-INFER-4: the receipt's work block must report what the
// provider actually did, not a placeholder.
//
// They exist because a semantic extract is the one task type whose entire purpose
// is to consume inference budget, and it was recording `tokensIn: 0`,
// `tokensOut: 0` and an empty `model` — i.e. it claimed to have cost nothing.
// The MVP's economics rest on that cost being real, so a receipt that understates
// it is not a cosmetic problem.

// usageTestKey is a well-known test key. It holds no value and is never used
// outside tests (CODING_RULES.md §8).
const usageTestKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"

// jsonServer serves a small JSON document for extraction.
func jsonServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// usageTestLoop builds a Loop that only ever produces semantic extract tasks.
//
// The generator cannot be forced to a task type from outside the mining package,
// so this drives iterations until an extract appears. With three task types the
// chance of never seeing one in 60 attempts is (2/3)^60, which is far below any
// threshold that would make the test flaky.
func usageTestLoop(t *testing.T, srcURL string, resolver mining.Resolver) *mining.Loop {
	t.Helper()

	agent, err := receipt.DeriveAgentID(usageTestKey, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	g, err := mining.NewGenerator([]string{srcURL})
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	g.SemanticFields = []string{"the page title"}
	g.Provider = "local"

	fetcher := anchor.NewFetcher()
	fetcher.Now = func() time.Time { return time.Unix(1791015800, 0) }

	return &mining.Loop{
		AgentID:     agent,
		PrivKeyHex:  usageTestKey,
		Generator:   g,
		Deps:        mining.Deps{Fetcher: fetcher, Resolver: resolver},
		EpochLength: 24 * time.Hour,
		Now:         func() time.Time { return time.Unix(1791015800, 0) },
	}
}

// firstExtract mines until an extract receipt is produced.
func firstExtract(t *testing.T, loop *mining.Loop) *receipt.Receipt {
	t.Helper()

	for i := 0; i < 60; i++ {
		res := loop.MineOnce()
		if res.Err != nil || res.Receipt == nil {
			continue
		}
		if res.Receipt.Task.Type == receipt.TaskExtract {
			return res.Receipt
		}
	}
	t.Skip("no extract task was produced in 60 attempts; the generator's randomness is not behaving as expected")
	return nil
}

// TestSemanticExtractRecordsRealTokenUsage is the RED test for REQ-INFER-4.
//
// A semantic extract must record the provider's reported token counts and the
// model that served them. Today it records zeros and an empty model, which says
// the task was free when it was not.
func TestSemanticExtractRecordsRealTokenUsage(t *testing.T) {
	srv := jsonServer(t, `{"title":"Example Domain","id":42}`)

	// A local provider with known usage: no network, no key, deterministic.
	provider := &LocalProvider{
		Answers:      map[string]string{"the page title": "Example Domain"},
		InputTokens:  1234,
		OutputTokens: 567,
	}
	resolver, err := NewFieldResolver(provider)
	if err != nil {
		t.Fatalf("NewFieldResolver: %v", err)
	}

	loop := usageTestLoop(t, srv.URL, resolver)
	r := firstExtract(t, loop)

	// The provider really was called; otherwise the rest proves nothing.
	if provider.Calls() == 0 {
		t.Fatal("the provider was never called, so there was no inference to record")
	}

	if got := r.Work.TokensIn; got != 1234 {
		t.Errorf("work.tokensIn = %d, want 1234 (the provider's reported input tokens)", got)
	}
	if got := r.Work.TokensOut; got != 567 {
		t.Errorf("work.tokensOut = %d, want 567 (the provider's reported output tokens)", got)
	}
	if got := r.Work.Model; got == "" {
		t.Error("work.model is empty, but a real model served this task")
	}
	if got := r.Work.Provider; got != "local" {
		t.Errorf("work.provider = %q, want %q", got, "local")
	}
}

// TestProbeAndComputeRecordNoTokens is the guard in the other direction: the
// economics depend on probe and compute being genuinely free, so a change that
// starts attributing inference to them is a regression, not an improvement.
func TestProbeAndComputeRecordNoTokens(t *testing.T) {
	srv := jsonServer(t, `{"title":"Example Domain"}`)

	provider := &LocalProvider{
		Answers:      map[string]string{"the page title": "Example Domain"},
		InputTokens:  1234,
		OutputTokens: 567,
	}
	resolver, err := NewFieldResolver(provider)
	if err != nil {
		t.Fatalf("NewFieldResolver: %v", err)
	}

	loop := usageTestLoop(t, srv.URL, resolver)

	seen := map[receipt.TaskType]bool{}
	for i := 0; i < 60 && len(seen) < 2; i++ {
		res := loop.MineOnce()
		if res.Err != nil || res.Receipt == nil {
			continue
		}
		switch res.Receipt.Task.Type {
		case receipt.TaskProbe, receipt.TaskCompute:
			seen[res.Receipt.Task.Type] = true
			if res.Receipt.Work.TokensIn != 0 || res.Receipt.Work.TokensOut != 0 {
				t.Errorf("%s recorded tokens (%d/%d); it consumes no inference and must record zero",
					res.Receipt.Task.Type, res.Receipt.Work.TokensIn, res.Receipt.Work.TokensOut)
			}
		}
	}

	if len(seen) == 0 {
		t.Skip("neither probe nor compute was produced in 60 attempts")
	}
}

// TestSemanticExtractWithoutResolverRecordsNoTokens covers the default path: a
// miner configured with semantic fields but no provider must not fabricate cost.
//
// The property under test is two-sided. Semantic extract cannot succeed (there is
// nothing to resolve the field), but the miner must keep working on the task types
// that need no model — and no receipt may claim tokens it did not spend.
func TestSemanticExtractWithoutResolverRecordsNoTokens(t *testing.T) {
	srv := jsonServer(t, `{"title":"Example Domain"}`)

	// No resolver: semantic extract should fail cleanly rather than silently
	// costing nothing.
	loop := usageTestLoop(t, srv.URL, nil)

	receipts := 0
	extracts := 0

	for i := 0; i < 60; i++ {
		res := loop.MineOnce()
		if res.Err != nil || res.Receipt == nil {
			continue
		}
		receipts++

		if res.Receipt.Task.Type == receipt.TaskExtract {
			extracts++
			// If an extract somehow produced a receipt with no resolver, it must
			// still not invent cost.
		}
		if res.Receipt.Work.TokensIn != 0 || res.Receipt.Work.TokensOut != 0 {
			t.Errorf("receipt %s recorded tokens (%d/%d) with no provider configured",
				res.Receipt.ReceiptID, res.Receipt.Work.TokensIn, res.Receipt.Work.TokensOut)
		}
	}

	// The loop must not be dead: an unconfigured provider may only stop the one
	// task type that needs it, not the whole miner.
	if receipts == 0 {
		t.Fatal("no receipt was produced at all; an unconfigured provider must not stall the miner")
	}
	if extracts != 0 {
		t.Errorf("%d extract task(s) produced a receipt without a resolver; semantic fields cannot be resolved", extracts)
	}
}

// TestSemanticExtractAccumulatesMultipleFieldCalls: several semantic fields in one
// task are several provider calls, and the receipt must report their sum. A
// last-call-wins implementation would understate a multi-field task.
func TestSemanticExtractAccumulatesMultipleFieldCalls(t *testing.T) {
	srv := jsonServer(t, `{"title":"Example Domain","price":"9.99"}`)

	provider := &LocalProvider{
		// Both descriptions must match, so both fields resolve and each incurs a
		// call. LocalProvider matches on any key contained in the prompt, so a
		// single entry would answer both — the two keys keep the intent explicit.
		Answers: map[string]string{
			"the page title":  "Example Domain",
			"the page price":  "9.99",
			"the page number": "7",
		},
		InputTokens:  100,
		OutputTokens: 10,
	}
	resolver, err := NewFieldResolver(provider)
	if err != nil {
		t.Fatalf("NewFieldResolver: %v", err)
	}

	agent, err := receipt.DeriveAgentID(usageTestKey, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	g, err := mining.NewGenerator([]string{srv.URL})
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	g.SemanticFields = []string{"the page title", "the page price", "the page number"}

	fetcher := anchor.NewFetcher()
	fetcher.Now = func() time.Time { return time.Unix(1791015800, 0) }

	loop := &mining.Loop{
		AgentID:     agent,
		PrivKeyHex:  usageTestKey,
		Generator:   g,
		Deps:        mining.Deps{Fetcher: fetcher, Resolver: resolver},
		EpochLength: 24 * time.Hour,
		Now:         func() time.Time { return time.Unix(1791015800, 0) },
	}

	r := firstExtract(t, loop)

	calls := provider.Calls()
	if calls < 2 {
		t.Fatalf("provider was called %d time(s); the test needs several fields to accumulate", calls)
	}

	// Three fields, three calls, 100/10 each.
	if want := uint64(calls * 100); r.Work.TokensIn != want {
		t.Errorf("work.tokensIn = %d, want %d (%d call(s) x 100) — calls must accumulate, not overwrite",
			r.Work.TokensIn, want, calls)
	}
	if want := uint64(calls * 10); r.Work.TokensOut != want {
		t.Errorf("work.tokensOut = %d, want %d (%d call(s) x 10)", r.Work.TokensOut, want, calls)
	}
}

// modelLessProvider reports tokens but no model, like the real HTTP providers do.
type modelLessProvider struct{ Usage }

func (p *modelLessProvider) Name() string  { return "openai" }
func (p *modelLessProvider) Model() string { return "gpt-5.5-test" }

func (p *modelLessProvider) Complete(_ context.Context, _, _ string) (string, Usage, error) {
	return "Example Domain", p.Usage, nil
}

// TestUsageModelFallsBackToProvider: the real providers return token counts
// without a model id, so the resolver must fall back to the provider's own Model()
// rather than leaving the receipt's model field empty.
func TestUsageModelFallsBackToProvider(t *testing.T) {
	provider := &modelLessProvider{Usage: Usage{InputTokens: 7, OutputTokens: 3}}

	resolver, err := NewFieldResolver(provider)
	if err != nil {
		t.Fatalf("NewFieldResolver: %v", err)
	}
	if _, _, err := resolver.Resolve(context.Background(), "the title", []byte(`{"title":"x"}`)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	u := resolver.TakeUsage()
	if u.Model != "gpt-5.5-test" {
		t.Errorf("usage.Model = %q, want the provider's model id as a fallback", u.Model)
	}
	if u.Provider != "openai" {
		t.Errorf("usage.Provider = %q, want %q", u.Provider, "openai")
	}
	if u.TokensIn != 7 || u.TokensOut != 3 {
		t.Errorf("usage = %d/%d, want 7/3", u.TokensIn, u.TokensOut)
	}
}

// TestUsageRecorderResetPerIteration: a second task must not inherit the first
// task's cost, or a long-running miner would inflate every subsequent receipt.
func TestUsageRecorderResetPerIteration(t *testing.T) {
	srv := jsonServer(t, `{"title":"Example Domain"}`)

	provider := &LocalProvider{
		Answers:      map[string]string{"the page title": "Example Domain"},
		InputTokens:  1000,
		OutputTokens: 100,
	}
	resolver, err := NewFieldResolver(provider)
	if err != nil {
		t.Fatalf("NewFieldResolver: %v", err)
	}

	loop := usageTestLoop(t, srv.URL, resolver)

	var extracts []*receipt.Receipt
	for i := 0; i < 60 && len(extracts) < 2; i++ {
		res := loop.MineOnce()
		if res.Err != nil || res.Receipt == nil {
			continue
		}
		if res.Receipt.Task.Type == receipt.TaskExtract {
			extracts = append(extracts, res.Receipt)
		}
	}
	if len(extracts) < 2 {
		t.Skip("fewer than two extract tasks were produced in 60 attempts")
	}

	// Each extract made exactly one call, so each receipt must report exactly one
	// call's worth — not the running total.
	for i, r := range extracts {
		if r.Work.TokensIn != 1000 || r.Work.TokensOut != 100 {
			t.Errorf("extract %d recorded %d/%d tokens, want 1000/100; cost leaked across iterations",
				i, r.Work.TokensIn, r.Work.TokensOut)
		}
	}
}
func TestFieldResolverReportsUsage(t *testing.T) {
	provider := &LocalProvider{
		Answers:      map[string]string{"the page title": "Example Domain"},
		InputTokens:  100,
		OutputTokens: 20,
	}
	resolver, err := NewFieldResolver(provider)
	if err != nil {
		t.Fatalf("NewFieldResolver: %v", err)
	}

	if _, _, err := resolver.Resolve(nil, "the page title", []byte(`{"title":"x"}`)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	reporter, ok := any(resolver).(interface {
		TakeUsage() mining.Usage
	})
	if !ok {
		t.Fatal("FieldResolver does not expose TakeUsage, so callers cannot record what a task cost")
	}

	u := reporter.TakeUsage()
	if u.TokensIn != 100 || u.TokensOut != 20 {
		t.Errorf("usage = %d/%d, want 100/20", u.TokensIn, u.TokensOut)
	}
	if !strings.Contains(u.Model, "local") {
		t.Errorf("usage.Model = %q, want the model id", u.Model)
	}

	// Taking usage must reset it, so the next task does not inherit this one's
	// cost. Without a reset a long-running miner would inflate every receipt.
	again := reporter.TakeUsage()
	if again.TokensIn != 0 || again.TokensOut != 0 {
		t.Errorf("usage after TakeUsage = %d/%d, want 0/0", again.TokensIn, again.TokensOut)
	}
}
