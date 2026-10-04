package publish_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/a2a"
	"github.com/relayfirst/relayfirst/internal/eip712"
)

// This is the S9-4/S9-5/S9-6 integration.
//
// The unit tests in internal/a2a derive states from events built in memory. This
// runs the same lifecycle through a REAL node over HTTP, which is where the
// composition can fail even when every piece is correct:
//
//   - the node must carry events it cannot verify or interpret,
//   - the bytes must survive storage unchanged, or the chain hashes break,
//   - and the client must derive the same state from what it pulled as it would
//     have from what it sent.
//
// The last point is the one that matters most. A node that re-serialized an event
// would break its hash chain silently: the event would still parse, the state
// would still derive, and the chain would fail to validate — which is exactly the
// failure mode the whole "store bytes, not structures" rule exists to prevent.

// keccakHasher is the real hasher for the chain, injected from the crypto layer.
func keccakHasher(b []byte) []byte { return eip712.Keccak256(b) }

// lifecycleEvents builds a signed-able two-actor task history.
//
// The signatures are real EIP-712-free stand-ins: this test is about transport and
// derivation, and the chain uses the real keccak256. Signature verification for
// events belongs with the signing layer and is not what this exercises.
func lifecycleEvents(t *testing.T, sessionID string) []a2a.Event {
	t.Helper()

	base := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

	// Actor A: create, offer, then wait for approval after B starts.
	aTypes := []a2a.EventType{
		a2a.EventTaskCreated,
		a2a.EventTaskOffered,
	}
	// Actor B: accept, start, ask for approval, then complete.
	bTypes := []a2a.EventType{
		a2a.EventTaskAccepted,
		a2a.EventTaskStarted,
		a2a.EventTaskWaitingForApprov,
		a2a.EventTaskApproved,
		a2a.EventTaskCompleted,
	}

	build := func(actor string, types []a2a.EventType, startOffset time.Duration) []a2a.Event {
		var (
			out      []a2a.Event
			prevHash string
		)
		for i, typ := range types {
			e := a2a.Event{
				EventID:           actor[len(actor)-4:] + "-" + string(rune('a'+i)),
				SessionID:         sessionID,
				TaskID:            "tsk_e2e",
				Actor:             actor,
				Type:              typ,
				Sequence:          uint64(i + 1),
				PreviousEventHash: prevHash,
				IssuedAt:          base.Add(startOffset + time.Duration(i)*time.Second),
			}
			h, err := a2a.EventHash(keccakHasher, e)
			if err != nil {
				t.Fatalf("hash: %v", err)
			}
			prevHash = h
			out = append(out, e)
		}
		return out
	}

	// A speaks first; B's events interleave after A's offer.
	aEvents := build(e2eAgentID(t), aTypes, 0)
	bEvents := build(otherAgentID(t), bTypes, 2*time.Second)

	return append(aEvents, bEvents...)
}

// otherAgentID derives a second, distinct identity for the executor side.
func otherAgentID(t *testing.T) string {
	t.Helper()
	const keyB = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	priv, err := eip712.PrivateKeyFromHex(keyB)
	if err != nil {
		t.Fatalf("private key: %v", err)
	}
	addr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	return "agent:eip155:8453:" + eip712.AddressToHex(addr)
}

