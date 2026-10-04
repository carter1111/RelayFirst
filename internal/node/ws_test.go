package node_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/relayfirst/relayfirst/internal/node"
)

// These tests cover S9-12, the WebSocket binding.
//
// The properties that matter are not "does a frame arrive" — that is mechanical — but:
//
//   - the HTTP endpoints are PRESERVED, because MVP.md §12.1 says the socket is an
//     addition and not a replacement;
//   - the frame says its ordering is local, because ARCHITECTURE.md §4.2 says there is
//     no global order and a client that assumed otherwise would build a fork;
//   - a slow subscriber cannot stall the publisher, because the publisher is an
//     unrelated sender's HTTP request;
//   - and a duplicate delivery is not re-broadcast, or a retry would look like new
//     activity to every subscriber.

// postEnvelopeTo posts an envelope to /messages. It is a thin alias for the existing
// helper so these tests read the same as the rest of the package.
func postEnvelopeTo(t *testing.T, srvURL string, env node.Envelope) *http.Response {
	t.Helper()
	return postEnvelope(t, srvURL, env)
}

// subscribe opens a WebSocket to the node's stream for an agent and returns the conn
// plus the acknowledgement.
func subscribe(t *testing.T, srvURL, agentID string) (*websocket.Conn, node.WSSubscribedFrame) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := strings.Replace(srvURL, "http://", "ws://", 1) + node.WSEndpoint(agentID)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read ack: %v", err)
	}
	var ack node.WSSubscribedFrame
	if err := json.Unmarshal(raw, &ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	return conn, ack
}

// TestWS_SubscriberReceivesPublishedMessage is the basic push path.
func TestWS_SubscriberReceivesPublishedMessage(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	agentID := "agent:eip155:8453:0x00000000000000000000000000000000000000aa"

	conn, ack := subscribe(t, srv.URL, agentID)
	if ack.Type != "subscribed" {
		t.Fatalf("first frame must be the acknowledgement, got %q", ack.Type)
	}
	if ack.AgentID != agentID {
		t.Errorf("ack agentId = %q, want %q", ack.AgentID, agentID)
	}
	// The ack must tell the client to pull once, or the gap between "stored" and
	// "subscribed" would be silent.
	if !strings.Contains(ack.Note, "pull") {
		t.Errorf("the ack must tell the client to pull once to close the gap, got: %q", ack.Note)
	}

	// Publish a message for that agent.
	env := node.Envelope{
		ID:      "evt-ws-1",
		AgentID: agentID,
		Kind:    node.KindEvent,
		Payload: []byte(`{"hello":"world"}`),
	}
	resp := postEnvelopeTo(t, srv.URL, env)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish returned %d", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var frame node.WSFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if frame.Type != "message" {
		t.Fatalf("frame type = %q, want message", frame.Type)
	}
	if frame.Message == nil || frame.Message.ID != "evt-ws-1" {
		t.Fatalf("the published message must be delivered, got %+v", frame.Message)
	}
	// The payload must arrive byte-identical: a socket that re-serialized would break
	// a signed receipt or event just as a store that re-serialized would.
	if string(frame.Message.Payload) != `{"hello":"world"}` {
		t.Errorf("payload must be delivered verbatim, got %q", frame.Message.Payload)
	}
}

// TestWS_FrameSaysItsOrderIsLocal is the honesty requirement from MVP.md §12.1.
//
// A WebSocket does NOT provide a global order: ARCHITECTURE.md §4.2 says relay sequence
// is local and the authoritative task order comes from the signed per-actor sequence and
// hash chain. A client that read delivery order as protocol order would derive a
// different task state than another node, which is the fork the protocol avoids. So the
// frame says so, on every frame, rather than relying on a reader finding this file.
func TestWS_FrameSaysItsOrderIsLocal(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	agentID := "agent:eip155:8453:0x00000000000000000000000000000000000000ab"

	conn, _ := subscribe(t, srv.URL, agentID)

	env := node.Envelope{
		ID:      "evt-ws-2",
		AgentID: agentID,
		Kind:    node.KindEvent,
		Payload: []byte(`{}`),
	}
	postEnvelopeTo(t, srv.URL, env)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var frame node.WSFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("decode frame: %v", err)
	}

	if frame.DeliverySeq != 1 {
		t.Errorf("deliverySeq must start at 1, got %d", frame.DeliverySeq)
	}
	if !strings.Contains(frame.Note, "local counter") {
		t.Errorf("the frame must say its counter is local, got: %q", frame.Note)
	}
	if !strings.Contains(frame.Note, "not a protocol sequence") {
		t.Errorf("the frame must say it is not a protocol sequence, got: %q", frame.Note)
	}
}

