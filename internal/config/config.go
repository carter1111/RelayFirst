// Package config persists a user's non-secret CLI settings.
//
// # What belongs here, and what must never
//
// This file holds choices a user would otherwise retype every run: the task
// sources, the relay nodes, the inference provider name and its model id.
//
// It deliberately holds NO credentials. A private key and a provider API key are
// both secrets, and a plaintext config file is the easiest place in the whole
// system for one to leak — into a backup, a dotfiles repo, a screenshot, or a
// support request.
//
// So:
//   - The agent private key is read from RELAYFIRST_PRIVATE_KEY or from an
//     explicit --key flag, never from here.
//   - The provider API key is read from OPENAI_API_KEY / ANTHROPIC_API_KEY,
//     never from here.
//
// There is no field for either, and Set rejects a key that looks like a secret.
// That is intentional: a refusal at write time is cheaper than discovering a
// leaked key later, and it means an operator cannot create the problem by
// accident.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the config file's name inside the config directory.
const FileName = "config.json"

// Config is the persisted, non-secret user configuration.
type Config struct {
	// Sources are the task source URLs the generator draws from.
	Sources []string `json:"sources,omitempty"`

	// Relays are the relay node base URLs receipts are published to.
	Relays []string `json:"relays,omitempty"`

	// Provider is the inference provider name: "local", "openai" or "anthropic".
	Provider string `json:"provider,omitempty"`

	// Model is the provider's model id, e.g. "gpt-5.5".
	Model string `json:"model,omitempty"`

	// Semantics are natural-language field descriptions for extract tasks.
	// Setting one makes extract tasks consume inference budget (ADR-0002).
	Semantics []string `json:"semantics,omitempty"`
}

// secretMarkers are substrings that indicate a value is credential material
// rather than a setting.
//
// This is a backstop, not the primary defence — the primary defence is that no
// field here is meant to hold a secret. It exists because the most likely way a
// key ends up in this file is someone passing one to the wrong flag, and a clear
// refusal is much better than a silent write.
var secretMarkers = []string{
	"sk-",         // OpenAI-style
	"sk-ant-",     // Anthropic-style
	"-----BEGIN",  // PEM
	"PRIVATE KEY", // as it appears in an error or a paste
}

// ErrSecretValue reports an attempt to persist something that looks like a
// credential.
type ErrSecretValue struct {
	Field string
}

func (e *ErrSecretValue) Error() string {
	return fmt.Sprintf(
		"config: %s looks like credential material and will not be written to the config file; "+
			"keys belong in environment variables (RELAYFIRST_PRIVATE_KEY, OPENAI_API_KEY, ANTHROPIC_API_KEY)",
		e.Field)
}

// Set assigns a value to one named key, rejecting anything secret-shaped.
//
// Named setters keep the CLI's `config set <key> <value>` honest: adding a field
// requires editing this function, so a new setting cannot be persisted before
// someone has decided whether it is safe to persist.
func (c *Config) Set(key, value string) error {
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)

	if value != "" {
		for _, marker := range secretMarkers {
			if strings.Contains(value, marker) {
				return &ErrSecretValue{Field: key}
			}
		}
	}

	switch key {
	case "provider":
		switch value {
		case "local", "openai", "anthropic", "":
			c.Provider = value
		default:
			return fmt.Errorf("config: unknown provider %q (want local, openai or anthropic)", value)
		}
	case "model":
		c.Model = value
	case "source":
		c.Sources = appendUnique(c.Sources, value)
	case "relay":
		c.Relays = appendUnique(c.Relays, value)
	case "semantic":
		c.Semantics = appendUnique(c.Semantics, value)
	case "key", "privatekey", "apikey", "api-key", "secret":
		// Named explicitly rather than falling through to "unknown key", so the
		// message explains *why* this cannot be stored instead of just that it is
		// unrecognised.
		return &ErrSecretValue{Field: key}
	default:
		return fmt.Errorf("config: unknown key %q (want provider, model, source, relay or semantic)", key)
	}
	return nil
}

// Get returns the value of one named key, for echoing back to the user.
//
// It returns an empty string for a key that is unset rather than an error, because
// "not configured" is a normal state and a caller printing current settings should
// not have to special-case it.
func (c *Config) Get(key string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "provider":
		return c.Provider, nil
	case "model":
		return c.Model, nil
	case "source", "sources":
		return strings.Join(c.Sources, ","), nil
	case "relay", "relays":
		return strings.Join(c.Relays, ","), nil
	case "semantic", "semantics":
		return strings.Join(c.Semantics, ","), nil
	default:
		return "", fmt.Errorf("config: unknown key %q", key)
	}
}

// appendUnique adds v if absent, preserving order.
//
// Order is preserved because a user's sources are a preference, and a config file
// that reshuffled itself on every write would be confusing to read and to diff.
func appendUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}

// Dir returns the config directory, honouring RELAYFIRST_CONFIG_DIR.
//
// The override exists so tests never touch a real user's home directory, and so a
// user can keep several identities apart without editing code.
func Dir() (string, error) {
	if d := strings.TrimSpace(os.Getenv("RELAYFIRST_CONFIG_DIR")); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: cannot locate a config directory: %w", err)
	}
	return filepath.Join(base, "relayfirst"), nil
}

// Path returns the full config file path.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads the config, returning an empty one when the file does not exist.
//
// A missing file is not an error: a first run has no configuration, and that is
// the state the 10-minute path starts from.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads the config at path.
func LoadFrom(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &c, nil
}

// Save writes the config to the default location.
func (c *Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	return c.SaveTo(path)
}

// SaveTo writes the config to path with 0600 permissions.
//
// The restrictive mode is applied even though this file holds no secrets, because
// it may hold relay URLs and a provider model that a user considers private, and
// because a config file that is world-readable today is one careless edit away
// from being a leaked key tomorrow.
func (c *Config) SaveTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: create directory: %w", err)
	}

	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	raw = append(raw, '\n')

	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}
