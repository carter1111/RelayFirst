package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// The WebSocket binding (S9-12, MVP.md §12.1).
//
// # What this adds, and what it deliberately does not
//
// It adds a push path: a client subscribes to an agent's stream and receives messages
// as they arrive, instead of polling GET /messages/{agentId}. The HTTP endpoints are all
// kept, because the plan requires that (§12.1) — the WebSocket is an additional
// AgentInterface, not a replacement.
//
// # What a WebSocket does NOT provide, stated because the misreading is easy
//
// It does not give a global order. ARCHITECTURE.md §4.2: `relaySequence` is local to a
// node, and the authoritative ordering of a task comes from per-actor `sequence` plus a
// per-actor hash chain, derived by the client. A client that treated arrival order over
// this socket as the truth would have exactly the state-machine fork the protocol is
// built to avoid.
//
// So this layer promises: "these bytes reached you, in this order, from this node." It
// promises nothing about how that order relates to what another node delivered. The
// frame carries the node's own delivery counter precisely so a client can SEE that the
// number is local rather than mistake it for a protocol sequence.
//
// # Why the node still cannot verify anything
//
// Frames carry the same opaque payloads the store holds. Nothing here parses a receipt
// or an event, and that is enforced by the import graph (MVP.md §7.1).

// WSFrame is what a subscriber receives.
//
// It is deliberately a distinct shape from protocol.Envelope rather than a re-wrapped
// one. An envelope is a protocol object; a frame is a delivery notice, and conflating
// them is how a delivery counter would end up looking like a protocol field.
type WSFrame struct {
	// Type is "message" for a delivery and "subscribed" for the acknowledgement.
	Type string `json:"type"`

	// Message is the delivered envelope, present when Type is "message".
	Message *Envelope `json:"message,omitempty"`

	// DeliverySeq is this node's own counter for this subscription, starting at 1.
	//
	// # Why it exists even though it is NOT a protocol sequence
	//
	// A client needs to detect a gap in what it received — a dropped frame is exactly
	// what the bounded buffer can cause. A local counter is enough for that, and naming
	// it `deliverySeq` rather than `sequence` keeps it from being confused with the
	// signed per-actor sequence that actually orders a task. A client that saw this
	// number and assumed protocol ordering would build a fork.
	DeliverySeq uint64 `json:"deliverySeq"`

	// Note explains the local-only semantics on every frame the client might rely on.
	// It is repeated rather than documented once because a client library author reads
	// the frame, not this file.
	Note string `json:"note,omitempty"`
}

// WSSubscribedFrame is the acknowledgement sent on connect.
//
// # Why an acknowledgement exists
//
// A subscriber that started listening only after a message was delivered would miss it
// silently. The ack states what the subscription covers and tells the client to pull
// once to close the gap, which is the honest contract for a lossy push channel.
type WSSubscribedFrame struct {
	Type    string `json:"type"`
	AgentID string `json:"agentId"`
	Note    string `json:"note"`
}

// hub delivers stored messages to subscribers.
//
// # Why a hub rather than per-connection polling
//
// The point of the binding is that a client is told when something arrives, so the
// delivery has to originate where the message is stored. A polling loop per connection
// would be the HTTP endpoint with extra steps, and would not become push when a real
// broker arrives.
type hub struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

// subscriber is one connected consumer.
type subscriber struct {
	agentID string
	ch      chan Envelope
	// dropped records that this subscriber overflowed, so the connection can be closed
	// with a reason that tells the client to reconnect and pull rather than silently
	// believing it is current.
	dropped bool
}

func newHub() *hub {
	return &hub{subs: map[*subscriber]struct{}{}}
}

func (h *hub) add(s *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs[s] = struct{}{}
}

func (h *hub) remove(s *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, s)
}

