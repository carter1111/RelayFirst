package publish_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/node"
	"github.com/relayfirst/relayfirst/internal/publish"
)

// These tests cover P1 #4: quorum, per-relay health, and bounded failover.
//
// The properties that matter:
//
//   - quorum counts INDEPENDENT holders, not relays, or one operator's three front-ends would
//     satisfy a quorum by itself and the requirement would mean nothing;
//   - a single timeout does not condemn a relay, since networks hiccup and a node restarts;
//   - an avoided relay is retried eventually, so a recovered node rejoins without intervention
//     and a bad minute does not permanently shrink the relay set;
//   - and failover is BOUNDED, because an unbounded retry over a dead set is a hang.

// relayBehaviour builds a relay test server with a fixed response.
func relayBehaviour(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// deadRelay returns a URL that refuses connections.
func deadRelay(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	return url
}

func testEnvelope() node.Envelope {
	return node.Envelope{
		ID:      "0x" + strings.Repeat("ab", 32),
		AgentID: "agent:eip155:8453:0x0000000000000000000000000000000000000001",
		Kind:    node.KindReceipt,
		Payload: []byte("{}"),
	}
}

// TestQuorum_CountsIndependentGroups is the property that makes a quorum worth asking for.
//
// One operator running three front-ends to one database is ONE place to lose the message. Counting
// them as three would let a single operator satisfy a quorum by itself.
func TestQuorum_CountsIndependentGroups(t *testing.T) {
	out := publish.Outcome{
		Results: []publish.RelayResult{
			{Relay: "opA-front-1", OK: true},
			{Relay: "opA-front-2", OK: true},
			{Relay: "opA-front-3", OK: true},
		},
		Acked: 3,
	}

	// Without grouping: three acknowledgements.
	loose := publish.QuorumPolicy{Required: 3}
	reached, _, ok := loose.Satisfied(out)
	if !ok || reached != 3 {
		t.Errorf("ungrouped: reached=%d ok=%v, want 3 and true", reached, ok)
	}

	// With grouping: one independent holder, so a quorum of 2 is NOT met.
	grouped := publish.QuorumPolicy{
		Required:      2,
		IndependentOf: map[string]string{"opA-front-1": "opA", "opA-front-2": "opA", "opA-front-3": "opA"},
	}
	reached, required, ok := grouped.Satisfied(out)
	if ok {
		t.Fatalf("three front-ends of ONE operator must not satisfy a quorum of %d: "+
			"the requirement would mean nothing", required)
	}
	if reached != 1 {
		t.Errorf("reached = %d, want 1 independent holder", reached)
	}
}

// TestQuorum_UnlabelledRelayIsItsOwnGroup keeps the default safe: an unlabelled relay counts as one
// independent holder rather than being silently merged with another.
func TestQuorum_UnlabelledRelayIsItsOwnGroup(t *testing.T) {
	out := publish.Outcome{
		Results: []publish.RelayResult{
			{Relay: "a", OK: true},
			{Relay: "b", OK: true},
		},
		Acked: 2,
	}
	p := publish.QuorumPolicy{Required: 2, IndependentOf: map[string]string{"a": "same"}}
	// "b" is not in the map, so it is its own group and the quorum is met.
	reached, _, ok := p.Satisfied(out)
	if !ok || reached != 2 {
		t.Errorf("reached=%d ok=%v, want 2 and true: an unlabelled relay is its own group", reached, ok)
	}
}

// TestQuorum_ReportsWhyItFellShort lets an operator tell "nobody answered" from "one answered and
// two were required".
func TestQuorum_ReportsWhyItFellShort(t *testing.T) {
	out := publish.Outcome{
		Results: []publish.RelayResult{
			{Relay: "a", OK: true},
			{Relay: "b", OK: false},
			{Relay: "c", OK: false},
		},
		Acked: 1, Failed: 2,
	}
	reached, required, ok := publish.QuorumPolicy{Required: 2}.Satisfied(out)
	if ok {
		t.Fatal("one acknowledgement must not meet a quorum of two")
	}
	if reached != 1 || required != 2 {
		t.Errorf("reached=%d required=%d, want 1 and 2 so the shortfall is explainable", reached, required)
	}
}

// TestQuorum_FractionOverAttempted uses the attempted count, not the configured one: a caller that
// failed over to a subset should be measured against what it tried.
func TestQuorum_FractionOverAttempted(t *testing.T) {
	out := publish.Outcome{
		Results: []publish.RelayResult{{Relay: "a", OK: true}, {Relay: "b", OK: true}},
		Acked:   2,
	}
	// Half of two attempted is one, so two acknowledgements meet it.
	if _, _, ok := (publish.QuorumPolicy{Fraction: 0.5}).Satisfied(out); !ok {
		t.Error("half of two attempted must be met by two acknowledgements")
	}
	// A fraction with a floor of one never requires zero.
	one := publish.Outcome{Results: []publish.RelayResult{{Relay: "a", OK: false}}}
	if _, required, _ := (publish.QuorumPolicy{Fraction: 0.1}).Satisfied(one); required < 1 {
		t.Errorf("required = %d, want at least 1: a quorum of zero is met by delivering nothing", required)
	}
}

// TestHealth_OneFailureDoesNotCondemn is the threshold rule.
func TestHealth_OneFailureDoesNotCondemn(t *testing.T) {
	h := publish.NewRelayHealth()
	now := time.Unix(1791015900, 0)

	h.Record("relay-a", false, now)
	if ok, reason := h.Healthy("relay-a", now); !ok {
		t.Errorf("one timeout must not condemn a relay: %s", reason)
	}
	// Nor two.
	h.Record("relay-a", false, now.Add(time.Second))
	if ok, _ := h.Healthy("relay-a", now.Add(time.Second)); !ok {
		t.Error("two failures must not condemn a relay at a threshold of three")
	}
	// The third marks it unhealthy.
	h.Record("relay-a", false, now.Add(2*time.Second))
	ok, reason := h.Healthy("relay-a", now.Add(2*time.Second))
	if ok {
		t.Error("three consecutive failures must mark a relay unhealthy")
	}
	if !strings.Contains(reason, "consecutive") {
		t.Errorf("the reason must explain the exclusion, got: %q", reason)
	}
}

// TestHealth_RecoversAfterTheWindow is why the window exists: permanent exclusion turns one bad
// minute into a permanently reduced relay set with no way to notice.
func TestHealth_RecoversAfterTheWindow(t *testing.T) {
	h := publish.NewRelayHealth()
	h.FailThreshold = 2
	h.RecoveryWindow = time.Minute
	now := time.Unix(1791015900, 0)

	h.Record("relay-a", false, now)
	h.Record("relay-a", false, now.Add(time.Second))
	if ok, _ := h.Healthy("relay-a", now.Add(time.Second)); ok {
		t.Fatal("two failures at threshold two must mark it unhealthy")
	}

	// Before the window it is avoided.
	if ok, _ := h.Healthy("relay-a", now.Add(30*time.Second)); ok {
		t.Error("before the recovery window the relay must still be avoided")
	}
	// After the window it is retried, so a recovered node rejoins without intervention.
	if ok, reason := h.Healthy("relay-a", now.Add(2*time.Minute)); !ok {
		t.Errorf("after the recovery window the relay must be retried: %s", reason)
	}
}

// TestHealth_UnknownRelayIsAttempted guards against a fresh process refusing to try anything.
func TestHealth_UnknownRelayIsAttempted(t *testing.T) {
	h := publish.NewRelayHealth()
	if ok, reason := h.Healthy("never-seen", time.Unix(1791015900, 0)); !ok {
		t.Errorf("an unseen relay must be attempted, not assumed unhealthy: %s", reason)
	}
}

// TestHealth_SuccessResetsTheCounter keeps a recovered relay from being condemned by old failures.
func TestHealth_SuccessResetsTheCounter(t *testing.T) {
	h := publish.NewRelayHealth()
	now := time.Unix(1791015900, 0)

	for i := 0; i < 2; i++ {
		h.Record("relay-a", false, now.Add(time.Duration(i)*time.Second))
	}
	h.Record("relay-a", true, now.Add(2*time.Second))
	h.Record("relay-a", false, now.Add(3*time.Second))

	if ok, reason := h.Healthy("relay-a", now.Add(3*time.Second)); !ok {
		t.Errorf("a success must reset the consecutive count, so one later failure is not fatal: %s", reason)
	}
}

// TestFailover_IsBounded is the anti-hang rule. Each attempt costs up to a timeout, so N dead
// relays means N timeouts unless the cap bounds it.
func TestFailover_IsBounded(t *testing.T) {
	var attempts int32
	counting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer counting.Close()

	// Five relays, all failing, cap of two.
	urls := []string{counting.URL, counting.URL + "/2", counting.URL + "/3", counting.URL + "/4", counting.URL + "/5"}
	pub, err := publish.New(urls...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pub.Client = counting.Client()

	out, err := pub.PublishWithPolicy(context.Background(), testEnvelope(),
		publish.QuorumPolicy{Required: 1}, nil, publish.FailoverPolicy{MaxAttempts: 2}, nil)
	if err != nil {
		t.Fatalf("PublishWithPolicy: %v", err)
	}
	if len(out.Results) != 2 {
		t.Errorf("attempts = %d, want 2: an unbounded failover over a dead set is a hang", len(out.Results))
	}
}

// TestFailover_StopsOnceEnoughAcked keeps a caller that needs one acknowledgement from paying for
// the rest.
func TestFailover_StopsOnceEnoughAcked(t *testing.T) {
	ok := relayBehaviour(t, http.StatusOK)
	pub, err := publish.New(ok.URL, ok.URL+"/2", ok.URL+"/3")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pub.Client = ok.Client()

	out, err := pub.PublishWithPolicy(context.Background(), testEnvelope(),
		publish.QuorumPolicy{Required: 1}, nil, publish.FailoverPolicy{MinAcked: 1}, nil)
	if err != nil {
		t.Fatalf("PublishWithPolicy: %v", err)
	}
	if len(out.Results) != 1 {
		t.Errorf("attempts = %d, want 1: MinAcked means stop once satisfied", len(out.Results))
	}
	if !out.QuorumMet {
		t.Error("the quorum must be reported as met")
	}
}

// TestFailover_TriesTheHealthyRelayFirst is the point of health tracking: a relay that times out
// every time should stop being tried first, or every publication pays its timeout.
func TestFailover_TriesTheHealthyRelayFirst(t *testing.T) {
	good := relayBehaviour(t, http.StatusOK)
	bad := relayBehaviour(t, http.StatusInternalServerError)

	// The BAD relay is configured first, so configured order alone would try it first.
	pub, err := publish.New(bad.URL, good.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pub.Client = good.Client()

	health := publish.NewRelayHealth()
	health.FailThreshold = 1
	now := time.Unix(1791015900, 0)
	// Record the bad relay as unhealthy. The label is the URL, matching what publish uses.
	health.Record(bad.URL, false, now)
	health.Record(bad.URL, false, now)

	out, err := pub.PublishWithPolicy(context.Background(), testEnvelope(),
		publish.QuorumPolicy{Required: 1}, health, publish.FailoverPolicy{MinAcked: 1},
		func() time.Time { return now })
	if err != nil {
		t.Fatalf("PublishWithPolicy: %v", err)
	}
	if len(out.Results) == 0 {
		t.Fatal("no attempts were made")
	}
	if out.Results[0].Relay != good.URL {
		t.Errorf("the healthy relay must be tried first, got %s", out.Results[0].Relay)
	}
}

// TestFailover_StillReachesQuorumOverTheSickOnes confirms an unhealthy relay is tried LAST rather
// than dropped: dropping would shrink the relay set with no way to notice.
func TestFailover_StillReachesQuorumOverTheSickOnes(t *testing.T) {
	ok := relayBehaviour(t, http.StatusOK)
	pub, err := publish.New(ok.URL, ok.URL+"/2")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pub.Client = ok.Client()

	health := publish.NewRelayHealth()
	now := time.Unix(1791015900, 0)
	// Mark the first as sick, so it is ordered last but still attempted.
	for i := 0; i < 5; i++ {
		health.Record(ok.URL, false, now)
	}

	out, err := pub.PublishWithPolicy(context.Background(), testEnvelope(),
		publish.QuorumPolicy{Required: 2}, health, publish.FailoverPolicy{}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("PublishWithPolicy: %v", err)
	}
	if len(out.Results) != 2 {
		t.Errorf("attempts = %d, want 2: a sick relay must still be tried, only later", len(out.Results))
	}
	if !out.QuorumMet {
		t.Errorf("quorum must be reachable over the sick relays, reached %d of %d",
			out.QuorumReached, out.QuorumRequired)
	}
}

// TestHealth_SnapshotIsStable keeps a report comparable across runs.
func TestHealth_SnapshotIsStable(t *testing.T) {
	h := publish.NewRelayHealth()
	now := time.Unix(1791015900, 0)
	h.Record("z-relay", true, now)
	h.Record("a-relay", false, now)
	h.Record("m-relay", true, now)

	snap := h.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("entries = %d, want 3", len(snap))
	}
	want := []string{"a-relay", "m-relay", "z-relay"}
	for i, w := range want {
		if snap[i].Relay != w {
			t.Errorf("position %d = %s, want %s: the report must be sorted for comparison", i, snap[i].Relay, w)
		}
	}

	report := publish.HealthReport(snap)
	if !strings.Contains(report, "a-relay") || !strings.Contains(report, "1 failed") {
		t.Errorf("the report must show the counters, got: %q", report)
	}
}

// TestHealthReport_EmptyIsHonest keeps "no data" from reading as "all healthy".
func TestHealthReport_EmptyIsHonest(t *testing.T) {
	got := publish.HealthReport(nil)
	if !strings.Contains(got, "no relay health recorded") {
		t.Errorf("an empty report must say so rather than implying health, got: %q", got)
	}
}

// TestQuorum_ShortfallIsAnOutcomeNotAnError keeps a delivery result out of the error channel, the
// same reasoning as a partial fan-out.
func TestQuorum_ShortfallIsAnOutcomeNotAnError(t *testing.T) {
	bad := relayBehaviour(t, http.StatusInternalServerError)
	pub, err := publish.New(bad.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pub.Client = bad.Client()

	out, err := pub.PublishWithPolicy(context.Background(), testEnvelope(),
		publish.QuorumPolicy{Required: 2}, nil, publish.FailoverPolicy{}, nil)
	if err != nil {
		t.Fatalf("falling short of a quorum is a result, not an error: %v", err)
	}
	if out.QuorumMet {
		t.Error("the quorum must not be reported as met")
	}
	if out.Acked != 0 {
		t.Errorf("acked = %d, want 0", out.Acked)
	}
}
