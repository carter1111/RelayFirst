package receipt

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/eip712"
)

// Tests for the versioning foundation (S9-0, invariant A9 — MVP.md §17.4/§17.5).
//
// The thing these protect is subtle, so it is worth stating plainly: an
// out-of-date verifier must say "I do not know this version", not "this is
// forged". Conflating those two makes an honest node look like a forger, and the
// damage is done the first time an operator acts on that false report.

func TestParseSchema(t *testing.T) {
	cases := []struct {
		in           string
		major, minor int
		ok           bool
	}{
		// The published string. Must parse, and must keep working forever.
		{"relayfirst.receipt.v1", 1, 0, true},

		// A missing minor is zero, so these two denote the same version.
		{"relayfirst.receipt.v1.0", 1, 0, true},
		{"relayfirst.receipt.v2", 2, 0, true},
		{"relayfirst.receipt.v2.7", 2, 7, true},
		{"relayfirst.receipt.v10.20", 10, 20, true},

		// Malformed: wrong namespace, missing/non-numeric version, empty minor.
		{"", 0, 0, false},
		{"relayfirst.receipt", 0, 0, false},
		{"relayfirst.receipt.", 0, 0, false},
		{"relayfirst.receipt.v", 0, 0, false},
		{"relayfirst.receipt.vX", 0, 0, false},
		{"relayfirst.receipt.v1.", 0, 0, false},
		{"relayfirst.receipt.v1.x", 0, 0, false},
		{"relayfirst.receipt.v-1", 0, 0, false},
		{"other.receipt.v1", 0, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			major, minor, ok := ParseSchema(tc.in)
			if ok != tc.ok {
				t.Fatalf("ParseSchema(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			}
			if !ok {
				return
			}
			if major != tc.major || minor != tc.minor {
				t.Errorf("ParseSchema(%q) = (%d,%d), want (%d,%d)", tc.in, major, minor, tc.major, tc.minor)
			}
		})
	}
}

// TestCheckSchema_AcceptsSupportedMajor pins the published string and the
// equivalent minor-zero form.
func TestCheckSchema_AcceptsSupportedMajor(t *testing.T) {
	for _, s := range []string{
		Schema,                    // the exact string already in circulation
		"relayfirst.receipt.v1",   // spelled out
		"relayfirst.receipt.v1.0", // same version, explicit minor
		"relayfirst.receipt.v1.9", // higher minor = additive, still checkable
	} {
		if err := CheckSchema(s); err != nil {
			t.Errorf("CheckSchema(%q) = %v, want nil", s, err)
		}
	}
}

// TestCheckSchema_NewerMajorIsUnsupportedNotInvalid is the core A9 distinction.
//
// A receipt from a future version must be reported as *UnsupportedError, because
// the honest answer is "upgrade the verifier" — not "this is broken".
func TestCheckSchema_NewerMajorIsUnsupportedNotInvalid(t *testing.T) {
	err := CheckSchema("relayfirst.receipt.v2")
	if err == nil {
		t.Fatal("a future major must not be accepted by this build")
	}

	var un *UnsupportedError
	if !errors.As(err, &un) {
		t.Fatalf("want *UnsupportedError, got %T: %v", err, err)
	}

	// It must NOT be a ValidationError. That type means "I checked and it is
	// broken", which would send an operator hunting for a forger.
	var ve *ValidationError
	if errors.As(err, &ve) {
		t.Errorf("future major reported as invalid, which misrepresents an old verifier as a detector of forgery: %v", err)
	}

	// The message must name the direction, since the fix differs by side.
	if !strings.Contains(err.Error(), "newer") {
		t.Errorf("message should say the receipt is newer than this build: %q", err.Error())
	}
}

// TestCheckSchema_MalformedIsInvalid is the other side: a genuinely broken
// string is a defect in the receipt, not a version mismatch.
func TestCheckSchema_MalformedIsInvalid(t *testing.T) {
	for _, s := range []string{"", "relayfirst.receipt", "relayfirst.receipt.vX", "nonsense"} {
		err := CheckSchema(s)
		if err == nil {
			t.Errorf("CheckSchema(%q) = nil, want an error", s)
			continue
		}
		var un *UnsupportedError
		if errors.As(err, &un) {
			t.Errorf("CheckSchema(%q) reported malformed input as merely unsupported: %v", s, err)
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("CheckSchema(%q) should be a ValidationError, got %T", s, err)
		}
	}
}

// TestValidateStructure_AcceptsHigherMinor confirms the grammar reaches the
// real validation path, not just the helper.
func TestValidateStructure_AcceptsHigherMinor(t *testing.T) {
	r := validReceipt(t)
	r.Schema = "relayfirst.receipt.v1.5"
	if err := r.ValidateStructure(); err != nil {
		t.Fatalf("a higher minor is additive and must validate: %v", err)
	}
}

// TestValidateStructure_FutureMajorIsUnsupported confirms the error type
// survives the full validation path.
func TestValidateStructure_FutureMajorIsUnsupported(t *testing.T) {
	r := validReceipt(t)
	r.Schema = "relayfirst.receipt.v2"

	err := r.ValidateStructure()
	var un *UnsupportedError
	if !errors.As(err, &un) {
		t.Fatalf("want *UnsupportedError from ValidateStructure, got %T: %v", err, err)
	}
}

// --- S9-0b2: verbatim signed payload -----------------------------------------

// TestSignedPayload_VerbatimMatchesStructural proves the two paths agree for an
// unchanged receipt.
//
// If they disagreed, switching a receipt to the verbatim form would silently
// change its payloadHash and invalidate its signature — a change that would look
// like a forgery.
func TestSignedPayload_VerbatimMatchesStructural(t *testing.T) {
	r := validReceipt(t)

	structural, err := r.SignedPayload()
	if err != nil {
		t.Fatalf("structural: %v", err)
	}

	// Adopt the structural bytes as the verbatim payload, as a signer would.
	r.Payload = string(structural)

	verbatim, err := r.SignedPayload()
	if err != nil {
		t.Fatalf("verbatim: %v", err)
	}

	if string(verbatim) != string(structural) {
		t.Errorf("verbatim and structural payloads differ:\n structural: %s\n verbatim:   %s", structural, verbatim)
	}
}

// TestSignedPayload_VerbatimIsUsedWithoutReserializing is the property that
// makes adding a field additive: when Payload is present, the structure is
// ignored entirely.
//
// The test mutates a structured field *after* freezing the payload. If the
// verifier re-serialized, the bytes would change; because it hashes verbatim,
// they do not. That is exactly the behaviour that lets a newer version add
// fields without breaking older verifiers.
func TestSignedPayload_VerbatimIsUsedWithoutReserializing(t *testing.T) {
	r := validReceipt(t)

	frozen, err := r.SignedPayload()
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	r.Payload = string(frozen)

	// Change the structure in a way that would alter a re-serialized payload.
	r.Result.Value = "500"
	r.Work.TokensOut = 999999

	got, err := r.SignedPayload()
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if string(got) != string(frozen) {
		t.Errorf("SignedPayload re-serialized despite a verbatim payload:\n want: %s\n got:  %s", frozen, got)
	}
}

// TestSignVerify_WithVerbatimPayload is the round trip: a receipt signed with a
// verbatim payload must verify.
func TestSignVerify_WithVerbatimPayload(t *testing.T) {
	r := validReceipt(t)

	// Freeze the payload the way a signer would, then sign.
	payload, err := r.SignedPayload()
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	r.Payload = string(payload)

	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := r.Validate(nil); err != nil {
		t.Fatalf("a receipt signed with a verbatim payload must verify: %v", err)
	}
}

// TestSignVerify_WithoutVerbatimPayloadStillWorks is the A9 §① guarantee:
// receipts signed before the field existed must keep verifying.
func TestSignVerify_WithoutVerbatimPayloadStillWorks(t *testing.T) {
	r := validReceipt(t)
	if r.Payload != "" {
		t.Fatal("precondition: the fixture must not carry a verbatim payload")
	}

	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := r.Validate(nil); err != nil {
		t.Fatalf("a receipt without a verbatim payload must still verify: %v", err)
	}
}

// TestMarshal_OmitsEmptyPayload keeps persisted bytes identical for receipts that
// predate the field.
//
// This matters because MarshalCanonical output is written to the store and used
// as the envelope payload. If an empty payload started appearing as
// `"payload":""`, every stored receipt's bytes would change — and a receipt's
// bytes are what its signature covers.
func TestMarshal_OmitsEmptyPayload(t *testing.T) {
	r := validReceipt(t)

	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"payload"`) {
		t.Errorf("an empty payload must be omitted so old receipts keep their bytes: %s", raw)
	}

	// And with a payload present, it must appear.
	r.Payload = `{"agentId":"x"}`
	raw, err = json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"payload"`) {
		t.Errorf("a non-empty payload must be serialized: %s", raw)
	}
}

