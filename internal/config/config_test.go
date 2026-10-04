package config_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/config"
)

// These tests care most about one property: a credential must never be able to
// end up in the config file. Everything else is convenience.

func TestSetAndGet(t *testing.T) {
	var c config.Config

	cases := []struct{ key, value string }{
		{"provider", "openai"},
		{"model", "gpt-5.5"},
		{"source", "https://example.com"},
		{"relay", "http://localhost:8080"},
		{"semantic", "the page title"},
	}

	for _, c2 := range cases {
		if err := c.Set(c2.key, c2.value); err != nil {
			t.Fatalf("Set(%s): %v", c2.key, err)
		}
	}

	for _, c2 := range cases {
		got, err := c.Get(c2.key)
		if err != nil {
			t.Fatalf("Get(%s): %v", c2.key, err)
		}
		// source/relay/semantic are lists, so Get returns them joined.
		if !strings.Contains(got, c2.value) {
			t.Errorf("Get(%s) = %q, want it to contain %q", c2.key, got, c2.value)
		}
	}
}

func TestSet_RejectsUnknownKey(t *testing.T) {
	var c config.Config
	if err := c.Set("nonsense", "x"); err == nil {
		t.Error("an unknown key must be rejected rather than silently dropped")
	}
}

func TestSet_RejectsUnknownProvider(t *testing.T) {
	var c config.Config
	if err := c.Set("provider", "gemini"); err == nil {
		t.Error("an unknown provider must be rejected rather than stored")
	}
}

// TestSet_RefusesSecretShapedValues is the important one.
//
// The config file is the easiest place in the system for a credential to leak:
// into a backup, a dotfiles repo, a screenshot, or a support request. Every path
// by which a key could be written must be closed, and the refusal must name the
// right environment variable so the user knows what to do instead.
func TestSet_RefusesSecretShapedValues(t *testing.T) {
	secrets := []string{
		"sk-1234567890abcdef",         // OpenAI
		"sk-ant-api03-abcdef",         // Anthropic
		"-----BEGIN PRIVATE KEY-----", // PEM
		"0xdeadbeefPRIVATE KEY material",
	}

	for _, s := range secrets {
		var c config.Config
		// Both the dedicated secret keys and an innocent-looking key must refuse.
		for _, key := range []string{"apikey", "api-key", "key", "privatekey", "secret", "model"} {
			err := c.Set(key, s)
			if err == nil {
				t.Errorf("Set(%s, %q) was accepted; a credential must never be stored", key, s)
				continue
			}
			var secretErr *config.ErrSecretValue
			if !errors.As(err, &secretErr) {
				t.Errorf("Set(%s, %q) failed with %v, want an ErrSecretValue", key, s, err)
			}
			if !strings.Contains(err.Error(), "RELAYFIRST_PRIVATE_KEY") {
				t.Errorf("the refusal should say where keys belong, got %q", err.Error())
			}
		}
	}
}

// TestSet_RefusesSecretKeyNamesEvenWhenEmpty: asking to set a credential is a
// mistake worth reporting even if the value is blank, because the next attempt
// would carry a real one.
func TestSet_RefusesSecretKeyNamesEvenWhenEmpty(t *testing.T) {
	for _, key := range []string{"key", "privatekey", "apikey", "secret"} {
		var c config.Config
		if err := c.Set(key, ""); err == nil {
			t.Errorf("Set(%s, \"\") was accepted; the key name itself is not a supported setting", key)
		}
	}
}

