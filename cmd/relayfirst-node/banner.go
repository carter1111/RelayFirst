package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

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

// runReport is `relayfirst-node report`: a one-shot summary, no port bound.
func runReport(args []string) error {
	cfg, err := parseFlags(args)
	if errors.Is(err, errStop) {
		return nil
	}
	if err != nil {
		return err
	}
	db, err := sqlite.Open(cfg.storage)
	if err != nil {
		return err
	}
	defer db.Close()
	printReport(cfg, snapshot(db))
	return nil
}

// runStatus is `relayfirst-node status`: check a RUNNING node over HTTP.
//
// # Why it is separate from report
//
// `report` reads a database file; `status` asks a live node. They answer different
// questions -- "what is stored" versus "is it up and what does it say about itself" --
// and folding them into one command would make the answer depend on whether a node
// happened to be running. The well-known document is fetched rather than the raw
// counts so the output is exactly what a client sees.
func runStatus(args []string) error {
	url := "http://localhost:8080"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--url":
			if i+1 >= len(args) {
				return fmt.Errorf("--url needs a value")
			}
			url = args[i+1]
			i++
		case "-h", "--help":
			fmt.Print(usage)
			return nil
		default:
			return fmt.Errorf("status: unknown flag %q", args[i])
		}
	}
	url = strings.TrimRight(url, "/")

	client := &http.Client{Timeout: 5 * time.Second}
	health, err := httpGet(client, url+"/healthz")
	if err != nil {
		return fmt.Errorf("node %s is not reachable: %w", url, err)
	}
	wk, err := httpGet(client, url+"/.well-known/relayfirst")
	if err != nil {
		return fmt.Errorf("node %s answered /healthz but not /.well-known: %w", url, err)
	}

	colourOn := isTTY(os.Stdout)
	bold := func(s string) string { return paint(s, colBold, colourOn) }
	fmt.Printf("%s  %s\n", bold("health"), strings.TrimSpace(string(health)))
	fmt.Printf("%s\n", bold("well-known"))
	// Re-indent the node's own JSON so it reads as a document rather than a blob; the
	// bytes are the node's, only whitespace is added.
	var pretty bytes.Buffer
	if json.Indent(&pretty, wk, "  ", "  ") == nil {
		fmt.Printf("  %s\n", pretty.String())
	} else {
		fmt.Printf("  %s\n", strings.TrimSpace(string(wk)))
	}
	return nil
}

func httpGet(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("returned %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// runInspect is `relayfirst-node inspect <what>`: list what the store holds, offline.
func runInspect(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("inspect needs a target: agents, tasks, observations, messages or db")
	}
	what := args[0]

	cfg := config{storage: "./relayfirst-node.db"}
	limit := 20
	subject := ""
	agent := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--storage":
			if i+1 >= len(args) {
				return fmt.Errorf("--storage needs a value")
			}
			cfg.storage = args[i+1]
			i++
		case "--limit":
			if i+1 >= len(args) {
				return fmt.Errorf("--limit needs a value")
			}
			if _, err := fmt.Sscanf(args[i+1], "%d", &limit); err != nil {
				return fmt.Errorf("--limit must be a number")
			}
			i++
		case "--subject":
			if i+1 >= len(args) {
				return fmt.Errorf("--subject needs a value")
			}
			subject = args[i+1]
			i++
		case "--agent":
			if i+1 >= len(args) {
				return fmt.Errorf("--agent needs a value")
			}
			agent = args[i+1]
			i++
		case "-h", "--help":
			fmt.Print(usage)
			return nil
		default:
			return fmt.Errorf("inspect: unknown flag %q", args[i])
		}
	}

	db, err := sqlite.Open(cfg.storage)
	if err != nil {
		return err
	}
	defer db.Close()

	switch what {
	case "agents":
		return inspectAgents(db, limit)
	case "tasks":
		return inspectTasks(db, limit)
	case "observations":
		return inspectObservations(db, subject, limit)
	case "messages":
		if agent == "" {
			return fmt.Errorf("inspect messages requires --agent <agentId>")
		}
		return inspectMessages(db, agent, limit)
	case "db":
		return inspectDB(cfg.storage, db)
	default:
		return fmt.Errorf("unknown inspect target %q (want agents, tasks, observations, messages or db)", what)
	}
}

