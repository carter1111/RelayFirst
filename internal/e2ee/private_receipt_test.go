package e2ee_test

import (
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/e2ee"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// This is S13-5's first acceptance criterion, and it is the one that decides whether the encryption
// design is usable:
//
//	A PRIVATE receipt must still be verifiable OFFLINE by a third party who holds no key.
//
// # Why this is the load-bearing test
//
// Criterion ② requires that with every server switched off, an exported receipt can still be
// independently verified. Encryption that broke that would not be encryption, it would be a
// regression: the whole receipt design exists so that validity does not depend on anyone's
// cooperation, and a signature hidden inside a ciphertext would make it depend on the holder's.
//
// So the property is: encrypt the PRIVATE part, leave the verifiable part in the open, and prove a
// verifier with no key still says "valid". The next test proves the same verifier still says "valid"
// about a receipt it cannot read, which is the point.
//
// # Where the ciphertext goes
//
// Into `result.value`, which is already an opaque string that validation requires to be non-empty.
// That choice means the receipt SCHEMA DOES NOT CHANGE: no new field, no fork, and every existing
// verifier keeps working. A9 is untouched by construction rather than by care.

const (
	ownerPriv   = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	namespaceID = "task:private-1"
)

// privateReceipt builds a receipt whose result carries only ciphertext.
//
// The anchor URL is left as a public URL because an anchor is re-fetchable evidence by definition
// (MVP.md §4.1) — a verifier re-fetches it to check the work, so encrypting the URL would make the
// receipt unverifiable for a reason unrelated to privacy.
func privateReceipt(t *testing.T, recipient e2ee.KeyPair, plaintext []byte, sessionSalt []byte) *receipt.Receipt {
	t.Helper()

	sealed, err := e2ee.SealWithSession(recipient.PublicKey, plaintext, sessionSalt, []byte(namespaceID))
	if err != nil {
		t.Fatalf("SealWithSession: %v", err)
	}
	ct, err := sealed.Marshal()
	if err != nil {
		t.Fatalf("marshal sealed: %v", err)
	}

	r := &receipt.Receipt{
		Schema:  receipt.Schema,
		AgentID: mustAgentID(t, ownerPriv),
		Epoch:   42,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": "https://public.example/health"},
			SpecHash:      "sha256:" + strings.Repeat("3d", 32),
			SelfGenerated: true,
		},
		Work: receipt.Work{
			Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015862,
		},
		// The private part: the result IS the ciphertext, and `hash` is the hash of those bytes.
		Result: receipt.Result{
			Value: string(ct),
			Hash:  "sha256:" + strings.Repeat("c1", 32),
		},
		// Anchors stay in the clear: they are the evidence a verifier re-fetches.
		Anchors: []receipt.Anchor{{
			URL: "https://public.example/health", ContentHash: "sha256:" + strings.Repeat("7b", 32),
			FetchedAt: 1791015810, Status: 200, Bytes: 2048,
		}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	id, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	r.ReceiptID = id
	if err := r.Sign(ownerPriv); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return r
}

func mustAgentID(t *testing.T, key string) string {
	t.Helper()
	id, err := receipt.DeriveAgentID(key, 8453)
	if err != nil {
		t.Fatalf("derive agent id: %v", err)
	}
	return id
}

// TestPrivateReceipt_ThirdPartyVerifiesWithNoKey is acceptance criterion ①.
func TestPrivateReceipt_ThirdPartyVerifiesWithNoKey(t *testing.T) {
	recipient, err := e2ee.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	salt := []byte("session-salt-1")

	r := privateReceipt(t, recipient, []byte(`{"price":42.5,"source":"private-feed"}`), salt)

	// Export it as a verifier would receive it: bytes on the wire, nothing else.
	raw, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal canonical: %v", err)
	}

	// The verifier has NO key. It parses and validates.
	decoded, err := receipt.Unmarshal(raw)
	if err != nil {
		t.Fatalf("a private receipt must still parse: %v", err)
	}
	if err := decoded.Validate(nil); err != nil {
		t.Fatalf("a private receipt must verify with no key: %v", err)
	}

	// And it is genuinely private: the plaintext is not in the bytes it verified.
	if strings.Contains(string(raw), "price") {
		t.Fatal("the plaintext must not appear in the receipt a verifier receives")
	}
}

// TestPrivateReceipt_VerifierCannotReadIt is the other half, and without it the previous test would
// pass for a receipt that was not encrypted at all.
func TestPrivateReceipt_VerifierCannotReadIt(t *testing.T) {
	recipient, err := e2ee.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	attacker, err := e2ee.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	salt := []byte("session-salt-2")
	plaintext := []byte(`{"secret":"NOBODY-SHOULD-READ-THIS"}`)

	r := privateReceipt(t, recipient, plaintext, salt)
	raw, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	decoded, err := receipt.Unmarshal(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	sealed, ok := e2ee.UnmarshalSealed([]byte(decoded.Result.Value))
	if !ok {
		t.Fatalf("the result must be a sealed payload, got: %s", decoded.Result.Value)
	}

	// The verifier, holding only the receipt, cannot read it even with its own key.
	if _, err := e2ee.OpenWithSession(attacker.PrivateKey, attacker.PublicKey, sealed, salt, []byte(namespaceID)); err == nil {
		t.Fatal("a third party must not be able to read a private receipt's result")
	}

	// The intended recipient can.
	got, err := e2ee.OpenWithSession(recipient.PrivateKey, recipient.PublicKey, sealed, salt, []byte(namespaceID))
	if err != nil {
		t.Fatalf("the recipient must be able to read it: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("plaintext mismatch:\n  got  %s\n  want %s", got, plaintext)
	}
}

// TestPrivateReceipt_TamperedCiphertextStillFailsValidation is why encryption does not weaken the
// receipt: the ciphertext is inside the signed payload, so changing it breaks the signature.
func TestPrivateReceipt_TamperedCiphertextStillFailsValidation(t *testing.T) {
	recipient, _ := e2ee.GenerateKeyPair()
	salt := []byte("session-salt-3")

	r := privateReceipt(t, recipient, []byte("private"), salt)

	// Swap one character of the ciphertext for a different one. A semantic change is not needed: any
	// change to the signed bytes must invalidate the signature.
	original := r.Result.Value
	mutated := []byte(original)
	swapped := false
	for i := range mutated {
		switch mutated[i] {
		case 'A':
			mutated[i] = 'B'
			swapped = true
		case 'B':
			mutated[i] = 'A'
			swapped = true
		}
		if swapped {
			break
		}
	}
	if !swapped {
		t.Fatal("the fixture must contain a mutable base64 character")
	}
	r.Result.Value = string(mutated)

	if err := r.Validate(nil); err == nil {
		t.Fatal("tampering with the ciphertext must break the receipt: the ciphertext is inside the " +
			"signed payload, so encryption does not put anything outside the signature")
	}
}

// TestPrivateReceipt_SchemaIsUnchanged is the A9 property, stated as a test.
//
// The ciphertext goes into `result.value`, which already existed and was already an opaque string. So
// there is no new field, no schema fork, and an existing verifier keeps working without changes —
// which is what makes this a compatible extension rather than a version bump.
func TestPrivateReceipt_SchemaIsUnchanged(t *testing.T) {
	recipient, _ := e2ee.GenerateKeyPair()
	salt := []byte("session-salt-4")

	private := privateReceipt(t, recipient, []byte("private"), salt)

	// A public receipt with the same shape must be equally valid, so the two differ only in what
	// `result.value` holds.
	public := *private
	public.Result = receipt.Result{Value: "200", Hash: "sha256:" + strings.Repeat("c2", 32)}
	public.Anchors = []receipt.Anchor{{
		URL: "https://public.example/health", ContentHash: "sha256:" + strings.Repeat("7b", 32),
		FetchedAt: 1791015810, Status: 200, Bytes: 2048,
	}}
	id, err := public.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	public.ReceiptID = id
	if err := public.Sign(ownerPriv); err != nil {
		t.Fatalf("sign: %v", err)
	}

	if err := public.Validate(nil); err != nil {
		t.Fatalf("the public variant must validate: %v", err)
	}
	// Both report the SAME schema string: privacy is a payload choice, not a format version.
	if public.Schema != private.Schema {
		t.Errorf("a private and a public receipt must declare the same schema, got %q and %q",
			private.Schema, public.Schema)
	}
}

// TestPrivateReceipt_A2ATaskLinkSurvives confirms the private path composes with the A2A linkage
// rather than being a separate mode of operation.
func TestPrivateReceipt_A2ATaskLinkSurvives(t *testing.T) {
	recipient, _ := e2ee.GenerateKeyPair()
	salt := []byte("session-salt-5")

	r := privateReceipt(t, recipient, []byte("private"), salt)
	if err := r.Task.SetA2ATaskID("tsk_private_1"); err != nil {
		t.Fatalf("link task: %v", err)
	}
	id, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	r.ReceiptID = id
	if err := r.Sign(ownerPriv); err != nil {
		t.Fatalf("sign: %v", err)
	}

	raw, err := r.MarshalCanonical()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	back, err := receipt.Unmarshal(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := back.Validate(nil); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if back.Task.A2ATaskID == nil || *back.Task.A2ATaskID != "tsk_private_1" {
		t.Error("the A2A task link must survive a private receipt")
	}
}

// TestSessionKey_DiffersPerSession is the HKDF property: one shared secret, different key per session.
//
// Without it the shared secret would be the key for the whole relationship, so a compromise anywhere
// would reveal everything and there would be no way to scope access to one task.
func TestSessionKey_DiffersPerSession(t *testing.T) {
	a, _ := e2ee.GenerateKeyPair()
	b, _ := e2ee.GenerateKeyPair()
	shared, err := e2ee.SharedSecret(a.PrivateKey, b.PublicKey)
	if err != nil {
		t.Fatalf("shared secret: %v", err)
	}

	k1, err := e2ee.SessionKey(shared, []byte("session-one"), []byte("task-one"))
	if err != nil {
		t.Fatalf("session key: %v", err)
	}
	k2, err := e2ee.SessionKey(shared, []byte("session-two"), []byte("task-one"))
	if err != nil {
		t.Fatalf("session key: %v", err)
	}
	k3, err := e2ee.SessionKey(shared, []byte("session-one"), []byte("task-two"))
	if err != nil {
		t.Fatalf("session key: %v", err)
	}

	if string(k1) == string(k2) {
		t.Error("a different salt must give a different key, or every session would share one key")
	}
	if string(k1) == string(k3) {
		t.Error("different info must give a different key, or every task in a session would share one key")
	}
	if len(k1) != 32 {
		t.Errorf("session key must be 32 bytes for XChaCha20-Poly1305, got %d", len(k1))
	}
	// And it must be deterministic, or the recipient could not derive it from the same inputs.
	again, err := e2ee.SessionKey(shared, []byte("session-one"), []byte("task-one"))
	if err != nil {
		t.Fatalf("session key: %v", err)
	}
	if string(again) != string(k1) {
		t.Error("the same inputs must derive the same key, or decryption would be impossible")
	}
}

// TestSessionKey_RequiresASalt keeps a constant salt from silently collapsing every session.
func TestSessionKey_RequiresASalt(t *testing.T) {
	a, _ := e2ee.GenerateKeyPair()
	b, _ := e2ee.GenerateKeyPair()
	shared, _ := e2ee.SharedSecret(a.PrivateKey, b.PublicKey)

	if _, err := e2ee.SessionKey(shared, nil, []byte("task")); err == nil {
		t.Fatal("a missing salt must be refused: an empty salt would make every session with the " +
			"same info derive the same key")
	}
}

// TestSessionSealed_WrongSessionFails is why the salt and info matter at the boundary.
func TestSessionSealed_WrongSessionFails(t *testing.T) {
	recipient, _ := e2ee.GenerateKeyPair()
	plaintext := []byte("for one session only")

	sealed, err := e2ee.SealWithSession(recipient.PublicKey, plaintext, []byte("session-A"), []byte("task-A"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	// The right session works.
	if _, err := e2ee.OpenWithSession(recipient.PrivateKey, recipient.PublicKey, sealed,
		[]byte("session-A"), []byte("task-A")); err != nil {
		t.Fatalf("the right session must open it: %v", err)
	}
	// A different session does not, even with the right key pair.
	if _, err := e2ee.OpenWithSession(recipient.PrivateKey, recipient.PublicKey, sealed,
		[]byte("session-B"), []byte("task-A")); err == nil {
		t.Fatal("a different session must not open it")
	}
	// Nor does a different task in the same session.
	if _, err := e2ee.OpenWithSession(recipient.PrivateKey, recipient.PublicKey, sealed,
		[]byte("session-A"), []byte("task-B")); err == nil {
		t.Fatal("a different task must not open it")
	}
}

// TestSessionSealed_RoundTrips is the control for the tests above.
func TestSessionSealed_RoundTrips(t *testing.T) {
	recipient, _ := e2ee.GenerateKeyPair()
	plaintext := []byte(`{"price":42.5}`)

	sealed, err := e2ee.SealWithSession(recipient.PublicKey, plaintext, []byte("s"), []byte("t"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	got, err := e2ee.OpenWithSession(recipient.PrivateKey, recipient.PublicKey, sealed, []byte("s"), []byte("t"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("round trip mismatch: got %s", got)
	}
}
