package publish_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/a2a"
	"github.com/relayfirst/relayfirst/internal/node"
)

// This is the two-node interoperability proof (P1 #5).
//
// # Why a single-node test is not enough
//
// S9-11 ran the whole lifecycle through ONE node, which proves the pieces compose but not the
// property the network rests on. ARCHITECTURE.md §4.2 says there is no global order: two
// independent relays receive the same events in different orders, and a client that derived a
// task state from arrival order would get two different answers.
//
// The protocol's answer is that determinism comes from the SIGNED DATA — per-actor sequence
// plus hash chain — and never from how anything was delivered. That claim is only demonstrated
// by actually delivering through two independent nodes with deliberately different orders and
// checking the derived state matches.
//
// # What could go wrong that this catches
//
//   - The node re-serialized an event, breaking the chain (already covered for one node, but a
//     second node with different storage is a second chance to get it wrong).
//   - The derivation consulted delivery order anywhere, which a single-node test would not
//     reveal because one node delivers in one order.
//   - The chain validation depended on an arrival pattern rather than on the events.

// twoNodes returns two independent nodes backed by separate databases.
//
// Separate databases matter: a shared store would make them one node with two front doors, and
// would not test independence at all.
func twoNodes(t *testing.T) (*relayNode, *relayNode) {
	t.Helper()
	return newIndependentNode(t), newIndependentNode(t)
}

// relayNode is a minimal named wrapper so tests can talk about "node A" and "node B" clearly.
type relayNode struct {
	URL   string
	close func()
}

func newIndependentNode(t *testing.T) *relayNode {
	t.Helper()
	srv := newE2ENode(t)
	return &relayNode{URL: srv.URL, close: srv.Close}
}

