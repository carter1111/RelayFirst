package receipt

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/eip712"
	ep "github.com/relayfirst/relayfirst/internal/epoch"
)

const testPrivKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"

func testAgentID(t *testing.T) string {
	t.Helper()
	priv, err := eip712.PrivateKeyFromHex(testPrivKey)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	addr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	return AgentID(8453, addr)
}

func sha32(ch string) string { return "sha256:" + strings.Repeat(ch, 32) }

func validReceipt(t *testing.T) Receipt {
	t.Helper()
	return Receipt{
		Schema:    Schema,
		ReceiptID: "0x" + strings.Repeat("9a", 32),
		AgentID:   testAgentID(t),
		Epoch:     42,
		Task: Task{
			Type:          TaskProbe,
			Spec:          map[string]any{"url": "https://api.example.com/health"},
			SpecHash:      sha32("3d"),
			SelfGenerated: true,
		},
		Work: Work{
			Provider:   "openai",
			Model:      "gpt-5.5",
			TokensIn:   1234,
			TokensOut:  567,
			StartedAt:  1791015800,
			FinishedAt: 1791015862,
		},
		Result: Result{Value: "200", Hash: sha32("c1")},
		Anchors: []Anchor{{
			URL:         "https://api.example.com/health",
			ContentHash: sha32("7b"),
			FetchedAt:   1791015810,
			Status:      200,
			Bytes:       20480,
		}},
		Verification: Verification{
			Status: VerificationPending,
			Stake:  50,
		},
	}
}

func TestValidateStructure_AcceptsValid(t *testing.T) {
	r := validReceipt(t)
	if err := r.ValidateStructure(); err != nil {
		t.Fatalf("expected valid receipt, got: %v", err)
	}
}

// TestValidateStructure_RejectsGeneratedTask is the guard for invariant A2:
// task types without independent ground truth must never be accepted, because
// accepting them would force a dispute layer into the MVP.
func TestValidateStructure_RejectsGeneratedTask(t *testing.T) {
	for _, bad := range []TaskType{"summarize", "classify", "translate", "fetch_and_summarize", ""} {
		r := validReceipt(t)
		r.Task.Type = bad
		err := r.ValidateStructure()
		if err == nil {
			t.Errorf("task type %q must be rejected", bad)
			continue
		}
		if !strings.Contains(err.Error(), "not accepted") {
			t.Errorf("task type %q: unexpected error %v", bad, err)
		}
	}
}

func TestValidateStructure_RequiresAnchor(t *testing.T) {
	r := validReceipt(t)
	r.Anchors = nil
	if err := r.ValidateStructure(); err == nil {
		t.Error("a receipt with no anchors must be rejected")
	}
}

func TestValidateStructure_RejectsBadHashes(t *testing.T) {
	cases := map[string]func(*Receipt){
		"short contentHash":   func(r *Receipt) { r.Anchors[0].ContentHash = "sha256:abcd" },
		"missing contentHash": func(r *Receipt) { r.Anchors[0].ContentHash = "" },
		"odd contentHash":     func(r *Receipt) { r.Anchors[0].ContentHash = "sha256:abc" },
		"empty result hash":   func(r *Receipt) { r.Result.Hash = "" },
		"empty result value":  func(r *Receipt) { r.Result.Value = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := validReceipt(t)
			mutate(&r)
			if err := r.ValidateStructure(); err == nil {
				t.Error("expected rejection")
			}
		})
	}
}

func TestValidateStructure_RejectsBadSchemaAndIDs(t *testing.T) {
	cases := map[string]func(*Receipt){
		"wrong schema":    func(r *Receipt) { r.Schema = "relayfirst.receipt.v2" },
		"empty receiptId": func(r *Receipt) { r.ReceiptID = "" },
		"empty agentId":   func(r *Receipt) { r.AgentID = "" },
		"spec hash empty": func(r *Receipt) { r.Task.SpecHash = "" },
		"nil spec":        func(r *Receipt) { r.Task.Spec = nil },
		"time runs back":  func(r *Receipt) { r.Work.FinishedAt = r.Work.StartedAt - 1 },
		"status unset":    func(r *Receipt) { r.Anchors[0].Status = 0 },
		"bad signature":   func(r *Receipt) { r.Signature = "0x1234" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := validReceipt(t)
			mutate(&r)
			if err := r.ValidateStructure(); err == nil {
				t.Error("expected rejection")
			}
		})
	}
}

