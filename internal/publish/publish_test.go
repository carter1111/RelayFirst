package publish_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/node"
	"github.com/relayfirst/relayfirst/internal/publish"
)

func env() node.Envelope {
	return node.Envelope{
		ID:      "0x" + "ab",
		AgentID: "agent:eip155:8453:0x1",
		Kind:    "receipt",
		Payload: []byte(`{"hello":"world"}`),
	}
}

// fakeRelay is a minimal node that records what it received.
type fakeRelay struct {
	*httptest.Server
	received atomic.Int64
	failWith int
}

func newFakeRelay(t *testing.T, failWith int) *fakeRelay {
	t.Helper()

	f := &fakeRelay{failWith: failWith}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got node.Envelope
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.received.Add(1)

		if f.failWith != 0 {
			w.WriteHeader(f.failWith)
			_, _ = w.Write([]byte(`{"ok":false,"error":"synthetic failure"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"stored":true}`))
	}))
	t.Cleanup(f.Close)
	return f
}

// TestPublisher_DeliversToAllRelays: fan-out reaches every configured node.
func TestPublisher_DeliversToAllRelays(t *testing.T) {
	a := newFakeRelay(t, 0)
	b := newFakeRelay(t, 0)

	p, err := publish.New(a.URL, b.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := p.Publish(context.Background(), env())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !out.OK() {
		t.Fatal("outcome should be OK when both relays acknowledged")
	}
	if out.Acked != 2 || out.Failed != 0 {
		t.Errorf("acked/failed = %d/%d, want 2/0", out.Acked, out.Failed)
	}
	if a.received.Load() != 1 || b.received.Load() != 1 {
		t.Errorf("received a=%d b=%d, want 1 each", a.received.Load(), b.received.Load())
	}
	// A full success must not look like a failure to the caller.
	if err := out.Error(); err != nil {
		t.Errorf("Error() = %v, want nil when every relay acknowledged", err)
	}
}

// TestPublisher_OneRelayDownStillDelivers is the S5-9 acceptance case.
//
// This is the property the fan-out exists for: one node being unreachable must not
// lose the work, because the receipt is the only proof it happened.
func TestPublisher_OneRelayDownStillDelivers(t *testing.T) {
	up := newFakeRelay(t, 0)

	// A server that is already closed: every request to it fails at the transport.
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	downURL := down.URL
	down.Close()

	p, err := publish.New(up.URL, downURL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := p.Publish(context.Background(), env())
	if err != nil {
		t.Fatalf("Publish returned an error for a partial delivery: %v", err)
	}

	// The important assertion: the message got through to at least one node.
	if !out.OK() {
		t.Fatal("a delivery to one healthy relay must count as success")
	}
	if out.Acked != 1 || out.Failed != 1 {
		t.Errorf("acked/failed = %d/%d, want 1/1", out.Acked, out.Failed)
	}
	if up.received.Load() != 1 {
		t.Errorf("the healthy relay received %d message(s), want 1", up.received.Load())
	}

	// The failure must still be reportable, so an operator can see the dead node
	// even though the delivery succeeded.
	if err := out.Error(); err == nil {
		t.Error("Error() should report the failed relay even when the delivery succeeded overall")
	}
}

// TestPublisher_AllRelaysDown is the genuine failure case.
func TestPublisher_AllRelaysDown(t *testing.T) {
	var urls []string
	for i := 0; i < 2; i++ {
		s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		urls = append(urls, s.URL)
		s.Close()
	}

	p, err := publish.New(urls...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := p.Publish(context.Background(), env())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if out.OK() {
		t.Error("outcome must not be OK when every relay failed")
	}
	if out.Acked != 0 || out.Failed != 2 {
		t.Errorf("acked/failed = %d/%d, want 0/2", out.Acked, out.Failed)
	}
}

// TestPublisher_HTTPErrorIsNotSuccess: a relay answering 500 did not take the
// message, and counting it as delivered would silently lose work.
func TestPublisher_HTTPErrorIsNotSuccess(t *testing.T) {
	bad := newFakeRelay(t, http.StatusInternalServerError)
	good := newFakeRelay(t, 0)

	p, err := publish.New(bad.URL, good.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := p.Publish(context.Background(), env())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !out.OK() {
		t.Fatal("the healthy relay should still make the delivery a success")
	}

	// The failing relay must be reported as failed, with the node's own reason
	// surfaced rather than just a status code.
	var found bool
	for _, r := range out.Results {
		if r.OK {
			continue
		}
		found = true
		if r.Err == nil {
			t.Error("a failed relay must carry an error")
		}
	}
	if !found {
		t.Error("the relay returning 500 should be reported as failed")
	}
}

// TestPublisher_TimeoutIsBounded: one unresponsive node must not stall the fan-out.
func TestPublisher_TimeoutIsBounded(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Outlive the publisher's timeout without ever answering.
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer slow.Close()

	fast := newFakeRelay(t, 0)

	p, err := publish.New(slow.URL, fast.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p.Client = &http.Client{Timeout: 200 * time.Millisecond}

	start := time.Now()
	out, err := p.Publish(context.Background(), env())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !out.OK() {
		t.Error("the fast relay should still have received the message")
	}
	// The bound is what matters: without it the whole fan-out waits on the slow
	// node and the miner stalls with it.
	if elapsed > 2*time.Second {
		t.Errorf("fan-out took %v; a slow relay must not stall delivery", elapsed)
	}
	if fast.received.Load() != 1 {
		t.Error("the fast relay should have received the message despite the slow one")
	}
}

// TestPublisher_ResultsAreStable: results follow the configured relay order, so a
// report does not reshuffle between runs.
func TestPublisher_ResultsAreStable(t *testing.T) {
	a := newFakeRelay(t, 0)
	b := newFakeRelay(t, 0)
	c := newFakeRelay(t, 0)

	p, err := publish.New(a.URL, b.URL, c.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := p.Publish(context.Background(), env())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(out.Results) != 3 {
		t.Fatalf("got %d results, want 3", len(out.Results))
	}

	want := []string{a.URL, b.URL, c.URL}
	for i, r := range out.Results {
		if r.Relay != want[i] {
			t.Errorf("result %d relay = %q, want %q", i, r.Relay, want[i])
		}
	}
}

func TestPublisher_RequiresRelays(t *testing.T) {
	if _, err := publish.New(); err == nil {
		t.Error("a publisher with no relays must be refused")
	}
	if _, err := publish.New("", "   "); err == nil {
		t.Error("a publisher with only blank URLs must be refused")
	}
}

// TestPublisher_NamedRelayIsUsedInReports: an operator distinguishing nodes in a
// log needs a label, not just a URL.
func TestPublisher_NamedRelayIsUsedInReports(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	downURL := down.URL
	down.Close()

	p := &publish.Publisher{
		Relays: []publish.Relay{{Name: "primary", URL: downURL}},
		Client: &http.Client{Timeout: time.Second},
	}

	out, err := p.Publish(context.Background(), env())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(out.Results))
	}
	if out.Results[0].Relay != "primary" {
		t.Errorf("relay label = %q, want the configured name", out.Results[0].Relay)
	}
}
