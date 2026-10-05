// Command relayfirst-dashboard is a read-only terminal dashboard for a relay node.
//
// # Why this is a separate binary, and not part of the node
//
// A node is a long-lived server; this is a client. The usual shape for observing a
// server is a separate program that connects to it (psql, redis-cli, k9s, lazydocker),
// and the reason applies here: the node stays small and carries no UI dependency, the
// dashboard can attach to a node that is ALREADY running, it can point at a remote
// node, and closing it cannot affect the node at all.
//
// # Security posture, stated because it is the point
//
// This holds no key and cannot sign anything (it does not import the signing
// packages). It reads ONLY public endpoints (the node has no auth to speak of — NET-1
// — and inventing one for a dashboard is out of scope), so it adds nothing to the
// node's exposure. It caps response sizes and never disables TLS verification. See
// internal/noderead for the enforcement.
//
// # Why polling, not a stream
//
// The node has no node-wide activity stream (its hub fans out per agentId), so the
// dashboard polls counts and differences them to show throughput. A single Client is
// used for every poll so the backoff actually protects the node.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/relayfirst/relayfirst/internal/noderead"
	"github.com/relayfirst/relayfirst/internal/term"
)

const usage = `relayfirst-dashboard — read-only terminal dashboard for a relay node

Usage:
  relayfirst-dashboard [flags]

Flags:
  --url <url>        Node to observe. Also RELAYFIRST_RELAY (the same variable
                     ` + "`relayfirst session`" + ` uses). Default: http://localhost:8080,
                     where a node on its own default :8080 is reachable. Point it
                     elsewhere for another port or host:
                       --url http://localhost:9000
                       --url https://relay.example.com
  --interval <dur>   Poll interval (default 1.5s)
  --version          Print the version

Precedence: flag > environment > default.

Nothing is required when the node runs locally on its defaults: run the node with
` + "`relayfirst-node`" + ` and this with no flags.

It reads only the node's public endpoints and never controls it. A node runs
headless; this is the thing you open in ANOTHER terminal to watch it.
`

const version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	// Precedence is flag > environment > default. RELAYFIRST_RELAY is the same variable
	// `relayfirst session` uses to name a node, so a shell that already exports it
	// points the dashboard at the same node with no extra flag — one name, one meaning.
	url := envOr("RELAYFIRST_RELAY", "http://localhost:8080")
	interval := 1500 * time.Millisecond

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--url":
			if i+1 >= len(args) {
				return fmt.Errorf("--url needs a value")
			}
			url = args[i+1]
			i++
		case "--interval":
			if i+1 >= len(args) {
				return fmt.Errorf("--interval needs a value")
			}
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				return fmt.Errorf("--interval must be a duration like 2s: %w", err)
			}
			interval = d
			i++
		case "--version", "-v":
			fmt.Println(version)
			return nil
		case "-h", "--help":
			fmt.Print(usage)
			return nil
		default:
			return fmt.Errorf("unknown flag %q (try --help)", args[i])
		}
	}

	client, err := noderead.New(url)
	if err != nil {
		return err
	}

	// A dashboard is interactive by nature. Without a terminal there is nothing to
	// render and no keys to read, so say so rather than emitting escape sequences into
	// a log. This is the same TTY rule the node follows.
	if !term.IsTTY(os.Stdout) {
		return fmt.Errorf("no terminal: relayfirst-dashboard is interactive. "+
			"For non-interactive checks use `relayfirst-node status --url %s`", url)
	}

	p := tea.NewProgram(newModel(client, interval), tea.WithAltScreen())
	_, err = p.Run()
	return err
}

