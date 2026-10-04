package llm

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// LocalProvider is an in-process Provider that performs no network calls.
//
// It exists for three reasons:
//
//   - tests need a deterministic model that does not reach the network;
//   - offline development should be possible without a key;
//   - a deployment may legitimately run a local model behind the same interface.
//
// It is NOT a language model. It answers from a configured table. That limitation
// is the point: it makes the interface exercisable without pretending to be
// something it is not.
type LocalProvider struct {
	// Answers maps a prompt fragment to a response. The first key contained in
	// the prompt (in sorted order, for determinism) wins.
	Answers map[string]string

	// Default is returned when no key matches. Empty means ErrEmptyResponse.
	Default string

	// InputTokens and OutputTokens simulate usage so callers that record token
	// counts can be tested without a real provider.
	InputTokens  int
	OutputTokens int

	mu    sync.Mutex
	calls int
}

var _ Provider = (*LocalProvider)(nil)

// Name identifies the provider.
func (p *LocalProvider) Name() string { return "local" }

// Model reports the model id.
func (p *LocalProvider) Model() string { return "local/lookup" }

// Complete answers from the configured table.
//
// Matching is deterministic: keys are examined in sorted order rather than map
// order, so the same prompt always yields the same answer. A model whose output
// changes between runs would make the receipt hash unstable, which is exactly
// what verification depends on.
func (p *LocalProvider) Complete(_ context.Context, _ string, user string) (string, Usage, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()

	keys := make([]string, 0, len(p.Answers))
	for k := range p.Answers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if strings.Contains(user, k) {
			return p.Answers[k], p.usage(), nil
		}
	}

	if p.Default == "" {
		return "", Usage{}, ErrEmptyResponse
	}
	return p.Default, p.usage(), nil
}

// Calls reports how many completions were requested.
//
// Tests use this to assert the cost claim: a positional spec must make zero calls
// while a semantic spec must make one.
func (p *LocalProvider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *LocalProvider) usage() Usage {
	return Usage{InputTokens: p.InputTokens, OutputTokens: p.OutputTokens, Model: p.Model()}
}

// NewProvider builds a Provider from a provider name and configuration.
//
// This is the single place that maps a configuration string to an implementation,
// so adding a provider is one case here rather than a change at every call site.
// An unknown name is an error rather than a silent fallback: guessing which model
// to spend money on would be worse than refusing.
func NewProvider(name string, cfg map[string]string) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "openai":
		return NewOpenAIProvider(OpenAIConfig{
			APIKey:  cfg["apiKey"],
			Model:   cfg["model"],
			BaseURL: cfg["baseURL"],
		})
	case "anthropic":
		return NewAnthropicProvider(AnthropicConfig{
			APIKey:  cfg["apiKey"],
			Model:   cfg["model"],
			BaseURL: cfg["baseURL"],
		})
	case "local":
		return &LocalProvider{
			Answers: map[string]string{},
			Default: cfg["default"],
		}, nil
	case "":
		return nil, fmt.Errorf("%w: no provider name given", ErrNotConfigured)
	default:
		return nil, fmt.Errorf("llm: unknown provider %q (want openai, anthropic or local)", name)
	}
}
