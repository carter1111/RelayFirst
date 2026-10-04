package mining

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// recordingSink captures saved receipts.
type recordingSink struct {
	mu      sync.Mutex
	saved   []*receipt.Receipt
	keys    []string
	failNth int // fail on the Nth call (1-based); 0 means never
	calls   int
	err     error
}

func (s *recordingSink) Save(r *receipt.Receipt, artifactKey string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++
	if s.failNth > 0 && s.calls == s.failNth {
		if s.err != nil {
			return s.err
		}
		return errors.New("sink failure")
	}
	s.saved = append(s.saved, r)
	s.keys = append(s.keys, artifactKey)
	return nil
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.saved)
}

// runnerTestPrivKey is a well-known test key. It holds no value and is never
// used outside tests (CODING_RULES.md §8).
const runnerTestPrivKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"

// testLoop builds a Loop over a live test server.
func testLoop(t *testing.T, srvURL string) *Loop {
	t.Helper()

	g, err := NewGenerator([]string{srvURL})
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}

	agent, err := receipt.DeriveAgentID(runnerTestPrivKey, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	return &Loop{
		AgentID:     agent,
		PrivKeyHex:  runnerTestPrivKey,
		Generator:   g,
		Deps:        Deps{Fetcher: newTestFetcher()},
		EpochLength: time.Hour,
		Now:         func() time.Time { return time.Unix(1791015800, 0) },
	}
}

// manualSleeper returns a sleeper that records durations and yields immediately,
// so a test can drive the loop without real waiting.
func manualSleeper(record *[]time.Duration) func(context.Context, time.Duration) error {
	return func(ctx context.Context, d time.Duration) error {
		*record = append(*record, d)
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
}

func TestNewRunner_RequiresLoop(t *testing.T) {
	if _, err := NewRunner(RunnerConfig{}); err == nil {
		t.Error("a runner without a loop must be refused")
	}
}

// TestRunner_ProducesAndPersists drives the runner for a fixed number of
// iterations by cancelling after the expected work is done.
func TestRunner_ProducesAndPersists(t *testing.T) {
	srv := jsonServer(t, 200, `{"ok":true}`)
	defer srv.Close()

	sink := &recordingSink{}
	var slept []time.Duration

	const want = 3
	var cancelled context.Context
	var cancel context.CancelFunc

	produced := 0
	cfg := RunnerConfig{
		Loop:     testLoop(t, srv.URL),
		Sink:     sink,
		Interval: time.Millisecond,
		Sleeper:  manualSleeper(&slept),
		OnResult: func(res RunResult) {
			if res.Err == nil {
				produced++
			}
			if produced >= want {
				cancel()
			}
		},
		ArtifactKey: func(r *receipt.Receipt) (string, error) { return "sha256:derived", nil },
	}

	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	cancelled, cancel = context.WithCancel(context.Background())
	defer cancel()

	err = r.Run(cancelled)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run should return ctx.Err() on cancellation, got %v", err)
	}

	if produced != want {
		t.Errorf("produced = %d, want %d", produced, want)
	}
	if sink.count() != want {
		t.Errorf("sink holds %d receipts, want %d", sink.count(), want)
	}

	gotProduced, gotFailed := r.Stats()
	if gotProduced != want || gotFailed != 0 {
		t.Errorf("Stats = (%d, %d), want (%d, 0)", gotProduced, gotFailed, want)
	}

	// The derived artifact key must reach the sink.
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for i, k := range sink.keys {
		if k != "sha256:derived" {
			t.Errorf("saved key %d = %q, want the derived key", i, k)
		}
	}
}

// alwaysFailSink fails every save, which makes each iteration fail regardless of
// which task type the generator chose.
type alwaysFailSink struct{ calls int }

func (s *alwaysFailSink) Save(*receipt.Receipt, string, time.Time) error {
	s.calls++
	return errors.New("sink unavailable")
}

// TestRunner_BacksOffOnFailure: a persistent failure must not spin at full rate.
//
// Failure is induced through the sink rather than the network on purpose. A
// network outage alone does not stop this runner, because `compute` tasks need no
// network — an occasional one succeeds and correctly resets the counter. That
// resilience is real, and it means only a failure that affects *every* task, such
// as an unwritable store, can drive the backoff.
func TestRunner_BacksOffOnFailure(t *testing.T) {
	srv := jsonServer(t, 200, `{"ok":true}`)

	sink := &alwaysFailSink{}
	var slept []time.Duration
	var cancel context.CancelFunc

	attempts := 0
	cfg := RunnerConfig{
		Loop:       testLoop(t, srv.URL),
		Sink:       sink,
		Interval:   time.Millisecond,
		MaxBackoff: 100 * time.Millisecond,
		Sleeper:    manualSleeper(&slept),
		OnResult: func(res RunResult) {
			if res.Err != nil {
				attempts++
			}
			if attempts >= 4 {
				cancel()
			}
		},
	}

	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	ctx, c := context.WithCancel(context.Background())
	cancel = c
	defer c()

	if err := r.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Run should return ctx.Err(), got %v", err)
	}

	if len(slept) < 3 {
		t.Fatalf("expected several backoff sleeps, got %d: %v", len(slept), slept)
	}

	// Every wait must respect the cap.
	for i, d := range slept {
		if d > 100*time.Millisecond {
			t.Errorf("wait %d = %v exceeded the cap of 100ms", i, d)
		}
	}

	// With consecutive failures and no successes in between, the delays must grow
	// until they reach the cap. The first wait is min(base, cap); with a 100ms cap
	// that is 100ms.
	if slept[0] != 100*time.Millisecond {
		t.Errorf("first wait = %v, want min(base, cap) = 100ms", slept[0])
	}

	produced, failed := r.Stats()
	if failed < 3 || produced != 0 {
		t.Errorf("Stats = (%d produced, %d failed), want (0, >=3)", produced, failed)
	}
}

