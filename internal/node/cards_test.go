package node_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/node"
)

// These tests cover the agent-card directory (S9-3).
//
// The property they exist to protect is negative: the node must store and return
// cards WITHOUT verifying them. A node that verified would be a node that could
// forge, and "validate on the client" (MVP.md §5.4) is what makes a hostile node
// unable to fabricate work. So the important assertions are that a card with a
// nonsense proof is accepted just the same as a good one, and that every response
// says the node did not check.

const cardAgentID = "agent:eip155:8453:0x7f4db0d9c4b8a6bce8e1c22da9c419e6e1f3a8b5"

func cardBody(agentID, marker string) map[string]any {
	return map[string]any{
		"agentId": agentID,
		"card": map[string]any{
			"name":                "relayfirst-node",
			"description":         "A node",
			"version":             "1.0.0",
			"supportedInterfaces": []any{},
			"skills":              []any{},
			"marker":              marker,
		},
		"proof": map[string]any{
			"agentId":   agentID,
			"cardHash":  "0x" + strings.Repeat("ab", 32),
			"signature": "0x" + strings.Repeat("cd", 65),
		},
	}
}

// TestNode_PublishesAndServesCard is the round trip.
func TestNode_PublishesAndServesCard(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	pub := postJSON(t, client, srv.URL+"/agents", cardBody(cardAgentID, "v1"))
	if pub.StatusCode != http.StatusOK {
		t.Fatalf("publish returned %d: %s", pub.StatusCode, readAll(t, pub))
	}

	resp := get(t, client, srv.URL+"/agents/"+cardAgentID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get returned %d: %s", resp.StatusCode, readAll(t, resp))
	}
	var got struct {
		AgentID string          `json:"agentId"`
		Card    json.RawMessage `json:"card"`
		Proof   json.RawMessage `json:"proof"`
	}
	decode(t, resp, &got)
	if got.AgentID != cardAgentID {
		t.Errorf("agentId = %q, want %q", got.AgentID, cardAgentID)
	}
	if !strings.Contains(string(got.Card), `"marker":"v1"`) {
		t.Errorf("card was not returned verbatim: %s", got.Card)
	}
}

// TestNode_StoresCardWithoutVerifying is the security property.
//
// A card whose proof is obvious nonsense must be accepted exactly like a valid
// one. If this node rejected it, the node would be making a validity judgement it
// is not allowed to make — and, worse, a client might then trust the node's
// acceptance as validation.
func TestNode_StoresCardWithoutVerifying(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	// A proof that cannot possibly verify: right shape, random signature.
	body := cardBody(cardAgentID, "garbage-proof")

	resp := postJSON(t, client, srv.URL+"/agents", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a node must accept a card it cannot verify (it verifies nothing); got %d: %s",
			resp.StatusCode, readAll(t, resp))
	}

	var ack struct {
		OK       bool   `json:"ok"`
		Verified bool   `json:"verified"`
		Note     string `json:"note"`
	}
	decode(t, resp, &ack)
	if !ack.OK {
		t.Error("acceptance must be acknowledged")
	}
	if ack.Verified {
		t.Fatal("a node must never report a card as verified: it cannot check signatures (MVP.md §7.1)")
	}
	if !strings.Contains(ack.Note, "not verified") {
		t.Errorf("the acknowledgement must say the card was not verified, got: %q", ack.Note)
	}

	// And the card must still be retrievable, byte for byte.
	got := get(t, client, srv.URL+"/agents/"+cardAgentID)
	var fetched struct {
		Card json.RawMessage `json:"card"`
	}
	decode(t, got, &fetched)
	if !strings.Contains(string(fetched.Card), `"marker":"garbage-proof"`) {
		t.Error("an unverified card must be stored and served unchanged")
	}
}

// TestNode_CardResponsesSayTheyAreUnverified checks that no read path implies
// validation. An omitted field would let a client assume the node filtered.
func TestNode_CardResponsesSayTheyAreUnverified(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	postJSON(t, client, srv.URL+"/agents", cardBody(cardAgentID, "v1"))

	paths := []string{"/agents", "/agents/" + cardAgentID}
	for _, p := range paths {
		resp := get(t, client, srv.URL+p)
		raw := readAll(t, resp)
		if !strings.Contains(raw, `"verified":false`) {
			t.Errorf("%s must state verified:false; body was: %s", p, raw)
		}
	}
}