func TestSet_SourcesAppendAndDedupe(t *testing.T) {
	var c config.Config

	for i := 0; i < 3; i++ {
		if err := c.Set("source", "https://a.example"); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	if err := c.Set("source", "https://b.example"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if len(c.Sources) != 2 {
		t.Errorf("Sources = %v, want 2 entries (duplicates collapsed)", c.Sources)
	}
	// Order is a user preference, so it must be preserved rather than sorted.
	if c.Sources[0] != "https://a.example" {
		t.Errorf("Sources[0] = %q, want the first one added", c.Sources[0])
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	c := &config.Config{Provider: "anthropic", Model: "claude-x"}
	if err := c.Set("source", "https://example.com"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := c.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	got, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got.Provider != "anthropic" || got.Model != "claude-x" {
		t.Errorf("round trip lost a value: %+v", got)
	}
	if len(got.Sources) != 1 || got.Sources[0] != "https://example.com" {
		t.Errorf("Sources = %v, want the saved source", got.Sources)
	}
}

// TestSaveTo_PermissionsAreRestrictive: the file holds no secrets today, but a
// world-readable config is one careless edit away from being a leaked key.
func TestSaveTo_PermissionsAreRestrictive(t *testing.T) {
	// A nested path so MkdirAll actually creates the directory. Using t.TempDir()
	// directly would test the mode of a directory the test framework made, not the
	// one this code creates.
	dir := filepath.Join(t.TempDir(), "relayfirst")
	path := filepath.Join(dir, "config.json")

	c := &config.Config{Provider: "local"}
	if err := c.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config file mode = %o, want 600", perm)
	}

	// The directory this code creates must not be world-readable.
	//
	// Note the scope: MkdirAll applies the mode only when it creates the
	// directory. If relayfirst/ already exists — created by an older version, or
	// by the user — this leaves its mode alone rather than chmod-ing a directory it
	// does not own. The file's own 0600 is the guarantee that matters.
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("newly created config dir mode = %o, want no group/other access", perm)
	}
}

// TestSaveTo_LeavesExistingDirectoryModeAlone records the deliberate limitation
// above, so a future change that starts chmod-ing a user's directory is noticed.
func TestSaveTo_LeavesExistingDirectoryModeAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pre-existing")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	c := &config.Config{Provider: "local"}
	if err := c.SaveTo(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("existing dir mode = %o, want 755 to be left alone", perm)
	}
}

// TestSaveTo_NeverWritesAKey is the end-to-end guard: even if every other check
// were bypassed, the serialized bytes must not contain a credential.
func TestSaveTo_NeverWritesAKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	c := &config.Config{Provider: "local", Model: "local/lookup"}
	if err := c.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// No field in the struct can carry these, so the only way one appears is a
	// future edit adding a field — which this catches.
	for _, forbidden := range []string{"sk-", "PRIVATE KEY", "privateKey", "apiKey", "secret"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("the serialized config contains %q; it must never hold credential material", forbidden)
		}
	}
}

// TestLoadFrom_MissingFileIsEmpty: a first run has no config, and that is the state
// the 10-minute path starts from.
func TestLoadFrom_MissingFileIsEmpty(t *testing.T) {
	c, err := config.LoadFrom(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("a missing file should not be an error, got %v", err)
	}
	if c.Provider != "" || len(c.Sources) != 0 {
		t.Errorf("expected an empty config, got %+v", c)
	}
}

func TestLoadFrom_MalformedIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"provider":`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// A malformed config must be reported rather than silently ignored: ignoring
	// it would make a user's saved sources appear to vanish.
	if _, err := config.LoadFrom(path); err == nil {
		t.Error("a malformed config file must be reported")
	}
}

func TestGet_UnknownKey(t *testing.T) {
	var c config.Config
	if _, err := c.Get("nonsense"); err == nil {
		t.Error("Get on an unknown key must fail")
	}
}

// TestConfigStructHasNoSecretFields is the structural half of the guarantee.
//
// The behavioural checks above can only test the fields that exist. If someone adds
// a `PrivateKey` or `APIKey` field later, those checks would keep passing while the
// file became a place credentials are written. This asserts the schema itself.
func TestConfigStructHasNoSecretFields(t *testing.T) {
	raw, err := json.Marshal(&config.Config{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	forbidden := []string{"key", "secret", "token", "password", "passphrase", "credential", "mnemonic", "seed"}
	for field := range m {
		lower := strings.ToLower(field)
		for _, f := range forbidden {
			if strings.Contains(lower, f) {
				t.Errorf("Config has a field %q that looks like credential material; "+
					"secrets belong in environment variables, not on disk", field)
			}
		}
	}
}
