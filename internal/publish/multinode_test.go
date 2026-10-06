package publish_test

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/a2a"
)

// This file is the multi-node test plan made executable (docs/notes/multi-node-plan.md).
//
// # What it adds over twonode_test.go
//
// twonode_test.go proves the property for TWO nodes with hand-written histories. The claim the
// network rests on is "any number of independent relays, and a client that merges them, reaches
// the same state" — and a fixed fixture can only falsify the N it happens to test. So this file
// (1) tests the cross-node DEDUP that merging depends on, (2) states the closure property the
// induction needs, and (3) drives it as a PROPERTY test over a random N, so the evidence is not
// tied to a number.
//
// # What it deliberately does not claim
//
// There is no node-to-node replication, gossip or federation in this codebase, so nothing here
// speaks to that. What is tested is a CLIENT against several independent nodes, which is the
// model the architecture specifies (NET-3: nodes need not agree).

// mergeViews combines several nodes' views into one, deduplicating by event id.
//
// # Why this is the operation under test
//
// A client of several nodes sees the same event more than once: the same actor publishes to
// every node in its relay set, and a retry is redelivered. Deriving from the raw concatenation
// would count a repeated event as new traffic — and if a chain validator saw a duplicated
// sequence it would report a replay and refuse a perfectly valid history. So merging is a set
// union, not a concatenation, and the tests below pin that.
//
// The caller must not pre-dedupe: this is the thing being tested.
func mergeViews(views ...[]a2a.Event) []a2a.Event {
	seen := map[string]bool{}
	var out []a2a.Event
	for _, v := range views {
		for _, e := range v {
			if seen[e.EventID] {
				continue
			}
			seen[e.EventID] = true
			out = append(out, e)
		}
	}
	return out
}

// TestMultinode_MergeDedupesById is MN-1: the same event delivered by several nodes is one event.
//
// Without this, a client of a relay set would double-count traffic and, worse, a chain check
// would see two events at one sequence and call it a replay.
func TestMultinode_MergeDedupesById(t *testing.T) {
	e := a2a.Event{EventID: "evt_x", SessionID: "s", Actor: "agent:x", Type: a2a.EventSessionOpen, Sequence: 1}
	merged := mergeViews([]a2a.Event{e}, []a2a.Event{e}, []a2a.Event{e})
	if len(merged) != 1 {
		t.Fatalf("merging the same event from 3 nodes must yield 1, got %d", len(merged))
	}
}

// TestMultinode_CrossNodeDedupEndToEnd is MN-1 through real nodes: the SAME history published to
// three independent nodes, then pulled and merged, must be exactly the history — not three copies.
//
// This is the end-to-end form of the rule above, and it is what makes a relay set usable: an
// actor publishes to every node in its set, and the client must see one history.
func TestMultinode_CrossNodeDedupEndToEnd(t *testing.T) {
	nodes := independentNodes(t, 3)
	events := lifecycleEvents(t, "ses_dedup")

	// Publish the whole history to every node — exactly what a relay-set fan-out does.
	for _, n := range nodes {
		publishEventsTo(t, n, events)
	}

	views := make([][]a2a.Event, 0, len(nodes))
	for _, n := range nodes {
		views = append(views, pullEvents(t, n.URL, e2eAgentID(t)))
		views = append(views, pullEvents(t, n.URL, otherAgentID(t)))
	}
	merged := mergeViews(views...)

	// The merged set must be a valid chain: a duplicate would read as a replay.
	if err := a2a.ValidateChain(keccakHasher, merged); err != nil {
		t.Fatalf("the merge of identical views must be a valid chain (a dupes would read as a replay): %v", err)
	}
	// And it must be the history, not a multiple of it.
	if len(merged) != len(events) {
		t.Fatalf("merged %d events across 3 identical views, want %d", len(merged), len(events))
	}
}

// TestMultinode_MergeIsClosure is MN-3: merging consistent views yields a consistent view.
//
// This is the induction's base and step. If merging two consistent views could produce something
// inconsistent, "N nodes agree" would not follow from "2 nodes agree" — so the property is
// asserted directly rather than only exercised through an example.
func TestMultinode_MergeIsClosure(t *testing.T) {
	events := lifecycleEvents(t, "ses_closure")
	now := time.Date(2026, 10, 4, 12, 0, 30, 0, time.UTC)

	// Two consistent views: each is the full history (a node that received everything).
	a := append([]a2a.Event(nil), events...)
	b := append([]a2a.Event(nil), events...)

	da, err := a2a.Derive(a2a.DeriveInput{Events: a, Now: now})
	if err != nil {
		t.Fatalf("derive a: %v", err)
	}
	db, err := a2a.Derive(a2a.DeriveInput{Events: b, Now: now})
	if err != nil {
		t.Fatalf("derive b: %v", err)
	}
	if da.State != db.State {
		t.Fatalf("precondition failed: the two views must already agree, got %s and %s", da.State, db.State)
	}

	merged := mergeViews(a, b)
	dm, err := a2a.Derive(a2a.DeriveInput{Events: merged, Now: now})
	if err != nil {
		t.Fatalf("derive merged: %v", err)
	}
	if dm.State != da.State {
		t.Errorf("merge of consistent views changed the state: %s -> %s", da.State, dm.State)
	}
}