// TestEventFlow_FullLifecycleThroughANode is the acceptance path for S9-6.
func TestEventFlow_FullLifecycleThroughANode(t *testing.T) {
	srv := newE2ENode(t)
	sessionID := "ses_e2e_1"
	events := lifecycleEvents(t, sessionID)

	// 1. Derive locally, as the sender would.
	want, err := a2a.Derive(a2a.DeriveInput{Events: events, Now: time.Date(2026, 10, 4, 12, 0, 30, 0, time.UTC)})
	if err != nil {
		t.Fatalf("local Derive: %v", err)
	}
	if want.State != a2a.StateCompleted {
		t.Fatalf("the fixture must complete the task, got %s", want.State)
	}

	// 2. Publish every event to the node as an opaque envelope.
	for _, e := range events {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		env := map[string]any{
			"id":      e.EventID,
			"agentId": e.Actor,
			"kind":    "event",
			"payload": raw,
		}
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		resp, err := srv.Client().Post(srv.URL+"/messages", "application/json", jsonBody(body))
		if err != nil {
			t.Fatalf("POST /messages: %v", err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("publishing %s returned %d", e.EventID, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// 3. Pull one actor's events back and check the bytes survived.
	pulled := pullEvents(t, srv.URL, otherAgentID(t))
	if len(pulled) != 5 {
		t.Fatalf("pulled %d events for the executor, want 5", len(pulled))
	}

	// 4. The pulled bytes must still form a valid chain. This is the assertion
	//    that catches a node re-serializing: the state would still derive, but the
	//    hashes would no longer link.
	all := append(pullEvents(t, srv.URL, e2eAgentID(t)), pulled...)
	if err := a2a.ValidateChain(keccakHasher, all); err != nil {
		t.Fatalf("the chain must survive a round trip through the node: %v", err)
	}

	// 5. And the derived state must match, regardless of pull order.
	got, err := a2a.Derive(a2a.DeriveInput{
		Events: all,
		Now:    time.Date(2026, 10, 4, 12, 0, 30, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Derive from pulled events: %v", err)
	}
	if got.State != want.State {
		t.Errorf("state derived from pulled events = %s, want %s; "+
			"a node that changed the bytes would change the answer", got.State, want.State)
	}
}

// TestEventFlow_NodeCarriesEventsItCannotVerify is the boundary assertion.
//
// The node has no hasher and no keys, so it cannot check a chain or a signature. It
// must still carry the event. This publishes an event whose chain link is
// deliberately wrong: a node that validated would reject it, and a node that
// carried it makes no claim either way. The client is the one that notices.
func TestEventFlow_NodeCarriesEventsItCannotVerify(t *testing.T) {
	srv := newE2ENode(t)

	broken := a2a.Event{
		EventID:           "evt-broken",
		SessionID:         "ses_x",
		TaskID:            "tsk_x",
		Actor:             e2eAgentID(t),
		Type:              a2a.EventTaskCreated,
		Sequence:          1,
		PreviousEventHash: "0x" + "ff", // sequence 1 must have no predecessor
		IssuedAt:          time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(broken)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	env := map[string]any{
		"id":      broken.EventID,
		"agentId": broken.Actor,
		"kind":    "event",
		"payload": raw,
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	resp, err := srv.Client().Post(srv.URL+"/messages", "application/json", jsonBody(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("a node must carry an event it cannot verify; got %d", resp.StatusCode)
	}

	// The client, which does have a hasher, must reject it.
	pulled := pullEvents(t, srv.URL, e2eAgentID(t))
	if len(pulled) != 1 {
		t.Fatalf("pulled %d events, want 1", len(pulled))
	}
	if err := a2a.ValidateChain(keccakHasher, pulled); err == nil {
		t.Fatal("the client must detect the broken chain the node carried; " +
			"if neither side checks, nothing does")
	}
}

// TestEventFlow_DuplicateDeliveryIsHarmless confirms the retry property holds for
// events as it does for receipts.
func TestEventFlow_DuplicateDeliveryIsHarmless(t *testing.T) {
	srv := newE2ENode(t)
	events := lifecycleEvents(t, "ses_dup")

	for _, e := range events {
		raw, _ := json.Marshal(e)
		body, _ := json.Marshal(map[string]any{
			"id": e.EventID, "agentId": e.Actor, "kind": "event", "payload": raw,
		})
		// Send twice.
		for i := 0; i < 2; i++ {
			resp, err := srv.Client().Post(srv.URL+"/messages", "application/json", jsonBody(body))
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			if resp.StatusCode != 200 {
				t.Fatalf("a duplicate delivery must be acknowledged, got %d", resp.StatusCode)
			}
			resp.Body.Close()
		}
	}

	// Only one copy of each must be stored.
	pulled := pullEvents(t, srv.URL, e2eAgentID(t))
	if len(pulled) != 2 {
		t.Errorf("actor A has %d events, want 2 (a duplicate must not be stored twice)", len(pulled))
	}
}

// TestEventFlow_SessionAndTaskDeriveIndependently checks the two state machines do
// not interfere when their events share one stream.
//
// The session open is part of actor A's own chain, so it is built as sequence 1 and
// the rest of A's events are re-chained after it. Re-sequencing by hand is exactly
// the kind of mistake this test would otherwise hide, so the chain is rebuilt with
// the same helper the other events use.
func TestEventFlow_SessionAndTaskDeriveIndependently(t *testing.T) {
	sessionID := "ses_mixed"
	base := time.Date(2026, 10, 4, 11, 59, 0, 0, time.UTC)

	actorA := e2eAgentID(t)
	actorB := otherAgentID(t)

	// A's own chain: open, create, offer.
	aTypes := []a2a.EventType{a2a.EventSessionOpen, a2a.EventTaskCreated, a2a.EventTaskOffered}
	bTypes := []a2a.EventType{
		a2a.EventTaskAccepted, a2a.EventTaskStarted,
		a2a.EventTaskWaitingForApprov, a2a.EventTaskApproved, a2a.EventTaskCompleted,
	}

	build := func(actor string, types []a2a.EventType, startOffset time.Duration) []a2a.Event {
		var (
			out      []a2a.Event
			prevHash string
		)
		for i, typ := range types {
			e := a2a.Event{
				EventID:           actor[len(actor)-4:] + "-" + string(rune('a'+i)),
				SessionID:         sessionID,
				Actor:             actor,
				Type:              typ,
				Sequence:          uint64(i + 1),
				PreviousEventHash: prevHash,
				IssuedAt:          base.Add(startOffset + time.Duration(i)*time.Second),
			}
			if taskScoped(typ) {
				e.TaskID = "tsk_mixed"
			}
			h, err := a2a.EventHash(keccakHasher, e)
			if err != nil {
				t.Fatalf("hash: %v", err)
			}
			prevHash = h
			out = append(out, e)
		}
		return out
	}

	all := append(build(actorA, aTypes, 0), build(actorB, bTypes, 2*time.Second)...)

	now := base.Add(30 * time.Second)
	sres, err := a2a.DeriveSession(all, now)
	if err != nil {
		t.Fatalf("DeriveSession: %v", err)
	}
	if sres.State != a2a.SessionOpenState {
		t.Errorf("session state = %s, want %s", sres.State, a2a.SessionOpenState)
	}

	tres, err := a2a.Derive(a2a.DeriveInput{Events: all, Now: now})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if tres.State != a2a.StateCompleted {
		t.Errorf("task state = %s, want %s", tres.State, a2a.StateCompleted)
	}
}

// taskScoped mirrors the package's scoping rule for building fixtures.
func taskScoped(t a2a.EventType) bool {
	return t != a2a.EventSessionOpen && t != a2a.EventSessionClose
}

// --- helpers -------------------------------------------------------------

// jsonBody wraps bytes as a request body with the JSON content type.
func jsonBody(b []byte) io.Reader { return strings.NewReader(string(b)) }

// pullEvents fetches one agent's stored events and decodes them.
func pullEvents(t *testing.T, baseURL, agentID string) []a2a.Event {
	t.Helper()
	resp, err := http.Get(baseURL + "/messages/" + agentID)
	if err != nil {
		t.Fatalf("GET messages: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET messages returned %d", resp.StatusCode)
	}
	var out struct {
		Messages []struct {
			// Payload is []byte so encoding/json handles the base64 the envelope
			// uses on the wire. Reading it as RawMessage would yield the base64
			// token, not the event JSON.
			Kind    string `json:"kind"`
			Payload []byte `json:"payload"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	var events []a2a.Event
	for _, m := range out.Messages {
		if m.Kind != "event" {
			continue
		}
		// DecodeEvent binds the received bytes, so the chain covers what the node
		// actually served — including any field this build does not know.
		decoded, err := a2a.DecodeEvent(m.Payload)
		if err != nil {
			t.Fatalf("decode event: %v", err)
		}
		events = append(events, decoded)
	}
	return events
}
