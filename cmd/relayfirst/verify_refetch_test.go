package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// These tests cover the opt-in `--refetch` path of `relayfirst verify` (S1-10).
//
// The distinction they exist to protect: the default path verifies the *receipt*
// (signature and structure, no network), while `--refetch` verifies the *claim* (does
// the source still return the recorded bytes?). Collapsing the two would either weaken
// acceptance criterion ② (offline verification) or silently start rejecting honest
// receipts whose source changed. Both directions are asserted below.

const refetchTestKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"

// writeSignedReceipt writes a signed receipt whose single anchor points at url and records
// contentHash. It returns the file path.
func writeSignedReceipt(t *testing.T, url, contentHash string) string {
	t.Helper()

	agentID, err := receipt.DeriveAgentID(refetchTestKey, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	r := &receipt.Receipt{
		Schema:  receipt.Schema,
		AgentID: agentID,
		Epoch:   42,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": url},
			SpecHash:      "sha256:3d4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f",
			SelfGenerated: true,
		},
		Work: receipt.Work{
			Provider:   "local",
			StartedAt:  1791015800,
			FinishedAt: 1791015862,
		},
		Result: receipt.Result{
			Value: "200",
			Hash:  "sha256:c1d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9",
		},
		Anchors: []receipt.Anchor{{
			URL:         url,
			ContentHash: contentHash,
			FetchedAt:   1791015810,
			Status:      200,
			Bytes:       5,
		}},
		Verification: receipt.Verification{Status: receipt.VerificationPending, Stake: 50},
	}
	// Derive the id from the signed payload; Validate rejects a free-standing one
	// (S9-0h, finding B2).
	derived, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("DerivedReceiptID: %v", err)
	}
	r.ReceiptID = derived

	if err := r.Sign(refetchTestKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write receipt: %v", err)
	}
	return path
}

// captureRunVerify runs runVerify with stdout redirected, returning the parsed JSON.
//
// It reaches into os.Stdout because that is the CLI's actual output surface; asserting on
// the returned map instead would test a function the user never sees.
func captureRunVerify(t *testing.T, path string, flagArgs []string) (map[string]any, error) {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	runErr := runVerify(path, flagArgs)

	_ = w.Close()
	os.Stdout = orig

	var out map[string]any
	if decErr := json.NewDecoder(r).Decode(&out); decErr != nil {
		// A failure before printing (e.g. bad flags) leaves stdout empty; that is fine.
		if runErr == nil {
			t.Fatalf("decode stdout: %v", decErr)
		}
		return nil, runErr
	}
	return out, runErr
}

// TestVerify_DefaultIsOffline is the criterion-② guard: without --refetch, no network call
// may happen. The anchor points at a server that fails the test if it is ever contacted.
func TestVerify_DefaultIsOffline(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	path := writeSignedReceipt(t, srv.URL+"/x", anchor.HashString("hello"))

	out, err := captureRunVerify(t, path, nil)
	if err != nil {
		t.Fatalf("default verify must succeed offline: %v", err)
	}
	if hit {
		t.Fatal("default verify contacted the network; acceptance criterion ② is weakened")
	}
	if out["mode"] != "offline" {
		t.Errorf("mode = %v, want \"offline\"", out["mode"])
	}
	if out["valid"] != true {
		t.Errorf("valid = %v, want true", out["valid"])
	}
}

// TestVerify_RefetchAcceptsMatchingAnchor is the honest case for the opt-in path.
func TestVerify_RefetchAcceptsMatchingAnchor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	path := writeSignedReceipt(t, srv.URL+"/x", anchor.HashString("hello"))

	out, err := captureRunVerify(t, path, []string{"--refetch"})
	if err != nil {
		t.Fatalf("matching anchor must pass: %v", err)
	}
	if out["mode"] != "refetch" {
		t.Errorf("mode = %v, want \"refetch\"", out["mode"])
	}
	if out["anchorConsistency"] != true {
		t.Errorf("anchorConsistency = %v, want true", out["anchorConsistency"])
	}
}

// TestVerify_RefetchRejectsDriftedAnchor is the reason the flag exists: the source now
// returns different bytes than the receipt recorded.
//
// It asserts that the receipt is still reported authentic (`valid` stays true) while the
// anchor check fails. Conflating "drifted" with "forged" would send a reviewer looking for
// a forger when the source simply changed.
func TestVerify_RefetchRejectsDriftedAnchor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("different content now"))
	}))
	defer srv.Close()

	path := writeSignedReceipt(t, srv.URL+"/x", anchor.HashString("hello"))

	out, err := captureRunVerify(t, path, []string{"--refetch"})
	if err == nil {
		t.Fatal("drifted anchor must be reported as a failure")
	}
	if out["anchorConsistency"] != false {
		t.Errorf("anchorConsistency = %v, want false", out["anchorConsistency"])
	}
	if out["valid"] != true {
		t.Errorf("valid = %v; the receipt is authentic, only its claim drifted — must stay true", out["valid"])
	}
}

// TestVerify_RefetchUnreachableSourceIsNotAPass guards the same trap the verifier's
// AnchorConsistency guards: treating an unreachable source as agreement would make the
// check avoidable by taking the source offline.
func TestVerify_RefetchUnreachableSourceIsNotAPass(t *testing.T) {
	// Bind and immediately close so the port is almost certainly refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	path := writeSignedReceipt(t, url+"/x", anchor.HashString("hello"))

	out, err := captureRunVerify(t, path, []string{"--refetch"})
	if err == nil {
		t.Fatal("an unreachable source must not be reported as agreement")
	}
	if out["anchorConsistency"] != false {
		t.Errorf("anchorConsistency = %v, want false", out["anchorConsistency"])
	}
}

// TestVerify_RefetchRejectsUnknownFlag keeps the flag surface honest: a typo must fail
// rather than be silently ignored.
func TestVerify_RefetchRejectsUnknownFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	path := writeSignedReceipt(t, srv.URL+"/x", anchor.HashString("hello"))

	if _, err := captureRunVerify(t, path, []string{"--refetchh"}); err == nil {
		t.Fatal("an unknown flag must be rejected, not ignored")
	}
}
