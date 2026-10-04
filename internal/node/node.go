// Package node implements the thin, self-hostable relay node (MVP.md §7.1).
//
// # What this is, and what it deliberately is not
//
// The node stores and forwards. It does not verify signatures, does not read a
// chain, and does not judge anything it carries. That is not an unfinished corner
// — it is the design. MVP.md §7.3 says the node is not the moat; its whole job is
// to make "I can run my own node" true, so a stranger can host one without
// operating a database cluster or trusting the node with anything that matters.
//
// Verification happens on the client (MVP.md §5.4), which is why a node cannot
// forge work: it never gets to decide what is valid. The worst a hostile node can
// do is drop or withhold messages, which is exactly why the publisher in
// internal/publish fans out to more than one node.
//
// # Why the payload is opaque
//
// A receipt's signature covers an exact byte sequence. A node that parsed and
// re-serialized a receipt would silently invalidate it — the receipt would still
// look fine and would fail verification somewhere else, later, which is the worst
// possible failure mode. So the body is stored and returned verbatim, and the
// indexing fields the node needs travel in the envelope instead.
//
// # The one exception, and why it is not one (S10-1)
//
// The observation index reads a few JSON paths out of a receipt payload to make the
// work findable. That is parsing, and it is the opposite of re-serializing: the bytes
// stored and served are untouched, and the index is a separate table. MVP.md §7.3
// authorises exactly this — indexing and querying do not break security, because
// whoever retrieves the data can re-verify it.
//
// The distinction the code keeps: parse to INDEX, never to VERIFY. This package cannot
// verify even if someone tried, because it cannot reach the signing code (see below),
// and a forged receipt is indexed like any other and discarded by the consumer's
// signature check.
//
// # Why this package cannot verify, structurally
//
// The wire format lives in internal/protocol, and this package imports that — not
// the receipt or eip712 packages. So "the node cannot check a signature" is a
// property of the import graph, checked by `go list -deps`, rather than something
// a reviewer has to keep noticing. An earlier draft kept the receipt-wrapping
// helpers here, which transitively linked secp256k1; the helpers moved to
// internal/publish so the dependency direction is enforceable.
package node

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/protocol"
	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// Envelope is the shared wire format (internal/protocol).
//
// It is aliased rather than re-declared so callers can read and construct it
// naturally, while the definition — and its dependency direction — live in one
// place.
type Envelope = protocol.Envelope

// KindReceipt is the envelope kind used for a mined receipt.
const KindReceipt = protocol.KindReceipt

// KindEvent is the envelope kind used for a signed protocol event (S9-6).
const KindEvent = protocol.KindEvent

// MaxPayloadBytes caps a single accepted message.
const MaxPayloadBytes = protocol.MaxPayloadBytes

// Config configures a node.
type Config struct {
	// Store is the durable message store. Required.
	Store *sqlite.MessageStore

	// Cards is the published agent-card directory (S9-3). Required: a node
	// serves cards as part of the A2A surface, and a nil directory would make
	// the /agents endpoints silently useless rather than absent.
	Cards *sqlite.CardStore

	// Observations is the receipt index (S10-1). Required for the same reason: a nil
	// index would make the query endpoints silently empty rather than absent, and an
	// empty result reads as "nothing exists" instead of "this node cannot answer".
	Observations *sqlite.ObservationStore

	// Tasks is the task board (S10-3). Required for the same reason.
	Tasks *sqlite.TaskStore

	// PublicURL is the address clients should reach this node at, used in the
	// well-known document. It is distinct from the listen address because a node
	// behind a proxy hears on localhost but is reached publicly.
	PublicURL string

	// Version is reported in the well-known document.
	Version string

	// MaxPayloadBytes overrides the default cap.
	MaxPayloadBytes int64

	// Logger receives structured events. Nil discards.
	Logger *slog.Logger

	// Now is injected for deterministic tests. Nil means time.Now.
	Now func() time.Time
}

// Node is the HTTP handler set for a thin node.
type Node struct {
	cfg Config

	// hub fans stored messages out to connected subscribers (S9-12).
	//
	// # Why the hub is transport state and not protocol state
	//
	// ARCHITECTURE.md §4.4 is explicit that ephemeral signals — presence, typing,
	// heartbeat — are transport-layer, not protocol objects, and are not signed. A
	// subscription is the same kind of thing: it answers "who wants to be told, right
	// now", which is a property of this process rather than of the protocol.
	//
	// Consequence worth stating: it is empty on restart, and it is per-node. A client
	// that needed a durable subscription or a cross-node one would still pull
	// (GET /messages/{agentId}), which is why that endpoint is kept.
	hub *hub
}

// hubFanoutBuffer is how many pending messages one subscriber may accumulate before
// it is considered too slow.
//
// A subscriber that cannot keep up must not be able to consume unbounded memory on a
// public endpoint, and it must not block the publisher either. So the buffer is
// bounded and a subscriber that overflows is dropped rather than stalling the node —
// the client reconnects and pulls, which is the recovery the pull endpoint exists for.
const hubFanoutBuffer = 64