// TestPayloadConsistency_TamperedFieldIsRejected is the regression guard for a
// defect introduced while building S9-0b2.
//
// # The defect
//
// Once a receipt carries a verbatim payload, the structured fields are a
// *rendering* of it rather than the signed content. The first implementation
// hashed the payload and never compared the two, so they could diverge: an
// attacker could flip `result.value` in the structured fields, the signature
// would still verify (it never covered them), and every consumer reading the
// fields would see a forged value on a receipt that looked authentic.
//
// This was found by tampering with a frozen corpus receipt and noticing the gate
// stayed green — which is why the gate is tested with a real attack rather than
// assumed to work.
func TestPayloadConsistency_TamperedFieldIsRejected(t *testing.T) {
	r := validReceipt(t)

	payload, err := r.SignedPayload()
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	r.Payload = string(payload)
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Sanity: the untampered receipt is valid.
	if err := r.Validate(nil); err != nil {
		t.Fatalf("precondition: untampered receipt must verify: %v", err)
	}

	// Now tamper with a field the signature does not directly cover.
	r.Result.Value = "999"

	err = r.Validate(nil)
	if err == nil {
		t.Fatal("a field that disagrees with the signed payload must be rejected; " +
			"otherwise the structured fields are unauthenticated and consumers read forged values")
	}
	if !strings.Contains(err.Error(), "payload does not match") {
		t.Errorf("expected a payload-consistency failure, got: %v", err)
	}
}

