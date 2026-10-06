package scoring

import (
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// fixtures ---------------------------------------------------------------

// probeReceipt builds a minimal valid probe receipt for scoring tests.
//
// It does not need a real signature: scoring reads structure, not signatures —
// verification is the verifier's job, and S1 already covers that.
func probeReceipt(agentID, url, contentHash string) *receipt.Receipt {
	return &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: "0x" + strings.Repeat("ab", 32),
		AgentID:   agentID,
		Epoch:     42,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": url},
			SpecHash:      "sha256:" + strings.Repeat("3d", 32),
			SelfGenerated: true,
		},
		Work: receipt.Work{
			Provider:   "local",
			StartedAt:  1791015800,
			FinishedAt: 1791015862,
		},
		Result: receipt.Result{
			Value: "200",
			Hash:  "sha256:" + strings.Repeat("c1", 32),
		},
		Anchors: []receipt.Anchor{{
			URL:         url,
			ContentHash: contentHash,
			FetchedAt:   1791015810,
			Status:      200,
			Bytes:       2048,
		}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
}

const testURL = "https://api.example.com/health"
const testContentHash = "sha256:" + "7b2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e"
const agentA = "agent:eip155:8453:0x000000000000000000000000000000000000000a"
const agentB = "agent:eip155:8453:0x000000000000000000000000000000000000000b"

func fullParams() Params {
	return Params{Verified: true, SameDomainRepeats: 0}
}

// artifactKey ------------------------------------------------------------

// TestArtifactKey_Deterministic: the same work must always hash the same, or the
// ledger cannot dedupe anything.
func TestArtifactKey_Deterministic(t *testing.T) {
	r := probeReceipt(agentA, testURL, testContentHash)

	first, err := ArtifactKey(r)
	if err != nil {
		t.Fatalf("ArtifactKey: %v", err)
	}
	for i := 0; i < 50; i++ {
		again, err := ArtifactKey(r)
		if err != nil {
			t.Fatalf("ArtifactKey: %v", err)
		}
		if again != first {
			t.Fatalf("artifact key drifted at iteration %d", i)
		}
	}
	if !strings.HasPrefix(first, "sha256:") {
		t.Errorf("artifact key should carry the sha256: prefix, got %s", first)
	}
}

// TestArtifactKey_IsAgentIndependent is the crux of A6.
//
// Two different agents observing the same url+content must produce the SAME
// artifact key. If the agent were part of the key, every agent could claim
// novelty for the same content and farming would pay.
func TestArtifactKey_IsAgentIndependent(t *testing.T) {
	a, err := ArtifactKey(probeReceipt(agentA, testURL, testContentHash))
	if err != nil {
		t.Fatalf("ArtifactKey: %v", err)
	}
	b, err := ArtifactKey(probeReceipt(agentB, testURL, testContentHash))
	if err != nil {
		t.Fatalf("ArtifactKey: %v", err)
	}

	if a != b {
		t.Errorf("artifact key must not depend on the agent (invariant A6):\n  A=%s\n  B=%s", a, b)
	}
}

func TestArtifactKey_DistinguishesInputs(t *testing.T) {
	base, err := ArtifactKey(probeReceipt(agentA, testURL, testContentHash))
	if err != nil {
		t.Fatalf("ArtifactKey: %v", err)
	}

	cases := map[string]*receipt.Receipt{
		"different url":     probeReceipt(agentA, "https://api.example.com/other", testContentHash),
		"different content": probeReceipt(agentA, testURL, "sha256:"+strings.Repeat("ff", 32)),
	}

	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ArtifactKey(r)
			if err != nil {
				t.Fatalf("ArtifactKey: %v", err)
			}
			if got == base {
				t.Error("artifact key must change when the observed artifact changes")
			}
		})
	}

	// A different task type over the same url+content is a different question,
	// so it must be a different key rather than shadowing the probe.
	t.Run("different task type", func(t *testing.T) {
		r := probeReceipt(agentA, testURL, testContentHash)
		r.Task.Type = receipt.TaskExtract
		got, err := ArtifactKey(r)
		if err != nil {
			t.Fatalf("ArtifactKey: %v", err)
		}
		if got == base {
			t.Error("artifact key must distinguish task types")
		}
	})
}