// TestWS_DeliverySeqIncrements lets a client detect a gap in what it received, which is
// exactly what the bounded buffer can cause.
func TestWS_DeliverySeqIncrements(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	agentID := "agent:eip155:8453:0x00000000000000000000000000000000000000ac"

	conn, _ := subscribe(t, srv.URL, agentID)

	for i := 1; i <= 3; i++ {
		postEnvelopeTo(t, srv.URL, node.Envelope{
			ID:      "evt-seq-" + string(rune('0'+i)),
			AgentID: agentID,
			Kind:    node.KindEvent,
			Payload: []byte(`{}`),
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for want := uint64(1); want <= 3; want++ {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read frame %d: %v", want, err)
		}
		var frame node.WSFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			t.Fatalf("decode frame: %v", err)
		}
		if frame.DeliverySeq != want {
			t.Errorf("frame %d has deliverySeq %d, want %d", want, frame.DeliverySeq, want)
		}
	}
}

// TestWS_DuplicateIsNotRebroadcast is the retry property.
//
// A duplicate delivery is normal — a sender whose acknowledgement was lost will retry.
// Re-broadcasting it would make every subscriber see new activity that is not new, and a
// subscriber acting on it would do the work twice.
func TestWS_DuplicateIsNotRebroadcast(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	agentID := "agent:eip155:8453:0x00000000000000000000000000000000000000ad"

	conn, _ := subscribe(t, srv.URL, agentID)

	env := node.Envelope{
		ID:      "evt-dup-1",
		AgentID: agentID,
		Kind:    node.KindEvent,
		Payload: []byte(`{}`),
	}
	// Send twice.
	postEnvelopeTo(t, srv.URL, env)
	postEnvelopeTo(t, srv.URL, env)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// The first delivery must arrive.
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("the first delivery must arrive: %v", err)
	}
	// The duplicate must NOT produce a second frame. A short deadline is the assertion:
	// no frame within it means nothing was re-broadcast.
	shortCtx, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	if _, raw, err := conn.Read(shortCtx); err == nil {
		t.Errorf("a duplicate delivery must not be re-broadcast, but a frame arrived: %s", raw)
	}
}

// TestWS_OnlySubscribersOfThatAgentAreNotified keeps the fan-out scoped.
func TestWS_OnlySubscribersOfThatAgentAreNotified(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	watching := "agent:eip155:8453:0x00000000000000000000000000000000000000ae"
	other := "agent:eip155:8453:0x00000000000000000000000000000000000000af"

	conn, _ := subscribe(t, srv.URL, watching)

	// Publish for a DIFFERENT agent.
	postEnvelopeTo(t, srv.URL, node.Envelope{
		ID:      "evt-other-1",
		AgentID: other,
		Kind:    node.KindEvent,
		Payload: []byte(`{}`),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, raw, err := conn.Read(ctx); err == nil {
		t.Errorf("a subscriber must not receive another agent's traffic, got: %s", raw)
	}
}

// TestHTTPEndpointsSurviveWebSocket is the MVP.md §12.1 requirement: the socket is
// ADDED, and every existing HTTP endpoint keeps working.
func TestHTTPEndpointsSurviveWebSocket(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()
	agentID := "agent:eip155:8453:0x00000000000000000000000000000000000000b0"

	// POST /messages and GET /messages/{agentId} still work.
	postEnvelopeTo(t, srv.URL, node.Envelope{
		ID:      "evt-http-1",
		AgentID: agentID,
		Kind:    node.KindEvent,
		Payload: []byte(`{}`),
	})
	pull := get(t, client, srv.URL+"/messages/"+agentID)
	if pull.StatusCode != http.StatusOK {
		t.Errorf("GET /messages must still work, got %d", pull.StatusCode)
	}

	// /healthz and /.well-known/relayfirst still work.
	if resp := get(t, client, srv.URL+"/healthz"); resp.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz must still work, got %d", resp.StatusCode)
	}
	wk := get(t, client, srv.URL+"/.well-known/relayfirst")
	if wk.StatusCode != http.StatusOK {
		t.Errorf("GET /.well-known/relayfirst must still work, got %d", wk.StatusCode)
	}
	var doc struct {
		Endpoints []string `json:"endpoints"`
	}
	decode(t, wk, &doc)
	joined := strings.Join(doc.Endpoints, " ")
	if !strings.Contains(joined, "/ws/messages/") {
		t.Errorf("the well-known document must advertise the new binding, got: %v", doc.Endpoints)
	}
	// And the four original endpoints must still be advertised.
	for _, want := range []string{"POST /messages", "GET /messages/{agentId}", "GET /agents"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the original endpoint %q must still be advertised, got: %v", want, doc.Endpoints)
		}
	}
}

// TestWS_UnknownAgentCanStillSubscribe documents that the node does not gate the
// subscription on existing traffic. A node is a public relay, and a subscriber may
// legitimately connect before anything has been published.
func TestWS_UnknownAgentCanStillSubscribe(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	conn, ack := subscribe(t, srv.URL, "agent:eip155:8453:0x00000000000000000000000000000000000000b1")
	if ack.Type != "subscribed" {
		t.Fatalf("subscribing to an agent with no messages must succeed, got %q", ack.Type)
	}
	_ = conn
}