// TestNode_CardRepublishReplaces covers the "card is current state" decision: an
// agent rotating its endpoint must not leave a stale card addressable.
func TestNode_CardRepublishReplaces(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	postJSON(t, client, srv.URL+"/agents", cardBody(cardAgentID, "old"))
	postJSON(t, client, srv.URL+"/agents", cardBody(cardAgentID, "new"))

	var got struct {
		Card json.RawMessage `json:"card"`
	}
	decode(t, get(t, client, srv.URL+"/agents/"+cardAgentID), &got)
	if !strings.Contains(string(got.Card), `"marker":"new"`) {
		t.Errorf("re-publishing must replace the previous card, got: %s", got.Card)
	}
	if strings.Contains(string(got.Card), `"marker":"old"`) {
		t.Error("the stale card must not remain addressable")
	}
}

func TestNode_ListCards(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	postJSON(t, client, srv.URL+"/agents", cardBody(cardAgentID, "v1"))
	other := "agent:eip155:1:0x0000000000000000000000000000000000000001"
	postJSON(t, client, srv.URL+"/agents", cardBody(other, "v1"))

	var got struct {
		Count int `json:"count"`
		Cards []struct {
			AgentID string `json:"agentId"`
		} `json:"cards"`
		Note string `json:"note"`
	}
	decode(t, get(t, client, srv.URL+"/agents"), &got)
	if got.Count != 2 {
		t.Errorf("count = %d, want 2", got.Count)
	}
	if !strings.Contains(got.Note, "does not verify") {
		t.Errorf("the listing must say it is a directory, not an authority; note was: %q", got.Note)
	}

	// limit must be honoured and validated.
	var limited struct {
		Count int `json:"count"`
	}
	decode(t, get(t, client, srv.URL+"/agents?limit=1"), &limited)
	if limited.Count != 1 {
		t.Errorf("limit=1 returned %d cards", limited.Count)
	}
	badResp := get(t, client, srv.URL+"/agents?limit=-1")
	if badResp.StatusCode != http.StatusBadRequest {
		t.Errorf("a negative limit must be rejected, got %d", badResp.StatusCode)
	}
}

func TestNode_GetUnknownCardIs404(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	resp := get(t, srv.Client(), srv.URL+"/agents/agent:eip155:1:0x0000000000000000000000000000000000000009")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown card returned %d, want 404", resp.StatusCode)
	}
}

// TestNode_RejectsMalformedCardPublish covers the index-integrity checks. These
// are syntax checks on the directory key, not judgements about the card.
func TestNode_RejectsMalformedCardPublish(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	cases := []struct {
		name string
		body any
	}{
		{"no agentId", map[string]any{
			"card":  map[string]any{"a": 1},
			"proof": map[string]any{"a": 1},
		}},
		{"malformed agentId", map[string]any{
			"agentId": "agent:eip155::0x00",
			"card":    map[string]any{"a": 1},
			"proof":   map[string]any{"a": 1},
		}},
		{"no card", map[string]any{
			"agentId": cardAgentID,
			"proof":   map[string]any{"a": 1},
		}},
		{"no proof", map[string]any{
			"agentId": cardAgentID,
			"card":    map[string]any{"a": 1},
		}},
		{"card is not an object", map[string]any{
			"agentId": cardAgentID,
			"card":    "not-an-object",
			"proof":   map[string]any{"a": 1},
		}},
		{"proof is an array", map[string]any{
			"agentId": cardAgentID,
			"card":    map[string]any{"a": 1},
			"proof":   []any{1, 2},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := postJSON(t, client, srv.URL+"/agents", c.body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("publishing with %s returned %d, want 400", c.name, resp.StatusCode)
			}
		})
	}
}

// TestNode_WellKnownAdvertisesAgentDirectory keeps the self-description honest:
// a client discovers the directory from here, so the endpoint list must include it.
func TestNode_WellKnownAdvertisesAgentDirectory(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	postJSON(t, client, srv.URL+"/agents", cardBody(cardAgentID, "v1"))

	var wk struct {
		Endpoints  []string `json:"endpoints"`
		AgentCards int      `json:"agentCards"`
		Verifies   bool     `json:"verifies"`
	}
	decode(t, get(t, client, srv.URL+"/.well-known/relayfirst"), &wk)

	joined := strings.Join(wk.Endpoints, " ")
	for _, want := range []string{"POST /agents", "GET /agents"} {
		if !strings.Contains(joined, want) {
			t.Errorf("well-known endpoints must advertise %q, got: %v", want, wk.Endpoints)
		}
	}
	if wk.AgentCards != 1 {
		t.Errorf("agentCards = %d, want 1", wk.AgentCards)
	}
	if wk.Verifies {
		t.Error("a node must still report verifies:false")
	}
}

// TestNode_CardIsNotAnEnvelopeKind guards against accidentally routing cards
// through the message store, where they would be counted as relayed messages and
// pollute the message counters.
func TestNode_CardIsNotAnEnvelopeKind(t *testing.T) {
	if node.KindReceipt == "agent-card" {
		t.Fatal("cards must not be a message kind; they live in their own directory")
	}
}