func TestArtifactKey_RejectsBadReceipts(t *testing.T) {
	cases := map[string]*receipt.Receipt{
		"nil": nil,
		"unaccepted task type": func() *receipt.Receipt {
			r := probeReceipt(agentA, testURL, testContentHash)
			r.Task.Type = "summarize"
			return r
		}(),
		"no anchors": func() *receipt.Receipt {
			r := probeReceipt(agentA, testURL, testContentHash)
			r.Anchors = nil
			return r
		}(),
		"empty content hash": func() *receipt.Receipt {
			r := probeReceipt(agentA, testURL, testContentHash)
			r.Anchors[0].ContentHash = ""
			return r
		}(),
		"nil spec": func() *receipt.Receipt {
			r := probeReceipt(agentA, testURL, testContentHash)
			r.Task.Spec = nil
			return r
		}(),
		"identifiable by nothing": func() *receipt.Receipt {
			r := probeReceipt(agentA, testURL, testContentHash)
			r.Task.Spec = map[string]any{"unrelated": 1}
			r.Task.SpecHash = ""
			return r
		}(),
	}

	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ArtifactKey(r); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// compute tasks have no url; their identity comes from the operation.
func TestArtifactKey_ComputeUsesOperation(t *testing.T) {
	mk := func(op, input string) *receipt.Receipt {
		r := probeReceipt(agentA, "", testContentHash)
		r.Task.Type = receipt.TaskCompute
		r.Task.Spec = map[string]any{"op": op, "input": input}
		r.Anchors[0].URL = "inline"
		return r
	}

	k1, err := ArtifactKey(mk("hash", "hello"))
	if err != nil {
		t.Fatalf("ArtifactKey: %v", err)
	}
	k2, err := ArtifactKey(mk("hash", "hello"))
	if err != nil {
		t.Fatalf("ArtifactKey: %v", err)
	}
	k3, err := ArtifactKey(mk("hash", "world"))
	if err != nil {
		t.Fatalf("ArtifactKey: %v", err)
	}

	if k1 != k2 {
		t.Error("identical computations must share an artifact key")
	}
	if k1 == k3 {
		t.Error("different computations must not collide")
	}
}

// domain -----------------------------------------------------------------

func TestDomainOfURL(t *testing.T) {
	cases := map[string]string{
		"https://api.example.com/health":  "api.example.com",
		"http://API.Example.COM/x":        "api.example.com",
		"https://example.com:8443/path":   "example.com",
		"https://user:pw@example.com/x":   "example.com",
		"https://example.com":             "example.com",
		"inline":                          "",
		"":                                "",
		"https://sub.example.com/a?b=1#c": "sub.example.com",
	}
	for in, want := range cases {
		if got := DomainOfURL(in); got != want {
			t.Errorf("DomainOfURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// ledger: global dedup (A6) ----------------------------------------------

func TestMemLedger_FirstSightingThenRepeat(t *testing.T) {
	l := NewMemLedger()
	at := time.Unix(1791015800, 0)

	seen, err := l.Observe("key-1", agentA, at)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !seen {
		t.Fatal("first observation should be novel")
	}

	seen, err = l.Observe("key-1", agentB, at)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if seen {
		t.Fatal("second observation by another agent must NOT be novel (invariant A6)")
	}

	s, ok := l.Lookup("key-1")
	if !ok {
		t.Fatal("Lookup should find the sighting")
	}
	if s.FirstAgentID != agentA {
		t.Errorf("FirstAgentID = %q, want the first observer %q", s.FirstAgentID, agentA)
	}
	if s.SeenCount != 2 {
		t.Errorf("SeenCount = %d, want 2", s.SeenCount)
	}
	if l.Len() != 1 {
		t.Errorf("Len = %d, want 1 (one distinct artifact)", l.Len())
	}
}

func TestMemLedger_RejectsBadInput(t *testing.T) {
	l := NewMemLedger()
	at := time.Unix(1791015800, 0)

	if _, err := l.Observe("", agentA, at); err == nil {
		t.Error("empty artifact key must be rejected")
	}
	if _, err := l.Observe("k", "", at); err == nil {
		t.Error("empty agent id must be rejected")
	}
}

func TestMemLedger_ConcurrentObserveIsSafe(t *testing.T) {
	l := NewMemLedger()
	at := time.Unix(1791015800, 0)

	const workers = 8
	done := make(chan struct{}, workers)
	novel := make(chan bool, workers)

	for w := 0; w < workers; w++ {
		go func() {
			defer func() { done <- struct{}{} }()
			// Every worker races on the SAME key: exactly one must win.
			seen, err := l.Observe("contended", agentA, at)
			if err != nil {
				t.Errorf("Observe: %v", err)
				return
			}
			novel <- seen
		}()
	}
	for i := 0; i < workers; i++ {
		<-done
	}
	close(novel)

	winners := 0
	for n := range novel {
		if n {
			winners++
		}
	}
	if winners != 1 {
		t.Errorf("%d workers claimed novelty, want exactly 1", winners)
	}
	if l.Len() != 1 {
		t.Errorf("Len = %d, want 1", l.Len())
	}
}
