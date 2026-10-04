package mining

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// ReceiptSink persists receipts produced by the runner.
//
// It is an interface rather than a concrete store so the mining package does not
// depend on the storage layer. That keeps mining testable without a database and
// lets a caller run in memory if persistence is not wanted.
type ReceiptSink interface {
	// Save persists a receipt. Implementations must be idempotent per receipt id,
	// because a retry after a timeout may deliver the same receipt twice.
	Save(r *receipt.Receipt, artifactKey string, at time.Time) error
}

// RunnerConfig configures a Runner.
type RunnerConfig struct {
	// Loop performs one unit of work. Required.
	Loop *Loop

	// Sink persists each receipt. Optional: a nil sink means receipts are
	// reported but not stored, which is useful for a dry run.
	Sink ReceiptSink

	// ArtifactKey derives the dedup key for a receipt. Optional; when nil the
	// key is left empty and the sink stores a null.
	ArtifactKey func(*receipt.Receipt) (string, error)

	// Interval is the pause between successful iterations. Zero means
	// DefaultInterval.
	Interval time.Duration

	// MaxBackoff caps the wait after consecutive failures. Zero means
	// DefaultMaxBackoff.
	MaxBackoff time.Duration

	// OnResult is called after every attempt, successful or not, so a CLI can
	// show live progress. Optional.
	OnResult func(RunResult)

	// Clock and Sleeper are injected for tests. Nil means real time.
	Now     func() time.Time
	Sleeper func(context.Context, time.Duration) error

	// Logger receives structured progress. Nil discards.
	Logger *slog.Logger
}

// Defaults for the runner.
const (
	DefaultInterval   = 5 * time.Second
	DefaultMaxBackoff = 5 * time.Minute
	// backoffBase is the first retry delay; each consecutive failure doubles it.
	backoffBase = 1 * time.Second
)

// RunResult reports one iteration.
type RunResult struct {
	// Receipt is set when the iteration produced work.
	Receipt *receipt.Receipt

	// Task is the task type that was executed.
	Task receipt.TaskType

	// Attempts is how many tasks were tried before one succeeded.
	Attempts int

	// Err is set when the iteration failed.
	Err error

	// At is when the iteration finished.
	At time.Time
}

// Runner repeatedly mines and persists receipts.
//
// # Design notes
//
//   - A failed iteration is not fatal. Individual tasks legitimately fail (a dead
//     URL, a refused connection), and stopping the whole loop on one bad URL would
//     make the miner useless on the open internet.
//   - Consecutive failures back off exponentially, because a persistent failure
//     (no network, a revoked key) should not spin at full rate.
//   - A successful iteration resets the backoff. Nothing else does.
type Runner struct {
	cfg RunnerConfig

	mu       sync.Mutex
	produced int
	failed   int
}

// NewRunner validates cfg and returns a runner.
func NewRunner(cfg RunnerConfig) (*Runner, error) {
	if cfg.Loop == nil {
		return nil, fmt.Errorf("mining: runner requires a loop")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = DefaultMaxBackoff
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleeper == nil {
		cfg.Sleeper = sleepCtx
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Runner{cfg: cfg}, nil
}

// Run mines until ctx is cancelled.
//
// It returns ctx.Err() when cancelled, so a caller can distinguish a clean
// shutdown from a fatal condition.
func (r *Runner) Run(ctx context.Context) error {
	consecutiveFailures := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		res := r.mineOnce(ctx)
		r.record(res)

		if r.cfg.OnResult != nil {
			r.cfg.OnResult(res)
		}

		var wait time.Duration
		if res.Err != nil {
			consecutiveFailures++
			wait = backoffFor(consecutiveFailures, r.cfg.MaxBackoff)
			r.cfg.Logger.Warn("iteration failed",
				"error", res.Err.Error(),
				"consecutive", consecutiveFailures,
				"retry_in", wait.String())
		} else {
			consecutiveFailures = 0
			wait = r.cfg.Interval
		}

		if wait > 0 {
			if err := r.cfg.Sleeper(ctx, wait); err != nil {
				// Cancellation while sleeping is a clean stop.
				return err
			}
		}
	}
}

// mineOnce performs a single iteration: mine, derive the artifact, persist.
func (r *Runner) mineOnce(ctx context.Context) RunResult {
	mined := r.cfg.Loop.MineOnce()

	res := RunResult{
		Receipt:  mined.Receipt,
		Task:     mined.Task,
		Attempts: mined.Attempts,
		Err:      mined.Err,
		At:       r.cfg.Now(),
	}

	if res.Err != nil || res.Receipt == nil {
		if res.Err == nil {
			res.Err = errors.New("mining: loop produced no receipt")
		}
		return res
	}

	if r.cfg.Sink != nil {
		key := ""
		if r.cfg.ArtifactKey != nil {
			k, err := r.cfg.ArtifactKey(res.Receipt)
			if err != nil {
				// A key that cannot be derived is not fatal: the receipt is still
				// worth storing, just without the dedup index.
				r.cfg.Logger.Warn("artifact key unavailable",
					"receipt", res.Receipt.ReceiptID, "error", err.Error())
			} else {
				key = k
			}
		}

		if err := r.cfg.Sink.Save(res.Receipt, key, res.At); err != nil {
			res.Err = fmt.Errorf("mining: persist receipt: %w", err)
			return res
		}
	}

	return res
}

func (r *Runner) record(res RunResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if res.Err != nil {
		r.failed++
		return
	}
	r.produced++
}

// Stats reports how many receipts were produced and how many iterations failed.
func (r *Runner) Stats() (produced, failed int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.produced, r.failed
}

// backoffFor returns the wait after n consecutive failures, capped at max.
//
// The growth is exponential with a ceiling, so a transient outage recovers
// quickly while a sustained one stops hammering.
//
// Note the first delay is min(backoffBase, max) rather than backoffBase. A cap
// below the base is legitimate (a test, or an aggressive retry budget), and
// without the min the first wait would already sit at the ceiling, making the
// growth invisible and the function indistinguishable from a constant.
func backoffFor(n int, max time.Duration) time.Duration {
	if n <= 0 {
		return 0
	}

	base := backoffBase
	if max > 0 && base > max {
		base = max
	}

	if n > 32 {
		// Guard the shift; the cap already binds long before this.
		return max
	}
	d := time.Duration(math.Pow(2, float64(n-1))) * base
	if d > max || d <= 0 {
		return max
	}
	return d
}

// sleepCtx sleeps for d, returning early if ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// discardWriter swallows log output for a runner with no logger configured.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
