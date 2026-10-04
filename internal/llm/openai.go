package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultHTTPTimeout bounds a single completion call.
const DefaultHTTPTimeout = 60 * time.Second

// OpenAIConfig configures the OpenAI-compatible provider.
type OpenAIConfig struct {
	// APIKey is required. It is never logged or included in errors.
	APIKey string

	// Model is the model id, e.g. "gpt-4o-mini". Required.
	Model string

	// BaseURL overrides the API root. Empty means the public OpenAI endpoint.
	// It exists so tests can point at a local stub, and so the provider can be
	// used against any OpenAI-compatible gateway.
	BaseURL string

	// Client overrides the HTTP client. Nil means a default with
	// DefaultHTTPTimeout.
	Client *http.Client

	// MaxTokens caps the response length. Zero means a small default suited to
	// single-field extraction, which is all this provider is used for.
	MaxTokens int
}

// OpenAIProvider calls an OpenAI-compatible chat completions endpoint.
//
// It is a deliberate minimal client: one endpoint, one shape. Anything richer
// belongs behind the Provider interface rather than in this type.
type OpenAIProvider struct {
	key       string
	model     string
	baseURL   string
	client    *http.Client
	maxTokens int
}

// compile-time assertion.
var _ Provider = (*OpenAIProvider)(nil)

// NewOpenAIProvider validates cfg and returns a provider.
//
// Construction fails when the key or model is missing, so a misconfiguration is
// caught before any task runs rather than after a wasted fetch.
func NewOpenAIProvider(cfg OpenAIConfig) (*OpenAIProvider, error) {
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		return nil, fmt.Errorf("%w: openai api key is empty", ErrNotConfigured)
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, fmt.Errorf("%w: openai model is empty", ErrNotConfigured)
	}

	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: DefaultHTTPTimeout}
	}

	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		// A field value is short. A generous cap would let a chatty model
		// inflate the token bill for no benefit.
		maxTokens = 256
	}

	return &OpenAIProvider{
		key:       key,
		model:     model,
		baseURL:   base,
		client:    client,
		maxTokens: maxTokens,
	}, nil
}

// Name identifies the provider.
func (p *OpenAIProvider) Name() string { return "openai" }

// Model reports the configured model.
func (p *OpenAIProvider) Model() string { return p.model }

type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature float64         `json:"temperature"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Complete sends one chat completion request.
//
// Temperature is pinned to zero. Extraction wants the most likely answer, not a
// creative one, and determinism is what keeps two honest runs comparable.
func (p *OpenAIProvider) Complete(ctx context.Context, system, user string) (string, Usage, error) {
	body := openAIRequest{
		Model: p.model,
		Messages: []openAIMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		MaxTokens:   p.maxTokens,
		Temperature: 0,
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		// The request shape is fixed, so this cannot fail in practice; wrapping
		// keeps the error path honest rather than panicking.
		return "", Usage{}, fmt.Errorf("llm: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		return "", Usage{}, fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.key)

	resp, err := p.client.Do(req)
	if err != nil {
		// The transport error can contain the URL but never the header, so the
		// key cannot leak here.
		return "", Usage{}, fmt.Errorf("llm: request failed: %w", err)
	}
	defer resp.Body.Close()

	// Cap the read so a misbehaving endpoint cannot exhaust memory.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", Usage{}, fmt.Errorf("llm: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Surface the provider's message, but only the parsed message field:
		// echoing the whole body risks including request context.
		var parsed openAIResponse
		if json.Unmarshal(raw, &parsed) == nil && parsed.Error != nil && parsed.Error.Message != "" {
			return "", Usage{}, fmt.Errorf("llm: openai returned status %d: %s", resp.StatusCode, parsed.Error.Message)
		}
		return "", Usage{}, fmt.Errorf("llm: openai returned status %d", resp.StatusCode)
	}

	var parsed openAIResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", Usage{}, fmt.Errorf("llm: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", Usage{}, fmt.Errorf("llm: openai error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", Usage{}, ErrEmptyResponse
	}

	text := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if text == "" {
		return "", Usage{}, ErrEmptyResponse
	}

	return text, Usage{
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
	}, nil
}