func TestSignAndValidate(t *testing.T) {
	r := validReceipt(t)
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := r.Validate(nil); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// TestValidate_TamperedReceiptRejected covers acceptance criterion ④: a
// receipt whose content is altered after signing must fail validation.
func TestValidate_TamperedReceiptRejected(t *testing.T) {
	cases := map[string]func(*Receipt){
		"anchor url changed":   func(r *Receipt) { r.Anchors[0].URL = "https://evil.example.com/x" },
		"anchor hash changed":  func(r *Receipt) { r.Anchors[0].ContentHash = sha32("ff") },
		"result value changed": func(r *Receipt) { r.Result.Value = "500" },
		"result hash changed":  func(r *Receipt) { r.Result.Hash = sha32("ff") },
		"epoch changed":        func(r *Receipt) { r.Epoch = 43 },
		"agentId swapped":      func(r *Receipt) { r.AgentID = strings.Replace(r.AgentID, "8453", "1", 1) },
		"tokens changed":       func(r *Receipt) { r.Work.TokensOut = 999999 },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := validReceipt(t)
			if err := r.Sign(testPrivKey); err != nil {
				t.Fatalf("Sign: %v", err)
			}
			mutate(&r)
			err := r.Validate(nil)
			if err == nil {
				t.Error("a tampered receipt must fail validation")
			}
		})
	}
}

// TestValidate_VerificationIsOutsideSignature matters because a verifier writes
// to the verification block after the producer signed. If it were covered by
// the signature, every verified receipt would fail its own validation.
func TestValidate_VerificationIsOutsideSignature(t *testing.T) {
	r := validReceipt(t)
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	verifier := "agent:eip155:8453:0xAb12"
	ts := int64(1791015900)
	recomputed := r.Result.Hash

	r.Verification = Verification{
		Status:         VerificationVerified,
		VerifierID:     &verifier,
		VerifiedAt:     &ts,
		Stake:          50,
		RecomputedHash: &recomputed,
	}

	if err := r.Validate(nil); err != nil {
		t.Fatalf("verification block must not affect signature validity: %v", err)
	}
}

func TestDigest_StableAcrossRuns(t *testing.T) {
	r := validReceipt(t)

	first, err := r.Digest()
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := r.Digest()
		if err != nil {
			t.Fatalf("Digest: %v", err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("digest is not deterministic at iteration %d", i)
		}
	}
}

// TestCanonicalJSON_Deterministic guards the byte-level stability that the
// signing digest depends on.
func TestCanonicalJSON_Deterministic(t *testing.T) {
	r := validReceipt(t)

	first, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}
	for i := 0; i < 50; i++ {
		again, err := r.MarshalCanonical()
		if err != nil {
			t.Fatalf("MarshalCanonical: %v", err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("canonical JSON is not deterministic at iteration %d", i)
		}
	}
}

// TestCanonicalJSON_KeyOrder covers the free-form task.spec map: its keys must
// be sorted, otherwise Go map iteration order would leak into the digest.
func TestCanonicalJSON_KeyOrder(t *testing.T) {
	r := validReceipt(t)
	r.Task.Spec = map[string]any{
		"zzz": 1, "aaa": 2, "mmm": 3, "bbb": 4, "yyy": 5,
	}

	out, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}

	i := strings.Index(string(out), `"spec":{`)
	if i < 0 {
		t.Fatal("spec object not found in canonical output")
	}
	spec := string(out)[i:]

	for _, pair := range [][2]string{{"aaa", "bbb"}, {"bbb", "mmm"}, {"mmm", "yyy"}, {"yyy", "zzz"}} {
		if strings.Index(spec, pair[0]) > strings.Index(spec, pair[1]) {
			t.Errorf("keys not sorted: %s should precede %s", pair[0], pair[1])
		}
	}
}

