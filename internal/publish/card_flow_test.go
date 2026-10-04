package publish_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	a2asdk "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/relayfirst/relayfirst/internal/a2a"
	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/eip712"
	"github.com/relayfirst/relayfirst/internal/node"
	"github.com/relayfirst/relayfirst/internal/publish"
	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// This is the S9-1/S9-2/S9-3 integration: the whole card path, end to end, over
// HTTP against a real node.
//
// The unit tests in internal/a2a and internal/publish check the pieces in
// isolation. This exists because the pieces can each be correct while the
// composition is not — and the composition is the acceptance criterion ⑧ story
// ("an agent publishes a card, another finds and verifies it"). The property worth
// proving here is the split: the NODE stores and returns bytes without checking
// them, and the CLIENT verifies. If the node ever started verifying, these tests
// would still pass, which is why the node's own tests assert the negative.
const (
	e2eKey     = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	e2eChainID = uint64(8453)
)

func e2eAgentID(t *testing.T) string {
	t.Helper()
	priv, err := eip712.PrivateKeyFromHex(e2eKey)
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

func newE2ENode(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "node.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	n, err := node.New(node.Config{
		Store:        sqlite.NewMessageStore(db),
		Cards:        sqlite.NewCardStore(db),
		Observations: sqlite.NewObservationStore(db),
		Tasks:        sqlite.NewTaskStore(db),
	})
	if err != nil {
		t.Fatalf("node.New: %v", err)
	}
	srv := httptest.NewServer(n.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// TestCardFlow_EndToEnd publishes a signed card to a real node and verifies it as
// a client would.
func TestCardFlow_EndToEnd(t *testing.T) {
	srv := newE2ENode(t)
	agentID := e2eAgentID(t)

	// 1. The agent builds its card (S9-2) and signs it (S9-3).
	card, err := a2a.Build(a2a.CardSpec{
		AgentID:     agentID,
		Name:        "relayfirst-node",
		Description: "An end-to-end test agent",
		URL:         srv.URL + "/a2a",
		Version:     "1.0.0",
		Skills: []a2asdk.AgentSkill{
			{ID: "relay", Name: "Relay messages", Description: "Store and forward"},
		},
	})
	if err != nil {
		t.Fatalf("build card: %v", err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	proof, err := publish.SignCard(e2eKey, e2eChainID, agentID, raw)
	if err != nil {
		t.Fatalf("sign card: %v", err)
	}
	proofRaw, err := json.Marshal(proof)
	if err != nil {
		t.Fatalf("marshal proof: %v", err)
	}

	// 2. Publish to the node, which stores both verbatim without verifying.
	publishBody, err := json.Marshal(map[string]any{
		"agentId": agentID,
		"card":    json.RawMessage(raw),
		"proof":   json.RawMessage(proofRaw),
	})
	if err != nil {
		t.Fatalf("marshal publish body: %v", err)
	}
	resp, err := srv.Client().Post(srv.URL+"/agents", "application/json", bytes.NewReader(publishBody))
	if err != nil {
		t.Fatalf("POST /agents: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("publish returned %d", resp.StatusCode)
	}

	// 3. A client discovers it by listing the directory.
	listResp, err := srv.Client().Get(srv.URL + "/agents")
	if err != nil {
		t.Fatalf("GET /agents: %v", err)
	}
	defer listResp.Body.Close()
	var listing struct {
		Count int `json:"count"`
		Cards []struct {
			AgentID string          `json:"agentId"`
			Card    json.RawMessage `json:"card"`
			Proof   json.RawMessage `json:"proof"`
		} `json:"cards"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listing); err != nil {
		t.Fatalf("decode listing: %v", err)
	}
	if listing.Count != 1 {
		t.Fatalf("directory has %d cards, want 1", listing.Count)
	}
	entry := listing.Cards[0]

	// 4. The client verifies the proof over the bytes it actually received.
	//
	// This is the step the node deliberately does not do. If the node had
	// re-serialized the card, the proof would fail here — which is exactly why the
	// node stores bytes rather than structures.
	var fetchedProof publish.CardProof
	if err := json.Unmarshal(entry.Proof, &fetchedProof); err != nil {
		t.Fatalf("decode fetched proof: %v", err)
	}
	if err := publish.VerifyCardProof(fetchedProof, entry.Card); err != nil {
		t.Fatalf("the card fetched from the node must verify against its proof: %v", err)
	}

	// 5. And the identity the proof establishes must be the one the card declares.
	var fetchedCard a2asdk.AgentCard
	if err := json.Unmarshal(entry.Card, &fetchedCard); err != nil {
		t.Fatalf("decode fetched card: %v", err)
	}
	if err := publish.VerifyCard(&fetchedCard, entry.Card, fetchedProof); err != nil {
		t.Fatalf("card and proof must agree: %v", err)
	}
	if got, ok := a2a.AgentIDFromCard(&fetchedCard); !ok || got != agentID {
		t.Errorf("card identity = %q ok=%v, want %q", got, ok, agentID)
	}
}

// TestCardFlow_NodeTamperingIsCaught is the point of the split.
//
// The node is untrusted. If it modifies the card on the way through — swaps an
// endpoint, changes a skill — the client must detect it. This simulates the node
// by mutating the bytes between storage and verification, which is precisely what
// a hostile node would do.
func TestCardFlow_NodeTamperingIsCaught(t *testing.T) {
	agentID := e2eAgentID(t)

	card, err := a2a.Build(a2a.CardSpec{
		AgentID:     agentID,
		Name:        "honest-agent",
		Description: "A node will tamper with this",
		URL:         "https://honest.example/a2a",
		Version:     "1.0.0",
		Skills:      []a2asdk.AgentSkill{{ID: "relay", Name: "Relay"}},
	})
	if err != nil {
		t.Fatalf("build card: %v", err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	proof, err := publish.SignCard(e2eKey, e2eChainID, agentID, raw)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// The node rewrites the endpoint to redirect traffic to itself.
	tampered := bytes.Replace(raw,
		[]byte("https://honest.example/a2a"),
		[]byte("https://evil.example/a2a"),
		1)
	if bytes.Equal(tampered, raw) {
		t.Fatal("test did not actually tamper with the card")
	}

	if err := publish.VerifyCardProof(proof, tampered); err == nil {
		t.Fatal("a card modified by a node must fail verification: " +
			"otherwise an untrusted node could redirect an agent's traffic")
	}
}
