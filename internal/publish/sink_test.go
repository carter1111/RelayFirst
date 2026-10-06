package publish_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/publish"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// recordingInner is a ReceiptSink that records what it was asked to save.
type recordingInner struct {
	saved    []*receipt.Receipt
	failWith error
	onSave   func()
}

func (r *recordingInner) Save(rec *receipt.Receipt, _ string, _ time.Time) error {
	if r.failWith != nil {
		return r.failWith
	}
	if r.onSave != nil {
		r.onSave()
	}
	r.saved = append(r.saved, rec)
	return nil
}

// validProbeReceipt builds an unsigned receipt attributed to the given agent.
//
// It is separate from testReceipt because the acceptance test needs the EXECUTOR to be
// the producer: attribution is one of the things criterion ⑧ exercises, and a fixture
// that always used one key would hide a mismatch.
func validProbeReceipt(t *testing.T, agentID string) *receipt.Receipt {
	t.Helper()
	return &receipt.Receipt{
		Schema:  receipt.Schema,
		AgentID: agentID,
		Epoch:   42,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": "https://example.com"},
			SpecHash:      "sha256:" + strings.Repeat("3d", 32),
			SelfGenerated: false, // produced for an A2A task, not invented by the agent
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015862},
		Result:       receipt.Result{Value: "200", Hash: "sha256:" + strings.Repeat("c1", 32)},
		Anchors:      []receipt.Anchor{{URL: "https://example.com", ContentHash: "sha256:" + strings.Repeat("7b", 32), FetchedAt: 1791015810, Status: 200, Bytes: 2048}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
}

func testReceipt(t *testing.T) *receipt.Receipt {
	t.Helper()

	const key = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	agent, err := receipt.DeriveAgentID(key, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	r := &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: "0x" + strings.Repeat("ab", 32),
		AgentID:   agent,
		Epoch:     42,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": "https://example.com"},
			SpecHash:      "sha256:" + strings.Repeat("3d", 32),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015862},
		Result:       receipt.Result{Value: "200", Hash: "sha256:" + strings.Repeat("c1", 32)},
		Anchors:      []receipt.Anchor{{URL: "https://example.com", ContentHash: "sha256:" + strings.Repeat("7b", 32), FetchedAt: 1791015810, Status: 200, Bytes: 2048}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	if err := r.Sign(key); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return r
}

// TestSink_PersistsBeforePublishing pins the ordering: if publishing fails, the
// work must still be recorded locally and re-sendable. Publishing first would risk
// a receipt that exists elsewhere but not on the machine that produced it.
func TestSink_PersistsBeforePublishing(t *testing.T) {
	inner := &recordingInner{}
	var savedBeforePublish bool

	// A relay that is already closed: publishing will fail.
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	downURL := down.URL
	down.Close()

	pub, err := publish.New(downURL)
	if err != nil {
		t.Fatalf("publish.New: %v", err)
	}

	inner.onSave = func() { savedBeforePublish = true }

	sink := &publish.Sink{Inner: inner, Publisher: pub}
	if err := sink.Save(testReceipt(t), "sha256:k", time.Unix(1791015800, 0)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if !savedBeforePublish {
		t.Error("the receipt was not persisted before publishing")
	}
	if len(inner.saved) != 1 {
		t.Errorf("inner sink holds %d receipt(s), want 1", len(inner.saved))
	}
}

// TestSink_PublishFailureDoesNotFailTheIteration: a relay problem is not a mining
// failure. Treating it as one would make the miner back off and stop producing
// work because a convenience node was down.
func TestSink_PublishFailureDoesNotFailTheIteration(t *testing.T) {
	inner := &recordingInner{}

	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	downURL := down.URL
	down.Close()

	pub, err := publish.New(downURL)
	if err != nil {
		t.Fatalf("publish.New: %v", err)
	}

	sink := &publish.Sink{Inner: inner, Publisher: pub}
	if err := sink.Save(testReceipt(t), "sha256:k", time.Unix(1791015800, 0)); err != nil {
		t.Errorf("Save returned %v; a dead relay must not fail the iteration", err)
	}
}

// TestSink_InnerFailureStopsPublishing: a receipt that was not stored must not be
// published, and the error must surface. Publishing work that failed to persist
// would create a receipt no local ledger knows about.
func TestSink_InnerFailureStopsPublishing(t *testing.T) {
	relay := newFakeRelay(t, 0)

	inner := &recordingInner{failWith: errors.New("disk full")}

	pub, err := publish.New(relay.URL)
	if err != nil {
		t.Fatalf("publish.New: %v", err)
	}

	sink := &publish.Sink{Inner: inner, Publisher: pub}
	err = sink.Save(testReceipt(t), "sha256:k", time.Unix(1791015800, 0))
	if err == nil {
		t.Fatal("a persistence failure must surface")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error should wrap the cause, got %v", err)
	}
	if relay.received.Load() != 0 {
		t.Error("a receipt that failed to persist must not be published")
	}
}

// TestSink_NoPublisherIsFine: a purely local miner needs no node. A node is
// optional for mining to work, so a missing publisher must not be an error.
func TestSink_NoPublisherIsFine(t *testing.T) {
	inner := &recordingInner{}
	sink := &publish.Sink{Inner: inner}

	if err := sink.Save(testReceipt(t), "sha256:k", time.Unix(1791015800, 0)); err != nil {
		t.Errorf("Save with no publisher = %v, want nil", err)
	}
	if len(inner.saved) != 1 {
		t.Errorf("inner sink holds %d receipt(s), want 1", len(inner.saved))
	}
}

func TestSink_RequiresInnerStore(t *testing.T) {
	sink := &publish.Sink{}
	if err := sink.Save(testReceipt(t), "", time.Unix(1791015800, 0)); err == nil {
		t.Error("a sink with no inner store must refuse to save")
	}
}

// TestSink_ReportsOutcomeToCaller: the CLI needs the fan-out result to tell an
// operator which relay is dead.
func TestSink_ReportsOutcomeToCaller(t *testing.T) {
	up := newFakeRelay(t, 0)

	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	downURL := down.URL
	down.Close()

	pub, err := publish.New(up.URL, downURL)
	if err != nil {
		t.Fatalf("publish.New: %v", err)
	}

	var got *publish.Outcome
	sink := &publish.Sink{
		Inner:     &recordingInner{},
		Publisher: pub,
		OnOutcome: func(_ *receipt.Receipt, o publish.Outcome) { got = &o },
	}

	if err := sink.Save(testReceipt(t), "sha256:k", time.Unix(1791015800, 0)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if got == nil {
		t.Fatal("OnOutcome was not called")
	}
	if got.Acked != 1 || got.Failed != 1 {
		t.Errorf("acked/failed = %d/%d, want 1/1", got.Acked, got.Failed)
	}
	if !got.OK() {
		t.Error("one healthy relay should make the delivery a success")
	}
}

// TestSink_PolicyPathIsReachableAndWired proves the RFN-05 machinery is on a
// production path: with a policy set, the sequential policy path runs, and with a
// quorum that cannot be met it reports a shortfall rather than silently succeeding.
//
// The point is not the number — it is that setting Sink.Policy changes the code path
// at all. Before this, PublishWithPolicy had no production caller, so the multi-relay
// ability was built and unreachable.
func TestSink_PolicyPathIsReachableAndWired(t *testing.T) {
	inner := &recordingInner{}

	// One relay that accepts, so a quorum of 1 is met on the policy path.
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ok.Close()

	pub, err := publish.New(ok.URL)
	if err != nil {
		t.Fatalf("publish.New: %v", err)
	}

	var got publish.Outcome
	sink := &publish.Sink{
		Inner:     inner,
		Publisher: pub,
		Policy:    &publish.DeliveryPolicy{Quorum: publish.QuorumPolicy{Required: 1}},
		OnOutcome: func(_ *receipt.Receipt, o publish.Outcome) { got = o },
	}
	if err := sink.Save(testReceipt(t), "sha256:k", time.Unix(1791015800, 0)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got.Acked != 1 {
		t.Errorf("Acked = %d, want 1 — the policy path must have delivered", got.Acked)
	}

	// A quorum the single relay cannot meet must be reported, not hidden.
	var short publish.Outcome
	sink2 := &publish.Sink{
		Inner:     &recordingInner{},
		Publisher: pub,
		Policy:    &publish.DeliveryPolicy{Quorum: publish.QuorumPolicy{Required: 3}},
		OnOutcome: func(_ *receipt.Receipt, o publish.Outcome) { short = o },
	}
	if err := sink2.Save(testReceipt(t), "sha256:k", time.Unix(1791015800, 0)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if short.Acked >= 3 {
		t.Error("a quorum of 3 cannot be met by 1 relay; the shortfall must be visible")
	}
}

// TestSink_NoPolicyKeepsTheConcurrentPath: without a policy, the sink must use the
// original concurrent fan-out, so existing behaviour (and its timeout bound) is
// unchanged for a caller that did not ask for quorum.
func TestSink_NoPolicyKeepsTheConcurrentPath(t *testing.T) {
	inner := &recordingInner{}
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()

	pub, _ := publish.New(ok.URL)
	sink := &publish.Sink{Inner: inner, Publisher: pub} // no Policy
	if err := sink.Save(testReceipt(t), "sha256:k", time.Unix(1791015800, 0)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if inner.saved == nil {
		t.Error("the receipt must still be persisted and delivered")
	}
}
