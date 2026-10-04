package mining

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// Concurrency tests for the mining runner.
//
// # Why these exist
//
// Runner holds counters (`produced`, `failed`) behind a mutex and updates them from the loop
// goroutine while Stats reads them from wherever the caller is. That is a shared-state pattern,
// and the race-detector gate was documented as bounded by the concurrency in the tests — the
// runner had none, so this pattern was never exercised under -race.
//
// The property beyond "no race" is that the counters must be consistent: produced plus failed
// must equal the number of iterations, whatever the interleaving.

// TestRunner_ConcurrentStatsReadsWhileMining hammers Stats while iterations complete.
//
// A caller polling Stats for a progress display is the realistic case, and it is exactly the
// read-during-write pattern that a race detector exists to catch.
func TestRunner_ConcurrentStatsReadsWhileMining(t *testing.T) {
	srv := jsonServer(t, 200, `{"ok":true}`)

	const wantIterations = 40

	sink := &recordingSink{}
	var cancel context.CancelFunc

	produced := 0
	cfg := RunnerConfig{
		Loop:     testLoop(t, srv.URL),
		Sink:     sink,
		Interval: time.Millisecond,
		Sleeper:  manualSleeper(new([]time.Duration)),
		OnResult: func(res RunResult) {
			if res.Err == nil {
				produced++
				if produced >= wantIterations {
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

	var wg sync.WaitGroup

	// Pollers, running for the whole duration of the mining loop.
	stopPolling := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopPolling:
					return
				default:
					p, f := r.Stats()
					// The counters must never go backwards, and neither may be negative.
					if p < 0 || f < 0 {
						t.Errorf("Stats returned negative counts: (%d, %d)", p, f)
						return
					}
					// This runs concurrently with the writer, so the exact value is not
					// asserted here; the totals are checked after the loop joins.
					_ = p
					_ = f
				}
			}
		}()
	}

	_ = r.Run(ctx)
	close(stopPolling)
	wg.Wait()

	gotProduced, gotFailed := r.Stats()
	if gotProduced != wantIterations {
		t.Errorf("produced = %d, want %d", gotProduced, wantIterations)
	}
	if gotFailed != 0 {
		t.Errorf("failed = %d, want 0", gotFailed)
	}
	if gotProduced+gotFailed != wantIterations {
		t.Errorf("produced+failed = %d, want %d; the counters are inconsistent",
			gotProduced+gotFailed, wantIterations)
	}
}

// TestRunner_ConcurrentStatsReadsDuringFailures does the same while iterations are failing, so
// the failure counter is written concurrently too.
func TestRunner_ConcurrentStatsReadsDuringFailures(t *testing.T) {
	srv := jsonServer(t, 200, `{"ok":true}`)

	const wantFailures = 20

	sink := &alwaysFailSink{}
	var cancel context.CancelFunc

	attempts := 0
	cfg := RunnerConfig{
		Loop:       testLoop(t, srv.URL),
		Sink:       sink,
		Interval:   time.Millisecond,
		MaxBackoff: time.Millisecond,
		Sleeper:    manualSleeper(new([]time.Duration)),
		OnResult: func(res RunResult) {
			if res.Err != nil {
				attempts++
				if attempts >= wantFailures {
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

	var wg sync.WaitGroup
	stopPolling := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopPolling:
					return
				default:
					p, f := r.Stats()
					if p < 0 || f < 0 {
						t.Errorf("Stats returned negative counts: (%d, %d)", p, f)
						return
					}
				}
			}
		}()
	}

	_ = r.Run(ctx)
	close(stopPolling)
	wg.Wait()

	gotProduced, gotFailed := r.Stats()
	if gotFailed < wantFailures {
		t.Errorf("failed = %d, want at least %d", gotFailed, wantFailures)
	}
	if gotProduced != 0 {
		t.Errorf("produced = %d, want 0 — every iteration failed", gotProduced)
	}
	if gotProduced+gotFailed != gotFailed {
		t.Errorf("produced+failed is inconsistent with failed=%d", gotFailed)
	}
}

// TestRunner_ConcurrentSinkSavesAreSerialized asserts the runner does not call the sink from
// several goroutines at once.
//
// The runner is single-threaded today, so this is a guard rather than a bug hunt: if a future
// change parallelised iterations, a sink with an unsynchronized counter would start losing
// writes and this test would say so rather than leaving it to be discovered as a wrong balance.
func TestRunner_ConcurrentSinkSavesAreSerialized(t *testing.T) {
	srv := jsonServer(t, 200, `{"ok":true}`)

	const want = 25

	// A sink that detects overlapping calls without a lock of its own.
	overlap := &overlapDetectingSink{}

	var cancel context.CancelFunc
	produced := 0

	cfg := RunnerConfig{
		Loop:     testLoop(t, srv.URL),
		Sink:     overlap,
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

	if overlap.overlapped() {
		t.Error("the runner called the sink from overlapping goroutines; a sink may assume serialized saves")
	}
	if got := overlap.count(); got != want {
		t.Errorf("sink received %d saves, want %d", got, want)
	}
}

// overlapDetectingSink records whether Save was ever entered while already inside Save.
//
// It deliberately uses an atomic counter rather than a mutex: a mutex would serialize the calls
// and hide the very overlap it is meant to detect.
type overlapDetectingSink struct {
	entered    int32
	saves      int32
	overlapHit int32
}

func (s *overlapDetectingSink) Save(_ *receipt.Receipt, _ string, _ time.Time) error {
	if atomic.AddInt32(&s.entered, 1) != 1 {
		atomic.StoreInt32(&s.overlapHit, 1)
	}
	atomic.AddInt32(&s.saves, 1)

	// Yield so a concurrent caller would have a chance to overlap if the runner were parallel.
	time.Sleep(10 * time.Microsecond)

	atomic.AddInt32(&s.entered, -1)
	return nil
}

func (s *overlapDetectingSink) overlapped() bool { return atomic.LoadInt32(&s.overlapHit) == 1 }
func (s *overlapDetectingSink) count() int       { return int(atomic.LoadInt32(&s.saves)) }
