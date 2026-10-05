package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/relayfirst/relayfirst/internal/noderead"
)

// These tests cover the model's logic — the parts that can be wrong without a
// terminal. Rendering itself is checked by hand under a pty; what a test can pin is
// the throughput derivation and the failure state.

// step feeds one message to the model and returns it, narrowing the interface type
// bubbletea hands back.
func step(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", next)
	}
	return got
}

// TestThroughput_FirstPollHasNoRate is the honesty guard: a rate needs two points, so
// the first poll must not invent one from a delta against nothing.
func TestThroughput_FirstPollHasNoRate(t *testing.T) {
	m := newModel(nil, time.Second)

	// One poll establishes a baseline and yields no throughput.
	m = step(t, m, pollMsg{well: noderead.WellKnown{Messages: 100}, at: time.Unix(1000, 0)})
	if m.throughput != 0 {
		t.Errorf("the first poll must not report a rate, got %v", m.throughput)
	}
	if m.lastMessages != 100 {
		t.Errorf("the first poll must set the baseline, got %d", m.lastMessages)
	}

	// A second poll one second later with 10 more messages is 10/s.
	m = step(t, m, pollMsg{well: noderead.WellKnown{Messages: 110}, at: time.Unix(1001, 0)})
	if m.throughput < 9.9 || m.throughput > 10.1 {
		t.Errorf("throughput = %v, want ~10", m.throughput)
	}
	if m.peak != m.throughput {
		t.Errorf("peak must track the max, got %v", m.peak)
	}
}

// TestThroughput_NegativeDeltaIsANewBaseline: the store cannot shrink, so a drop means
// the node changed (restart or a different node). Reporting a negative rate would be
// nonsense, so it is treated as a fresh baseline.
func TestThroughput_NegativeDeltaIsANewBaseline(t *testing.T) {
	m := newModel(nil, time.Second)
	m = step(t, m, pollMsg{well: noderead.WellKnown{Messages: 500}, at: time.Unix(2000, 0)})
	m = step(t, m, pollMsg{well: noderead.WellKnown{Messages: 3}, at: time.Unix(2001, 0)})
	if m.throughput < 0 {
		t.Errorf("a shrunk count must not produce a negative rate, got %v", m.throughput)
	}
	if m.lastMessages != 3 {
		t.Errorf("the new count must become the baseline, got %d", m.lastMessages)
	}
}

// TestPollErrorIsShown keeps a down node from rendering as an all-zero, healthy-looking
// dashboard.
func TestPollErrorIsShown(t *testing.T) {
	m := newModel(nil, time.Second)
	m = step(t, m, pollMsg{err: errUnreachable, at: time.Now()})
	if m.pollErr == nil {
		t.Fatal("a poll error must be retained")
	}
	v := m.View()
	if !strings.Contains(v, "unreachable") {
		t.Errorf("the view must say the node is unreachable, got:\n%s", v)
	}
}

// TestView_ShowsTheTrustPosition keeps the one line that matters on screen: this node
// holds no key.
func TestView_ShowsTheTrustPosition(t *testing.T) {
	m := newModel(nil, time.Second)
	m = step(t, m, pollMsg{well: noderead.WellKnown{Name: "relayfirst-node", Messages: 5}, at: time.Now()})
	v := m.View()
	for _, want := range []string{"relayfirst-node", "messages", "holds no key"} {
		if !strings.Contains(v, want) {
			t.Errorf("the view must contain %q, got:\n%s", want, v)
		}
	}
}

var errUnreachable = &unreachableErr{}

type unreachableErr struct{}

func (*unreachableErr) Error() string { return "connection refused" }
