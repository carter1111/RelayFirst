package node_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/node"
)

// These tests cover S10-1 (observation index) and S10-2 (cross-verification view).
//
// The properties that matter are not "does a query return rows" but:
//
//   - the index records what was CLAIMED and never claims to have verified it, because
//     MVP.md §7.1 keeps the node unable to verify and a consumer that read a bare list
//     would assume the node had filtered it;
//   - a receipt the index cannot understand does not break DELIVERY, because delivery is
//     the node's obligation and indexing is additive;
//   - cross-verification counts DISTINCT AGENTS, not rows, or one agent could manufacture
//     agreement by submitting twice;
//   - and disagreement stays visible rather than being averaged away by a count.

// receiptPayload builds the JSON a node would index. It is built by hand rather than with
// internal/receipt, because the node deliberately cannot import that package — a test that
// did would not be testing what the node can actually do.
func receiptPayload(receiptID, agentID, subject, contentHash, resultHash string, epoch uint64) []byte {
	body := map[string]any{
		"schema":    "relayfirst.receipt.v1",
		"receiptId": receiptID,
		"agentId":   agentID,
		"epoch":     epoch,
		"task": map[string]any{
			"type":          "probe",
			"spec":          map[string]any{"url": subject},
			"specHash":      "sha256:" + strings.Repeat("3d", 32),
			"selfGenerated": true,
			"a2aTaskId":     nil,
		},
		"result": map[string]any{"value": "200", "hash": resultHash},
		"anchors": []map[string]any{{
			"url":         subject,
			"contentHash": contentHash,
			"fetchedAt":   1791015810,
			"status":      200,
			"bytes":       2048,
		}},
	}
	raw, _ := json.Marshal(body)
	return raw
}

// publishReceipt sends a receipt envelope to the node.
func publishReceipt(t *testing.T, srvURL, receiptID, agentID string, payload []byte) *http.Response {
	t.Helper()
	return postEnvelopeTo(t, srvURL, node.Envelope{
		ID:      receiptID,
		AgentID: agentID,
		Kind:    node.KindReceipt,
		Payload: payload,
	})
}

// TestObservations_IndexesAndServes is the basic query path.
func TestObservations_IndexesAndServes(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	const (
		subject = "https://api.example.com/price"
		agentID = "agent:eip155:8453:0x00000000000000000000000000000000000000a1"
	)
	contentHash := "sha256:" + strings.Repeat("7b", 32)
	receiptID := "0x" + strings.Repeat("ab", 32)
	resp := publishReceipt(t, srv.URL, receiptID, agentID,
		receiptPayload(receiptID, agentID, subject, contentHash, "sha256:"+strings.Repeat("c1", 32), 42))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publishing a receipt returned %d", resp.StatusCode)
	}

	got := get(t, client, srv.URL+"/observations?subject="+subject)
	if got.StatusCode != http.StatusOK {
		t.Fatalf("query returned %d: %s", got.StatusCode, readAll(t, got))
	}
	var out struct {
		Subject string `json:"subject"`
		Count   int    `json:"count"`
		Entries []struct {
			ReceiptID   string `json:"receiptId"`
			ContentHash string `json:"contentHash"`
			AgentID     string `json:"agentId"`
			Verified    bool   `json:"verified"`
		} `json:"entries"`
		Note string `json:"note"`
	}
	decode(t, got, &out)

	if out.Count != 1 {
		t.Fatalf("count = %d, want 1", out.Count)
	}
	if out.Entries[0].ReceiptID != receiptID {
		t.Errorf("receipt id = %q, want %q", out.Entries[0].ReceiptID, receiptID)
	}
	if out.Entries[0].ContentHash != contentHash {
		t.Errorf("content hash = %q, want %q", out.Entries[0].ContentHash, contentHash)
	}
	if !strings.Contains(out.Note, "does not verify") {
		t.Errorf("the response must say the node did not verify, got: %q", out.Note)
	}
	if out.Entries[0].Verified {
		t.Error("an indexed observation must never be reported as verified")
	}
}

