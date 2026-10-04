package publish

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// ReceiptSink is the minimal storage interface this package needs.
//
// It is declared here, structurally, rather than importing mining: Go interfaces
// are satisfied structurally, so `mining.ReceiptSink` already satisfies this. That
// keeps the direction of dependency clean — publish depends on the shape of a
// sink, not on the mining package — and means the relay stack never drags in the
// miner, the LLM providers or their credential handling.
type ReceiptSink interface {
	// Save persists a receipt. Implementations must be idempotent per receipt id.
	Save(r *receipt.Receipt, artifactKey string, at time.Time) error
}

// Sink persists receipts and then publishes them to relays.
//
// # Ordering
//
// Persist first, publish second. If publishing fails, the work is still recorded
// locally and can be re-sent; publishing first would risk a receipt that exists
// somewhere else but not on the machine that produced it.
//
// # Why a publish failure does not fail the iteration
//
// The receipt is already safely stored and scored by the time publishing runs, so
// a relay problem is not a mining failure. Turning it into one would make the
// miner back off and stop producing work because a convenience node was down —
// the wrong trade, and exactly the coupling that a fan-out is meant to avoid.
// Failures are logged and reported per relay instead.
type Sink struct {
	// Inner persists the receipt. Required.
	Inner ReceiptSink

	// Publisher delivers to the relay set. Required.
	Publisher *Publisher

	// ArtifactKey derives the dedup key recorded alongside the receipt. Optional;
	// when nil the inner sink stores a null, matching the miner's own behaviour.
	ArtifactKey func(*receipt.Receipt) (string, error)

	// OnOutcome, when set, receives the fan-out result so a CLI can show delivery
	// per relay.
	OnOutcome func(r *receipt.Receipt, o Outcome)

	// Logger receives delivery warnings. Nil discards.
	Logger *slog.Logger
}

// Save persists r and then attempts delivery to every configured relay.
func (s *Sink) Save(r *receipt.Receipt, artifactKey string, at time.Time) error {
	if s.Inner == nil {
		return fmt.Errorf("publish: sink has no inner store")
	}

	if err := s.Inner.Save(r, artifactKey, at); err != nil {
		return err
	}

	if s.Publisher == nil {
		// No relays configured: a purely local miner. Not an error — a node is
		// optional for mining to work.
		return nil
	}

	env, err := ReceiptEnvelope(r)
	if err != nil {
		// The receipt could not be wrapped, which is a bug rather than a relay
		// problem. Log it and keep the stored receipt rather than failing the
		// iteration, because the work itself is fine.
		s.logger().Warn("could not wrap receipt for publishing",
			"receipt", r.ReceiptID, "error", err.Error())
		return nil
	}

	out, err := s.Publisher.Publish(context.Background(), env)
	if err != nil {
		s.logger().Warn("publish call failed", "receipt", r.ReceiptID, "error", err.Error())
		return nil
	}

	if s.OnOutcome != nil {
		s.OnOutcome(r, out)
	}

	// A partial fan-out is expected and fine; only a total failure is worth a
	// warning, because only then is the receipt reachable solely from this host.
	if !out.OK() {
		s.logger().Warn("receipt reached no relay",
			"receipt", r.ReceiptID, "relays", len(out.Results), "error", out.Error().Error())
	}

	return nil
}

func (s *Sink) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
