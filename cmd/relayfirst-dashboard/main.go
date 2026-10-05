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
  --url <url>        Node to observe (default http://localhost:8080)
  --interval <dur>   Poll interval (default 1.5s)
  --version          Print the version

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
	url := "http://localhost:8080"
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
type pollMsg struct {
	health noderead.Health
	well   noderead.WellKnown
	err    error
	at     time.Time
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
	pollErr error
	lastAt2 time.Time

	width int
}

const historyLen = 48

func newModel(client *noderead.Client, interval time.Duration) model {
	m := model{client: client, interval: interval, history: make([]float64, 0, historyLen)}
	if client != nil {
		m.base = client.BaseURL()
	}
	return m
}

// poll fires one poll immediately, then on the interval.
func (m model) poll() tea.Cmd {
	return func() tea.Msg {
		h, err := m.client.Health()
		if err != nil {
			return pollMsg{err: err, at: time.Now()}
		}
		wk, err := m.client.WellKnown()
		return pollMsg{health: h, well: wk, err: err, at: time.Now()}
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
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "r":
			return m, m.poll()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width

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

func (m model) View() string {
	var b strings.Builder

	// Header: node address and liveness.
	dot := okStyle.Render("● live")
	if m.pollErr != nil {
		dot = errStyle.Render("● unreachable")
	}
	fmt.Fprintf(&b, "%s  %s   %s\n\n",
		titleStyle.Render("RELAY dashboard"),
		orDash(m.base),
		dot)

	if m.pollErr != nil {
		fmt.Fprintf(&b, "%s\n\n", errStyle.Render("  "+m.pollErr.Error()))
		fmt.Fprintf(&b, "%s\n", dimStyle.Render("  the node may be down; retrying with backoff. press r to retry now, q to quit"))
		return b.String()
	}

	// Identity block.
	fmt.Fprintf(&b, "  %-14s %s\n", labelStyle.Render("node"), orDash(m.well.Name))
	fmt.Fprintf(&b, "  %-14s %s\n", labelStyle.Render("version"), orDash(m.well.Version))
	fmt.Fprintf(&b, "  %-14s %s\n", labelStyle.Render("protocol"), orDash(m.well.Protocol))
	fmt.Fprintf(&b, "  %-14s %s\n", labelStyle.Render("verifies"),
		errStyle.Render("false")+dimStyle.Render(" (holds no key)"))
	fmt.Fprintf(&b, "\n")

	// Counts.
	fmt.Fprintf(&b, "  %s\n", labelStyle.Render("holds"))
	fmt.Fprintf(&b, "    messages      %d\n", m.well.Messages)
	fmt.Fprintf(&b, "    agents        %d\n", m.well.Agents)
	fmt.Fprintf(&b, "    agent cards   %d\n", m.well.AgentCards)
	fmt.Fprintf(&b, "    observations  %d\n", m.well.Observations)
	fmt.Fprintf(&b, "    tasks         %d\n\n", m.well.Tasks)

	// Throughput, derived from count deltas.
	fmt.Fprintf(&b, "  %s  %s\n", labelStyle.Render("throughput"),
		fmt.Sprintf("%.1f msg/s   peak %.1f", m.throughput, m.peak))
	fmt.Fprintf(&b, "  %s\n", sparkline(m.history))
	fmt.Fprintf(&b, "%s\n", dimStyle.Render("  derived from count deltas, not a message stream (the node has no node-wide stream)"))

	b.WriteString("\n")
	fmt.Fprintf(&b, "%s\n", dimStyle.Render("  [r] refresh  [q] quit"))

	return b.String()
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

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return dimStyle.Render("—")
	}
	return s
}