// TestObservations_UnindexableReceiptStillDelivers is the delivery property.
//
// A receipt the index cannot read is still a message the node must store and forward.
// Dropping it because an optional index did not like it would turn an indexing limitation
// into a delivery failure — the node would be silently censoring traffic it could not
// understand.
func TestObservations_UnindexableReceiptStillDelivers(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	const agentID = "agent:eip155:8453:0x00000000000000000000000000000000000000a2"
	receiptID := "0x" + strings.Repeat("cd", 32)
	// Valid JSON, but no receiptId and no url: nothing the index can key on.
	payload := []byte(`{"schema":"relayfirst.receipt.v1","note":"not really a receipt"}`)

	resp := publishReceipt(t, srv.URL, receiptID, agentID, payload)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an unindexable receipt must still be accepted for delivery, got %d", resp.StatusCode)
	}

	// And it must be retrievable, proving it was stored.
	pull := get(t, client, srv.URL+"/messages/"+agentID)
	var msgs struct {
		Count int `json:"count"`
	}
	decode(t, pull, &msgs)
	if msgs.Count != 1 {
		t.Errorf("the unindexable message must still be stored, got %d messages", msgs.Count)
	}
}

// TestObservations_EmptySubjectIsRejected keeps the query honest: a subject is the key the
// table exists for, and an empty one would match nothing while looking like a valid query.
func TestObservations_EmptySubjectIsRejected(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	resp := get(t, srv.Client(), srv.URL+"/observations")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a query with no subject must be rejected, got %d", resp.StatusCode)
	}
}

// TestEvidence_CountsDistinctAgentsNotRows is the property that stops one agent from
// manufacturing agreement.
//
// Two receipts from the same agent are one claim repeated. Counting rows would let a single
// agent submit ten times and appear to be ten independent observers, which would defeat the
// only thing a cross-verification view is for.
func TestEvidence_CountsDistinctAgentsNotRows(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	const (
		subject  = "https://api.example.com/popular"
		agentOne = "agent:eip155:8453:0x00000000000000000000000000000000000000b1"
	)
	contentHash := "sha256:" + strings.Repeat("aa", 32)

	// The same agent submits three times.
	for i, id := range []string{
		"0x" + strings.Repeat("11", 32),
		"0x" + strings.Repeat("12", 32),
		"0x" + strings.Repeat("13", 32),
	} {
		resp := publishReceipt(t, srv.URL, id, agentOne,
			receiptPayload(id, agentOne, subject, contentHash, "sha256:"+strings.Repeat("c1", 32), uint64(40+i)))
		resp.Body.Close()
	}

	ev := get(t, client, srv.URL+"/observations/0x"+strings.Repeat("11", 32)+"/evidence")
	if ev.StatusCode != http.StatusOK {
		t.Fatalf("evidence returned %d: %s", ev.StatusCode, readAll(t, ev))
	}
	var out struct {
		Subject string `json:"subject"`
		Groups  []struct {
			ContentHash    string `json:"contentHash"`
			DistinctAgents int    `json:"distinctAgents"`
			Observations   int    `json:"observations"`
		} `json:"groups"`
		Note string `json:"note"`
	}
	decode(t, ev, &out)

	if len(out.Groups) != 1 {
		t.Fatalf("one content hash must produce one group, got %d", len(out.Groups))
	}
	g := out.Groups[0]
	if g.Observations != 3 {
		t.Errorf("observations = %d, want 3", g.Observations)
	}
	if g.DistinctAgents != 1 {
		t.Errorf("distinctAgents = %d, want 1: three receipts from one agent are ONE claim, "+
			"and counting rows would let a single agent manufacture agreement", g.DistinctAgents)
	}
	if !strings.Contains(out.Note, "collude") {
		t.Errorf("the response must warn that agreement is not proof, got: %q", out.Note)
	}
}

// TestEvidence_DisagreementStaysVisible is the other half: two content hashes for one
// subject must produce two groups rather than being collapsed into one count.
func TestEvidence_DisagreementStaysVisible(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	const subject = "https://api.example.com/changing"
	hashA := "sha256:" + strings.Repeat("aa", 32)
	hashB := "sha256:" + strings.Repeat("bb", 32)

	idA := "0x" + strings.Repeat("21", 32)
	idB := "0x" + strings.Repeat("22", 32)
	publishReceipt(t, srv.URL, idA, "agent:eip155:8453:0x00000000000000000000000000000000000000c1",
		receiptPayload(idA, "agent:eip155:8453:0x00000000000000000000000000000000000000c1", subject, hashA, "sha256:"+strings.Repeat("c1", 32), 42)).Body.Close()
	publishReceipt(t, srv.URL, idB, "agent:eip155:8453:0x00000000000000000000000000000000000000c2",
		receiptPayload(idB, "agent:eip155:8453:0x00000000000000000000000000000000000000c2", subject, hashB, "sha256:"+strings.Repeat("c2", 32), 43)).Body.Close()

	ev := get(t, client, srv.URL+"/observations/"+idA+"/evidence")
	var out struct {
		Groups []struct {
			ContentHash    string `json:"contentHash"`
			DistinctAgents int    `json:"distinctAgents"`
		} `json:"groups"`
	}
	decode(t, ev, &out)

	if len(out.Groups) != 2 {
		t.Fatalf("two different content hashes must produce two groups, got %d: "+
			"collapsing them would hide a disagreement behind one count", len(out.Groups))
	}
	// Each group must have exactly one agent: they are different observers with different
	// claims, which is a disagreement and not a consensus.
	for _, g := range out.Groups {
		if g.DistinctAgents != 1 {
			t.Errorf("group %s has %d agents, want 1", g.ContentHash[:16], g.DistinctAgents)
		}
	}
}

