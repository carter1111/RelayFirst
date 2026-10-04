package receipt_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// This is an external test (package receipt_test) so it exercises the public
// API exactly as the CLI does: Unmarshal -> ValidateStructure -> Validate.

const demoPrivKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"

func demoReceipt(t *testing.T) *receipt.Receipt {
	t.Helper()

	agentID, err := receipt.DeriveAgentID(demoPrivKey, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}

	r := &receipt.Receipt{
		Schema:  receipt.Schema,
		AgentID: agentID,
		Epoch:   42,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": "https://api.example.com/health"},
			SpecHash:      "sha256:3d4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f",
			SelfGenerated: true,
		},
		Work: receipt.Work{
			Provider:   "openai",
			Model:      "gpt-5.5",
			TokensIn:   1234,
			TokensOut:  567,
			StartedAt:  1791015800,
			FinishedAt: 1791015862,
		},
		Result: receipt.Result{
			Value: "200",
			Hash:  "sha256:c1d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9",
		},
		Anchors: []receipt.Anchor{{
			URL:         "https://api.example.com/health",
			ContentHash: "sha256:7b2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e",
			FetchedAt:   1791015810,
			Status:      200,
			Bytes:       20480,
		}},
		Verification: receipt.Verification{Status: receipt.VerificationPending, Stake: 50},
	}

	// Derive the id from the signed payload: Validate rejects a free-standing id
	// (S9-0h, finding B2).
	id, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive receipt id: %v", err)
	}
	r.ReceiptID = id

	if err := r.Sign(demoPrivKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return r
}

// TestOfflineVerifier_AcceptsSignedReceipt mirrors acceptance criterion ②:
// after a JSON round trip (as if read from disk), the receipt still validates
// with no server, no network, and no database.
func TestOfflineVerifier_AcceptsSignedReceipt(t *testing.T) {
	r := demoReceipt(t)

	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Write to a temp file and read it back, exactly as `relayfirst verify` does.
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write temp receipt: %v", err)
	}
	fromDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp receipt: %v", err)
	}

	parsed, err := receipt.Unmarshal(fromDisk)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := parsed.ValidateStructure(); err != nil {
		t.Fatalf("structure: %v", err)
	}
	if err := parsed.Validate(nil); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestOfflineVerifier_RejectsTamperedFile is the negative half of criterion ②
// and the positive half of ④.
func TestOfflineVerifier_RejectsTamperedFile(t *testing.T) {
	r := demoReceipt(t)

	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}

	// Flip the result while leaving the signature untouched.
	generic["result"].(map[string]any)["value"] = "500"

	tampered, err := json.Marshal(generic)
	if err != nil {
		t.Fatalf("marshal tampered: %v", err)
	}

	parsed, err := receipt.Unmarshal(tampered)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := parsed.Validate(nil); err == nil {
		t.Fatal("a tampered receipt must not validate")
	}
}

// TestDeriveAgentID is stable so that a user's identity does not change across
// runs — otherwise receipts would stop verifying after an upgrade.
func TestDeriveAgentID(t *testing.T) {
	first, err := receipt.DeriveAgentID(demoPrivKey, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}
	second, err := receipt.DeriveAgentID(demoPrivKey, 8453)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}
	if first != second {
		t.Errorf("agent id is not deterministic: %s vs %s", first, second)
	}

	// Same key on a different chain must yield a different agent id.
	other, err := receipt.DeriveAgentID(demoPrivKey, 1)
	if err != nil {
		t.Fatalf("DeriveAgentID: %v", err)
	}
	if other == first {
		t.Error("agent id must embed the chain id")
	}
}
