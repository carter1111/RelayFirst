// Package noderead is a read-only client for a relay node's public HTTP surface.
//
// # Why it is read-only, and why that is a security decision
//
// A node has no authentication (NET-1: there is no official relay, and no session or
// cookie to protect). Any "control" operation would therefore need a whole
// authentication scheme invented for it, which is a new product surface and a new
// risk. So this client only reads endpoints that are already public — the same ones
// any agent may read — and it adds nothing to the node's exposure.
//
// # What it bounds, and why
//
// The node is untrusted (MVP.md §7.1), so a client must not let it do anything but
// return data:
//
//   - Response bodies are capped, so a malicious or broken node cannot stream an
//     unbounded payload into a dashboard's memory.
//   - Nothing is eval'd or treated as an instruction; responses are decoded into
//     fixed structs and displayed.
//   - The transport is whatever the URL names. An https URL is verified normally —
//     certificate checking is never disabled, so a remote dashboard is not trivially
//     MITM'd.
//
// # Why polling, and why a single Client
//
// The node has no node-wide event stream (its hub fans out per agentId), so a
// dashboard learns "it is working" by polling counts and differencing them. Every
// poll costs the node work, so N dashboards must not multiply into a soft denial of
// service: the caller is expected to hold ONE Client and poll through it, and the
// backoff below keeps a failing node from being hammered. This is a client-side
// courtesy; the node's own rate limiting (ARCHITECTURE.md §18) is the eventual
// enforcement and is not implemented yet.
package noderead

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MaxResponseBytes caps a single response body. It is generous for the node's
// documents (the largest is a card listing) and far below anything that would trouble
// the client.
const MaxResponseBytes = 4 << 20 // 4 MiB

// Client reads from one node.
//
// Hold one and reuse it: a new Client per poll would defeat the backoff, which is the
// only thing keeping a failing node from being retried in a tight loop.
type Client struct {
	base string
	http *http.Client

	// backoff grows on consecutive failures and resets on success. It is the reason a
	// flapping or down node is not polled as hard as a healthy one.
	backoff time.Duration
}

// New returns a read-only client for a node URL, e.g. http://localhost:8080.
//
// A URL with a scheme other than http/https is refused: a dashboard given a file://
// or a bare host would otherwise fail obscurely at the first request.
func New(baseURL string) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("noderead: parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("noderead: url must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("noderead: url has no host")
	}
	return &Client{
		base: strings.TrimRight(u.String(), "/"),
		// Default transport: certificate verification is on, redirects are followed,
		// and a per-request timeout bounds a hung node. No custom TLS config, on
		// purpose — InsecureSkipVerify here would silently un-protect a remote node.
		http: &http.Client{Timeout: 8 * time.Second},
	}, nil
}

// BaseURL is the node this client reads.
func (c *Client) BaseURL() string { return c.base }

// Backoff is the current retry delay. It doubles per consecutive failure up to a cap,
// and resets to zero on success.
func (c *Client) Backoff() time.Duration { return c.backoff }

// WellKnown is the node's self-description (GET /.well-known/relayfirst).
//
// Only the fields a dashboard shows are declared; unknown fields are ignored, which is
// what lets a newer node add fields without breaking this client.
type WellKnown struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	PublicURL    string   `json:"publicUrl"`
	Protocol     string   `json:"protocol"`
	Endpoints    []string `json:"endpoints"`
	Messages     int      `json:"messages"`
	Agents       int      `json:"agents"`
	AgentCards   int      `json:"agentCards"`
	Observations int      `json:"observations"`
	Tasks        int      `json:"tasks"`
	Verifies     bool     `json:"verifies"`
	Note         string   `json:"note"`
	NodeID       string   `json:"nodeId"`
}

// Health is the response to GET /healthz.
type Health struct {
	OK bool `json:"ok"`
}

// Card is one entry of GET /agents.
type Card struct {
	AgentID  string          `json:"agentId"`
	Card     json.RawMessage `json:"card"`
	Proof    json.RawMessage `json:"proof"`
	Verified bool            `json:"verified"`
	Note     string          `json:"note"`
}

