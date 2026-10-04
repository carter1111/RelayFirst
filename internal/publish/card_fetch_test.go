package publish_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	a2asdk "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/relayfirst/relayfirst/internal/a2a"
	"github.com/relayfirst/relayfirst/internal/publish"
)

// signedCardFor builds and signs a card for the e2e test key.
func signedCardFor(t *testing.T, name, cardURL string) (string, []byte, publish.CardProof) {
	t.Helper()
	agentID := e2eAgentID(t)
	card, err := a2a.Build(a2a.CardSpec{
		AgentID:     agentID,
		Name:        name,
		Description: "fetcher test",
		URL:         cardURL,
		Version:     "1.0.0",
		Skills:      []a2asdk.AgentSkill{{ID: "relay", Name: "Relay"}},
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
	return agentID, raw, proof
}

func publishTo(t *testing.T, srvURL, agentID string, raw []byte, proof publish.CardProof) {
	t.Helper()
	proofRaw, err := json.Marshal(proof)
	if err != nil {
		t.Fatalf("marshal proof: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"agentId": agentID,
		"card":    json.RawMessage(raw),
		"proof":   json.RawMessage(proofRaw),
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	resp, err := http.Post(srvURL+"/agents", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST /agents: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("publish returned %d", resp.StatusCode)
	}
}

// TestCardFetcher_FetchVerifies is the happy path: fetch from a real node and get
// a verified card.
func TestCardFetcher_FetchVerifies(t *testing.T) {
	srv := newE2ENode(t)
	agentID, raw, proof := signedCardFor(t, "fetchable", "https://node.example/a2a")
	publishTo(t, srv.URL, agentID, raw, proof)

	fetcher := publish.NewCardFetcher(srv.URL)
	card, err := fetcher.Fetch(context.Background(), agentID)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if card.Name != "fetchable" {
		t.Errorf("fetched card name = %q, want %q", card.Name, "fetchable")
	}
}

// TestCardFetcher_RejectsTamperedCard is the reason verification is inside Fetch.
//
// A hostile node serves a card whose endpoint points elsewhere. The proof covers
// the original bytes, so the tampered card must be rejected — and because Fetch
// verifies, there is no way for a caller to accidentally receive it unchecked.
func TestCardFetcher_RejectsTamperedCard(t *testing.T) {
	agentID, raw, proof := signedCardFor(t, "honest", "https://honest.example/a2a")

	// A node that rewrites the endpoint before serving.
	tampered := strings.Replace(string(raw), "https://honest.example/a2a", "https://evil.example/a2a", 1)
	if tampered == string(raw) {
		t.Fatal("test did not tamper")
	}
	srv := maliciousNode(t, agentID, []byte(tampered), mustJSON(t, proof))

	fetcher := publish.NewCardFetcher(srv.URL)
	_, err := fetcher.Fetch(context.Background(), agentID)
	if err == nil {
		t.Fatal("Fetch must reject a card a node modified: " +
			"otherwise a directory operator could redirect an agent's traffic")
	}
	if !strings.Contains(err.Error(), "failed verification") {
		t.Errorf("the error must name verification as the failure, got: %v", err)
	}
}

// TestCardFetcher_RejectsNodeClaimingVerified catches a node that misrepresents
// its own capability. It cannot verify anything, so a claim that it did is a
// reason to distrust the response, not a helpful signal.
func TestCardFetcher_RejectsNodeClaimingVerified(t *testing.T) {
	agentID, raw, proof := signedCardFor(t, "honest", "https://honest.example/a2a")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agentId":  agentID,
			"card":     json.RawMessage(raw),
			"proof":    json.RawMessage(mustJSON(t, proof)),
			"verified": true, // the lie
			"note":     "trust me",
		})
	}))
	defer srv.Close()

	_, err := publish.NewCardFetcher(srv.URL).Fetch(context.Background(), agentID)
	if err == nil {
		t.Fatal("a node claiming to have verified a card must be rejected: it has no way to check a signature")
	}
	if !strings.Contains(err.Error(), "cannot do") {
		t.Errorf("the error must explain the node cannot verify, got: %v", err)
	}
}

// TestCardFetcher_RejectsWrongAgent covers a node that answers with a different
// agent's card than the one requested.
func TestCardFetcher_RejectsWrongAgent(t *testing.T) {
	agentID, raw, proof := signedCardFor(t, "honest", "https://honest.example/a2a")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agentId": "agent:eip155:1:0x0000000000000000000000000000000000000009",
			"card":    json.RawMessage(raw),
			"proof":   json.RawMessage(mustJSON(t, proof)),
		})
	}))
	defer srv.Close()

	_, err := publish.NewCardFetcher(srv.URL).Fetch(context.Background(), agentID)
	if err == nil {
		t.Fatal("a node returning a card for a different agent must be rejected")
	}
}

// TestCardFetcher_ListSkipsBadEntries is the shared-directory property.
//
// Any agent can publish anything to a directory. If one unverifiable card aborted
// the listing, a single bad actor could hide every other agent from readers. So
// bad entries are reported, not fatal.
func TestCardFetcher_ListSkipsBadEntries(t *testing.T) {
	agentID, raw, proof := signedCardFor(t, "good", "https://good.example/a2a")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": 2,
			"cards": []map[string]any{
				{
					"agentId": agentID,
					"card":    json.RawMessage(raw),
					"proof":   json.RawMessage(mustJSON(t, proof)),
				},
				{
					"agentId": "agent:eip155:1:0x0000000000000000000000000000000000000009",
					"card":    json.RawMessage(`{"name":"forged"}`),
					"proof":   json.RawMessage(`{"agentId":"agent:eip155:1:0x0000000000000000000000000000000000000009","cardHash":"0x00","signature":"0x00"}`),
				},
			},
			"note": "directory",
		})
	}))
	defer srv.Close()

	listing, err := publish.NewCardFetcher(srv.URL).List(context.Background(), 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listing.Cards) != 1 {
		t.Errorf("verified cards = %d, want 1", len(listing.Cards))
	}
	if len(listing.Rejected) != 1 {
		t.Errorf("rejected cards = %d, want 1 (bad entries must be reported, not fatal)", len(listing.Rejected))
	}
	if len(listing.Rejected) > 0 && listing.Rejected[0].Reason == "" {
		t.Error("a rejected entry must carry a reason")
	}
}

// TestCardFetcher_RejectsMalformedAgentID confirms the fetcher refuses to ask for
// something that cannot be an agent, before making a request.
func TestCardFetcher_RejectsMalformedAgentID(t *testing.T) {
	srv := newE2ENode(t)
	fetcher := publish.NewCardFetcher(srv.URL)

	for _, bad := range []string{"", "not-an-id", "agent:eip155::0x00"} {
		if _, err := fetcher.Fetch(context.Background(), bad); err == nil {
			t.Errorf("Fetch(%q) must fail before making a request", bad)
		}
	}
}

// TestCardFetcher_UnknownAgentIsAnError covers a 404 from the directory.
func TestCardFetcher_UnknownAgentIsAnError(t *testing.T) {
	srv := newE2ENode(t)
	_, err := publish.NewCardFetcher(srv.URL).Fetch(
		context.Background(),
		"agent:eip155:1:0x0000000000000000000000000000000000000009")
	if err == nil {
		t.Fatal("fetching an agent with no published card must fail")
	}
}

// maliciousNode serves the given card and proof for any agent path.
func maliciousNode(t *testing.T, agentID string, card, proof []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agentId": agentID,
			"card":    json.RawMessage(card),
			"proof":   json.RawMessage(proof),
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