// TestCanonicalJSON_NoHTMLEscaping pins the deliberate divergence from Go's
// default encoding/json, which escapes <, > and & — JSON.stringify does not.
// If this regresses, Go and TypeScript digests diverge on such input.
func TestCanonicalJSON_NoHTMLEscaping(t *testing.T) {
	r := validReceipt(t)
	r.Result.Value = `<a href="x">Tom & Jerry</a>`

	out, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}
	s := string(out)

	if strings.Contains(s, `\u003c`) || strings.Contains(s, `\u0026`) || strings.Contains(s, `\u003e`) {
		t.Errorf("canonical JSON must not HTML-escape, got: %s", s)
	}
	if !strings.Contains(s, "Tom & Jerry") {
		t.Error("expected unescaped ampersand in output")
	}
}

func TestCanonicalJSON_MultibytePreserved(t *testing.T) {
	r := validReceipt(t)
	r.Result.Value = "立即中继 🚀"

	out, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}
	if !strings.Contains(string(out), "立即中继 🚀") {
		t.Errorf("multibyte text must be preserved verbatim, got: %s", out)
	}
}

func TestUnmarshal_RejectsUnknownFields(t *testing.T) {
	r := validReceipt(t)
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if _, err := Unmarshal(raw); err != nil {
		t.Fatalf("valid receipt should unmarshal: %v", err)
	}

	// Splice an unknown field into the object.
	injected := strings.Replace(string(raw), `{"schema":`, `{"evilField":1,"schema":`, 1)
	if _, err := Unmarshal([]byte(injected)); err == nil {
		t.Error("unknown fields must be rejected so schema drift cannot pass silently")
	}
}

func TestRoundTrip_MarshalUnmarshalValidate(t *testing.T) {
	r := validReceipt(t)
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	back, err := Unmarshal(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := back.Validate(nil); err != nil {
		t.Fatalf("round-tripped receipt must still validate: %v", err)
	}
}

func TestAgentID_Format(t *testing.T) {
	addr, err := eip712.HexToAddress("0x7F4dB0D9C4B8A6BCE8E1C22dA9c419E6e1F3A8B5")
	if err != nil {
		t.Fatalf("parse address: %v", err)
	}
	got := AgentID(8453, addr)
	want := "agent:eip155:8453:0x7f4db0d9c4b8a6bce8e1c22da9c419e6e1f3a8b5"
	if got != want {
		t.Errorf("agent id mismatch\n  got  %s\n  want %s", got, want)
	}
}

func TestNewEpoch(t *testing.T) {
	// The epoch clock is anchored at internal/epoch.GenesisValue
	// (2026-10-01T00:00:00Z == 1790841600), NOT at the unix zero.
	//
	// t0 is 2026-10-03T00:00:00Z == 1791014400, which is exactly two days after
	// genesis, so with a one-hour epoch it is epoch 48.
	const t0 = int64(1791014400)
	cases := []struct {
		offset int64
		want   uint64
	}{
		{0, 48},
		{3599, 48},
		{3600, 49},
	}
	for _, c := range cases {
		got := NewEpoch(time.Unix(t0+c.offset, 0), time.Hour)
		if got != c.want {
			t.Errorf("epoch at +%ds: got %d, want %d", c.offset, got, c.want)
		}
	}

	if got := NewEpoch(time.Unix(t0, 0), 0); got != 0 {
		t.Errorf("zero epoch length must yield epoch 0, got %d", got)
	}

	// Genesis itself is epoch 0, and a timestamp before it clamps to 0 rather
	// than underflowing into a huge uint64.
	if got := NewEpoch(ep.Genesis(), time.Hour); got != 0 {
		t.Errorf("genesis must be epoch 0, got %d", got)
	}
	if got := NewEpoch(time.Unix(ep.GenesisValue-86400, 0), time.Hour); got != 0 {
		t.Errorf("a pre-genesis timestamp must clamp to epoch 0, got %d", got)
	}
}