// TestPayloadConsistency_EachSignedFieldIsCovered walks the fields that live
// inside the signed payload and confirms each one is actually protected.
//
// A single spot-check on one field would not catch a check that silently skips
// others, so every field the payload covers is exercised.
func TestPayloadConsistency_EachSignedFieldIsCovered(t *testing.T) {
	mutations := map[string]func(r *Receipt){
		"result.value":        func(r *Receipt) { r.Result.Value = "tampered" },
		"result.hash":         func(r *Receipt) { r.Result.Hash = sha32("ff") },
		"task.specHash":       func(r *Receipt) { r.Task.SpecHash = sha32("ee") },
		"task.type":           func(r *Receipt) { r.Task.Type = TaskCompute },
		"work.tokensIn":       func(r *Receipt) { r.Work.TokensIn = 1 },
		"anchors.contentHash": func(r *Receipt) { r.Anchors[0].ContentHash = sha32("dd") },
		"anchors.url":         func(r *Receipt) { r.Anchors[0].URL = "https://evil.example.com/x" },
		"epoch":               func(r *Receipt) { r.Epoch = 43 },
		"agentId":             func(r *Receipt) { r.AgentID = "agent:eip155:8453:0x0000000000000000000000000000000000000001" },
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			r := validReceipt(t)

			payload, err := r.SignedPayload()
			if err != nil {
				t.Fatalf("payload: %v", err)
			}
			r.Payload = string(payload)
			if err := r.Sign(testPrivKey); err != nil {
				t.Fatalf("sign: %v", err)
			}

			mutate(&r)

			if err := r.Validate(nil); err == nil {
				t.Errorf("mutating %s was not detected; that field is not covered by the signed payload", name)
			}
		})
	}
}

