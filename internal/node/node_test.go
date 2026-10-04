package node_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/node"
	"github.com/relayfirst/relayfirst/internal/publish"
	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// testNodeKey is a well-known test key. It holds no value and is never used
// outside tests (CODING_RULES.md §8).
const testNodeKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"

func newTestNode(t *testing.T, publicURL string) (*httptest.Server, *sqlite.MessageStore) {
	t.Helper()

	db, err := sqlite.Open(filepath.Join(t.TempDir(), "node.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ms := sqlite.NewMessageStore(db)
	n, err := node.New(node.Config{
		Store:        ms,
		Cards:        sqlite.NewCardStore(db),
		Observations: sqlite.NewObservationStore(db),
		Tasks:        sqlite.NewTaskStore(db),
		PublicURL:    publicURL,
		Version:      "test",
		Now:          func() time.Time { return time.Unix(1791015800, 0) },
	})
	if err != nil {
		t.Fatalf("node.New: %v", err)
	}

	srv := httptest.NewServer(n.Handler())
	t.Cleanup(srv.Close)
	return srv, ms
}

// signedReceipt builds a genuinely signed probe receipt.
func signedReceipt(t *testing.T, url string) *receipt.Receipt {
	t.Helper()

	agent, err := receipt.DeriveAgentID(testNodeKey, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	r := &receipt.Receipt{
		Schema:  receipt.Schema,
		AgentID: agent,
		Epoch:   42,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": url},
			SpecHash:      "sha256:" + strings.Repeat("3d", 32),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015862},
		Result:       receipt.Result{Value: "200", Hash: "sha256:" + strings.Repeat("c1", 32)},
		Anchors:      []receipt.Anchor{{URL: url, ContentHash: "sha256:" + strings.Repeat("7b", 32), FetchedAt: 1791015810, Status: 200, Bytes: 2048}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}

	// Derive the id from the signed payload; Validate rejects a free-standing one
	// (S9-0h, finding B2).
	derived, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("DerivedReceiptID: %v", err)
	}
	r.ReceiptID = derived

	if err := r.Sign(testNodeKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return r
}

func postEnvelope(t *testing.T, url string, env node.Envelope) *http.Response {
	t.Helper()

	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	resp, err := http.Post(url+"/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// postJSON POSTs v as JSON to url and returns the response. The caller owns the
// body (t.Cleanup closes it), matching postEnvelope.
func postJSON(t *testing.T, client *http.Client, url string, v any) *http.Response {
	t.Helper()

	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// get performs a GET. The caller owns the body.
func get(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// readAll returns the response body as a string, for assertions and diagnostics.
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// decode reads resp's body into v.
func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// TestNode_ReceiptRoundTripSurvivesSignature is the load-bearing test of S5.
//
// A receipt carries a signature over an exact byte sequence. If the node re-encoded
// the payload on the way through, the stored receipt would still look plausible and
// would fail verification later, somewhere else — the worst possible failure.
// So the bytes must survive storage and a pull unchanged, and the receipt must still
// verify afterwards.
func TestNode_ReceiptRoundTripSurvivesSignature(t *testing.T) {
	srv, _ := newTestNode(t, "")

	r := signedReceipt(t, "https://example.com/health")
	if err := r.Validate(nil); err != nil {
		t.Fatalf("the test receipt is not valid to begin with: %v", err)
	}

	env, err := publish.ReceiptEnvelope(r)
	if err != nil {
		t.Fatalf("ReceiptEnvelope: %v", err)
	}

	resp := postEnvelope(t, srv.URL, env)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", resp.StatusCode)
	}

	pull, err := http.Get(srv.URL + "/messages/" + r.AgentID)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer pull.Body.Close()

	var got struct {
		Count    int             `json:"count"`
		Messages []node.Envelope `json:"messages"`
	}
	if err := json.NewDecoder(pull.Body).Decode(&got); err != nil {
		t.Fatalf("decode pull: %v", err)
	}
	if got.Count != 1 || len(got.Messages) != 1 {
		t.Fatalf("pull returned %d message(s), want 1", got.Count)
	}

	// The envelope's payload must equal the original marshalling byte-for-byte.
	want, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}
	if !bytes.Equal(got.Messages[0].Payload, want) {
		t.Error("the stored payload differs from the original bytes; the node must not reserialize a receipt")
	}

	// And the receipt decoded from the node must still verify. This is the
	// assertion that actually matters: a node must not be able to strip or damage
	// a signature just by relaying.
	back, err := publish.DecodeReceipt(got.Messages[0])
	if err != nil {
		t.Fatalf("DecodeReceipt: %v", err)
	}
	if err := back.Validate(nil); err != nil {
		t.Errorf("the receipt no longer verifies after passing through the node: %v", err)
	}
}

// TestNode_AcknowledgementShape is S5-4.
func TestNode_AcknowledgementShape(t *testing.T) {
	srv, _ := newTestNode(t, "")
	r := signedReceipt(t, "https://example.com/a")
	env, _ := publish.ReceiptEnvelope(r)

	resp := postEnvelope(t, srv.URL, env)

	var ack struct {
		OK     bool   `json:"ok"`
		ID     string `json:"id"`
		Stored bool   `json:"stored"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if !ack.OK {
		t.Error("ack.ok should be true")
	}
	if ack.ID != r.ReceiptID {
		t.Errorf("ack.id = %q, want %q", ack.ID, r.ReceiptID)
	}
	if !ack.Stored {
		t.Error("ack.stored should be true on first delivery")
	}
}

// TestNode_DuplicateIsAcknowledgedNotRejected is S5-3 at the HTTP layer.
//
// A retry after a timeout must not be an error. If a duplicate returned non-2xx,
// a sender would retry forever against a node that already has the message.
func TestNode_DuplicateIsAcknowledgedNotRejected(t *testing.T) {
	srv, ms := newTestNode(t, "")
	r := signedReceipt(t, "https://example.com/a")
	env, _ := publish.ReceiptEnvelope(r)

	for i := 0; i < 3; i++ {
		resp := postEnvelope(t, srv.URL, env)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("delivery %d status = %d, want 200 even for a duplicate", i, resp.StatusCode)
		}

		var ack struct {
			OK     bool `json:"ok"`
			Stored bool `json:"stored"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&ack)
		if !ack.OK {
			t.Errorf("delivery %d: ack.ok = false", i)
		}
		if wantStored := i == 0; ack.Stored != wantStored {
			t.Errorf("delivery %d: stored = %v, want %v", i, ack.Stored, wantStored)
		}
	}

	if got := ms.Count(); got != 1 {
		t.Errorf("stored %d message(s), want 1", got)
	}
}

// TestNode_PullIsEmptyForUnknownAgent: a pull for someone else's id must not
// return another agent's mail.
func TestNode_PullIsEmptyForUnknownAgent(t *testing.T) {
	srv, _ := newTestNode(t, "")
	r := signedReceipt(t, "https://example.com/a")
	env, _ := publish.ReceiptEnvelope(r)

	if resp := postEnvelope(t, srv.URL, env); resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d", resp.StatusCode)
	}

	resp, err := http.Get(srv.URL + "/messages/agent:eip155:8453:0xdeadbeef")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var got struct {
		Count    int             `json:"count"`
		Messages []node.Envelope `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Count != 0 || len(got.Messages) != 0 {
		t.Errorf("an unknown agent received %d message(s), want 0", got.Count)
	}
}

// TestNode_WellKnownDocumentsNoVerification is S5-5, and pins the honesty field.
//
// A client must never infer that a node's acceptance means a receipt is valid.
// The cheapest way to prevent that is to state it in the document the client
// fetches first.
func TestNode_WellKnownDocumentsNoVerification(t *testing.T) {
	srv, _ := newTestNode(t, "https://relay.myagent.xyz")

	resp, err := http.Get(srv.URL + "/.well-known/relayfirst")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}

	var doc struct {
		Name      string   `json:"name"`
		Protocol  string   `json:"protocol"`
		PublicURL string   `json:"publicUrl"`
		Endpoints []string `json:"endpoints"`
		Verifies  bool     `json:"verifies"`
		Note      string   `json:"note"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if doc.Name != "relayfirst-node" {
		t.Errorf("name = %q", doc.Name)
	}
	if doc.PublicURL != "https://relay.myagent.xyz" {
		t.Errorf("publicUrl = %q, want the configured URL", doc.PublicURL)
	}
	if doc.Verifies {
		t.Error("verifies must be false: the node does not check signatures")
	}
	if !strings.Contains(strings.ToLower(doc.Note), "store-and-forward") {
		t.Errorf("note should state the node's role plainly, got %q", doc.Note)
	}
	if len(doc.Endpoints) == 0 {
		t.Error("endpoints must be advertised so a client can discover how to talk to the node")
	}
}

// TestNode_Health: the liveness probe a container runtime expects.
func TestNode_Health(t *testing.T) {
	srv, _ := newTestNode(t, "")

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestNode_RejectsMalformed is the public-endpoint boundary: a node is reachable
// by anyone, so every malformed input must be a clean 4xx rather than a panic or
// a 500.
func TestNode_RejectsMalformed(t *testing.T) {
	srv, _ := newTestNode(t, "")

	cases := []struct {
		name string
		body string
		want int
	}{
		{"not json", `not json at all`, http.StatusBadRequest},
		{"missing id", `{"agentId":"a","kind":"receipt","payload":"AA=="}`, http.StatusBadRequest},
		{"missing agent", `{"id":"0x1","kind":"receipt","payload":"AA=="}`, http.StatusBadRequest},
		{"missing kind", `{"id":"0x1","agentId":"a","payload":"AA=="}`, http.StatusBadRequest},
		{"missing payload", `{"id":"0x1","agentId":"a","kind":"receipt"}`, http.StatusBadRequest},
		{"bad receipt id", `{"id":"nope","agentId":"a","kind":"receipt","payload":"AA=="}`, http.StatusBadRequest},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := http.Post(srv.URL+"/messages", "application/json", strings.NewReader(c.body))
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != c.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, c.want)
			}
			if resp.StatusCode >= 500 {
				t.Error("malformed input must not produce a server error")
			}
		})
	}
}

// TestNode_RejectsOversizePayload: an uncapped body would be an unbounded memory
// and disk commitment offered to anyone who can reach the port.
func TestNode_RejectsOversizePayload(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "node.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer db.Close()

	n, err := node.New(node.Config{
		Store:           sqlite.NewMessageStore(db),
		Cards:           sqlite.NewCardStore(db),
		Observations:    sqlite.NewObservationStore(db),
		Tasks:           sqlite.NewTaskStore(db),
		MaxPayloadBytes: 128,
	})
	if err != nil {
		t.Fatalf("node.New: %v", err)
	}
	srv := httptest.NewServer(n.Handler())
	defer srv.Close()

	big := strings.Repeat("A", 4096)
	body := fmt.Sprintf(`{"id":"0x%s","agentId":"a","kind":"receipt","payload":"%s"}`,
		strings.Repeat("ab", 32), big)

	resp, err := http.Post(srv.URL+"/messages", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
}

// TestNode_AcceptsNonReceiptKinds: the node is payload-agnostic, so a future
// message kind must not require a code change here.
func TestNode_AcceptsNonReceiptKinds(t *testing.T) {
	srv, ms := newTestNode(t, "")

	resp := postEnvelope(t, srv.URL, node.Envelope{
		ID:      "event-1",
		AgentID: "agent:a",
		Kind:    "event",
		Payload: []byte(`{"hello":"world"}`),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := ms.Count(); got != 1 {
		t.Errorf("Count = %d, want 1", got)
	}
}

// TestNode_PullLimitValidation: a nonsense limit must be a clean 400.
func TestNode_PullLimitValidation(t *testing.T) {
	srv, _ := newTestNode(t, "")

	for _, raw := range []string{"abc", "-1"} {
		resp, err := http.Get(srv.URL + "/messages/agent:a?limit=" + raw)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("limit=%q: status = %d, want 400", raw, resp.StatusCode)
		}
	}
}