// pollMsg carries one poll's result to the model.
//
// Agents and tasks are fetched in the same poll as the counts: a second, independent
// refresh path would let the header and the list disagree, and there is no reason to
// spend two round trips on one node when one will do.
type pollMsg struct {
	health noderead.Health
	well   noderead.WellKnown
	agents []noderead.Card
	tasks  []noderead.Task

	// observations are fetched for the subject the user typed. They are carried with
	// the poll so the same backoff and cap apply, and so an empty result is a real
	// answer rather than a missing fetch.
	observations []noderead.Observation
	// obsSubject echoes what the observations were fetched for, so a result is not
	// attributed to a subject the user has since changed.
	obsSubject string

	// err is a CONNECTIVITY failure (health or well-known): the node is unreachable.
	// listErr is a listing failure on a reachable node, which must not be shown as
	// "unreachable" — the counts are still live.
	err     error
	listErr error
	at      time.Time
}

// model is the dashboard state.
type model struct {
	client *noderead.Client
	// base is the node address, captured at construction so View never dereferences
	// the client. Rendering must not be able to panic on a partially-built model.
	base     string
	interval time.Duration

	// last counts and the time they changed, for the throughput computation.
	lastMessages int
	lastAt       time.Time

	// throughput is messages/second derived from count deltas.
	throughput float64
	// peak is the largest throughput seen, so a spike is not lost between redraws.
	peak float64
	// history keeps recent throughput for the sparkline.
	history []float64

	health  noderead.Health
	well    noderead.WellKnown
	pollErr error // connectivity: the node is unreachable
	listErr error // a listing failed on a reachable node
	lastAt2 time.Time

	// tab selects the view; cursor selects a row within a list view, offset is the
	// first row shown so a list longer than the window scrolls instead of overflowing.
	tab    int
	cursor int
	offset int

	// help shows the key reference. It is a mode: while it is up, the keys that would
	// otherwise act are consumed by closing it.
	help bool

	// filter is a local, case-insensitive substring applied to the Agents and Tasks
	// lists. It narrows what is ALREADY fetched; it does not ask the node for anything
	// new, so it is instant and cannot be a query-injection surface.
	filter string
	// subject is the URL the Observations tab queries. The node indexes observations
	// by subject, so this is the one list that is a real query rather than a filter.
	subject string
	// observations holds the last subject's results.
	observations []noderead.Observation

	// input is the text being typed when an input line is open, and editing says one is.
	editing bool
	input   string
	// editingSubject distinguishes "typing a filter" from "typing a subject", since both
	// use the same input line.
	editingSubject bool

	// agents and tasks are the active listings. They are refreshed on every poll so
	// switching tabs shows current data without a separate fetch path.
	agents []noderead.Card
	tasks  []noderead.Task

	width  int
	height int
}

const historyLen = 48

// The tabs, in order. Kept as a slice so the header and the key handler agree on how
// many there are and what they are called.
var tabs = []string{"Overview", "Agents", "Tasks", "Observations", "Config"}

func newModel(client *noderead.Client, interval time.Duration) model {
	m := model{client: client, interval: interval, history: make([]float64, 0, historyLen)}
	if client != nil {
		m.base = client.BaseURL()
	}
	return m
}

// listLimit caps each listing. A dashboard shows a working view, not an archive; the
// node is asked for a bounded page so a large store does not become a large render.
const listLimit = 50

// poll fires one poll immediately, then on the interval.
//
// The listings are fetched here rather than on tab switch so the data is already
// present when a tab is shown, and so a slow request cannot make a tab appear to hang.
func (m model) poll() tea.Cmd {
	return func() tea.Msg {
		h, err := m.client.Health()
		if err != nil {
			return pollMsg{err: err, at: time.Now()}
		}
		wk, err := m.client.WellKnown()
		if err != nil {
			return pollMsg{health: h, err: err, at: time.Now()}
		}
		// A listing failure is not fatal to the overview: counts still render. The
		// error is carried so the list views can show why they are empty.
		agents, aErr := m.client.Agents(listLimit)
		tasks, tErr := m.client.Tasks("", listLimit)
		var lErr error
		if aErr != nil {
			lErr = aErr
		} else if tErr != nil {
			lErr = tErr
		}

		// Observations are only fetched when a subject is set: the node indexes them by
		// subject and has no "list all" endpoint, so an empty subject is not a query.
		msg := pollMsg{health: h, well: wk, agents: agents, tasks: tasks, listErr: lErr, at: time.Now()}
		if m.subject != "" {
			obs, oErr := m.client.Observations(m.subject, listLimit)
			if oErr != nil && lErr == nil {
				msg.listErr = oErr
			}
			msg.observations = obs
			msg.obsSubject = m.subject
		}
		return msg
	}
}

