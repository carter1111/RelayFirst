package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/mining"
)

// providers --------------------------------------------------------------

// TestProvidersRefuseWithoutKey: a missing credential must fail at construction,
// not at first call. A misconfiguration should surface before any work begins.
func TestProvidersRefuseWithoutKey(t *testing.T) {
	if _, err := NewOpenAIProvider(OpenAIConfig{Model: "gpt-x"}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("openai without a key: got %v, want ErrNotConfigured", err)
	}
	if _, err := NewAnthropicProvider(AnthropicConfig{Model: "claude-x"}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("anthropic without a key: got %v, want ErrNotConfigured", err)
	}

	// Model is equally required.
	if _, err := NewOpenAIProvider(OpenAIConfig{APIKey: "k"}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("openai without a model: got %v, want ErrNotConfigured", err)
	}
}

// TestProviderErrorDoesNotLeakKey is the secret-handling check required by
// CODING_RULES.md §8.
func TestProviderErrorDoesNotLeakKey(t *testing.T) {
	const secret = "sk-super-secret-value-1234567890"

	p, err := NewOpenAIProvider(OpenAIConfig{APIKey: secret, Model: "gpt-x"})
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}

	// Force a transport failure by pointing at an unroutable base URL.
	p.baseURL = "http://127.0.0.1:1/v1"

	_, _, callErr := p.Complete(context.Background(), "sys", "user")
	if callErr == nil {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(callErr.Error(), secret) {
		t.Errorf("error message leaked the API key: %v", callErr)
	}

	a, err := NewAnthropicProvider(AnthropicConfig{APIKey: secret, Model: "claude-x"})
	if err != nil {
		t.Fatalf("NewAnthropicProvider: %v", err)
	}
	a.baseURL = "http://127.0.0.1:1/v1"

	_, _, callErr = a.Complete(context.Background(), "sys", "user")
	if callErr == nil {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(callErr.Error(), secret) {
		t.Errorf("error message leaked the API key: %v", callErr)
	}
}

func TestRedactKey_NeverRevealsValue(t *testing.T) {
	const secret = "sk-abcdef123456"
	got := redactKey(secret)
	if strings.Contains(got, secret) {
		t.Errorf("redactKey revealed the key: %s", got)
	}
	if redactKey("") != "(unset)" {
		t.Errorf("empty key should report unset, got %s", redactKey(""))
	}
}

func TestNewProvider_Dispatch(t *testing.T) {
	if _, err := NewProvider("openai", map[string]string{"apiKey": "k", "model": "m"}); err != nil {
		t.Errorf("openai: %v", err)
	}
	if _, err := NewProvider("anthropic", map[string]string{"apiKey": "k", "model": "m"}); err != nil {
		t.Errorf("anthropic: %v", err)
	}
	if _, err := NewProvider("local", nil); err != nil {
		t.Errorf("local: %v", err)
	}
	if _, err := NewProvider("", nil); err == nil {
		t.Error("empty provider name must be rejected")
	}
	if _, err := NewProvider("gemini", nil); err == nil {
		t.Error("an unknown provider must be rejected rather than silently defaulted")
	}
}

// local provider ---------------------------------------------------------

func TestLocalProvider_DeterministicMatching(t *testing.T) {
	p := &LocalProvider{
		Answers: map[string]string{
			"price": "1234.5",
			"name":  "Widget",
		},
	}

	// Map iteration order must not affect which answer wins.
	for i := 0; i < 50; i++ {
		text, _, err := p.Complete(context.Background(), "sys", "what is the price")
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if text != "1234.5" {
			t.Fatalf("answer drifted at iteration %d: %q", i, text)
		}
	}
	if p.Calls() != 50 {
		t.Errorf("Calls = %d, want 50", p.Calls())
	}
}

func TestLocalProvider_DefaultAndEmpty(t *testing.T) {
	p := &LocalProvider{Default: "fallback"}
	text, _, err := p.Complete(context.Background(), "", "anything")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if text != "fallback" {
		t.Errorf("text = %q, want fallback", text)
	}

	empty := &LocalProvider{}
	if _, _, err := empty.Complete(context.Background(), "", "x"); !errors.Is(err, ErrEmptyResponse) {
		t.Errorf("empty provider: got %v, want ErrEmptyResponse", err)
	}
}

func TestLocalProvider_RecordsUsage(t *testing.T) {
	p := &LocalProvider{Answers: map[string]string{"x": "y"}, InputTokens: 120, OutputTokens: 8}

	_, u, err := p.Complete(context.Background(), "", "x")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if u.InputTokens != 120 || u.OutputTokens != 8 || u.Total() != 128 {
		t.Errorf("usage = %+v", u)
	}
}

// resolver: the normalization guarantee -------------------------------

// TestResolver_NormalizationHappensUpstream is the most important test in this
// package.
//
// A model will happily answer "approximately $1,234.50" one run and "$1,234.50"
// the next. If those reached the receipt unchanged, two honest runs over the same
// document would hash differently and verification would stop being binary — the
// property MVP.md §5.0 depends on.
//
// Normalization lives in the mining layer, not here: this package performs
// inference, and the wire format is owned by mining. What matters is that the two
// compose correctly, so the assertion is made through mining.ResolveFields — the
// path a real task actually takes.
func TestResolver_NormalizationHappensUpstream(t *testing.T) {
	variants := []string{
		"$1,234.50",
		"1234.50",
		"1234.5",
		" 1,234.5 ",
		`"1234.50"`, // model adds quotes
		"'$1,234.50'",
	}

	for _, v := range variants {
		p := &LocalProvider{Answers: map[string]string{"price": v}}
		r, err := NewFieldResolver(p)
		if err != nil {
			t.Fatalf("NewFieldResolver: %v", err)
		}

		specs := []mining.FieldSpec{{Semantic: "price"}}
		got, err := mining.ResolveFields(context.Background(), nil, specs, r, []byte("doc"))
		if err != nil {
			t.Fatalf("ResolveFields(%q): %v", v, err)
		}

		// The stored result must be identical no matter how the model phrased it.
		if !strings.Contains(got, `"1234.5"`) {
			t.Errorf("model said %q, stored result is %s, want the canonical \"1234.5\"", v, got)
		}
	}
}

// TestResolver_PlainTextPassesThrough: normalization must not mangle non-numeric
// answers, or a legitimate text field would be corrupted.
func TestResolver_PlainTextPassesThrough(t *testing.T) {
	p := &LocalProvider{Answers: map[string]string{"name": "  Widget Pro  "}}
	r, _ := NewFieldResolver(p)

	got, found, err := r.Resolve(context.Background(), "name", []byte("doc"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !found || got != "Widget Pro" {
		t.Errorf("got %q found=%v, want \"Widget Pro\"", got, found)
	}
}

// TestResolver_NotFoundIsAbsence: the sentinel must become found=false, which the
// mining layer records as null. Absence is an observation, not a failure.
func TestResolver_NotFoundIsAbsence(t *testing.T) {
	for _, sentinel := range []string{"NOT_FOUND", "not_found", "not found", "N/A"} {
		p := &LocalProvider{Answers: map[string]string{"price": sentinel}}
		r, _ := NewFieldResolver(p)

		_, found, err := r.Resolve(context.Background(), "price", []byte("doc"))
		if err != nil {
			t.Fatalf("%s: Resolve: %v", sentinel, err)
		}
		if found {
			t.Errorf("%s: should be reported as absent", sentinel)
		}
	}
}

func TestResolver_BoundsBodySentToModel(t *testing.T) {
	var seen string
	spy := &spyProvider{capture: &seen}

	r, err := NewFieldResolver(spy)
	if err != nil {
		t.Fatalf("NewFieldResolver: %v", err)
	}
	r.MaxBodyChars = 100

	big := strings.Repeat("x", 5000)
	if _, _, err := r.Resolve(context.Background(), "price", []byte(big)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if strings.Count(seen, "x") > 200 {
		t.Errorf("document was not truncated: %d x's reached the model", strings.Count(seen, "x"))
	}
}

func TestResolver_RequiresProviderAndDescription(t *testing.T) {
	if _, err := NewFieldResolver(nil); err == nil {
		t.Error("a resolver with no provider must be refused")
	}

	p := &LocalProvider{Default: "x"}
	r, _ := NewFieldResolver(p)
	if _, _, err := r.Resolve(context.Background(), "  ", []byte("doc")); err == nil {
		t.Error("an empty description must be rejected")
	}
}

func TestResolver_ReportsUsage(t *testing.T) {
	p := &LocalProvider{Answers: map[string]string{"x": "1"}, InputTokens: 50, OutputTokens: 3}
	r, _ := NewFieldResolver(p)

	var gotProvider, gotModel string
	var gotUsage Usage
	r.OnUsage = func(provider, model string, u Usage) {
		gotProvider, gotModel, gotUsage = provider, model, u
	}

	if _, _, err := r.Resolve(context.Background(), "x", []byte("doc")); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if gotProvider != "local" || gotModel == "" {
		t.Errorf("provider=%q model=%q", gotProvider, gotModel)
	}
	if gotUsage.InputTokens != 50 || gotUsage.OutputTokens != 3 {
		t.Errorf("usage = %+v", gotUsage)
	}
}

func TestStripWrappingQuotes(t *testing.T) {
	cases := map[string]string{
		`"abc"`: "abc",
		"'abc'": "abc",
		"`abc`": "abc",
		`"abc`:  `"abc`, // unmatched, left alone
		`abc"`:  `abc"`,
		`"a"b"`: `a"b`,
		"  x  ": "x",
		`""`:    "",
	}
	for in, want := range cases {
		if got := stripWrappingQuotes(in); got != want {
			t.Errorf("stripWrappingQuotes(%q) = %q, want %q", in, got, want)
		}
	}
}

// spyProvider records the prompt it received.
type spyProvider struct {
	capture *string
}

func (s *spyProvider) Name() string  { return "spy" }
func (s *spyProvider) Model() string { return "spy-1" }

func (s *spyProvider) Complete(_ context.Context, _, user string) (string, Usage, error) {
	if s.capture != nil {
		*s.capture = user
	}
	return "42", Usage{InputTokens: 1, OutputTokens: 1}, nil
}
