// Command relayfirst-node runs a thin, self-hostable relay node (MVP.md §7).
//
// It is a separate binary from the `relayfirst` miner CLI on purpose. A node and a
// miner are different roles: a node is a long-lived server that strangers may
// depend on, while the miner is a local tool. Shipping them as one binary would
// mean a stranger's `docker run` pulls in the mining code, the LLM providers and
// their credential handling for no reason.
//
// # The node is dumb, and that is the design
//
// It stores and forwards. It does not verify signatures, does not read a chain,
// and does not decide anything about what it carries (MVP.md §7.1, §7.3). Any
// invariant about validity is enforced on the client, so a hostile node cannot
// forge work — the worst it can do is withhold, which is why publishers fan out.
//
// Usage:
//
//	relayfirst-node --listen :8080 --storage ./relayfirst-node.db
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/relayfirst/relayfirst/internal/node"
	"github.com/relayfirst/relayfirst/internal/sqlite"
)

const version = "0.1.0-s5"

const usage = `relayfirst-node — thin store-and-forward relay node

Usage:
  relayfirst-node [flags]

Flags:
  --listen <addr>        Address to listen on (default :8080)
  --storage <path>       SQLite database file (default ./relayfirst-node.db)
  --public-url <url>     URL clients should reach this node at, reported in
                         /.well-known/relayfirst
  --max-payload <bytes>  Largest accepted message (default 1048576)
  --version              Print the version

This node does not verify signatures and does not read any chain. It stores what
it is given and hands it back on request. Receipts are validated on the client
(MVP.md §5.4), which is what stops a node from being able to forge work.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := parseFlags(args)
	// Help was printed; that is a successful exit, not an error.
	if errors.Is(err, errStop) {
		return nil
	}
	if err != nil {
		return err
	}

	if cfg.version {
		fmt.Println(version)
		return nil
	}

	db, err := sqlite.Open(cfg.storage)
	if err != nil {
		return err
	}
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	n, err := node.New(node.Config{
		Store:           sqlite.NewMessageStore(db),
		PublicURL:       cfg.publicURL,
		Version:         version,
		MaxPayloadBytes: cfg.maxPayload,
		Logger:          logger,
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.listen,
		Handler:           n.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// A write timeout bounds a slow reader, so one client cannot hold a
		// connection indefinitely against a public endpoint.
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Report the storage path rather than the full public URL, which may contain
	// a hostname but never a secret. No credential is ever logged.
	logger.Info("relayfirst-node starting",
		"version", version,
		"listen", cfg.listen,
		"storage", cfg.storage,
		"verify", false)

	// Shut down cleanly on interrupt so SQLite closes its write-ahead log rather
	// than leaving it for the next start to recover.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("listen on %s: %w", cfg.listen, err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}

type config struct {
	listen     string
	storage    string
	publicURL  string
	maxPayload int64
	version    bool
}

// parseFlags is a tiny flag parser, matching the miner CLI's style.
//
// The standard flag package would be fine here, but keeping both binaries on one
// parsing convention means an operator learns one set of rules.
func parseFlags(args []string) (config, error) {
	cfg := config{
		listen:     ":8080",
		storage:    "./relayfirst-node.db",
		maxPayload: node.MaxPayloadBytes,
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		name := strings.TrimPrefix(arg, "--")
		value := ""

		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name, value = name[:eq], name[eq+1:]
		} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			value = args[i+1]
			i++
		}

		switch name {
		case "help", "-h":
			// Help short-circuits before any other flag is interpreted, so a typo
			// in an unrelated flag cannot stop someone from reading the usage.
			fmt.Print(usage)
			return cfg, errStop
		case "listen":
			cfg.listen = value
		case "storage":
			cfg.storage = value
		case "public-url":
			cfg.publicURL = value
		case "max-payload":
			var n int64
			if _, err := fmt.Sscanf(value, "%d", &n); err != nil || n <= 0 {
				return cfg, fmt.Errorf("invalid --max-payload %q", value)
			}
			cfg.maxPayload = n
		case "version", "-v":
			cfg.version = true
		default:
			return cfg, fmt.Errorf("unknown flag %q", arg)
		}
	}
	return cfg, nil
}

// errStop signals that help was printed and nothing more should run.
var errStop = errors.New("stop")