// Task is one entry of GET /tasks.
type Task struct {
	TaskID    string          `json:"taskId"`
	Requester string          `json:"requester"`
	Subject   string          `json:"subject"`
	ExpiresAt *time.Time      `json:"expiresAt"`
	OfferedAt time.Time       `json:"offeredAt"`
	Claims    int             `json:"claims"`
	Verified  bool            `json:"verified"`
	Note      string          `json:"note"`
	Spec      json.RawMessage `json:"spec"`
}

// Observation is one entry of GET /observations.
type Observation struct {
	ReceiptID   string `json:"receiptId"`
	Subject     string `json:"subject"`
	ContentHash string `json:"contentHash"`
	ResultHash  string `json:"resultHash"`
	AgentID     string `json:"agentId"`
	TaskType    string `json:"taskType"`
	Epoch       uint64 `json:"epoch"`
	Verified    bool   `json:"verified"`
	Note        string `json:"note"`
}

// get performs one request and decodes the body, applying the cap and the backoff
// bookkeeping in one place so no caller can forget either.
func (c *Client) get(path string, into any) error {
	resp, err := c.http.Get(c.base + path)
	if err != nil {
		c.fail()
		return fmt.Errorf("noderead: %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		c.fail()
		return fmt.Errorf("noderead: %s returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(detail)))
	}

	// One byte past the cap is read so an over-limit body is detected rather than
	// silently truncated into a JSON decode error that hides the real cause.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		c.fail()
		return fmt.Errorf("noderead: %s: read: %w", path, err)
	}
	if int64(len(raw)) > MaxResponseBytes {
		c.fail()
		return fmt.Errorf("noderead: %s exceeded %d bytes; refusing to buffer more", path, MaxResponseBytes)
	}

	if err := json.Unmarshal(raw, into); err != nil {
		c.fail()
		return fmt.Errorf("noderead: %s: decode: %w", path, err)
	}
	c.succeed()
	return nil
}

// backoffStep and backoffMax bound the retry delay.
const (
	backoffStep = 1 * time.Second
	backoffMax  = 30 * time.Second
)

func (c *Client) fail() {
	if c.backoff == 0 {
		c.backoff = backoffStep
		return
	}
	c.backoff *= 2
	if c.backoff > backoffMax {
		c.backoff = backoffMax
	}
}

func (c *Client) succeed() { c.backoff = 0 }

// Health fetches GET /healthz.
func (c *Client) Health() (Health, error) {
	var h Health
	err := c.get("/healthz", &h)
	return h, err
}

// WellKnown fetches GET /.well-known/relayfirst.
func (c *Client) WellKnown() (WellKnown, error) {
	var wk WellKnown
	err := c.get("/.well-known/relayfirst", &wk)
	return wk, err
}

// Agents fetches GET /agents.
func (c *Client) Agents(limit int) ([]Card, error) {
	var out struct {
		Count int    `json:"count"`
		Cards []Card `json:"cards"`
	}
	path := "/agents"
	if limit > 0 {
		path = fmt.Sprintf("/agents?limit=%d", limit)
	}
	if err := c.get(path, &out); err != nil {
		return nil, err
	}
	return out.Cards, nil
}

// Tasks fetches GET /tasks, optionally filtered by subject.
func (c *Client) Tasks(subject string, limit int) ([]Task, error) {
	var out struct {
		Count  int    `json:"count"`
		Offers []Task `json:"offers"`
	}
	q := url.Values{}
	if subject != "" {
		q.Set("subject", subject)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprint(limit))
	}
	path := "/tasks"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	if err := c.get(path, &out); err != nil {
		return nil, err
	}
	return out.Offers, nil
}

// Observations fetches GET /observations for a subject.
func (c *Client) Observations(subject string, limit int) ([]Observation, error) {
	if strings.TrimSpace(subject) == "" {
		return nil, errors.New("noderead: observations need a subject; the node indexes by subject, not recency")
	}
	var out struct {
		Subject string        `json:"subject"`
		Count   int           `json:"count"`
		Entries []Observation `json:"entries"`
	}
	q := url.Values{}
	q.Set("subject", subject)
	if limit > 0 {
		q.Set("limit", fmt.Sprint(limit))
	}
	if err := c.get("/observations?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return out.Entries, nil
}