// publish delivers env to every subscriber watching its agent.
//
// # Why it never blocks
//
// publish is called from the HTTP handler that just stored a message. Blocking there
// would let one slow subscriber stall an unrelated sender's request, which turns a
// subscriber's problem into the node's. So a full buffer marks the subscriber dropped
// and the connection is closed; the client's recovery is to reconnect and pull, which is
// the path that already exists and is already tested.
func (h *hub) publish(env Envelope) {
	h.mu.Lock()
	var overflowed []*subscriber
	for s := range h.subs {
		if s.agentID != env.AgentID {
			continue
		}
		select {
		case s.ch <- env:
		default:
			s.dropped = true
			overflowed = append(overflowed, s)
		}
	}
	h.mu.Unlock()

	// Close outside the lock: closing a channel while holding the hub lock would
	// deadlock with the reader that is trying to remove itself.
	for _, s := range overflowed {
		h.remove(s)
		close(s.ch)
	}
}

// handleSubscribe is the WebSocket endpoint.
func (n *Node) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	agentID := strings.TrimSpace(r.PathValue("agentId"))
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agentId is required")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// No origin restriction: a relay node is a public endpoint that any agent may
		// subscribe to, and a browser check would be security theatre here because the
		// node has no session or cookie to protect. Access control is not this layer's
		// job — a receipt is verified by its signature, not by who fetched it.
		InsecureSkipVerify: true,
	})
	if err != nil {
		// Accept already wrote a response.
		n.cfg.Logger.Debug("websocket accept failed", "error", err.Error())
		return
	}
	defer conn.CloseNow()

	sub := &subscriber{agentID: agentID, ch: make(chan Envelope, hubFanoutBuffer)}
	n.hub.add(sub)
	defer n.hub.remove(sub)

	// Read-only socket: the client sends nothing. CloseRead makes the library treat an
	// incoming data frame as a protocol error rather than buffering it forever, which is
	// what lets a half-open peer be detected.
	ctx := conn.CloseRead(r.Context())

	// The acknowledgement tells the client the subscription is live and that it should
	// pull once to catch anything stored before it connected. Without that, the gap
	// between "stored" and "subscribed" would be silent.
	if err := writeFrame(ctx, conn, WSSubscribedFrame{
		Type:    "subscribed",
		AgentID: agentID,
		Note: "pull GET /messages/{agentId} once to close the gap before this subscription; " +
			"frames are delivered in this node's local order, which is NOT the protocol's task order",
	}); err != nil {
		return
	}

	var seq uint64
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-sub.ch:
			if !ok {
				// The hub dropped this subscriber for falling behind. Say so, so the
				// client reconnects and pulls rather than assuming it is current.
				_ = conn.Close(websocket.StatusPolicyViolation,
					"subscriber fell behind and was dropped; reconnect and pull to catch up")
				return
			}
			seq++
			frame := WSFrame{
				Type:        "message",
				Message:     &env,
				DeliverySeq: seq,
				Note: "deliverySeq is this node's local counter, not a protocol sequence; " +
					"task order comes from the signed per-actor sequence and hash chain",
			}
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := writeFrame(writeCtx, conn, frame)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// writeFrame marshals and sends one JSON frame.
func writeFrame(ctx context.Context, conn *websocket.Conn, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("node: marshal frame: %w", err)
	}
	return conn.Write(ctx, websocket.MessageText, raw)
}

// WSEndpoint is the WebSocket path for an agent's stream.
//
// It is exported so the agent card can advertise exactly this path and the two cannot
// drift.
func WSEndpoint(agentID string) string {
	return "/ws/messages/" + agentID
}

// WSPathPattern is the route pattern, for the same reason.
const WSPathPattern = "GET /ws/messages/{agentId}"

// wsSubprotocol is the protocol name offered in the agent card.
const wsSubprotocol = "relayfirst.stream.v1"

// Subprotocol returns the binding identifier advertised for the WebSocket interface.
func Subprotocol() string { return wsSubprotocol }

// errNoHub is returned when the hub was not initialized, which is a programming error
// rather than a runtime condition.
var errNoHub = errors.New("node: websocket hub is not initialized")

// checkHub guards the subscribe path so a Node built without New fails loudly instead of
// panicking on a nil map.
func (n *Node) checkHub() error {
	if n.hub == nil {
		return errNoHub
	}
	return nil
}
