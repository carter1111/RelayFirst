package noderead_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/noderead"
)

// These tests cover the read-only client. The properties that matter are the safety
// ones — a bad URL is refused, a node cannot make the client buffer unbounded data,
// and a failing node backs off — because a client is the side that must not trust the
// node.

func TestNew_RejectsNonHTTPURL(t *testing.T) {
	for _, bad := range []string{"file:///etc/passwd", "ftp://x", "localhost:8080", ""} {
		if _, err := noderead.New(bad); err == nil {
			t.Errorf("New(%q) must be refused; a non-http(s) URL fails obscurely later", bad)
		}
	}
}

func TestNew_AcceptsHTTPAndHTTPS(t *testing.T) {
	for _, ok := range []string{"http://localhost:8080", "https://relay.example", "http://127.0.0.1:1/x"} {
		if _, err := noderead.New(ok); err != nil {
			t.Errorf("New(%q) must be accepted, got %v", ok, err)
		}
	}
}

func TestReads_DecodeTheNodeDocuments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/.well-known/relayfirst":
			_, _ = w.Write([]byte(`{"name":"relayfirst-node","version":"9","messages":7,"agents":2,"verifies":false,"note":"n"}`))
		case "/agents":
			_, _ = w.Write([]byte(`{"count":1,"cards":[{"agentId":"agent:x","verified":false}]}`))
		case "/tasks":
			_, _ = w.Write([]byte(`{"count":1,"offers":[{"taskId":"t1","requester":"r","subject":"s","claims":3}]}`))
		case "/observations":
			_, _ = w.Write([]byte(`{"subject":"s","count":1,"entries":[{"receiptId":"rc","subject":"s","epoch":4}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c, err := noderead.New(srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if h, err := c.Health(); err != nil || !h.OK {
		t.Errorf("Health: %+v err=%v", h, err)
	}
	wk, err := c.WellKnown()
	if err != nil || wk.Messages != 7 || wk.Agents != 2 || wk.Name != "relayfirst-node" {
		t.Errorf("WellKnown: %+v err=%v", wk, err)
	}
	if cards, err := c.Agents(0); err != nil || len(cards) != 1 || cards[0].AgentID != "agent:x" {
		t.Errorf("Agents: %+v err=%v", cards, err)
	}
	if tasks, err := c.Tasks("", 0); err != nil || len(tasks) != 1 || tasks[0].Claims != 3 {
		t.Errorf("Tasks: %+v err=%v", tasks, err)
	}
	if obs, err := c.Observations("s", 0); err != nil || len(obs) != 1 || obs[0].Epoch != 4 {
		t.Errorf("Observations: %+v err=%v", obs, err)
	}
}

// TestObservations_RequiresSubject: the node indexes by subject, so a list without one
// is not a question it can answer; failing here is clearer than a 400 later.
func TestObservations_RequiresSubject(t *testing.T) {
	c, _ := noderead.New("http://localhost:1")
	if _, err := c.Observations("", 0); err == nil {
		t.Fatal("Observations without a subject must error client-side")
	}
}

// TestResponseIsCapped is the safety property: a node must not be able to make the
// client buffer unbounded data.
func TestResponseIsCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Comfortably over the cap.
		_, _ = w.Write([]byte(`{"name":"` + strings.Repeat("a", noderead.MaxResponseBytes+16) + `"}`))
	}))
	defer srv.Close()

	c, _ := noderead.New(srv.URL)
	_, err := c.WellKnown()
	if err == nil {
		t.Fatal("a response over the cap must be refused, not buffered")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("the error should name the cap, got: %v", err)
	}
}

// TestBackoff_GrowsOnFailureAndResetsOnSuccess: the client must not hammer a down node.
func TestBackoff_GrowsOnFailureAndResetsOnSuccess(t *testing.T) {
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c, _ := noderead.New(srv.URL)

	if c.Backoff() != 0 {
		t.Fatalf("a fresh client must start with no backoff, got %v", c.Backoff())
	}
	_, _ = c.Health()
	first := c.Backoff()
	if first <= 0 {
		t.Fatal("a failure must set a backoff")
	}
	_, _ = c.Health()
	if c.Backoff() <= first {
		t.Errorf("backoff must grow on consecutive failures: %v then %v", first, c.Backoff())
	}

	fail = false
	if _, err := c.Health(); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if c.Backoff() != 0 {
		t.Errorf("a success must reset the backoff, got %v", c.Backoff())
	}
}

// TestErrorNamesTheStatus keeps a non-200 from looking like an empty result.
func TestErrorNamesTheStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"subject is required"}`))
	}))
	defer srv.Close()

	c, _ := noderead.New(srv.URL)
	_, err := c.WellKnown()
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("a 400 must be reported with its status, got: %v", err)
	}
}

// TestEvidence_TakesAReceiptID: the node's evidence route is keyed by receipt id, not by
// subject, so the client must address it that way. Getting this wrong is a silent 404.
func TestEvidence_TakesAReceiptID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/observations/rc-1/evidence" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"subject":"https://a.example/x","groups":[{"contentHash":"0xdef","distinctAgents":3,"observations":4,"receiptIds":["rc-1"]}]}`))
	}))
	defer srv.Close()

	c, _ := noderead.New(srv.URL)
	subject, groups, err := c.Evidence("rc-1")
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	if subject != "https://a.example/x" {
		t.Errorf("subject = %q", subject)
	}
	if len(groups) != 1 || groups[0].DistinctAgents != 3 {
		t.Errorf("groups = %+v, want one group with 3 distinct agents", groups)
	}
}

func TestEvidence_RequiresAReceiptID(t *testing.T) {
	c, _ := noderead.New("http://localhost:1")
	if _, _, err := c.Evidence(""); err == nil {
		t.Fatal("Evidence without a receipt id must error client-side")
	}
}
