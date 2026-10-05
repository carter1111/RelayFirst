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

// TestTabs_CycleAndClamp checks the tab selection stays in range.
func TestTabs_CycleAndClamp(t *testing.T) {
	m := newModel(nil, time.Second)
	if m.tab != 0 {
		t.Fatalf("must start on Overview, got %d", m.tab)
	}
	// Tab past the end wraps to 0.
	for i := 0; i < len(tabs); i++ {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyTab})
	}
	if m.tab != 0 {
		t.Errorf("tabbing %d times must wrap to 0, got %d", len(tabs), m.tab)
	}
	// Shift+tab from 0 wraps to the last.
	m = step(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.tab != len(tabs)-1 {
		t.Errorf("shift+tab from 0 must wrap to %d, got %d", len(tabs)-1, m.tab)
	}
}

// TestViewTasks_ShowsRowsAndTheCaveat: the list must show data AND repeat the node's
// "not an authority" note, because a dashboard is where a reader most easily forgets it.
func TestViewTasks_ShowsRowsAndTheCaveat(t *testing.T) {
	m := newModel(nil, time.Second)
	m.tab = 2
	m.tasks = []noderead.Task{
		{TaskID: "tsk_1", Subject: "https://a.example/x", Claims: 3},
		{TaskID: "tsk_2", Subject: "https://b.example/y", Claims: 0},
	}
	v := m.View()
	for _, want := range []string{"tsk_1", "tsk_2", "claims", "not an authority"} {
		if !strings.Contains(v, want) {
			t.Errorf("tasks view must contain %q, got:\n%s", want, v)
		}
	}
}

// TestViewAgents_EmptyIsExplained keeps an empty directory from looking broken.
func TestViewAgents_EmptyIsExplained(t *testing.T) {
	m := newModel(nil, time.Second)
	m.tab = 1
	v := m.View()
	if !strings.Contains(v, "no agent cards") {
		t.Errorf("an empty agents view must say so, got:\n%s", v)
	}
}

// TestViewConfig_ShowsConnectionAndNote keeps the config tab useful: it must show what
// it is connected to and repeat the node's own trust note.
func TestViewConfig_ShowsConnectionAndNote(t *testing.T) {
	m := newModel(nil, time.Second)
	m.base = "http://localhost:8080"
	m.tab = 3
	m.well = noderead.WellKnown{Name: "relayfirst-node", Note: "holds no key"}
	v := m.View()
	for _, want := range []string{"http://localhost:8080", "holds no key", "poll"} {
		if !strings.Contains(v, want) {
			t.Errorf("config view must contain %q, got:\n%s", want, v)
		}
	}
}

// TestCursor_ClampsToRowCount keeps a shrinking list from leaving the cursor past the
// end, where nothing would be highlighted.
func TestCursor_ClampsToRowCount(t *testing.T) {
	m := newModel(nil, time.Second)
	m.tab = 2
	m.tasks = []noderead.Task{{TaskID: "a"}, {TaskID: "b"}}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Errorf("cursor must clamp to the last row (1), got %d", m.cursor)
	}
	// A poll that returns fewer rows resets the cursor.
	m = step(t, m, pollMsg{well: noderead.WellKnown{}, tasks: nil, at: time.Now()})
	if m.cursor != 0 {
		t.Errorf("cursor must reset when the listing shrinks, got %d", m.cursor)
	}
}

// TestListErrorDoesNotLookUnreachable is the distinction that matters: a reachable node
// whose listing failed must not be shown as down, because the counts are still live.
func TestListErrorDoesNotLookUnreachable(t *testing.T) {
	m := newModel(nil, time.Second)
	m = step(t, m, pollMsg{
		well:    noderead.WellKnown{Name: "relayfirst-node"},
		listErr: errUnreachable,
		at:      time.Now(),
	})
	if m.pollErr != nil {
		t.Fatal("a listing error must not set the connectivity error")
	}
	v := m.View()
	if strings.Contains(v, "unreachable") {
		t.Errorf("a listing failure must not read as unreachable, got:\n%s", v)
	}
	m.tab = 2
	if v := m.View(); !strings.Contains(v, "listing failed") {
		t.Errorf("the list view must explain its failure, got:\n%s", v)
	}
}
