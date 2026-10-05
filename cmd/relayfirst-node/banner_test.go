package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// These tests cover the node's banner/report output and the TTY split that keeps it
// from breaking `docker logs` and pipelines.
//
// The property worth testing is not "does the logo look right" but "does a piped run
// stay free of decoration", because that is what a log scraper and a health check
// depend on. The interactive path is exercised manually (it needs a pty); the
// non-interactive path is what a test can pin.

func TestPaint_RespectsColourFlag(t *testing.T) {
	if got := paint("x", colCyan, false); got != "x" {
		t.Errorf("colour off must emit no escapes, got %q", got)
	}
	if got := paint("x", colCyan, true); !strings.Contains(got, colCyan) || !strings.Contains(got, colReset) {
		t.Errorf("colour on must wrap with the escape and a reset, got %q", got)
	}
}

func TestIsTTY_FalseForAPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	if isTTY(w) {
		t.Error("a pipe must not be treated as a terminal, or escapes would reach a log file")
	}
	if isTTY(nil) {
		t.Error("a nil file must not be treated as a terminal")
	}
}

// TestReportToAPipeHasNoANSI is the load-bearing test: the report is a deliverable,
// and a redirected one must contain no escape sequences.
//
// isTTY reads the file it is given, so pointing os.Stdout at a pipe is exactly what
// `relayfirst-node report > file` does, and the assertion here is the same one a
// reader would make of that file.
func TestReportToAPipeHasNoANSI(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	printReport(config{storage: "x.db", listen: ":8080"}, counts{messages: 7, agents: 2, cards: 1, observations: 3, tasks: 4})
	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read: %v", err)
	}
	_ = r.Close()
	out := buf.String()

	if strings.Contains(out, "\033") {
		t.Errorf("a piped report must contain no ANSI escapes, got:\n%s", out)
	}
	// And it must still carry the data, or "no escapes" would pass on empty output.
	// The logo is block art, so the marker is the subtitle, not the literal word.
	for _, want := range []string{"store-and-forward", "messages", "7", "agents", "observations", "serves"} {
		if !strings.Contains(out, want) {
			t.Errorf("a piped report must still contain %q, got:\n%s", want, out)
		}
	}
}