// printJSON writes one indented JSON value. Every inspect target emits JSON rather
// than a table for the same reason the node's HTTP endpoints do: the output stays
// pipeline-able, and no ANSI needs stripping.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func inspectAgents(db *sqlite.DB, limit int) error {
	cards, err := sqlite.NewCardStore(db).List(limit)
	if err != nil {
		return err
	}
	type row struct {
		AgentID   string `json:"agentId"`
		UpdatedAt string `json:"updatedAt"`
		CardBytes int    `json:"cardBytes"`
		ProofLen  int    `json:"proofBytes"`
	}
	rows := make([]row, 0, len(cards))
	for _, c := range cards {
		rows = append(rows, row{c.AgentID, c.UpdatedAt.UTC().Format(time.RFC3339), len(c.Card), len(c.Proof)})
	}
	// The note mirrors GET /agents, so a reader of this command and of the HTTP
	// endpoint sees the same caveat: a directory is not an authority.
	return printJSON(map[string]any{
		"count": len(rows), "cards": rows,
		"note": "a directory, not an authority: this node does not verify proofs or endorse the agents listed",
	})
}

func inspectTasks(db *sqlite.DB, limit int) error {
	offers, err := sqlite.NewTaskStore(db).List("", limit)
	if err != nil {
		return err
	}
	type row struct {
		TaskID    string `json:"taskId"`
		Requester string `json:"requester"`
		Subject   string `json:"subject"`
		Claims    int    `json:"claims"`
		OfferedAt string `json:"offeredAt"`
		ExpiresAt string `json:"expiresAt,omitempty"`
	}
	rows := make([]row, 0, len(offers))
	for _, o := range offers {
		r := row{o.TaskID, o.Requester, o.Subject, o.Claims, o.OfferedAt.UTC().Format(time.RFC3339), ""}
		if o.ExpiresAt != nil {
			r.ExpiresAt = o.ExpiresAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, r)
	}
	return printJSON(map[string]any{
		"count": len(rows), "offers": rows,
		"note": "a noticeboard, not an authority: no exclusivity and no expiry enforcement; verify the requester yourself",
	})
}

func inspectObservations(db *sqlite.DB, subject string, limit int) error {
	if subject == "" {
		return fmt.Errorf("inspect observations requires --subject <url> (the node indexes by subject, not by recency)")
	}
	obs, err := sqlite.NewObservationStore(db).BySubject(subject, limit)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{
		"subject": subject, "count": len(obs), "observations": obs,
		"note": "raw claims, not verdicts: this node does not verify the receipts behind them",
	})
}

func inspectMessages(db *sqlite.DB, agent string, limit int) error {
	msgs, err := sqlite.NewMessageStore(db).ByAgent(agent, limit)
	if err != nil {
		return err
	}
	type row struct {
		ID         string `json:"id"`
		Kind       string `json:"kind"`
		Bytes      int    `json:"payloadBytes"`
		ReceivedAt string `json:"receivedAt"`
	}
	rows := make([]row, 0, len(msgs))
	for _, m := range msgs {
		rows = append(rows, row{m.ID, m.Kind, len(m.Payload), m.ReceivedAt.UTC().Format(time.RFC3339)})
	}
	// The payload is NOT printed: it is opaque and can be large. The command reports
	// that it exists and how big it is, which is what an operator needs to see.
	return printJSON(map[string]any{
		"agentId": agent, "count": len(rows), "messages": rows,
		"note": "payloads are opaque and deliberately not printed; use GET /messages/{agentId} for the bytes",
	})
}

func inspectDB(path string, db *sqlite.DB) error {
	var journal string
	_ = db.Handle().QueryRow("PRAGMA journal_mode").Scan(&journal)
	var size int64
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}
	return printJSON(map[string]any{
		"storage": path, "sizeBytes": size, "journalMode": journal,
		"note": "size is the main database file; a write-ahead log beside it may hold recent writes",
	})
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
