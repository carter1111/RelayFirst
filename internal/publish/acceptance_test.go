package publish_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	a2asdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/coder/websocket"

	"github.com/relayfirst/relayfirst/internal/a2a"
	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/eip712"
	"github.com/relayfirst/relayfirst/internal/node"
	"github.com/relayfirst/relayfirst/internal/publish"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// This is S9-11: the end-to-end acceptance test for criterion ⑧.
//
//	"Two independent agents can: publish an Agent Card → open a session → assign a
//	 task → execute → collect a receipt → verify. And the wire is compatible with the
//	 official A2A SDK."
//
// # Why this is one long test rather than several
//
// Criterion ⑧ is a statement about a SEQUENCE. Each step can pass in isolation while the
// handoff between two of them is wrong — a card whose endpoint the session cannot use, a
// task whose events the receipt cannot reference, a receipt whose verification mode the
// verifier refuses. So this test runs the whole path and asserts the joins, which is where
// the composition failures live.
//
// # The two agents are genuinely separate
//
// Requester and executor have different keys, different identities and different event
// chains. A single-agent test would not exercise attribution, the self-verification rules,
// or the fact that one actor's chain says nothing about another's.

// Two independent identities for the two sides of the exchange.
const (
	requesterKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	executorKey  = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
)

func identityFor(t *testing.T, key string) string {
	t.Helper()
	priv, err := eip712.PrivateKeyFromHex(key)
	if err != nil {
		t.Fatalf("private key: %v", err)
	}
	addr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	id, err := agentid.Format(e2eChainID, eip712.AddressToHex(addr))
	if err != nil {
		t.Fatalf("format id: %v", err)
	}
	return id
}