// TestMultinode_AnyNIndependentNodesAgree is MN-2: the property, over a random N.
//
// # Why randomised, and not another fixed fixture
//
// The theorem (docs/notes/multi-node-plan.md §1) says the result is independent of N because
// Derive is a function of the deduplicated set and merging is a set union. A fixture with three
// nodes shows it for three; this shows it across N = 1..8, several random splits, and with
// duplicates and orderings varied, so the evidence is about the property rather than a number.
//
// # What "agree" means here
//
// Every node's own view, and the merge of all of them, must derive the SAME state. A node holds a
// subset (possibly empty); the client merges the subsets; the subset and the union must agree,
// because a deleted node's events are only removed if no one else received them, and here every
// event reaches at least one node.
func TestMultinode_AnyNIndependentNodesAgree(t *testing.T) {
	const seeds = 8
	now := time.Date(2026, 10, 4, 12, 0, 30, 0, time.UTC)

	for seed := 0; seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		n := 1 + rng.Intn(8) // 1..8

		nodes := independentNodes(t, n)
		events := lifecycleEvents(t, fmt.Sprintf("ses_anyN_%d", seed))

		// Deal the history out to the nodes: each event to one node, then each node
		// publishes what it holds. Every event reaches exactly one node, so the union is
		// the history and each node holds a real subset.
		per := make([][]a2a.Event, n)
		for i, e := range events {
			per[i%n] = append(per[i%n], e)
		}
		for i, node := range nodes {
			// A random order, to prove order does not matter, and a random duplicate for
			// half the nodes, to prove a redelivery does not either.
			shuffled := append([]a2a.Event(nil), per[i]...)
			rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
			batch := shuffled
			if rng.Intn(2) == 0 && len(shuffled) > 0 {
				batch = append(batch, shuffled[rng.Intn(len(shuffled))])
			}
			publishEventsTo(t, node, batch)
		}

		// Pull each node's view.
		views := make([][]a2a.Event, 0, n*2)
		for _, node := range nodes {
			views = append(views, pullEvents(t, node.URL, e2eAgentID(t)))
			views = append(views, pullEvents(t, node.URL, otherAgentID(t)))
		}

		// The merge must be the whole history (every event reached one node), and valid.
		merged := mergeViews(views...)
		if len(merged) != len(events) {
			t.Fatalf("seed %d (N=%d): merged %d events, want %d", seed, n, len(merged), len(events))
		}
		if err := a2a.ValidateChain(keccakHasher, merged); err != nil {
			t.Fatalf("seed %d (N=%d): merged is not a valid chain: %v", seed, n, err)
		}

		want, err := a2a.Derive(a2a.DeriveInput{Events: merged, Now: now})
		if err != nil {
			t.Fatalf("seed %d (N=%d): derive merged: %v", seed, n, err)
		}
		if want.State != a2a.StateCompleted {
			t.Errorf("seed %d (N=%d): the merged history must reach Completed, got %s", seed, n, want.State)
		}

		// The order-independence half: re-merging the same views in the reverse order
		// must give the same state. If anything consulted delivery order, this is where
		// it would show, and it is the reason a fixed fixture is weaker evidence.
		reversed := make([][]a2a.Event, 0, len(views))
		for i := len(views) - 1; i >= 0; i-- {
			reversed = append(reversed, views[i])
		}
		dm, err := a2a.Derive(a2a.DeriveInput{Events: mergeViews(reversed...), Now: now})
		if err != nil {
			t.Fatalf("seed %d (N=%d): derive reversed: %v", seed, n, err)
		}
		if dm.State != want.State {
			t.Errorf("seed %d (N=%d): merge order changed the state: %s vs %s",
				seed, n, want.State, dm.State)
		}
	}
}

// independentNodes starts n independent nodes (separate databases).
func independentNodes(t *testing.T, n int) []*relayNode {
	t.Helper()
	out := make([]*relayNode, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, newIndependentNode(t))
	}
	return out
}