// New returns a node, validating cfg.
func New(cfg Config) (*Node, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("node: a message store is required")
	}
	if cfg.Cards == nil {
		return nil, fmt.Errorf("node: a card store is required")
	}
	if cfg.Observations == nil {
		return nil, fmt.Errorf("node: an observation store is required")
	}
	if cfg.Tasks == nil {
		return nil, fmt.Errorf("node: a task store is required")
	}
	if cfg.MaxPayloadBytes <= 0 {
		cfg.MaxPayloadBytes = MaxPayloadBytes
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Node{cfg: cfg, hub: newHub()}, nil
}

// Handler returns the node's routes.
func (n *Node) Handler() http.Handler {
	mux := http.NewServeMux()

	// POST /messages receives a message (S5-1, S5-4).
	mux.HandleFunc("POST /messages", n.handlePost)

	// GET /messages/{agentId} pulls an agent's messages (S5-2).
	mux.HandleFunc("GET /messages/{agentId}", n.handlePull)

	// GET /healthz is the liveness probe a container orchestrator expects.
	mux.HandleFunc("GET /healthz", n.handleHealth)

	// GET /.well-known/relayfirst advertises the node (S5-5).
	mux.HandleFunc("GET /.well-known/relayfirst", n.handleWellKnown)

	// Agent Card directory (S9-3). The node stores and serves cards; it does not
	// verify their proofs, which is why every response says so (see cards.go).
	mux.HandleFunc("POST /agents", n.handlePublishCard)
	mux.HandleFunc("GET /agents", n.handleListCards)
	mux.HandleFunc("GET /agents/{agentId}", n.handleGetCard)

	// WebSocket push binding (S9-12, MVP.md §12.1). This is ADDED alongside the four
	// HTTP endpoints, not a replacement for them: a client that needs durability or a
	// cross-node view still pulls. See ws.go for what a socket does and does not promise.
	mux.HandleFunc(WSPathPattern, n.handleSubscribe)

	// Observation index and cross-verification (S10-1, S10-2). The node indexes what
	// receipts claim without verifying them; see observations.go for why that is not a
	// hole. Both responses say so, because a bare list invites a reader to assume the
	// node filtered it.
	mux.HandleFunc("GET /observations", n.handleObservations)
	mux.HandleFunc("GET /observations/{id}/evidence", n.handleEvidence)

	// Task board (S10-3). A noticeboard, not an authority: see tasks.go for what the node
	// refuses to decide, and why a claim is recorded as interest rather than as a grant.
	mux.HandleFunc("POST /tasks", n.handleOfferTask)
	mux.HandleFunc("GET /tasks", n.handleListTasks)
	mux.HandleFunc("POST /tasks/{id}/claim", n.handleClaimTask)

	return mux
}

// postResponse is the acknowledgement returned to a sender (S5-4).
//
// stored distinguishes "accepted and filed" from "already had it". Both are
// successes — a sender retrying after a timeout must not see an error — but an
// operator debugging a delivery wants to tell them apart.
type postResponse struct {
	OK     bool   `json:"ok"`
	ID     string `json:"id"`
	Stored bool   `json:"stored"`
	Note   string `json:"note,omitempty"`
}

func (n *Node) handlePost(w http.ResponseWriter, r *http.Request) {
	// Cap the read before decoding: a public endpoint must not accept an
	// unbounded body into memory.
	body := http.MaxBytesReader(w, r.Body, n.cfg.MaxPayloadBytes)
	defer body.Close()

	raw, err := io.ReadAll(body)
	if err != nil {
		// MaxBytesReader returns an error for an oversized body, so this covers
		// both a read failure and exceeding the cap.
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("payload exceeds the %d byte limit", n.cfg.MaxPayloadBytes))
		return
	}

	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		writeError(w, http.StatusBadRequest, "body must be a JSON envelope")
		return
	}

	if err := n.validate(env); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	stored, err := n.cfg.Store.Put(sqlite.Message{
		ID:         env.ID,
		AgentID:    env.AgentID,
		Kind:       env.Kind,
		Payload:    env.Payload,
		ReceivedAt: n.cfg.Now(),
	})
	if err != nil {
		n.cfg.Logger.Error("store message", "id", env.ID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "could not store the message")
		return
	}

	// Fan out to live subscribers (S9-12). Only on a genuinely new message: a duplicate
	// was already delivered when it first arrived, and re-sending it would make a retry
	// look like new activity to every subscriber.
	if stored {
		n.hub.publish(env)
		// Index a receipt for the observation queries. Best-effort: a receipt the index
		// cannot read is still delivered, because delivery is the node's obligation and
		// indexing is additive.
		n.indexReceiptEnvelope(env)
	}

	// A duplicate is an acknowledgement, not a conflict: the sender's job is done
	// either way, and answering non-2xx would make a retry loop retry forever.
	resp := postResponse{OK: true, ID: env.ID, Stored: stored}
	if !stored {
		resp.Note = "already stored; a duplicate delivery is not an error"
	}
	writeJSON(w, http.StatusOK, resp)
}

