package verification_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/epoch"
	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/verification"
)

// ---------------------------------------------------------------- the window

// TestEpochWindow_OpenWithinTheEpoch is the normal case: a receipt verified during its own
// epoch is allowed.
func TestEpochWindow_OpenWithinTheEpoch(t *testing.T) {
	w := verification.NewEpochWindow(24 * time.Hour)
	start, end := epoch.Bounds(7, 24*time.Hour)

	r := &receipt.Receipt{ReceiptID: "0x1", Epoch: 7}

	if err := w.Open(r, start); err != nil {
		t.Errorf("the start of the epoch must be inside the window: %v", err)
	}
	if err := w.Open(r, end.Add(-time.Second)); err != nil {
		t.Errorf("the instant before the end must be inside the window: %v", err)
	}
}

// TestEpochWindow_ClosedAfterTheEpoch is the point of the window: it stops a verifier from
// judging an old receipt, where the anchor's content may have changed and the producer was
// honest at the time.
func TestEpochWindow_ClosedAfterTheEpoch(t *testing.T) {
	w := verification.NewEpochWindow(24 * time.Hour)
	_, end := epoch.Bounds(7, 24*time.Hour)

	r := &receipt.Receipt{ReceiptID: "0x1", Epoch: 7}

	err := w.Open(r, end)
	if err == nil {
		t.Fatal("a receipt whose epoch has ended must not be verifiable")
	}
	// The message must say why, because "closed" and "you are wrong" are very different
	// conclusions for an operator.
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("the refusal should explain the window closed, got %q", err)
	}
}

// TestEpochWindow_RefusesAFutureEpoch: a receipt dated ahead of now is a clock problem or a
// receipt that has not happened; verifying it would be meaningless.
func TestEpochWindow_RefusesAFutureEpoch(t *testing.T) {
	w := verification.NewEpochWindow(time.Hour)
	start, _ := epoch.Bounds(500, time.Hour)

	r := &receipt.Receipt{ReceiptID: "0x1", Epoch: 500}

	if err := w.Open(r, start.Add(-time.Second)); err == nil {
		t.Error("an epoch that has not started must not be verifiable")
	}
}

func TestEpochWindow_RejectsNilReceipt(t *testing.T) {
	w := verification.NewEpochWindow(time.Hour)
	if err := w.Open(nil, time.Now()); err == nil {
		t.Error("a nil receipt must be refused")
	}
}

// TestEpochWindow_DefaultLength: a zero length must fall back to the documented default
// rather than producing a window of zero width, which would refuse everything.
func TestEpochWindow_DefaultLength(t *testing.T) {
	w := verification.NewEpochWindow(0)
	start, _ := epoch.Bounds(3, verification.DefaultEpochLength)

	r := &receipt.Receipt{ReceiptID: "0x1", Epoch: 3}
	if err := w.Open(r, start.Add(time.Hour)); err != nil {
		t.Errorf("a zero length should default to 24h, got %v", err)
	}
}

// TestAlwaysOpen_PermitsAndSaysSo: the permissive window must be nameable, so an operator
// can tell that a rejection might be a false one.
func TestAlwaysOpen_PermitsAndSaysSo(t *testing.T) {
	r := &receipt.Receipt{ReceiptID: "0x1", Epoch: 1}

	if err := (verification.AlwaysOpen{}).Open(r, time.Now()); err != nil {
		t.Errorf("AlwaysOpen must permit: %v", err)
	}
	if got := (verification.AlwaysOpen{}).Describe(); !strings.Contains(strings.ToLower(got), "no window") {
		t.Errorf("Describe() = %q, should say there is no window", got)
	}
}

// TestVerifierHonoursTheWindow: the window must actually gate verification, not merely exist.
func TestVerifierHonoursTheWindow(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("71", 32), "https://example.com/health", "200")

	_, end := epoch.Bounds(r.Epoch, verification.DefaultEpochLength)

	v, err := verification.New(verification.Config{
		Policy:     verification.AllowAll{Verifier: agentOf(t, keyVerifier)},
		Stakes:     verification.NewMemStakeLedger(),
		Receipts:   newFakeUpdater(),
		Recomputer: &fakeRecomputer{result: r.Result},
		Window:     verification.NewEpochWindow(verification.DefaultEpochLength),
		// A clock past the epoch's end.
		Now: func() time.Time { return end.Add(time.Hour) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := v.Verify(context.Background(), r); err == nil {
		t.Error("a closed window must stop verification")
	}
	if r.Verification.Status == receipt.VerificationVerified {
		t.Error("a receipt outside its window must not be marked verified")
	}
}

// ---------------------------------------------------------------- default window

// TestVerifierDefaultsToAnOpenWindow records the deliberate default: no window means
// accuracy suffers rather than everything being refused.
func TestVerifierDefaultsToAnOpenWindow(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("72", 32), "https://example.com/health", "200")

	v, err := verification.New(verification.Config{
		Policy:     verification.AllowAll{Verifier: agentOf(t, keyVerifier)},
		Stakes:     verification.NewMemStakeLedger(),
		Receipts:   newFakeUpdater(),
		Recomputer: &fakeRecomputer{result: r.Result},
		// No Window configured.
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := v.Verify(context.Background(), r); err != nil {
		t.Errorf("with no window configured, verification should proceed: %v", err)
	}
}
