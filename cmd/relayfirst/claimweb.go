package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/relayfirst/relayfirst/internal/scoring"
	"github.com/relayfirst/relayfirst/internal/store"
)

// claimWeb is a local, read-only view of a claim, reached by a one-time ticket.
//
// # Why a ticket, not a wallet
//
// The key lives in the local CLI, not in a browser wallet, so a "connect wallet" button
// would be a lie about where the signing happens. The flow is instead: the CLI mints a
// short-lived ticket and prints a URL; the page reads the claim data the local ledger
// already holds and shows it. Nothing is signed here, and the page never sees a key.
//
// # The three boundaries
//
//   - Local only: it binds 127.0.0.1, never 0.0.0.0, so it is not reachable off the box.
//   - Read only: every route is a GET that reads the ledger; there is no endpoint that
//     writes, signs, or spends.
//   - Short-lived: the ticket expires after ttl, and the server is meant to be stopped
//     when done. Both are deliberate -- a ticket is a capability, and a capability should
//     not outlive the conversation that created it.
type claimWeb struct {
	db      *store.DB
	token   string
	expires time.Time
}

// newClaimWeb makes a claim view with a fresh random ticket.
//
// The ticket is 16 bytes from crypto/rand: it is the only thing standing between "any
// local process" and this view, so it must not be guessable, and it must not be a counter
// or a timestamp.
func newClaimWeb(db *store.DB, ttl time.Duration) (*claimWeb, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("claim web: mint ticket: %w", err)
	}
	return &claimWeb{
		db:      db,
		token:   hex.EncodeToString(raw[:]),
		expires: time.Now().Add(ttl),
	}, nil
}

// token returns the ticket value, for building the URL the CLI prints.
func (w *claimWeb) tokenValue() string { return w.token }

// authorized reports whether the request carries the live ticket.
//
// It compares in constant time: the ticket is a secret and a value comparison that exits
// early leaks its length and prefix to a process that can time the response.
func (w *claimWeb) authorized(r *http.Request) bool {
	if time.Now().After(w.expires) {
		return false
	}
	got := r.URL.Query().Get("t")
	return subtleConstantTimeEqual(got, w.token)
}

// handler serves the read-only view.
func (w *claimWeb) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", w.serveClaim)
	return mux
}

// serveClaim renders the claim for the requested agent and epoch.
func (w *claimWeb) serveClaim(rw http.ResponseWriter, r *http.Request) {
	if !w.authorized(r) {
		http.Error(rw, "this link has expired. Run `relayfirst claim --web` again for a new one.", http.StatusForbidden)
		return
	}

	epoch := scoring.EpochOf(time.Now())
	if raw := r.URL.Query().Get("epoch"); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &epoch); err != nil {
			http.Error(rw, "epoch must be a number", http.StatusBadRequest)
			return
		}
	}

	agent := r.URL.Query().Get("agent")
	if agent == "" {
		var err error
		agent, err = soleClaimAgent(w.db)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
	}

	data, err := claimProof(w.db, agent, epoch)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusNotFound)
		return
	}

	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	// No inline scripts, so nothing here can fetch or exfiltrate; the page is static data.
	rw.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	_ = claimPage.Execute(rw, data)
}

// soleClaimAgent returns the only agent with a claimable balance, or an error if there are
// zero or several. It is the same "do not guess" rule the CLI uses.
func soleClaimAgent(db *store.DB) (string, error) {
	cumulative, err := store.NewPointsSpend(db).CumulativeUsableMicro(scoring.EpochOf(time.Now()))
	if err != nil {
		return "", err
	}
	if len(cumulative) == 0 {
		return "", errors.New("no claimable points in this store yet")
	}
	if len(cumulative) > 1 {
		return "", errors.New("several agents have points; add ?agent=0x... to the URL")
	}
	for a := range cumulative {
		return a, nil
	}
	return "", errors.New("unreachable")
}

// subtleConstantTimeEqual compares two strings without an early exit.
func subtleConstantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// runClaimWeb starts the local read-only claim view and blocks until interrupted.
func runClaimWeb(db *store.DB, ttl time.Duration) error {
	w, err := newClaimWeb(db, ttl)
	if err != nil {
		return err
	}

	// Bind an ephemeral port on loopback only.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("claim web: listen: %w", err)
	}

	url := fmt.Sprintf("http://%s/?t=%s", ln.Addr().String(), w.tokenValue())
	fmt.Fprintf(os.Stderr,
		"\n  Claim view (read-only, this machine only):\n\n    %s\n\n"+
			"  The link expires in %s. Press Ctrl-C to stop.\n"+
			"  This page cannot sign or spend; it only shows what your ledger already holds.\n\n",
		url, ttl)

	srv := &http.Server{Handler: w.handler(), ReadHeaderTimeout: 5 * time.Second}
	return srv.Serve(ln)
}

// claimPage is the read-only view. It is plain HTML with inline style and no script, so
// there is nothing to execute and nothing to fetch.
var claimPage = template.Must(template.New("claim").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>RelayFirst claim</title>
<style>
  body{font:14px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;background:#0b0d10;color:#e6e6e6;margin:2rem;max-width:44rem}
  h1{font-size:1.1rem;color:#7fd1ff} .k{color:#8b949e} .v{color:#e6e6e6}
  code{color:#c9d1d9;word-break:break-all} .n{color:#8b949e;margin-top:1.5rem}
  .big{font-size:1.6rem;color:#7ee787}
</style></head><body>
<h1>RelayFirst — claim proof</h1>
<p>This is a <strong>proof</strong>, not a claim. Producing it needs no key, and this page
cannot sign or spend. Submitting it on-chain is a later, wallet-signed step.</p>
<p class="k">agent</p><p><code class="v">{{.agentId}}</code></p>
<p class="k">claimable through epoch {{.epoch}}</p><p class="big">{{.total}}</p>
<p class="k">total (micro)</p><p><code>{{.totalMicro}}</code></p>
<p class="k">balance root</p><p><code>{{.root}}</code></p>
<p class="k">leaf</p><p><code>{{.leaf}}</code></p>
<p class="k">index</p><p><code>{{.index}}</code></p>
<p class="k">proof</p><p><code>{{range .proof}}{{.}}<br>{{end}}</code></p>
<p class="n">{{.note}}</p>
</body></html>`))
