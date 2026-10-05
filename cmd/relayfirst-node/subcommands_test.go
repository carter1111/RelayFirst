package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()
	return buf.String(), runErr
}

// TestRun_LeadingFlagIsNotASubcommand is the backward-compatibility guard.
//
// A deployment runs `relayfirst-node --listen :8080`. If a leading flag were ever
// mistaken for a subcommand, every existing command line and every systemd/Compose
// file would stop starting a node. This pins that a leading `--` is served, not
// dispatched: --version prints and returns without touching a store.
func TestRun_LeadingFlagIsNotASubcommand(t *testing.T) {
	out, err := captureStdout(t, func() error { return run([]string{"--version"}) })
	if err != nil {
		t.Fatalf("run --version: %v", err)
	}
	if !strings.Contains(out, version) {
		t.Fatalf("--version must print the version (a flag was treated as a subcommand?), got %q", out)
	}
}

// TestInspect_RequiresATarget keeps a bare `inspect` from doing nothing.
func TestInspect_RequiresATarget(t *testing.T) {
	if err := runInspect(nil); err == nil {
		t.Fatal("inspect with no target must error rather than print nothing")
	}
}

// TestInspect_UnknownTargetErrors keeps a typo from looking like an empty result.
func TestInspect_UnknownTargetErrors(t *testing.T) {
	err := runInspect([]string{"agentz", "--storage", filepath.Join(t.TempDir(), "x.db")})
	if err == nil {
		t.Fatal("an unknown inspect target must error")
	}
	if !strings.Contains(err.Error(), "unknown inspect target") {
		t.Errorf("the error should name the problem, got: %v", err)
	}
}

// TestInspect_MessagesNeedsAgent closes the one target that cannot answer without an
// argument, rather than returning an empty list that reads as "no messages".
func TestInspect_MessagesNeedsAgent(t *testing.T) {
	err := runInspect([]string{"messages", "--storage", filepath.Join(t.TempDir(), "x.db")})
	if err == nil {
		t.Fatal("inspect messages without --agent must error")
	}
}

// TestInspect_ObservationsNeedsSubject: the store indexes observations by subject, so
// a list without one is not a question it can answer.
func TestInspect_ObservationsNeedsSubject(t *testing.T) {
	err := runInspect([]string{"observations", "--storage", filepath.Join(t.TempDir(), "x.db")})
	if err == nil {
		t.Fatal("inspect observations without --subject must error")
	}
}

// TestReport_OpensAStoreWithoutServing is the offline contract: report must open (and
// create, if absent) the store and return without binding a port.
func TestReport_OpensAStoreWithoutServing(t *testing.T) {
	db := filepath.Join(t.TempDir(), "x.db")
	out, err := captureStdout(t, func() error { return runReport([]string{"--storage", db}) })
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if _, statErr := os.Stat(db); statErr != nil {
		t.Errorf("report must open/create the store at --storage, got stat error: %v", statErr)
	}
	// And it must show the counts section, or "it returned" would pass on empty output.
	if !strings.Contains(out, "holds") || !strings.Contains(out, "messages") {
		t.Errorf("report must print the counts section, got:\n%s", out)
	}
}

// TestInspect_AgentsAndTasksPrintJSON checks the two targets that work on an empty
// store produce valid, warning-bearing JSON rather than prose.
func TestInspect_AgentsAndTasksPrintJSON(t *testing.T) {
	db := filepath.Join(t.TempDir(), "x.db")
	for _, target := range []string{"agents", "tasks", "db"} {
		out, err := captureStdout(t, func() error {
			return runInspect([]string{target, "--storage", db})
		})
		if err != nil {
			t.Fatalf("inspect %s: %v", target, err)
		}
		if !strings.HasPrefix(strings.TrimSpace(out), "{") {
			t.Errorf("inspect %s must print a JSON object, got:\n%s", target, out)
		}
		if strings.Contains(out, "\033") {
			t.Errorf("inspect %s must not emit ANSI escapes", target)
		}
	}
}
