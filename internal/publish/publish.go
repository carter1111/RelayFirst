// Package publish delivers store-and-forward messages to relay nodes.
//
// # Why fan-out is the whole point (S5-9)
//
// A single node is a single point of failure and a single point of censorship:
// whoever runs it can drop a receipt. Publishing to more than one node means one
// node being down, unreachable or hostile does not lose the work. That matters
// more here than in most systems, because the receipt is the only proof the work
// happened — losing it loses the points with it.
//
// # Partial success is success
//
// The outcome type separates "some node took it" from "every node took it". A
// caller cares about the first; only an operator cares about the second. Treating
// a partial fan-out as a failure would make a miner retry work that is already
// safely stored on at least one node, which is exactly the behaviour that makes a
// network fragile.
package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/relayfirst/relayfirst/internal/node"
)

// DefaultTimeout bounds one relay request.
//
// Without a bound, one unresponsive node would stall the whole fan-out and the
// miner with it. A relay is a convenience, not a dependency, so a slow one must
// cost a bounded delay and nothing more.
const DefaultTimeout = 10 * time.Second

// Relay is one node to deliver to.
type Relay struct {
	// Name is a label for reporting. Defaults to the URL's host.
	Name string

	// URL is the node's base URL, e.g. "http://localhost:8080". The messages
	// endpoint is appended.
	URL string
}

// endpoint returns the relay's receive URL.
//
// The path is appended rather than configured, so a relay entry cannot silently
// point somewhere unrelated to the node protocol.
func (r Relay) endpoint() string {
	return strings.TrimRight(r.URL, "/") + "/messages"
}

func (r Relay) label() string {
	if strings.TrimSpace(r.Name) != "" {
		return r.Name
	}
	return r.URL
}

// Publisher delivers envelopes to a set of relays.
type Publisher struct {
	// Relays are the delivery targets. At least one is required.
	Relays []Relay

	// Client performs the requests. Nil means a client with DefaultTimeout, so a
	// caller that forgets cannot accidentally create an unbounded one.
	Client *http.Client
}

// New returns a publisher over the given relay URLs.
//
// It exists so callers do not have to remember to set a timeout: the default
// client here is bounded, and an unbounded client is the easy mistake.
func New(urls ...string) (*Publisher, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("publish: at least one relay is required")
	}
	relays := make([]Relay, 0, len(urls))
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		relays = append(relays, Relay{URL: u})
	}
	if len(relays) == 0 {
		return nil, fmt.Errorf("publish: at least one non-empty relay URL is required")
	}
	return &Publisher{
		Relays: relays,
		Client: &http.Client{Timeout: DefaultTimeout},
	}, nil
}

// RelayResult reports one delivery attempt.
type RelayResult struct {
	// Relay is the target's label.
	Relay string

	// OK is true when the node acknowledged the message. Note that a duplicate is
	// still OK: the node confirming it already has the message means the message
	// is delivered.
	OK bool

	// Stored is false when the node reported it already had this id.
	Stored bool

	// Err is set when this relay did not acknowledge.
	Err error
}

// Outcome aggregates a fan-out.
type Outcome struct {
	// Results are per-relay outcomes, in the order the relays were configured so
	// a report is stable across runs.
	Results []RelayResult

	// Acked and Failed are the counts of acknowledged and unacknowledged relays.
	Acked  int
	Failed int
}

// OK reports whether at least one relay acknowledged.
//
// One acknowledgement is enough to have delivered the message. Requiring all
// would turn every node into a dependency and make the network no more reliable
// than its least available member.
func (o Outcome) OK() bool { return o.Acked > 0 }

// Error summarises the failures, or nil when every relay acknowledged.
func (o Outcome) Error() error {
	if o.Failed == 0 {
		return nil
	}
	parts := make([]string, 0, o.Failed)
	for _, r := range o.Results {
		if r.Err != nil {
			parts = append(parts, fmt.Sprintf("%s: %v", r.Relay, r.Err))
		}
	}
	return fmt.Errorf("publish: %d of %d relay(s) failed: %s",
		o.Failed, len(o.Results), strings.Join(parts, "; "))
}

// Publish delivers env to every relay.
//
// It returns an error only for a problem with the call itself (no relays, no
// client). Per-relay failures are reported inside the Outcome, because a partial
// delivery is a normal result rather than a programming error — and a caller that
// received an error here would be tempted to treat a successful one-relay
// delivery as a failure.
func (p *Publisher) Publish(ctx context.Context, env node.Envelope) (Outcome, error) {
	if p == nil || len(p.Relays) == 0 {
		return Outcome{}, fmt.Errorf("publish: no relays configured")
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}

	out := Outcome{Results: make([]RelayResult, 0, len(p.Relays))}

	// Fan out concurrently so the worst-case wait is bounded by the slowest relay rather than by
	// the sum over all of them.
	//
	// # Why this matters rather than being an optimisation
	//
	// Each delivery has its own timeout. Delivered sequentially, N relays means a worst case of
	// N timeouts — so a single unresponsive node adds its full timeout to every publication, and
	// the miner pays that delay on every iteration. Concurrently the bound is one timeout,
	// whatever N is.
	//
	// # Why results are collected by index
	//
	// Writing into a pre-sized slice by position keeps the reporting order equal to the configured
	// relay order, so a report is stable across runs. Appending from goroutines would both race
	// and produce an arrival-order report, which is useless for comparing two runs.
	results := make([]RelayResult, len(p.Relays))

	var wg sync.WaitGroup
	for i := range p.Relays {
		wg.Add(1)
		go func(i int) {
			// Done is deferred so a panic inside deliver cannot leave Wait hanging.
			defer wg.Done()
			results[i] = p.deliver(ctx, client, p.Relays[i], env)
		}(i)
	}
	wg.Wait()

	for _, res := range results {
		out.Results = append(out.Results, res)
		if res.OK {
			out.Acked++
		} else {
			out.Failed++
		}
	}

	return out, nil
}

// deliver posts one envelope to one relay.
//
// Every failure mode is folded into RelayResult rather than returned, so one bad
// relay cannot stop the loop from trying the next.
func (p *Publisher) deliver(ctx context.Context, client *http.Client, relay Relay, env node.Envelope) RelayResult {
	res := RelayResult{Relay: relay.label()}

	body, err := json.Marshal(env)
	if err != nil {
		res.Err = fmt.Errorf("encode envelope: %w", err)
		return res
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, relay.endpoint(), bytes.NewReader(body))
	if err != nil {
		res.Err = fmt.Errorf("build request: %w", err)
		return res
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		// The transport error can name the URL, which is not a secret, but the
		// wrap keeps the label in the message so an operator can tell which node
		// is down.
		res.Err = fmt.Errorf("request failed: %w", err)
		return res
	}
	defer resp.Body.Close()

	// The body is read (bounded) rather than discarded so the node's own error
	// message can be surfaced. An operator debugging a rejected delivery needs
	// the node's reason, not just a status code.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))

	if resp.StatusCode != http.StatusOK {
		res.Err = fmt.Errorf("relay returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		return res
	}

	var ack struct {
		OK     bool `json:"ok"`
		Stored bool `json:"stored"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		res.Err = fmt.Errorf("decode acknowledgement: %w", err)
		return res
	}
	if !ack.OK {
		res.Err = fmt.Errorf("relay did not acknowledge")
		return res
	}

	res.OK = true
	res.Stored = ack.Stored
	return res
}