// TestEvidence_IndependentAgreementCounts is the control: genuinely different agents
// agreeing must be counted as agreement, or the view would never report anything useful.
func TestEvidence_IndependentAgreementCounts(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	const subject = "https://api.example.com/stable"
	contentHash := "sha256:" + strings.Repeat("dd", 32)
	agents := []string{
		"agent:eip155:8453:0x00000000000000000000000000000000000000d1",
		"agent:eip155:8453:0x00000000000000000000000000000000000000d2",
	}
	var firstID string
	for i, a := range agents {
		id := "0x" + strings.Repeat("3"+string(rune('0'+i)), 32)
		if i == 0 {
			firstID = id
		}
		publishReceipt(t, srv.URL, id, a,
			receiptPayload(id, a, subject, contentHash, "sha256:"+strings.Repeat("c1", 32), 42)).Body.Close()
	}

	ev := get(t, client, srv.URL+"/observations/"+firstID+"/evidence")
	var out struct {
		Groups []struct {
			DistinctAgents int `json:"distinctAgents"`
		} `json:"groups"`
	}
	decode(t, ev, &out)
	if len(out.Groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(out.Groups))
	}
	if out.Groups[0].DistinctAgents != 2 {
		t.Errorf("distinctAgents = %d, want 2: two independent agents agreeing is the signal",
			out.Groups[0].DistinctAgents)
	}
}

// TestEvidence_UnknownReceiptIs404 covers the lookup miss.
func TestEvidence_UnknownReceiptIs404(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	resp := get(t, srv.Client(), srv.URL+"/observations/0x"+strings.Repeat("ff", 32)+"/evidence")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("an unindexed receipt must be 404, got %d", resp.StatusCode)
	}
}

// TestObservations_DuplicateIndexesOnce is the idempotence rule.
//
// Two rows for one receipt would inflate the cross-verification count, and that count is
// the one number a consumer reads. A duplicate arrives routinely: a sender whose
// acknowledgement was lost retries.
func TestObservations_DuplicateIndexesOnce(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	const (
		subject = "https://api.example.com/dup"
		agentID = "agent:eip155:8453:0x00000000000000000000000000000000000000e1"
	)
	receiptID := "0x" + strings.Repeat("41", 32)
	payload := receiptPayload(receiptID, agentID, subject, "sha256:"+strings.Repeat("ee", 32),
		"sha256:"+strings.Repeat("c1", 32), 42)

	publishReceipt(t, srv.URL, receiptID, agentID, payload).Body.Close()
	publishReceipt(t, srv.URL, receiptID, agentID, payload).Body.Close()

	got := get(t, client, srv.URL+"/observations?subject="+subject)
	var out struct {
		Count int `json:"count"`
	}
	decode(t, got, &out)
	if out.Count != 1 {
		t.Errorf("count = %d, want 1: a duplicate must not create a second observation, "+
			"or the cross-verification count would be wrong", out.Count)
	}
}

// TestObservations_WellKnownAdvertisesTheIndex keeps discovery honest.
func TestObservations_WellKnownAdvertisesTheIndex(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	var wk struct {
		Endpoints    []string `json:"endpoints"`
		Observations int      `json:"observations"`
	}
	decode(t, get(t, srv.Client(), srv.URL+"/.well-known/relayfirst"), &wk)

	joined := strings.Join(wk.Endpoints, " ")
	for _, want := range []string{"GET /observations", "GET /observations/{id}/evidence"} {
		if !strings.Contains(joined, want) {
			t.Errorf("well-known must advertise %q, got: %v", want, wk.Endpoints)
		}
	}
}