// publishEventsTo sends events to a node in the given order.
//
// The order is a PARAMETER precisely because the test is about order not mattering. Reversing
// it at one node while the other receives the natural order is the scenario the protocol must
// survive.
func publishEventsTo(t *testing.T, n *relayNode, events []a2a.Event) {
	t.Helper()
	for _, e := range events {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		body, err := json.Marshal(map[string]any{
			"id": e.EventID, "agentId": e.Actor, "kind": string(node.KindEvent), "payload": raw,
		})
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		resp, err := http.Post(n.URL+"/messages", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatalf("post to %s: %v", n.URL, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("publishing %s to %s returned %d", e.EventID, n.URL, resp.StatusCode)
		}
	}
}

// TestTwoNodeInterop_SameEventsDifferentOrdersDeriveTheSameState is the core property.
//
// The same history is delivered to two independent nodes in DIFFERENT orders, both orders are
// pulled back, and the derived task state must match. If derivation consulted delivery order,
// the two would disagree — and a disagreement about state is the fork the protocol exists to
// prevent.
func TestTwoNodeInterop_SameEventsDifferentOrdersDeriveTheSameState(t *testing.T) {
	nodeA, nodeB := twoNodes(t)

	events := lifecycleEvents(t, "ses_two_node")
	now := time.Date(2026, 10, 4, 12, 0, 30, 0, time.UTC)

	// Node A receives the natural order.
	publishEventsTo(t, nodeA, events)

	// Node B receives the REVERSE order. A relay is free to deliver in whatever order it
	// stored things, and this models the least favourable case.
	reversed := make([]a2a.Event, len(events))
	for i := range events {
		reversed[len(events)-1-i] = events[i]
	}
	publishEventsTo(t, nodeB, reversed)

	// Pull from both and derive.
	pullFrom := func(n *relayNode) []a2a.Event {
		a := pullEvents(t, n.URL, e2eAgentID(t))
		b := pullEvents(t, n.URL, otherAgentID(t))
		return append(append([]a2a.Event{}, a...), b...)
	}
	fromA, fromB := pullFrom(nodeA), pullFrom(nodeB)

	if len(fromA) != len(events) {
		t.Fatalf("node A returned %d events, want %d", len(fromA), len(events))
	}
	if len(fromB) != len(events) {
		t.Fatalf("node B returned %d events, want %d", len(fromB), len(events))
	}

	// Each node's own store must yield a valid chain.
	if err := a2a.ValidateChain(keccakHasher, fromA); err != nil {
		t.Fatalf("node A's events do not form a valid chain: %v", err)
	}
	if err := a2a.ValidateChain(keccakHasher, fromB); err != nil {
		t.Fatalf("node B's events do not form a valid chain: %v", err)
	}

	derivedA, err := a2a.Derive(a2a.DeriveInput{Events: fromA, Now: now})
	if err != nil {
		t.Fatalf("derive from node A: %v", err)
	}
	derivedB, err := a2a.Derive(a2a.DeriveInput{Events: fromB, Now: now})
	if err != nil {
		t.Fatalf("derive from node B: %v", err)
	}

	if derivedA.State != derivedB.State {
		t.Fatalf("two independent nodes derived different states (%s vs %s) from the same events; "+
			"determinism must come from the signed data, not from delivery order (ARCHITECTURE.md §4.2)",
			derivedA.State, derivedB.State)
	}
	if derivedA.State != a2a.StateCompleted {
		t.Errorf("state = %s, want %s", derivedA.State, a2a.StateCompleted)
	}
}

// TestTwoNodeInterop_ClientUnionOfBothNodesDerivesTheSameState is the multi-relay client case.
//
// A client that does not trust any one node may collect events from several. The union must
// still derive the same state — and it must, because the client sorts by the protocol's rules
// rather than by which node answered first.
func TestTwoNodeInterop_ClientUnionOfBothNodesDerivesTheSameState(t *testing.T) {
	nodeA, nodeB := twoNodes(t)

	events := lifecycleEvents(t, "ses_union")
	now := time.Date(2026, 10, 4, 12, 0, 30, 0, time.UTC)

	// Split the history across the two nodes, as a partial view of each would be.
	publishEventsTo(t, nodeA, events[:len(events)/2])
	publishEventsTo(t, nodeB, events[len(events)/2:])

	var union []a2a.Event
	union = append(union, pullEvents(t, nodeA.URL, e2eAgentID(t))...)
	union = append(union, pullEvents(t, nodeA.URL, otherAgentID(t))...)
	union = append(union, pullEvents(t, nodeB.URL, e2eAgentID(t))...)
	union = append(union, pullEvents(t, nodeB.URL, otherAgentID(t))...)

	if err := a2a.ValidateChain(keccakHasher, union); err != nil {
		t.Fatalf("the union of two nodes must form a valid chain: %v", err)
	}
	derived, err := a2a.Derive(a2a.DeriveInput{Events: union, Now: now})
	if err != nil {
		t.Fatalf("derive from the union: %v", err)
	}
	if derived.State != a2a.StateCompleted {
		t.Errorf("state = %s, want %s: a client combining two partial views must still reach "+
			"the same conclusion", derived.State, a2a.StateCompleted)
	}
}

// TestTwoNodeInterop_OneNodeDownStillWorks is the availability half, and it uses only what
// exists: the client pulls from both and tolerates one being unreachable.
//
// This is deliberately NOT a quorum/healing test. Per-relay health tracking and failover policy
// are P1 #4 and are not implemented; claiming otherwise would be the kind of overstatement this
// project keeps correcting. What is proven here is narrower and true: a client that tries two
// nodes and finds one dead still reaches the right state from the other.
func TestTwoNodeInterop_OneNodeDownStillWorks(t *testing.T) {
	nodeA := newIndependentNode(t)
	dead := newIndependentNode(t)
	deadURL := dead.URL
	dead.close()

	events := lifecycleEvents(t, "ses_one_down")
	now := time.Date(2026, 10, 4, 12, 0, 30, 0, time.UTC)
	publishEventsTo(t, nodeA, events)

	// The client tries the dead node first, as a multi-relay client would.
	var union []a2a.Event
	for _, url := range []string{deadURL, nodeA.URL} {
		got := tryPullEvents(url, e2eAgentID(t))
		union = append(union, got...)
		union = append(union, tryPullEvents(url, otherAgentID(t))...)
	}

	if len(union) == 0 {
		t.Fatal("the live node must still serve the events")
	}
	derived, err := a2a.Derive(a2a.DeriveInput{Events: union, Now: now})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if derived.State != a2a.StateCompleted {
		t.Errorf("state = %s, want %s: one unreachable node must not prevent the client from "+
			"reaching the right state", derived.State, a2a.StateCompleted)
	}
}

// TestTwoNodeInterop_NodesAreActuallyIndependent guards the fixture itself.
//
// If the two nodes shared storage the test above would pass trivially — one node with two
// addresses, not two independent relays. This checks they hold separate data.
func TestTwoNodeInterop_NodesAreActuallyIndependent(t *testing.T) {
	nodeA, nodeB := twoNodes(t)

	events := lifecycleEvents(t, "ses_independence")
	publishEventsTo(t, nodeA, events)

	// B received nothing, so it must have nothing. If it answered, the nodes share a store and
	// every other test in this file would be vacuous.
	fromB := pullEvents(t, nodeB.URL, e2eAgentID(t))
	if len(fromB) != 0 {
		t.Fatalf("node B returned %d events it was never sent, so the two nodes are not "+
			"independent and these tests prove nothing", len(fromB))
	}
}

// tryPullEvents pulls without failing the test, returning nothing when the node is unreachable.
func tryPullEvents(baseURL, agentID string) []a2a.Event {
	resp, err := http.Get(baseURL + "/messages/" + agentID)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var out struct {
		Messages []struct {
			Kind    string `json:"kind"`
			Payload []byte `json:"payload"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil
	}
	var events []a2a.Event
	for _, m := range out.Messages {
		if m.Kind != string(node.KindEvent) {
			continue
		}
		e, err := a2a.DecodeEvent(m.Payload)
		if err != nil {
			continue
		}
		events = append(events, e)
	}
	return events
}
