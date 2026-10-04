package publish_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/node"
	"github.com/relayfirst/relayfirst/internal/publish"
)

// Concurrency tests for the relay fan-out.
//
// # Why these exist
//
// Delivery was sequential: a loop over the relays, each with its own timeout. That means N
// relays gave a worst case of N timeouts, so one unresponsive node added its full timeout to
// every publication — and a miner pays that on every iteration.
//
// Making it concurrent changes the bound to one timeout. These tests assert that bound
// *measurably*, not just structurally, because "it uses goroutines now" is not evidence that the
// delay went away. They also close the publish gap recorded in the S4 report, since publish had
// no concurrent tests because it had no concurrency.
//
// # What they assert beyond "no race"
//
// Reporting order must still follow the configured relay order. Concurrent results reported in
// arrival order would make two runs incomparable, which is the opposite of what an operator
// wants from a delivery report.

// slowRelay starts a server that sleeps for d before acknowledging.
func slowRelay(t *testing.T, d time.Duration) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(d):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"stored":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPublisher_FanOutIsBoundedByTheSlowestRelay is the measurement that justifies the change.
//
// Five relays each taking 300ms: sequentially that is ~1.5s, concurrently ~300ms. The assertion
// is deliberately loose (under half the sequential time) so it is not flaky on a loaded machine,
// while still failing loudly if the fan-out reverts to sequential.
func TestPublisher_FanOutIsBoundedByTheSlowestRelay(t *testing.T) {
	const relays = 5
	const perRelay = 300 * time.Millisecond

	var urls []string
	for i := 0; i < relays; i++ {
		urls = append(urls, slowRelay(t, perRelay).URL)
	}

	p, err := publish.New(urls...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Generous per-request timeout so the test measures fan-out, not timeouts.
	p.Client = &http.Client{Timeout: 10 * time.Second}

	start := time.Now()
	out, err := p.Publish(context.Background(), env())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if out.Acked != relays {
		t.Fatalf("acked %d of %d relays", out.Acked, relays)
	}

	sequential := perRelay * relays
	if elapsed >= sequential/2 {
		t.Errorf("fan-out took %v for %d relays of %v each; sequential would be ~%v, so the "+
			"deliveries are not overlapping", elapsed, relays, perRelay, sequential)
	}
}

// TestPublisher_OneSlowRelayDoesNotBlockTheRest: the slowest relay bounds the call, but it must
// not delay the others' successes from being recorded.
func TestPublisher_OneSlowRelayDoesNotBlockTheRest(t *testing.T) {
	fast := newFakeRelay(t, 0)
	slow := slowRelay(t, 200*time.Millisecond)

	p, err := publish.New(slow.URL, fast.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	start := time.Now()
	out, err := p.Publish(context.Background(), env())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !out.OK() {
		t.Fatal("the fast relay should have made the delivery a success")
	}
	if fast.received.Load() != 1 {
		t.Errorf("the fast relay received %d messages, want 1", fast.received.Load())
	}
	// It must finish when the slow relay does, not later.
	if elapsed > 3*time.Second {
		t.Errorf("publish took %v; the slow relay should bound the call", elapsed)
	}
}

// TestPublisher_ConcurrentResultsPreserveConfiguredOrder is the reporting guarantee.
//
// The relays are started in a deliberately slow-to-fast order so arrival order differs from
// configuration order. If results were appended as they arrived, this would fail.
func TestPublisher_ConcurrentResultsPreserveConfiguredOrder(t *testing.T) {
	slow := slowRelay(t, 150*time.Millisecond)
	medium := slowRelay(t, 75*time.Millisecond)
	fast := newFakeRelay(t, 0)

	// Configuration order: slow, medium, fast.
	p, err := publish.New(slow.URL, medium.URL, fast.URL)
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

	want := []string{slow.URL, medium.URL, fast.URL}
	for i, r := range out.Results {
		if r.Relay != want[i] {
			t.Errorf("result %d is %q, want %q — results must follow the configured order, "+
				"not arrival order", i, r.Relay, want[i])
		}
	}
}

// TestPublisher_ConcurrentPartialFailureStillSucceeds: the partial-success semantics must survive
// concurrency, because that is the property that keeps one dead node from losing a receipt.
func TestPublisher_ConcurrentPartialFailureStillSucceeds(t *testing.T) {
	// Three healthy, two dead.
	var urls []string
	healthy := make([]*fakeRelay, 0, 3)
	for i := 0; i < 3; i++ {
		f := newFakeRelay(t, 0)
		healthy = append(healthy, f)
		urls = append(urls, f.URL)
	}
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
	if !out.OK() {
		t.Fatal("three healthy relays must make the delivery a success")
	}
	if out.Acked != 3 || out.Failed != 2 {
		t.Errorf("acked/failed = %d/%d, want 3/2", out.Acked, out.Failed)
	}
	for _, f := range healthy {
		if f.received.Load() != 1 {
			t.Errorf("a healthy relay received %d messages, want 1", f.received.Load())
		}
	}
	// Every failure must still carry a reason, even when collected from a goroutine.
	for _, r := range out.Results {
		if !r.OK && r.Err == nil {
			t.Errorf("relay %s failed without an error", r.Relay)
		}
	}
}

// TestPublisher_ConcurrentAllHealthyDeliverExactlyOnce: no relay may be contacted twice, and none
// may be skipped, when the fan-out runs concurrently.
func TestPublisher_ConcurrentAllHealthyDeliverExactlyOnce(t *testing.T) {
	const relays = 12

	var urls []string
	fakes := make([]*fakeRelay, 0, relays)
	for i := 0; i < relays; i++ {
		f := newFakeRelay(t, 0)
		fakes = append(fakes, f)
		urls = append(urls, f.URL)
	}

	p, err := publish.New(urls...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := p.Publish(context.Background(), env())
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if out.Acked != relays {
		t.Errorf("acked = %d, want %d", out.Acked, relays)
	}
	for i, f := range fakes {
		if got := f.received.Load(); got != 1 {
			t.Errorf("relay %d received %d message(s), want exactly 1", i, got)
		}
	}
}

// TestPublisher_ConcurrentAndRepeated runs many concurrent Publish calls to exercise the shared
// Publisher under the race detector.
func TestPublisher_ConcurrentAndRepeated(t *testing.T) {
	var urls []string
	for i := 0; i < 4; i++ {
		urls = append(urls, newFakeRelay(t, 0).URL)
	}

	p, err := publish.New(urls...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const callers = 16
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			e := node.Envelope{
				ID:      fmt.Sprintf("0x%064x", i),
				AgentID: "agent:concurrent",
				Kind:    "receipt",
				Payload: []byte(`{"n":1}`),
			}
			out, err := p.Publish(context.Background(), e)
			if err != nil {
				t.Errorf("Publish: %v", err)
				return
			}
			if !out.OK() {
				t.Errorf("caller %d: no relay acknowledged", i)
			}
			if len(out.Results) != 4 {
				t.Errorf("caller %d: %d results, want 4", i, len(out.Results))
			}
		}(i)
	}

	close(start)
	wg.Wait()
}

// TestPublisher_ContextCancellationIsRespected: a concurrent fan-out must still stop when the
// caller cancels, rather than running every delivery to completion.
func TestPublisher_ContextCancellationIsRespected(t *testing.T) {
	var received atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"stored":true}`))
	}))
	t.Cleanup(srv.Close)

	p, err := publish.New(srv.URL, srv.URL, srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	out, err := p.Publish(ctx, env())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Cancellation turns every delivery into a failure, but it must not hang.
	if elapsed > 2*time.Second {
		t.Errorf("publish took %v after cancellation; the fan-out must not outlive the context", elapsed)
	}
	if out.OK() {
		t.Error("a cancelled publish should not report success")
	}
	if out.Failed != 3 {
		t.Errorf("failed = %d, want 3", out.Failed)
	}
}