// TestAcceptance_A2AClosedLoop is criterion ⑧, end to end.
func TestAcceptance_A2AClosedLoop(t *testing.T) {
	requester := identityFor(t, requesterKey)
	executor := identityFor(t, executorKey)

	if requester == executor {
		t.Fatal("the two agents must be independent for this test to mean anything")
	}

	srv := newE2ENode(t)
	client := srv.Client()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

	// ---------------------------------------------------------------- 1. Agent Card
	//
	// The executor publishes a card declaring both bindings (S9-12), signs it with its
	// EVM identity (S9-3), and the requester discovers and verifies it.

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) + node.WSEndpoint(executor)
	card, err := a2a.Build(a2a.CardSpec{
		AgentID:      executor,
		Name:         "executor-agent",
		Description:  "Executes probe tasks and issues receipts",
		URL:          srv.URL + "/a2a",
		WebSocketURL: wsURL,
		Version:      "1.0.0",
		Skills: []a2asdk.AgentSkill{
			{ID: "probe", Name: "Probe a URL", Description: "Checks reachability"},
		},
	})
	if err != nil {
		t.Fatalf("build card: %v", err)
	}
	cardBytes, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	proof, err := publish.SignCard(executorKey, e2eChainID, executor, cardBytes)
	if err != nil {
		t.Fatalf("sign card: %v", err)
	}
	proofBytes, err := json.Marshal(proof)
	if err != nil {
		t.Fatalf("marshal proof: %v", err)
	}

	pubBody, err := json.Marshal(map[string]any{
		"agentId": executor,
		"card":    json.RawMessage(cardBytes),
		"proof":   json.RawMessage(proofBytes),
	})
	if err != nil {
		t.Fatalf("marshal publish body: %v", err)
	}
	resp, err := client.Post(srv.URL+"/agents", "application/json", strings.NewReader(string(pubBody)))
	if err != nil {
		t.Fatalf("publish card: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publishing the card returned %d", resp.StatusCode)
	}

	// The requester discovers it through the client-side fetcher, which verifies rather
	// than trusting the node (S9-3).
	fetched, err := publish.NewCardFetcher(srv.URL).Fetch(context.Background(), executor)
	if err != nil {
		t.Fatalf("the requester must be able to discover and verify the card: %v", err)
	}
	gotID, ok := a2a.AgentIDFromCard(fetched)
	if !ok || gotID != executor {
		t.Fatalf("the discovered card must name the executor, got %q ok=%v", gotID, ok)
	}
	// And the card must declare both bindings, so the requester can pick one.
	if len(fetched.SupportedInterfaces) != 2 {
		t.Fatalf("the card must declare both bindings, got %d", len(fetched.SupportedInterfaces))
	}

	// ---------------------------------------------------------------- 2. Session
	//
	// The requester opens a session. Both sides' events go into one stream.

	sessionID, err := a2a.SessionIDFor(requester, "criterion-8")
	if err != nil {
		t.Fatalf("session id: %v", err)
	}
	taskID := "tsk_criterion_8"

	// A helper that chains events per actor, since sequences are per-actor.
	type chained struct {
		actor    string
		types    []a2a.EventType
		start    time.Duration
		eventIDs []string
	}
	buildChain := func(c chained) []a2a.Event {
		var (
			out      []a2a.Event
			prevHash string
		)
		for i, typ := range c.types {
			e := a2a.Event{
				EventID:           c.eventIDs[i],
				SessionID:         sessionID,
				Actor:             c.actor,
				Type:              typ,
				Sequence:          uint64(i + 1),
				PreviousEventHash: prevHash,
				IssuedAt:          now.Add(c.start + time.Duration(i)*time.Second),
			}
			if typ != a2a.EventSessionOpen && typ != a2a.EventSessionClose {
				e.TaskID = taskID
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

	requesterEvents := buildChain(chained{
		actor:    requester,
		start:    0,
		eventIDs: []string{"r-open", "r-created", "r-offered"},
		types: []a2a.EventType{
			a2a.EventSessionOpen, a2a.EventTaskCreated, a2a.EventTaskOffered,
		},
	})
	executorEvents := buildChain(chained{
		actor:    executor,
		start:    3 * time.Second,
		eventIDs: []string{"e-accepted", "e-started", "e-progress", "e-completed"},
		types: []a2a.EventType{
			a2a.EventTaskAccepted, a2a.EventTaskStarted,
			a2a.EventTaskProgress, a2a.EventTaskCompleted,
		},
	})
	allEvents := append(append([]a2a.Event{}, requesterEvents...), executorEvents...)

	// Both state machines derive from the same stream.
	sres, err := a2a.DeriveSession(allEvents, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("DeriveSession: %v", err)
	}
	if sres.State != a2a.SessionOpenState {
		t.Fatalf("session state = %s, want %s", sres.State, a2a.SessionOpenState)
	}
	if len(sres.Participants) != 2 {
		t.Fatalf("the session must show both participants, got %v", sres.Participants)
	}

	tres, err := a2a.Derive(a2a.DeriveInput{Events: allEvents, Now: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if tres.State != a2a.StateCompleted {
		t.Fatalf("task state = %s, want %s", tres.State, a2a.StateCompleted)
	}

	// Every event must reach the node and survive the round trip with its chain intact.
	for _, e := range allEvents {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		env := map[string]any{
			"id": e.EventID, "agentId": e.Actor, "kind": "event", "payload": raw,
		}
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		r, err := client.Post(srv.URL+"/messages", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatalf("post event: %v", err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("publishing %s returned %d", e.EventID, r.StatusCode)
		}
	}

	// ---------------------------------------------------------------- 3. Push binding
	//
	// The requester subscribes over WebSocket and receives a live event. This is the
	// S9-12 binding doing real work in the loop rather than in isolation.

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx,
		strings.Replace(srv.URL, "http://", "ws://", 1)+node.WSEndpoint(requester), nil)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer conn.CloseNow()
	// The acknowledgement.
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read ack: %v", err)
	}

	// A follow-up event arrives live.
	closeEvent := buildChain(chained{
		actor:    requester,
		start:    20 * time.Second,
		eventIDs: []string{"r-close-4"},
		types:    []a2a.EventType{a2a.EventSessionClose},
	})[0]
	// Its sequence must continue the requester's chain (it is the 4th requester event).
	closeEvent.Sequence = 4
	closeEvent.PreviousEventHash = ""
	// Re-chain properly against the requester's third event.
	h3, err := a2a.EventHash(keccakHasher, requesterEvents[2])
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	closeEvent.PreviousEventHash = h3
	closeRaw, err := json.Marshal(closeEvent)
	if err != nil {
		t.Fatalf("marshal close event: %v", err)
	}
	closeBody, err := json.Marshal(map[string]any{
		"id": closeEvent.EventID, "agentId": requester, "kind": "event", "payload": closeRaw,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	r, err := client.Post(srv.URL+"/messages", "application/json", strings.NewReader(string(closeBody)))
	if err != nil {
		t.Fatalf("post close event: %v", err)
	}
	r.Body.Close()

	_, frameRaw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("the live frame must arrive: %v", err)
	}
	var frame node.WSFrame
	if err := json.Unmarshal(frameRaw, &frame); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if frame.Type != "message" || frame.Message == nil || frame.Message.ID != "r-close-4" {
		t.Fatalf("the live event must be pushed, got %+v", frame)
	}

	// ---------------------------------------------------------------- 4. Receipt
	//
	// The executor issues a receipt for the work, linked to the A2A task (S9-10).

	r2 := validProbeReceipt(t, executor)
	if err := r2.Task.SetA2ATaskID(taskID); err != nil {
		t.Fatalf("link task id: %v", err)
	}
	// The task declared recompute (S9-7): the mode this build can check.
	r2.Task.Verification = receipt.VerificationRecompute
	id, err := r2.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	r2.ReceiptID = id
	if err := r2.Sign(executorKey); err != nil {
		t.Fatalf("sign receipt: %v", err)
	}

	// ---------------------------------------------------------------- 5. Verify
	//
	// The requester verifies the receipt offline: no server involved.

	receiptRaw, err := r2.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	decoded, err := receipt.Unmarshal(receiptRaw)
	if err != nil {
		t.Fatalf("unmarshal receipt: %v", err)
	}
	if err := decoded.Validate(nil); err != nil {
		t.Fatalf("the receipt must verify: %v", err)
	}

	// The link must survive and point at the A2A task.
	if decoded.Task.A2ATaskID == nil {
		t.Fatal("the receipt must carry the A2A task link")
	}
	if *decoded.Task.A2ATaskID != taskID {
		t.Errorf("link = %q, want %q", *decoded.Task.A2ATaskID, taskID)
	}
	if got := decoded.Task.VerificationOrDefault(); got != receipt.VerificationRecompute {
		t.Errorf("verification mode = %s, want %s", got, receipt.VerificationRecompute)
	}

	// And the chain still validates from the bytes the node served.
	pulledRequester := pullEvents(t, srv.URL, requester)
	pulledExecutor := pullEvents(t, srv.URL, executor)
	all := append(append([]a2a.Event{}, pulledRequester...), pulledExecutor...)
	if err := a2a.ValidateChain(keccakHasher, all); err != nil {
		t.Fatalf("the event chains must survive the node round trip: %v", err)
	}
}

// TestAcceptance_WireIsA2ACompatible is the other half of criterion ⑧: the wire must be
// readable by the official SDK, not merely by us.
//
// The check is a round trip through the SDK's own types: if a card we produce cannot be
// decoded by an unmodified SDK client, "compatible with A2A" is a claim we have not met.
func TestAcceptance_WireIsA2ACompatible(t *testing.T) {
	executor := identityFor(t, executorKey)

	card, err := a2a.Build(a2a.CardSpec{
		AgentID:      executor,
		Name:         "executor-agent",
		Description:  "A2A compatibility check",
		URL:          "https://node.example/a2a",
		WebSocketURL: "wss://node.example/ws/messages/agent",
		Version:      "1.0.0",
		Skills: []a2asdk.AgentSkill{
			{ID: "probe", Name: "Probe", Description: "Checks reachability"},
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Decode with the SDK's own type, which is what a third-party client would do.
	var decoded a2asdk.AgentCard
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("an unmodified SDK client must decode our card: %v", err)
	}
	if decoded.Name != card.Name {
		t.Errorf("name did not survive: got %q", decoded.Name)
	}
	if len(decoded.SupportedInterfaces) != 2 {
		t.Fatalf("both interfaces must survive SDK decoding, got %d", len(decoded.SupportedInterfaces))
	}
	// The standard field names must be exactly what the SDK expects, which is the
	// property that makes the card interoperable rather than merely similar.
	for i, iface := range decoded.SupportedInterfaces {
		if iface.URL == "" || iface.ProtocolBinding == "" || iface.ProtocolVersion == "" {
			t.Errorf("interface[%d] lost a required field in SDK decoding: %+v", i, iface)
		}
	}

	// Our own identity extension must still be readable after the SDK round trip, since
	// an SDK client ignores it and a RelayFirst client needs it.
	gotID, ok := a2a.AgentIDFromCard(&decoded)
	if !ok || gotID != executor {
		t.Errorf("the identity extension must survive SDK decoding, got %q ok=%v", gotID, ok)
	}
}