// TestRunner_NetworkOutageDoesNotStopMining records the resilience finding above
// as an explicit expectation, so a future change that makes every task
// network-dependent is noticed rather than silently degrading the miner.
func TestRunner_NetworkOutageDoesNotStopMining(t *testing.T) {
	// A server that is already closed: every network call fails.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	var cancel context.CancelFunc
	const want = 5
	produced := 0

	cfg := RunnerConfig{
		Loop:     testLoop(t, url),
		Interval: time.Millisecond,
		Sleeper:  manualSleeper(new([]time.Duration)),
		OnResult: func(res RunResult) {
			if res.Err == nil {
				produced++
				if produced >= want {
					cancel()
				}
			}
		},
	}

	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	ctx, c := context.WithCancel(context.Background())
	cancel = c
	defer c()

	_ = r.Run(ctx)

	if produced < want {
		t.Errorf("produced %d receipts during a network outage, want at least %d — "+
			"offline task types should keep the miner alive", produced, want)
	}
}

func TestBackoffFor(t *testing.T) {
	const max = 10 * time.Second

	if got := backoffFor(0, max); got != 0 {
		t.Errorf("backoffFor(0) = %v, want 0", got)
	}
	if got := backoffFor(1, max); got != backoffBase {
		t.Errorf("backoffFor(1) = %v, want %v", got, backoffBase)
	}
	// Doubling.
	if got := backoffFor(3, max); got != 4*backoffBase {
		t.Errorf("backoffFor(3) = %v, want %v", got, 4*backoffBase)
	}
	// Capped, and a very large n must not overflow into a negative duration.
	if got := backoffFor(64, max); got != max {
		t.Errorf("backoffFor(64) = %v, want the cap %v", got, max)
	}
	if got := backoffFor(1000, max); got != max {
		t.Errorf("backoffFor(1000) = %v, want the cap %v", got, max)
	}
}

// TestRunner_SinkFailureCountsAsFailure: if persistence fails the iteration must
// be reported as failed, because a receipt that was not stored is lost work.
func TestRunner_SinkFailureCountsAsFailure(t *testing.T) {
	srv := jsonServer(t, 200, `{"ok":true}`)

	sink := &recordingSink{failNth: 1}
	var cancel context.CancelFunc

	cfg := RunnerConfig{
		Loop:     testLoop(t, srv.URL),
		Sink:     sink,
		Interval: time.Millisecond,
		Sleeper:  manualSleeper(new([]time.Duration)),
		OnResult: func(res RunResult) {
			if res.Err != nil {
				cancel()
			}
		},
	}

	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	ctx, c := context.WithCancel(context.Background())
	cancel = c
	defer c()

	if err := r.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Run should return ctx.Err(), got %v", err)
	}

	produced, failed := r.Stats()
	if failed != 1 || produced != 0 {
		t.Errorf("Stats = (%d produced, %d failed), want (0, 1)", produced, failed)
	}
}

// TestRunner_WorksWithoutSink: a dry run needs no persistence.
func TestRunner_WorksWithoutSink(t *testing.T) {
	srv := jsonServer(t, 200, `{"ok":true}`)

	var cancel context.CancelFunc
	produced := 0

	cfg := RunnerConfig{
		Loop:     testLoop(t, srv.URL),
		Interval: time.Millisecond,
		Sleeper:  manualSleeper(new([]time.Duration)),
		OnResult: func(res RunResult) {
			if res.Err == nil {
				produced++
				cancel()
			}
		},
	}

	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	ctx, c := context.WithCancel(context.Background())
	cancel = c
	defer c()

	if err := r.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Run: %v", err)
	}
	if produced != 1 {
		t.Errorf("produced = %d, want 1", produced)
	}
}

// TestRunner_ConcurrentReceiptsAreValid: the runner must never persist a receipt
// that fails our own validation.
func TestRunner_ConcurrentReceiptsAreValid(t *testing.T) {
	srv := jsonServer(t, 200, `{"ok":true}`)

	sink := &recordingSink{}
	var cancel context.CancelFunc
	const want = 5
	produced := 0

	cfg := RunnerConfig{
		Loop:     testLoop(t, srv.URL),
		Sink:     sink,
		Interval: time.Millisecond,
		Sleeper:  manualSleeper(new([]time.Duration)),
		OnResult: func(res RunResult) {
			if res.Err == nil {
				produced++
				if produced >= want {
					cancel()
				}
			}
		},
	}

	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	ctx, c := context.WithCancel(context.Background())
	cancel = c
	defer c()

	_ = r.Run(ctx)

	sink.mu.Lock()
	defer sink.mu.Unlock()

	if len(sink.saved) != want {
		t.Fatalf("saved %d receipts, want %d", len(sink.saved), want)
	}
	for i, rec := range sink.saved {
		if err := rec.Validate(nil); err != nil {
			t.Errorf("saved receipt %d fails validation: %v", i, err)
		}
	}
}