// validate checks the envelope's shape without interpreting its payload.
func (n *Node) validate(env Envelope) error {
	if strings.TrimSpace(env.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if strings.TrimSpace(env.AgentID) == "" {
		return fmt.Errorf("agentId is required")
	}
	if strings.TrimSpace(env.Kind) == "" {
		return fmt.Errorf("kind is required")
	}
	if len(env.Payload) == 0 {
		return fmt.Errorf("payload is required")
	}
	// The node does not validate the payload's contents — that is the client's
	// job (MVP.md §7.1). It only refuses an id that could not belong to a
	// receipt, so a caller gets an early, clear error instead of discovering the
	// mistake at verification time. Note this is a shape check, not a claim that
	// the receipt is authentic.
	if env.Kind == KindReceipt && !protocol.LooksLikeReceiptID(env.ID) {
		return fmt.Errorf("a %s message needs a 32-byte hex id", KindReceipt)
	}
	// An event id is chosen by its actor rather than derived, so only its shape is
	// checked. The node still does not look inside (S9-6).
	if env.Kind == KindEvent && !protocol.LooksLikeEventID(env.ID) {
		return fmt.Errorf("an %s message needs a non-empty id of at most 256 bytes", KindEvent)
	}
	return nil
}

// pullResponse is the body of a successful pull.
type pullResponse struct {
	AgentID  string     `json:"agentId"`
	Count    int        `json:"count"`
	Messages []Envelope `json:"messages"`
}

func (n *Node) handlePull(w http.ResponseWriter, r *http.Request) {
	agentID := strings.TrimSpace(r.PathValue("agentId"))
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agentId is required")
		return
	}

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		limit = v
	}

	msgs, err := n.cfg.Store.ByAgent(agentID, limit)
	if err != nil {
		n.cfg.Logger.Error("pull messages", "agent", agentID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "could not read messages")
		return
	}

	out := pullResponse{AgentID: agentID, Count: len(msgs), Messages: make([]Envelope, 0, len(msgs))}
	for _, m := range msgs {
		out.Messages = append(out.Messages, Envelope{
			ID:      m.ID,
			AgentID: m.AgentID,
			Kind:    m.Kind,
			Payload: m.Payload,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (n *Node) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// wellKnown is the node's self-description (S5-5), the minimal RFN-04 form.
//
// It exists so a client can discover how to talk to a node without being told out
// of band, and so an operator can confirm the node is alive and serving. The
// fields are the minimum a publisher needs to address it.
type wellKnown struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	PublicURL    string   `json:"publicUrl,omitempty"`
	Protocol     string   `json:"protocol"`
	Endpoints    []string `json:"endpoints"`
	MessageKind  []string `json:"messageKinds"`
	Messages     int      `json:"messages"`
	Agents       int      `json:"agents"`
	AgentCards   int      `json:"agentCards"`
	Observations int      `json:"observations"`
	Tasks        int      `json:"tasks"`

	// Verifies states plainly that this node does not check signatures. A client
	// must not assume a node's acceptance means a receipt is valid, and the
	// cheapest way to prevent that assumption is to say so in the document it
	// fetches first.
	Verifies bool   `json:"verifies"`
	Note     string `json:"note"`
}

func (n *Node) handleWellKnown(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, wellKnown{
		Name:      "relayfirst-node",
		Version:   n.cfg.Version,
		PublicURL: n.cfg.PublicURL,
		Protocol:  "relayfirst.store-and-forward.v1",
		Endpoints: []string{
			"POST /messages", "GET /messages/{agentId}",
			"POST /agents", "GET /agents", "GET /agents/{agentId}",
			// The WebSocket binding is advertised here so a client discovers it from the
			// same document it already fetches (MVP.md §12.1: a second AgentInterface,
			// not a replacement).
			"GET /ws/messages/{agentId}",
			"GET /observations", "GET /observations/{id}/evidence",
			"POST /tasks", "GET /tasks", "POST /tasks/{id}/claim",
		},
		MessageKind:  []string{KindReceipt, KindEvent},
		Messages:     n.cfg.Store.Count(),
		Agents:       n.cfg.Store.AgentCount(),
		AgentCards:   n.cfg.Cards.Count(),
		Observations: n.cfg.Observations.Count(),
		Tasks:        n.cfg.Tasks.Count(),
		Verifies:     false,
		Note: "store-and-forward only: this node does not verify signatures and does not read any chain; " +
			"validate receipts on the client (MVP.md §5.4)",
	})
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": msg})
}