func (m model) tick() tea.Cmd {
	return tea.Tick(m.interval, func(t time.Time) tea.Msg { return tickMsg{t} })
}

type tickMsg struct{ at time.Time }

func (m model) Init() tea.Cmd {
	return tea.Batch(m.poll(), m.tick())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// While help is up it swallows everything except the keys that dismiss it, so a
		// stray 'r' or 'j' cannot act on a view the user cannot fully see.
		if m.help {
			switch msg.String() {
			case "?", "esc", "q", "enter", " ":
				m.help = false
			case "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}

		// The input line is a mode too: while it is open, printable keys edit the buffer
		// rather than acting as commands. A `/` typed into a filter is text, not a second
		// command, which is the behaviour a reader expects.
		if m.editing {
			return m.updateEditing(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "?":
			m.help = true
		case "r":
			return m, m.poll()
		case "tab", "right":
			m.tab = (m.tab + 1) % len(tabs)
			m.cursor = 0
		case "shift+tab", "left":
			m.tab = (m.tab - 1 + len(tabs)) % len(tabs)
			m.cursor = 0
		case "1", "2", "3", "4", "5":
			// Number keys jump straight to a tab. The mapping is derived from the key
			// string so it stays correct if tabs are reordered, and it is bounded by the
			// tab count rather than assumed to be a fixed number.
			for i := range tabs {
				if msg.String() == fmt.Sprint(i+1) {
					m.tab = i
					m.cursor = 0
					m.offset = 0
					break
				}
			}
		case "/":
			// Filter the current list. It is a local narrowing of what is already fetched,
			// so it is instant and asks the node for nothing.
			if m.tab == 1 || m.tab == 2 {
				m.editing = true
				m.editingSubject = false
				m.input = m.filter
			}
		case "s":
			// Set the subject the Observations tab queries. This IS a fetch, because the
			// node indexes observations by subject and cannot list them all.
			if m.tab == 3 {
				m.editing = true
				m.editingSubject = true
				m.input = m.subject
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < m.rowCount()-1 {
				m.cursor++
			}
		case "pgdown", "ctrl+f":
			m.cursor += m.pageSize()
			if m.cursor > m.rowCount()-1 {
				m.cursor = m.rowCount() - 1
			}
		case "pgup", "ctrl+b":
			m.cursor -= m.pageSize()
			if m.cursor < 0 {
				m.cursor = 0
			}
		case "home", "g":
			m.cursor = 0
		case "end", "G":
			m.cursor = m.rowCount() - 1
		}
		m.clampOffset()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampOffset()

	case tickMsg:
		return m, tea.Batch(m.poll(), m.tick())

	case pollMsg:
		m.lastAt2 = msg.at
		if msg.err != nil {
			m.pollErr = msg.err
			return m, nil
		}
		m.pollErr = nil
		m.health = msg.health
		m.well = msg.well
		m.listErr = msg.listErr
		m.agents = msg.agents
		m.tasks = msg.tasks
		// Only accept observations for the subject still current, so a late reply for an
		// old subject cannot replace the results for a newer one.
		if msg.obsSubject == m.subject {
			m.observations = msg.observations
		}
		// Clamp the cursor: a listing can shrink between polls, and a cursor past the
		// end would leave nothing highlighted. The offset is clamped with it so the
		// window does not start past the new end.
		if m.cursor >= m.rowCount() {
			m.cursor = 0
		}
		m.clampOffset()

		// Throughput from the count delta. The FIRST poll establishes a baseline and
		// yields no rate, because a delta against an unknown previous value would be a
		// made-up number.
		if !m.lastAt.IsZero() {
			dt := msg.at.Sub(m.lastAt).Seconds()
			if dt > 0 {
				dm := float64(msg.well.Messages - m.lastMessages)
				if dm < 0 {
					// The store cannot shrink, so a negative delta means the node changed
					// (restart or a different node); treat it as a new baseline.
					dm = 0
				}
				m.throughput = dm / dt
				if m.throughput > m.peak {
					m.peak = m.throughput
				}
				m.history = append(m.history, m.throughput)
				if len(m.history) > historyLen {
					m.history = m.history[len(m.history)-historyLen:]
				}
			}
		}
		m.lastMessages = msg.well.Messages
		m.lastAt = msg.at
		return m, nil
	}
	return m, nil
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	dimStyle   = lipgloss.NewStyle().Faint(true)
	labelStyle = lipgloss.NewStyle().Bold(true)
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// updateEditing handles keys while the input line is open.
//
// The mode exists so a key that is normally a command becomes text: a reader filtering
// for "task/1" must be able to type the slash. Enter commits, Esc cancels, and the
// cursor is moved to the end of whatever was committed so a filtered list starts at the
// top rather than at a stale position.
func (m model) updateEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		if m.editingSubject {
			m.subject = strings.TrimSpace(m.input)
		} else {
			m.filter = strings.TrimSpace(m.input)
		}
		m.editing = false
		m.cursor = 0
		m.offset = 0
		// A new subject is a new query; a filter is local and needs no fetch.
		if m.editingSubject {
			return m, m.poll()
		}
		return m, nil
	case tea.KeyEsc:
		m.editing = false
		return m, nil
	case tea.KeyBackspace:
		if len(m.input) > 0 {
			r := []rune(m.input)
			m.input = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.input += string(msg.Runes)
		if msg.Type == tea.KeySpace {
			m.input += " "
		}
		return m, nil
	}
	return m, nil
}

// filteredAgents and filteredTasks apply the local filter, case-insensitively.
//
// They return indices into the full slice so the cursor and the row it points at stay
// consistent, and so the view can show a row without re-deriving which it is.
func (m model) filteredAgents() []int {
	needle := strings.ToLower(m.filter)
	out := make([]int, 0, len(m.agents))
	for i, c := range m.agents {
		if needle == "" || strings.Contains(strings.ToLower(c.AgentID), needle) {
			out = append(out, i)
		}
	}
	return out
}

func (m model) filteredTasks() []int {
	needle := strings.ToLower(m.filter)
	out := make([]int, 0, len(m.tasks))
	for i, t := range m.tasks {
		if needle == "" ||
			strings.Contains(strings.ToLower(t.TaskID), needle) ||
			strings.Contains(strings.ToLower(t.Subject), needle) ||
			strings.Contains(strings.ToLower(t.Requester), needle) {
			out = append(out, i)
		}
	}
	return out
}

// rowCount is the number of selectable rows in the current tab.
func (m model) rowCount() int {
	switch m.tab {
	case 1:
		return len(m.filteredAgents())
	case 2:
		return len(m.filteredTasks())
	case 3:
		return len(m.observations)
	default:
		return 0
	}
}

// listHeight is how many rows the list area can show.
//
// It is derived from the window and the fixed chrome above the list (header, tab bar,
// the note line, the footer). A minimum keeps a tiny window usable rather than showing
// zero rows.
func (m model) listHeight() int {
	const chrome = 9
	h := m.height - chrome
	if h < 3 {
		return 3
	}
	return h
}

func (m model) pageSize() int { return m.listHeight() }

// clampOffset keeps the cursor inside the visible window: if it moved past the bottom
// the window follows, and if it moved above the top the window follows up. This is what
// makes a long list scroll instead of drawing past the screen.
func (m *model) clampOffset() {
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
	// If the list shrank, the window may now start past the end.
	if max := m.rowCount() - h; m.offset > max {
		m.offset = max
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m model) View() string {
	var b strings.Builder

	// Header: node address and liveness.
	dot := okStyle.Render("● live")
	if m.pollErr != nil {
		dot = errStyle.Render("● unreachable")
	}
	fmt.Fprintf(&b, "%s  %s   %s\n", titleStyle.Render("RELAY dashboard"), orDash(m.base), dot)
	fmt.Fprintf(&b, "  %s\n\n", m.tabBar())

	if m.help {
		m.viewHelp(&b)
		return b.String()
	}

	if m.pollErr != nil {
		fmt.Fprintf(&b, "%s\n\n", errStyle.Render("  "+m.pollErr.Error()))
		fmt.Fprintf(&b, "%s\n", dimStyle.Render("  the node may be down; retrying with backoff. [r] retry now, [q] quit"))
		return b.String()
	}

	switch m.tab {
	case 1:
		m.viewAgents(&b)
	case 2:
		m.viewTasks(&b)
	case 3:
		m.viewObservations(&b)
	case 4:
		m.viewConfig(&b)
	default:
		m.viewOverview(&b)
	}

	b.WriteString("\n")
	// The input line replaces the footer when it is open, so what is being typed is
	// always visible and never competes with the hint text.
	if m.editing {
		label := "filter"
		if m.editingSubject {
			label = "subject"
		}
		fmt.Fprintf(&b, "  %s %s%s\n", labelStyle.Render(label+":"), m.input, titleStyle.Render("▏"))
		fmt.Fprintf(&b, "%s\n", dimStyle.Render("  [Enter] apply  [Esc] cancel"))
		return b.String()
	}
	fmt.Fprintf(&b, "%s\n", dimStyle.Render("  [Tab/1-5] section  [↑↓] select  [/]"+m.filterHint()+"  [r] refresh  [?] help  [q] quit"))
	return b.String()
}

// filterHint names the input key for the current tab, so the footer does not offer a
// key that would do nothing.
func (m model) filterHint() string {
	switch m.tab {
	case 1, 2:
		return "filter"
	case 3:
		return "subject"
	default:
		return "—"
	}
}

// viewHelp draws the key reference.
//
// It is a mode rather than a line because the key set is larger than one line and a
// dashboard is used occasionally enough that a reference beats recall.
func (m model) viewHelp(b *strings.Builder) {
	fmt.Fprintf(b, "  %s\n\n", labelStyle.Render("keys"))
	rows := [][2]string{
		{"Tab / →", "next section"},
		{"Shift-Tab / ←", "previous section"},
		{"1 – 5", "jump to a section"},
		{"↑ ↓ / k j", "move the selection"},
		{"PgUp / PgDn", "move a page at a time"},
		{"g / G", "first / last row"},
		{"/", "filter Agents / Tasks (local, instant)"},
		{"s", "set the Observations subject (a query)"},
		{"r", "refresh now"},
		{"?", "toggle this help"},
		{"q / Ctrl-C", "quit (the node keeps running)"},
	}
	for _, r := range rows {
		fmt.Fprintf(b, "    %-16s %s\n", titleStyle.Render(r[0]), r[1])
	}
	fmt.Fprintf(b, "\n%s\n", dimStyle.Render("  this dashboard is read-only: it never writes to or controls the node"))
}

// tabBar renders the tab strip with the active one highlighted.
func (m model) tabBar() string {
	parts := make([]string, 0, len(tabs))
	for i, name := range tabs {
		if i == m.tab {
			parts = append(parts, titleStyle.Render("["+name+"]"))
		} else {
			parts = append(parts, dimStyle.Render(name))
		}
	}
	return strings.Join(parts, " ")
}

func (m model) viewOverview(b *strings.Builder) {
	fmt.Fprintf(b, "  %-14s %s\n", labelStyle.Render("node"), orDash(m.well.Name))
	fmt.Fprintf(b, "  %-14s %s\n", labelStyle.Render("version"), orDash(m.well.Version))
	fmt.Fprintf(b, "  %-14s %s\n", labelStyle.Render("protocol"), orDash(m.well.Protocol))
	fmt.Fprintf(b, "  %-14s %s\n\n", labelStyle.Render("verifies"),
		errStyle.Render("false")+dimStyle.Render(" (holds no key)"))

	fmt.Fprintf(b, "  %s\n", labelStyle.Render("holds"))
	fmt.Fprintf(b, "    messages      %d\n", m.well.Messages)
	fmt.Fprintf(b, "    agents        %d\n", m.well.Agents)
	fmt.Fprintf(b, "    agent cards   %d\n", m.well.AgentCards)
	fmt.Fprintf(b, "    observations  %d\n", m.well.Observations)
	fmt.Fprintf(b, "    tasks         %d\n\n", m.well.Tasks)

	fmt.Fprintf(b, "  %s  %s\n", labelStyle.Render("throughput"),
		fmt.Sprintf("%.1f msg/s   peak %.1f", m.throughput, m.peak))
	fmt.Fprintf(b, "  %s\n", sparkline(m.history))
	fmt.Fprintf(b, "%s\n", dimStyle.Render("  derived from count deltas; the node has no node-wide message stream"))
}

func (m model) viewAgents(b *strings.Builder) {
	if m.listErr != nil {
		fmt.Fprintf(b, "%s\n", errStyle.Render("  listing failed: "+m.listErr.Error()))
		return
	}
	if len(m.agents) == 0 {
		fmt.Fprintf(b, "%s\n", dimStyle.Render("  no agent cards published to this node"))
		return
	}
	// The note repeats the node's own: a directory is not an authority. A dashboard that
	// dropped it would be the one place a reader forgets it.
	fmt.Fprintf(b, "%s\n\n", dimStyle.Render("  a directory, not an authority: this node has not verified any proof"))
	idx := m.filteredAgents()
	if m.filter != "" {
		fmt.Fprintf(b, "%s\n", dimStyle.Render(fmt.Sprintf("  filter %q → %d of %d", m.filter, len(idx), len(m.agents))))
	}
	lo, hi := m.window(len(idx))
	for i := lo; i < hi; i++ {
		c := m.agents[idx[i]]
		line := fmt.Sprintf("%-46s  card %4dB  proof %4dB", orDash(short(c.AgentID, 46)), len(c.Card), len(c.Proof))
		if i == m.cursor {
			fmt.Fprintf(b, "%s\n", titleStyle.Render("  ▸ "+line))
		} else {
			fmt.Fprintf(b, "    %s\n", line)
		}
	}
	m.scrollHint(b, len(idx), lo, hi)
}

// window returns the visible slice bounds [lo, hi) for a list of n rows.
func (m model) window(n int) (int, int) {
	lo := m.offset
	if lo > n {
		lo = n
	}
	hi := lo + m.listHeight()
	if hi > n {
		hi = n
	}
	return lo, hi
}

// scrollHint says how many rows are hidden, so a scrolled list does not look complete.
func (m model) scrollHint(b *strings.Builder, n, lo, hi int) {
	if n <= hi-lo {
		return
	}
	fmt.Fprintf(b, "%s\n", dimStyle.Render(fmt.Sprintf("  showing %d–%d of %d", lo+1, hi, n)))
}

func (m model) viewTasks(b *strings.Builder) {
	if m.listErr != nil {
		fmt.Fprintf(b, "%s\n", errStyle.Render("  listing failed: "+m.listErr.Error()))
		return
	}
	if len(m.tasks) == 0 {
		fmt.Fprintf(b, "%s\n", dimStyle.Render("  no tasks on this node's board"))
		return
	}
	fmt.Fprintf(b, "%s\n\n", dimStyle.Render("  a noticeboard, not an authority: no exclusivity, no expiry enforcement"))
	idx := m.filteredTasks()
	if m.filter != "" {
		fmt.Fprintf(b, "%s\n", dimStyle.Render(fmt.Sprintf("  filter %q → %d of %d", m.filter, len(idx), len(m.tasks))))
	}
	lo, hi := m.window(len(idx))
	for i := lo; i < hi; i++ {
		t := m.tasks[idx[i]]
		line := fmt.Sprintf("%-22s  claims %-3d  %s", orDash(short(t.TaskID, 22)), t.Claims, orDash(short(t.Subject, 40)))
		if i == m.cursor {
			fmt.Fprintf(b, "%s\n", titleStyle.Render("  ▸ "+line))
		} else {
			fmt.Fprintf(b, "    %s\n", line)
		}
	}
	m.scrollHint(b, len(idx), lo, hi)
}

// viewObservations shows observations for a subject. Unlike the other lists this is a
// real query, because the node indexes observations by subject and has no "list all"
// endpoint — so the view must show what it is querying for, including when none is set.
func (m model) viewObservations(b *strings.Builder) {
	if m.subject == "" {
		fmt.Fprintf(b, "%s\n", dimStyle.Render("  no subject set — press [s] and type the URL an observation was made about"))
		fmt.Fprintf(b, "%s\n", dimStyle.Render("  (the node indexes observations by subject, not by recency)"))
		return
	}
	fmt.Fprintf(b, "%s\n", dimStyle.Render("  subject: "+m.subject))
	fmt.Fprintf(b, "%s\n\n", dimStyle.Render("  raw claims, not verdicts: this node does not verify the receipts behind them"))
	if m.listErr != nil {
		fmt.Fprintf(b, "%s\n", errStyle.Render("  query failed: "+m.listErr.Error()))
		return
	}
	if len(m.observations) == 0 {
		fmt.Fprintf(b, "%s\n", dimStyle.Render("  no observations for this subject on this node"))
		return
	}
	lo, hi := m.window(len(m.observations))
	for i := lo; i < hi; i++ {
		o := m.observations[i]
		line := fmt.Sprintf("%-18s  %-8s  %-20s  %s",
			orDash(short(o.ReceiptID, 18)), orDash(o.TaskType), orDash(short(o.AgentID, 20)), orDash(short(o.ContentHash, 20)))
		if i == m.cursor {
			fmt.Fprintf(b, "%s\n", titleStyle.Render("  ▸ "+line))
		} else {
			fmt.Fprintf(b, "    %s\n", line)
		}
	}
	m.scrollHint(b, len(m.observations), lo, hi)
}

func (m model) viewConfig(b *strings.Builder) {
	fmt.Fprintf(b, "  %-14s %s\n", labelStyle.Render("node url"), orDash(m.base))
	fmt.Fprintf(b, "  %-14s %s\n", labelStyle.Render("poll"), m.interval.String())
	fmt.Fprintf(b, "  %-14s %s\n", labelStyle.Render("node name"), orDash(m.well.Name))
	fmt.Fprintf(b, "  %-14s %s\n", labelStyle.Render("version"), orDash(m.well.Version))
	fmt.Fprintf(b, "  %-14s %s\n", labelStyle.Render("publicUrl"), orDash(m.well.PublicURL))
	fmt.Fprintf(b, "  %-14s %s\n\n", labelStyle.Render("nodeId"), orDash(m.well.NodeID))
	if m.well.Note != "" {
		fmt.Fprintf(b, "  %s\n", labelStyle.Render("node's own note"))
		fmt.Fprintf(b, "%s\n", dimStyle.Render("    "+m.well.Note))
	}
}

// short truncates s to n runes with an ellipsis, so a long id does not push the rest
// of a row off screen.
func short(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// sparkline renders recent throughput as block characters.
//
// It scales to the window's own peak so a quiet node still shows shape; a flat zero
// history renders as a low line rather than blank, because "nothing is flowing" is
// information.
func sparkline(vals []float64) string {
	const blocks = "▁▂▃▄▅▆▇█"
	if len(vals) == 0 {
		return dimStyle.Render("  (collecting…)")
	}
	max := 0.0
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	var sb strings.Builder
	sb.WriteString("  ")
	for _, v := range vals {
		idx := 0
		if max > 0 {
			idx = int(v / max * float64(len([]rune(blocks))-1))
		}
		if idx < 0 {
			idx = 0
		}
		if idx >= len([]rune(blocks)) {
			idx = len([]rune(blocks)) - 1
		}
		sb.WriteRune([]rune(blocks)[idx])
	}
	return sb.String()
}

// envOr returns the environment value for key, or fallback when it is unset or blank.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return dimStyle.Render("—")
	}
	return s
}
