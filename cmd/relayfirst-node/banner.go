package main

import (
	"fmt"
	"io"
	"os"

	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// This file holds the node's human-facing output: the startup banner and the
// `report` summary.
//
// # The constraint that shapes everything here
//
// A node runs under `docker -d`, systemd and CI, where stderr is NOT a terminal.
// Decoration that assumed a TTY would corrupt `docker logs` and every pipeline,
// and a node that refused to start without a terminal would be broken.
//
// So the split is by TTY, not by a flag:
//
//   - interactive (stderr is a terminal): the logo, in colour, for a human.
//   - non-interactive (piped, `docker logs`, CI): no logo, no colour escapes, the
//     single structured line the operator and their tooling already parse.
//
// The non-interactive path is byte-for-byte what the node printed before this file
// existed, which is what keeps log scraping and health checks working.

// logo is the wordmark shown when stderr is a terminal.
//
// ANSI Shadow block letters for "RELAY". It is about 40 columns wide, so it fits a
// standard terminal without wrapping.
const logo = `██████╗ ███████╗██╗      █████╗ ██╗   ██╗
██╔══██╗██╔════╝██║     ██╔══██╗╚██╗ ██╔╝
██████╔╝█████╗  ██║     ███████║ ╚████╔╝
██╔══██╗██╔══╝  ██║     ██╔══██║  ╚██╔╝
██║  ██║███████╗███████╗██║  ██║   ██║
╚═╝  ╚═╝╚══════╝╚══════╝╚═╝  ╚═╝   ╚═╝`

// ANSI escapes. Emitted only when the writer is a terminal, so a piped log never
// contains them.
const (
	colReset = "\033[0m"
	colBold  = "\033[1m"
	colDim   = "\033[2m"
	colCyan  = "\033[36m"
)

// isTTY reports whether f is an interactive terminal.
//
// It uses os.FileMode and nothing else, so no dependency is added for this. The
// check is ModeCharDevice: a pipe or a redirected file is not a character device.
func isTTY(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// counts is the node's store summary, shared by the banner and `report`.
type counts struct {
	messages     int
	agents       int
	cards        int
	observations int
	tasks        int
}

func snapshot(db *sqlite.DB) counts {
	return counts{
		messages:     sqlite.NewMessageStore(db).Count(),
		agents:       sqlite.NewMessageStore(db).AgentCount(),
		cards:        sqlite.NewCardStore(db).Count(),
		observations: sqlite.NewObservationStore(db).Count(),
		tasks:        sqlite.NewTaskStore(db).Count(),
	}
}

// paint wraps s in an escape when colour is on.
//
// Every coloured write goes through here, so there is one place that decides whether
// escapes are emitted, and no path can leak them into a piped log.
func paint(s, colour string, colourOn bool) string {
	if !colourOn {
		return s
	}
	return colour + s + colReset
}

// printBanner writes the human-facing banner.
//
// colourOn is the caller's decision, derived from the stream being written (see
// printReport and the server startup path). Passing it in rather than deciding here
// is what keeps a report and a log line able to differ: they write to different
// streams and each must be judged on its own.
func printBanner(w io.Writer, cfg config, c counts, colourOn bool) {
	cyan := func(s string) string { return paint(s, colCyan, colourOn) }
	dim := func(s string) string { return paint(s, colDim, colourOn) }

	fmt.Fprintln(w)
	fmt.Fprintln(w, cyan(logo))
	fmt.Fprintf(w, "  %s\n", dim("store-and-forward relay · permissionless · multi-relay"))
	fmt.Fprintf(w, "  %s\n\n", dim("no key · cannot forge · validate on the client (MVP.md §5.4)"))

	fmt.Fprintf(w, "  version  %s\n", version)
	fmt.Fprintf(w, "  listen   %s\n", cfg.listen)
	fmt.Fprintf(w, "  storage  %s\n", cfg.storage)
	if cfg.publicURL != "" {
		fmt.Fprintf(w, "  public   %s\n", cfg.publicURL)
	}
	fmt.Fprintf(w, "  verify   %s\n\n", cyan("false")+" "+dim("(this node holds no key)"))
}

// reportEndpoints lists what this node serves, for `report`.
//
// The list duplicates internal/node's well-known document on purpose: this is the
// operator-facing view and should stay readable. The well-known document remains the
// machine-readable source.
func reportEndpoints(w io.Writer, colourOn bool) {
	dim := func(s string) string { return paint(s, colDim, colourOn) }
	bold := func(s string) string { return paint(s, colBold, colourOn) }
	fmt.Fprintf(w, "  %s\n", bold("serves"))
	for _, e := range []string{
		"POST /messages · GET /messages/{agentId} · GET /ws/messages/{agentId}",
		"POST /agents · GET /agents · GET /agents/{agentId}",
		"GET /observations · GET /observations/{id}/evidence",
		"POST /tasks · GET /tasks · POST /tasks/{id}/claim",
		"GET /.well-known/relayfirst · GET /healthz",
	} {
		fmt.Fprintf(w, "    %s\n", dim(e))
	}
}

// printReport writes the `report` summary: the banner plus live counts.
//
// It writes to stdout, because the report IS the command's output, and it decides
// colour from that same stream. Deciding from stderr while writing stdout was wrong:
// `relayfirst-node report > file` sends the deliverable to a file while stderr stays
// a terminal, and the file would then contain ANSI escapes.
//
// It is a one-shot view of a database without starting the server, so an operator can
// inspect what a node holds (and confirm the database opens) without occupying the port.
func printReport(cfg config, c counts) {
	w := os.Stdout
	colourOn := isTTY(os.Stdout)
	bold := func(s string) string { return paint(s, colBold, colourOn) }
	printBanner(w, cfg, c, colourOn)

	fmt.Fprintf(w, "  %s\n", bold("holds"))
	fmt.Fprintf(w, "    messages      %d\n", c.messages)
	fmt.Fprintf(w, "    agents        %d\n", c.agents)
	fmt.Fprintf(w, "    agent cards   %d\n", c.cards)
	fmt.Fprintf(w, "    observations  %d\n", c.observations)
	fmt.Fprintf(w, "    tasks         %d\n\n", c.tasks)

	reportEndpoints(w, colourOn)
}