// TestMarshalCanonical_PreservesPayload is the regression guard for a real defect
// (found by security review, not by the tests that existed).
//
// # The defect
//
// canonicalJSON's `case Receipt` enumerates keys explicitly and omitted
// `payload`. That writer — not json.Marshal — is the only serializer on the
// persistence, export and relay paths, so a receipt signed with a verbatim
// payload was silently stored and exported as a structural one. The signed blob
// was destroyed, and with it the A9 §④ guarantee that a verifier can hash the
// exact signed bytes.
//
// # Why the original test missed it
//
// The earlier test asserted on json.Marshal, which *does* include the field, so
// it passed while the real path dropped it. This test therefore goes through
// MarshalCanonical and a full round trip, and asserts on verification rather
// than on the presence of a substring: the question is not "is the field in the
// JSON" but "does the receipt still verify after being persisted".
func TestMarshalCanonical_PreservesPayload(t *testing.T) {
	r := validReceipt(t)

	payload, err := r.SignedPayload()
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	r.Payload = string(payload)
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Persist exactly as the store, exporter and relay do.
	canonical, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal canonical: %v", err)
	}
	if !strings.Contains(string(canonical), `"payload"`) {
		t.Fatalf("the canonical writer dropped the signed payload; every persistence, export and relay path would lose it")
	}

	// Round trip through the real serializer, then verify. This is the assertion
	// that matters: the receipt must still be valid after storage.
	back, err := Unmarshal(canonical)
	if err != nil {
		t.Fatalf("unmarshal canonical: %v", err)
	}
	if back.Payload == "" {
		t.Fatal("payload did not survive the canonical round trip")
	}
	if err := back.Validate(nil); err != nil {
		t.Fatalf("a payload-bearing receipt no longer verifies after a canonical round trip: %v", err)
	}
}

// --- S9-0h: receiptId binding (finding B2) ------------------------------------

// TestReceiptID_TamperedIDIsRejected is the fix for a critical finding.
//
// receiptId is outside the signed payload, so the signature never covered it —
// yet it is the key the store, the dedup ledger, points idempotency and verdict
// lookup all use. Before this check, an attacker holding one valid receipt could
// re-emit it under arbitrary fresh ids, and each new id would defeat every one of
// those guards, because each looked like a different receipt.
func TestReceiptID_TamperedIDIsRejected(t *testing.T) {
	r := validReceipt(t)
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Sanity: the untampered receipt is valid.
	if err := r.Validate(nil); err != nil {
		t.Fatalf("precondition: a correctly-derived id must validate: %v", err)
	}

	// Re-emit under a fresh id, which is the attack.
	r.ReceiptID = "0x" + strings.Repeat("ab", 32)

	err := r.Validate(nil)
	if err == nil {
		t.Fatal("a receipt re-emitted under a fresh id must be rejected; " +
			"otherwise the dedup key is attacker-chosen and A6 is unenforced")
	}
	if !strings.Contains(err.Error(), "does not match the signed payload") {
		t.Errorf("expected a receiptId mismatch, got: %v", err)
	}
}

// TestReceiptID_DerivationIsPayloadBound confirms the id depends only on signed
// bytes, which is what makes recomputation a valid check.
func TestReceiptID_DerivationIsPayloadBound(t *testing.T) {
	a := validReceipt(t)
	b := validReceipt(t)

	idA, err := a.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	idB, err := b.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if idA != idB {
		t.Fatal("the same payload must derive the same id (dedup depends on it)")
	}

	// A change to a signed field must change the id.
	c := validReceipt(t)
	c.Result.Value = "different"
	idC, err := c.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if idC == idA {
		t.Error("changing a signed field must change the derived id")
	}
}

