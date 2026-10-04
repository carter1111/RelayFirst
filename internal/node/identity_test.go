package node_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/node"
	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// These tests cover S10-0: node identity publication, and the boundary ADR-0004 ruled on.
//
// The property that matters is a NEGATIVE one. A node may publish an identity, but it must
// never be given a key — because the import-graph gate that proves "a node cannot forge" is
// only true while this package cannot sign anything. So the tests assert what the node
// CANNOT do as much as what it can.

// newNodeWithIdentity builds a node with an identity configured.
func newNodeWithIdentity(t *testing.T, nodeID string, asserts bool) *httptestServer {
	t.Helper()
	db, err := sqlite.Open(t.TempDir() + "/node.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	n, err := node.New(node.Config{
		Store:        sqlite.NewMessageStore(db),
		Cards:        sqlite.NewCardStore(db),
		Observations: sqlite.NewObservationStore(db),
		Tasks:        sqlite.NewTaskStore(db),
		NodeID:       nodeID,
		Asserts:      asserts,
		Version:      "identity-test",
		Now:          func() time.Time { return time.Unix(1791015800, 0) },
	})
	if err != nil {
		t.Fatalf("node.New: %v", err)
	}
	srv := httptest.NewServer(n.Handler())
	t.Cleanup(srv.Close)
	return &httptestServer{URL: srv.URL, close: srv.Close}
}

type httptestServer struct {
	URL   string
	close func()
}

// TestNode_PublishesNodeIDWhenConfigured is the positive half of S10-0.
func TestNode_PublishesNodeIDWhenConfigured(t *testing.T) {
	const nodeID = "agent:eip155:8453:0x00000000000000000000000000000000000000a9"
	srv := newNodeWithIdentity(t, nodeID, true)

	resp, err := http.Get(srv.URL + "/.well-known/relayfirst")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var doc struct {
		NodeID   string `json:"nodeId"`
		Verifies bool   `json:"verifies"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.NodeID != nodeID {
		t.Errorf("nodeId = %q, want %q", doc.NodeID, nodeID)
	}
	if !doc.Verifies {
		t.Error("a node configured to assert must report verifies:true")
	}
	if !strings.Contains(doc.Note, "attributable claims") {
		t.Errorf("an asserting node must say its verdicts are claims, got: %q", doc.Note)
	}
}

// TestNode_OmitsNodeIDWhenNotConfigured is the boundary ADR-0004 ruled on.
//
// The field is omitted rather than sent empty, because a client reading an empty nodeId
// could not tell "no identity" from "identity not published" — and the two differ for
// exactly the trust question this field answers.
func TestNode_OmitsNodeIDWhenNotConfigured(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")

	resp, err := http.Get(srv.URL + "/.well-known/relayfirst")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	raw := readAll(t, resp)

	if strings.Contains(raw, `"nodeId"`) {
		t.Errorf("a node with no identity must omit nodeId entirely, got: %s", raw)
	}
	if !strings.Contains(raw, `"verifies":false`) {
		t.Errorf("the default node must report verifies:false, got: %s", raw)
	}
	// And it must say it cannot sign anything, which is the structural property.
	if !strings.Contains(raw, "holds no key") {
		t.Errorf("a keyless node must say so, got: %s", raw)
	}
	if !strings.Contains(raw, "cannot sign anything") {
		t.Errorf("a keyless node must state that it cannot sign, got: %s", raw)
	}
}

// TestNode_HasNoKeyField is the structural assertion, expressed against the config type.
//
// ADR-0004 chose a separate verifier binary precisely so this stays true. If a key were ever
// added to node.Config, the import-graph gate that proves a node cannot forge would become a
// lie while still passing — so the boundary is pinned here, where a change is visible.
func TestNode_HasNoKeyField(t *testing.T) {
	// The check is behavioural rather than reflective: configure the node with everything a
	// key would need and confirm the resulting document claims no signing ability.
	srv := newNodeWithIdentity(t, "agent:eip155:8453:0x00000000000000000000000000000000000000b9", false)

	resp, err := http.Get(srv.URL + "/.well-known/relayfirst")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var doc struct {
		NodeID   string `json:"nodeId"`
		Verifies bool   `json:"verifies"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// An identity without assertion: the node is addressable but claims nothing.
	if doc.Verifies {
		t.Error("an identity alone must not make a node assert: asserting requires a key, " +
			"and the node has none (ADR-0004)")
	}
	if !strings.Contains(doc.Note, "makes no verification claims") {
		t.Errorf("the note must say no verification claims are made, got: %q", doc.Note)
	}
}

// TestNode_NoteIsAccurateInBothStates keeps the wording honest, since this document is where
// a client forms its assumption about what the node is worth.
func TestNode_NoteIsAccurateInBothStates(t *testing.T) {
	nodeID := "agent:eip155:8453:0x00000000000000000000000000000000000000c9"

	keyless, _ := newTestNode(t, "http://test.local")
	asserting := newNodeWithIdentity(t, nodeID, true)

	notes := map[string]string{}
	for name, url := range map[string]string{
		"keyless":   keyless.URL,
		"asserting": asserting.URL,
	} {
		resp, err := http.Get(url + "/.well-known/relayfirst")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		var doc struct {
			Note string `json:"note"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
			resp.Body.Close()
			t.Fatalf("decode: %v", err)
		}
		resp.Body.Close()
		notes[name] = doc.Note
	}

	if notes["keyless"] == notes["asserting"] {
		t.Error("the two trust positions must be described differently; " +
			"a single shared string could only be accurate for one of them")
	}
	if !strings.Contains(notes["keyless"], "cannot sign anything") {
		t.Errorf("the keyless note must state the structural limit, got: %q", notes["keyless"])
	}
	if !strings.Contains(notes["asserting"], "verify independently") {
		t.Errorf("the asserting note must tell a client to verify independently, got: %q", notes["asserting"])
	}
}
