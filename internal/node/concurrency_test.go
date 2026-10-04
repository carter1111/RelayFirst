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
	"sync"
	"testing"

	"github.com/relayfirst/relayfirst/internal/node"
	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// Concurrency tests for the relay node.
//
// # Why these exist
//
// The node is an HTTP server: every request runs on its own goroutine, and they all share the
// message store and the node's config. That is exactly the shape where a data race hides — it
// needs two requests overlapping to appear, which a sequential test never produces.
//
// They exist because the race-detector gate added in the previous round was documented as being
// bounded by how much concurrency the tests actually exercise, and the node had none. Without
// these, that gate was only confirming that sequential code is sequential.
//
// # What they assert beyond "no race"
//
// A race-free server can still be wrong. The properties that matter here are:
//
//   - concurrent duplicate deliveries store exactly once, because the store's ON CONFLICT is
//     what makes a retry safe under load rather than merely in a single-threaded test;
//   - no request can read another agent's mailbox, because a node that leaked one user's
//     traffic to another would be worse than useless.

// concurrentNode builds a node over a real SQLite store.
func concurrentNode(t *testing.T) (*node.Node, *sqlite.MessageStore) {
	t.Helper()

	db, err := sqlite.Open(filepath.Join(t.TempDir(), "node.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ms := sqlite.NewMessageStore(db)
	n, err := node.New(node.Config{Store: ms, Version: "concurrency-test"})
	if err != nil {
		t.Fatalf("node.New: %v", err)
	}
	return n, ms
}

// postEnvelopeRaw posts one envelope and reports the status and whether the node said it
// stored the message newly.
func postEnvelopeRaw(base string, env node.Envelope) (int, bool) {
	body, err := json.Marshal(env)
	if err != nil {
		return 0, false
	}
	resp, err := http.Post(base+"/messages", "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()

	var ack struct {
		OK     bool `json:"ok"`
		Stored bool `json:"stored"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&ack)
	return resp.StatusCode, ack.Stored
}

// TestNode_ConcurrentDuplicateDeliveriesStoreOnce is the load-bearing concurrency assertion.
//
// Many clients deliver the identical message at the same moment. The store must end with one
// row. That is what the primary key plus ON CONFLICT buys, and it is only meaningful under
// concurrency: a check-then-insert would pass a sequential test and fail here, because two
// requests could both observe "absent" before either wrote.
func TestNode_ConcurrentDuplicateDeliveriesStoreOnce(t *testing.T) {
	n, ms := concurrentNode(t)
	srv := httptest.NewServer(n.Handler())
	defer srv.Close()

	const deliveries = 32

	var (
		wg          sync.WaitGroup
		mu          sync.Mutex
		storedCount int
		badStatus   []int
	)

	env := node.Envelope{
		ID:      "0x" + strings.Repeat("ab", 32),
		AgentID: "agent:concurrent",
		Kind:    node.KindReceipt,
		Payload: []byte(`{"duplicate":true}`),
	}

	start := make(chan struct{})
	for i := 0; i < deliveries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release everyone at once, to maximise overlap
			code, stored := postEnvelopeRaw(srv.URL, env)

			mu.Lock()
			if code != http.StatusOK {
				badStatus = append(badStatus, code)
			}
			if stored {
				storedCount++
			}
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if len(badStatus) > 0 {
		t.Errorf("a duplicate delivery returned %v, want 200; a retry must not be an error", badStatus)
	}
	if storedCount != 1 {
		t.Errorf("%d deliveries reported stored=true, want exactly 1", storedCount)
	}
	if got := ms.Count(); got != 1 {
		t.Errorf("the store holds %d message(s), want 1 — concurrent duplicates must collapse", got)
	}
}

// TestNode_ConcurrentAgentsDoNotCrossContaminate runs parallel writers and readers across many
// agents and asserts every pull returns only its own agent's messages.
func TestNode_ConcurrentAgentsDoNotCrossContaminate(t *testing.T) {
	n, ms := concurrentNode(t)
	srv := httptest.NewServer(n.Handler())
	defer srv.Close()

	const agents = 8
	const perAgent = 12

	var wg sync.WaitGroup
	start := make(chan struct{})

	// Writers.
	for a := 0; a < agents; a++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			<-start
			agent := fmt.Sprintf("agent:%d", a)
			for i := 0; i < perAgent; i++ {
				env := node.Envelope{
					ID:      fmt.Sprintf("0x%02x%062x", a, i),
					AgentID: agent,
					Kind:    "event",
					Payload: []byte(fmt.Sprintf(`{"agent":%d,"n":%d}`, a, i)),
				}
				if code, _ := postEnvelopeRaw(srv.URL, env); code != http.StatusOK {
					t.Errorf("agent %d message %d: status %d", a, i, code)
					return
				}
			}
		}(a)
	}

	// Readers, overlapping the writers.
	for a := 0; a < agents; a++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			<-start
			agent := fmt.Sprintf("agent:%d", a)
			for i := 0; i < 10; i++ {
				resp, err := http.Get(srv.URL + "/messages/" + agent)
				if err != nil {
					t.Errorf("pull %s: %v", agent, err)
					return
				}
				var got struct {
					Messages []node.Envelope `json:"messages"`
				}
				_ = json.NewDecoder(resp.Body).Decode(&got)
				_ = resp.Body.Close()

				for _, m := range got.Messages {
					if m.AgentID != agent {
						t.Errorf("a pull for %s returned a message for %s", agent, m.AgentID)
						return
					}
				}
			}
		}(a)
	}

	close(start)
	wg.Wait()

	if got := ms.Count(); got != agents*perAgent {
		t.Errorf("stored %d messages, want %d", got, agents*perAgent)
	}
	if got := ms.AgentCount(); got != agents {
		t.Errorf("AgentCount = %d, want %d", got, agents)
	}

	for a := 0; a < agents; a++ {
		agent := fmt.Sprintf("agent:%d", a)
		msgs, err := ms.ByAgent(agent, 0)
		if err != nil {
			t.Fatalf("ByAgent(%s): %v", agent, err)
		}
		if len(msgs) != perAgent {
			t.Errorf("%s holds %d message(s), want %d", agent, len(msgs), perAgent)
		}
	}
}

// TestNode_ConcurrentReadsDuringWrites exercises the read path while the write path is active,
// which is where a partially-written row could be observed.
func TestNode_ConcurrentReadsDuringWrites(t *testing.T) {
	n, _ := concurrentNode(t)
	srv := httptest.NewServer(n.Handler())
	defer srv.Close()

	var wg sync.WaitGroup
	start := make(chan struct{})

	for a := 0; a < 4; a++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			<-start
			agent := fmt.Sprintf("agent:rw%d", a)
			for i := 0; i < 40; i++ {
				env := node.Envelope{
					ID:      fmt.Sprintf("0x%02x%062x", a, i),
					AgentID: agent,
					Kind:    "event",
					Payload: []byte(`{"x":1}`),
				}
				postEnvelopeRaw(srv.URL, env)
			}
		}(a)
	}

	for a := 0; a < 4; a++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			<-start
			agent := fmt.Sprintf("agent:rw%d", a)
			for i := 0; i < 40; i++ {
				resp, err := http.Get(srv.URL + "/messages/" + agent + "?limit=5")
				if err != nil {
					t.Errorf("pull: %v", err)
					return
				}
				var got struct {
					Messages []node.Envelope `json:"messages"`
				}
				_ = json.NewDecoder(resp.Body).Decode(&got)
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()

				// A limited pull must never exceed the limit, whatever the store's state.
				if len(got.Messages) > 5 {
					t.Errorf("limit=5 returned %d messages", len(got.Messages))
					return
				}
			}
		}(a)
	}

	close(start)
	wg.Wait()
}

// TestNode_ConcurrentReadsOfSharedCounters hammers the read-only endpoints alongside writes.
//
// They are read-only, but the well-known document reads shared counters, so it is worth
// covering rather than assuming.
func TestNode_ConcurrentReadsOfSharedCounters(t *testing.T) {
	n, _ := concurrentNode(t)
	srv := httptest.NewServer(n.Handler())
	defer srv.Close()

	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 40; i++ {
			env := node.Envelope{
				ID:      fmt.Sprintf("0x%062x", i),
				AgentID: "agent:wk",
				Kind:    "event",
				Payload: []byte(`{}`),
			}
			postEnvelopeRaw(srv.URL, env)
		}
	}()

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 20; j++ {
				for _, path := range []string{"/.well-known/relayfirst", "/healthz"} {
					resp, err := http.Get(srv.URL + path)
					if err != nil {
						t.Errorf("GET %s: %v", path, err)
						return
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						t.Errorf("GET %s returned %d", path, resp.StatusCode)
						return
					}
				}
			}
		}()
	}

	close(start)
	wg.Wait()
}