// --- S9-0h: schema binding across versions (finding B3) ----------------------

// TestTypedData_V1ProfileIsFrozen is the A9 §① guarantee for signing.
//
// v1 receipts are already in circulation. If the v1 profile changed, every one of
// them would stop verifying — the exact outcome A9 forbids. So the v1 digest must
// stay byte-identical: same domain version, same three message fields.
func TestTypedData_V1ProfileIsFrozen(t *testing.T) {
	r := validReceipt(t)
	td, err := r.TypedData()
	if err != nil {
		t.Fatalf("typed data: %v", err)
	}

	if td.Domain.Version != "1" {
		t.Errorf("v1 domain version must stay \"1\", got %q", td.Domain.Version)
	}

	fields := td.Types["RelayReceipt"]
	if len(fields) != 3 {
		t.Fatalf("v1 must sign exactly 3 fields, got %d: %+v", len(fields), fields)
	}
	for i, want := range []string{"agentId", "epoch", "payloadHash"} {
		if fields[i].Name != want {
			t.Errorf("v1 field %d = %q, want %q", i, fields[i].Name, want)
		}
	}
}

// TestTypedData_V2BindsSchemaAndReceiptID is the fix for B3.
//
// A v2 receipt must sign its schema major and its receiptId, so that relabelling
// a v1 receipt as v2 (or swapping an id) changes the digest and fails the
// signature rather than being silently accepted.
func TestTypedData_V2BindsSchemaAndReceiptID(t *testing.T) {
	r := validReceipt(t)
	r.Schema = "relayfirst.receipt.v2"

	td, err := r.typedDataForMajor(2)
	if err != nil {
		t.Fatalf("typed data: %v", err)
	}

	if td.Domain.Version != "2" {
		t.Errorf("v2 domain version = %q, want \"2\"", td.Domain.Version)
	}

	names := map[string]bool{}
	for _, f := range td.Types["RelayReceipt"] {
		names[f.Name] = true
	}
	for _, want := range []string{"receiptId", "schemaMajor"} {
		if !names[want] {
			t.Errorf("the v2 profile must sign %q; otherwise it stays unauthenticated metadata", want)
		}
	}
	if _, ok := td.Message["schemaMajor"]; !ok {
		t.Error("the v2 message must carry schemaMajor")
	}
}

// TestTypedData_ProfilesDiffer proves the relabel attack is closed: the same
// receipt signed under v1 and validated under v2 must produce a different digest,
// so the signature cannot carry over.
func TestTypedData_ProfilesDiffer(t *testing.T) {
	r := validReceipt(t)

	v1, err := r.typedDataForMajor(1)
	if err != nil {
		t.Fatalf("v1 typed data: %v", err)
	}
	v2, err := r.typedDataForMajor(2)
	if err != nil {
		t.Fatalf("v2 typed data: %v", err)
	}

	d1, err := eip712.HashTypedData(v1)
	if err != nil {
		t.Fatalf("v1 digest: %v", err)
	}
	d2, err := eip712.HashTypedData(v2)
	if err != nil {
		t.Fatalf("v2 digest: %v", err)
	}

	if string(d1) == string(d2) {
		t.Fatal("the v1 and v2 profiles must differ, or a v1 signature would verify under v2 rules")
	}
}

// TestTypedData_MalformedSchemaIsRejected confirms signing refuses to guess a
// profile. Guessing is how a receipt gets signed under the wrong rules.
func TestTypedData_MalformedSchemaIsRejected(t *testing.T) {
	r := validReceipt(t)
	r.Schema = "not-a-schema"

	if _, err := r.TypedData(); err == nil {
		t.Fatal("a malformed schema must not silently select a signing profile")
	}
}
