package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// AnthropicConfig configures the Anthropic provider.
type AnthropicConfig struct {
	// APIKey is required. Never logged, never included in errors.
	APIKey string

	// Model is the model id. Required.
	Model string

	// BaseURL overrides the API root. Empty means the public Anthropic endpoint.
	BaseURL string

	// Client overrides the HTTP client.
	Client *http.Client

	// MaxTokens caps the response length.
	MaxTokens int

	// Version is the required anthropic-version header. Empty means a pinned
	// default, so a future API change is an explicit edit rather than a silent
	// behavioural shift.
	Version string
}

// DefaultAnthropicVersion is pinned rather than floating.
const DefaultAnthropicVersion = "2023-06-01"

// AnthropicProvider calls the Anthropic messages endpoint.
type AnthropicProvider struct {
	key       string
	model     string
	baseURL   string
	client    *http.Client
	maxTokens int
	version   string
}

var _ Provider = (*AnthropicProvider)(nil)

// NewAnthropicProvider validates cfg and returns a provider.
func NewAnthropicProvider(cfg AnthropicConfig) (*AnthropicProvider, error) {
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		return nil, fmt.Errorf("%w: anthropic api key is empty", ErrNotConfigured)
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, fmt.Errorf("%w: anthropic model is empty", ErrNotConfigured)
	}

	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = "https://api.anthropic.com/v1"
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: DefaultHTTPTimeout}
	}

	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 256
	}

	version := strings.TrimSpace(cfg.Version)
	if version == "" {
		version = DefaultAnthropicVersion
	}

	return &AnthropicProvider{
		key:       key,
		model:     model,
		baseURL:   base,
		client:    client,
		maxTokens: maxTokens,
		version:   version,
	}, nil
}

// Name identifies the provider.
func (p *AnthropicProvider) Name() string { return "anthropic" }

// Model reports the configured model.
func (p *AnthropicProvider) Model() string { return p.model }

type anthropicRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends one messages request.
//
// Temperature is pinned to zero for the same reason as the OpenAI client: the
// answer should be the most likely one, and stable across runs.
func (p *AnthropicProvider) Complete(ctx context.Context, system, user string) (string, Usage, error) {
	body := anthropicRequest{
		Model:       p.model,
		System:      system,
		Messages:    []anthropicMessage{{Role: "user", Content: user}},
		MaxTokens:   p.maxTokens,
		Temperature: 0,
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return "", Usage{}, fmt.Errorf("llm: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/messages", bytes.NewReader(encoded))
	if err != nil {
		return "", Usage{}, fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.key)
	req.Header.Set("anthropic-version", p.version)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", Usage{}, fmt.Errorf("llm: request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", Usage{}, fmt.Errorf("llm: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var parsed anthropicResponse
		if json.Unmarshal(raw, &parsed) == nil && parsed.Error != nil && parsed.Error.Message != "" {
			return "", Usage{}, fmt.Errorf("llm: anthropic returned status %d: %s", resp.StatusCode, parsed.Error.Message)
		}
		return "", Usage{}, fmt.Errorf("llm: anthropic returned status %d", resp.StatusCode)
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", Usage{}, fmt.Errorf("llm: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", Usage{}, fmt.Errorf("llm: anthropic error: %s", parsed.Error.Message)
	}

	// Anthropic returns a block list; concatenate the text blocks, ignoring
	// non-text ones so a tool or thinking block does not corrupt the value.
	var b strings.Builder
	for _, block := range parsed.Content {
		if block.Type == "text" || block.Type == "" {
			b.WriteString(block.Text)
		}
	}

	text := strings.TrimSpace(b.String())
	if text == "" {
		return "", Usage{}, ErrEmptyResponse
	}

	return text, Usage{
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
	}, nil
}
