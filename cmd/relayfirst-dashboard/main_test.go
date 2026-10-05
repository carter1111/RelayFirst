package main

import (
	"fmt"
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

// TestFilter_NarrowsLocally: the filter must narrow the list already fetched without
// asking the node for anything.
func TestFilter_NarrowsLocally(t *testing.T) {
	m := newModel(nil, time.Second)
	m.tab = 2
	m.tasks = []noderead.Task{
		{TaskID: "tsk_1", Subject: "https://a.example/price"},
		{TaskID: "tsk_2", Subject: "https://b.example/status"},
	}
	m.filter = "price"
	if got := m.rowCount(); got != 1 {
		t.Errorf("filter must narrow to 1 row, got %d", got)
	}
	if v := m.View(); !strings.Contains(v, "tsk_1") || strings.Contains(v, "tsk_2") {
		t.Errorf("the filtered view must show only the match, got:\n%s", v)
	}
}

// TestInputLine_IsAMode: while typing, a key that is normally a command becomes text.
func TestInputLine_IsAMode(t *testing.T) {
	m := newModel(nil, time.Second)
	m.tab = 1
	// Open the filter.
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !m.editing {
		t.Fatal("/ on Agents must open the filter input")
	}
	// Type "a/b": the slash must be text, not a second command, and "q" must not quit.
	for _, r := range "a/b" {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.input != "a/b" {
		t.Fatalf("input = %q, want \"a/b\" (keys must be text while editing)", m.input)
	}
	// Enter commits.
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.editing {
		t.Error("enter must close the input")
	}
	if m.filter != "a/b" {
		t.Errorf("enter must commit the filter, got %q", m.filter)
	}
}

// TestInputLine_EscCancels keeps a half-typed filter from being applied.
func TestInputLine_EscCancels(t *testing.T) {
	m := newModel(nil, time.Second)
	m.filter = "keep"
	m.tab = 2
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.editing {
		t.Error("esc must close the input")
	}
	if m.filter != "keep" {
		t.Errorf("esc must not change the committed filter, got %q", m.filter)
	}
}

// TestObservations_NoSubjectIsExplained: the one list that needs a query must say so
// rather than looking empty.
func TestObservations_NoSubjectIsExplained(t *testing.T) {
	m := newModel(nil, time.Second)
	m.tab = 3
	v := m.View()
	if !strings.Contains(v, "no subject set") {
		t.Errorf("the observations view must prompt for a subject, got:\n%s", v)
	}
}

// TestObservations_ShowsResultsAndCaveat keeps the subject visible and the caveat present.
func TestObservations_ShowsResultsAndCaveat(t *testing.T) {
	m := newModel(nil, time.Second)
	m.tab = 3
	m.subject = "https://a.example/price"
	m.observations = []noderead.Observation{
		{ReceiptID: "0xabc", Subject: m.subject, TaskType: "probe", ContentHash: "0xdef"},
	}
	v := m.View()
	for _, want := range []string{"https://a.example/price", "0xabc", "not verdicts"} {
		if !strings.Contains(v, want) {
			t.Errorf("the observations view must contain %q, got:\n%s", want, v)
		}
	}
}

// TestHelpIsAMode: while help is up, other keys must not act on the view.
func TestHelpIsAMode(t *testing.T) {
	m := newModel(nil, time.Second)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if !m.help {
		t.Fatal("? must open help")
	}
	// A tab key while help is up must not change the tab.
	m = step(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.tab != 0 {
		t.Errorf("keys other than the dismiss set must be swallowed by help, tab moved to %d", m.tab)
	}
	if v := m.View(); !strings.Contains(v, "keys") {
		t.Errorf("the help view must list keys, got:\n%s", v)
	}
	// A dismiss key closes it.
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.help {
		t.Error("esc must close help")
	}
}

// TestScroll_KeepsCursorInWindow: a list longer than the window must scroll rather
// than draw past the screen.
func TestScroll_KeepsCursorInWindow(t *testing.T) {
	m := newModel(nil, time.Second)
	m.height = 20 // listHeight = 11
	m.tab = 2
	for i := 0; i < 50; i++ {
		m.tasks = append(m.tasks, noderead.Task{TaskID: fmt.Sprintf("t%d", i)})
	}
	// Move past the first page.
	for i := 0; i < 15; i++ {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.cursor != 15 {
		t.Fatalf("cursor = %d, want 15", m.cursor)
	}
	if m.offset == 0 {
		t.Fatal("the window must have scrolled once the cursor passed the first page")
	}
	if m.cursor < m.offset || m.cursor >= m.offset+m.listHeight() {
		t.Errorf("cursor %d must be inside the window [%d,%d)", m.cursor, m.offset, m.offset+m.listHeight())
	}
	// And the render must not include a row outside the window.
	v := m.View()
	if strings.Contains(v, "t0 ") || strings.Contains(v, "t0\n") {
		t.Errorf("a scrolled view must not render the first (off-screen) row, got:\n%s", v)
	}
	if !strings.Contains(v, "showing") {
		t.Errorf("a scrolled view must say how many rows are hidden, got:\n%s", v)
	}
}

// TestScroll_EndAndHome covers the jump keys.
func TestScroll_EndAndHome(t *testing.T) {
	m := newModel(nil, time.Second)
	m.height = 20
	m.tab = 2
	for i := 0; i < 30; i++ {
		m.tasks = append(m.tasks, noderead.Task{TaskID: fmt.Sprintf("t%d", i)})
	}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if m.cursor != 29 {
		t.Errorf("G must go to the last row, got %d", m.cursor)
	}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if m.cursor != 0 {
		t.Errorf("g must go to the first row, got %d", m.cursor)
	}
	if m.offset != 0 {
		t.Errorf("home must reset the window, got offset %d", m.offset)
	}
}

// TestEnvOr covers the precedence rule's building block: an env value wins over the
// fallback, and a blank one counts as unset so an empty `RELAYFIRST_RELAY=` does not
// silently point the dashboard at nothing.
func TestEnvOr(t *testing.T) {
	const key = "RELAYFIRST_TEST_RELAY"
	t.Setenv(key, "http://from-env:9")
	if got := envOr(key, "http://fallback"); got != "http://from-env:9" {
		t.Errorf("env must win, got %q", got)
	}
	t.Setenv(key, "   ")
	if got := envOr(key, "http://fallback"); got != "http://fallback" {
		t.Errorf("a blank env must fall back, got %q", got)
	}
	t.Setenv(key, "")
	if got := envOr(key, "http://fallback"); got != "http://fallback" {
		t.Errorf("an empty env must fall back, got %q", got)
	}
}

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
	m.tab = 4 // Config; Observations took slot 3
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
