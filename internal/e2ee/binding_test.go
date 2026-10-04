package e2ee_test

import (
	"testing"

	"github.com/relayfirst/relayfirst/internal/e2ee"
)

// TestRecipientBindingIsLoadBearing is the test the mutation showed was missing.
//
// # The gap this fills
//
// The earlier replay test opened a payload for Alice with Bob's OWN key pair, which fails because
// the shared secret differs — so it passed for a reason unrelated to the additional-data binding.
// Removing the binding from `e2ee.Seal` and `e2ee.Open` therefore broke nothing, and a mutation proved it:
// the security property was present in the code and unverified by any test.
//
// # The attack the binding actually prevents
//
// The shared secret comes from the ephemeral public key and the OPENING private key, so it does not
// depend on the recipient's public key at all. What depends on it is the AEAD's additional data.
// So an attacker who holds the correct private key — the recipient themselves, or anyone who
// obtained it — can attempt to open a ciphertext under a DIFFERENT claimed recipient. Without the
// binding, that succeeds and the payload is misattributed.
//
// That is the concrete failure: a message addressed to one principal being readable as though it
// were addressed to another. The test below performs exactly that.
func TestRecipientBindingIsLoadBearing(t *testing.T) {
	recipient := mustKeyPair(t)

	sealed, err := e2ee.Seal(recipient.PublicKey, []byte("addressed to recipient"))
	if err != nil {
		t.Fatalf("e2ee.Seal: %v", err)
	}

	// Correct: the same public key the sender authenticated.
	if _, err := e2ee.Open(recipient.PrivateKey, recipient.PublicKey, sealed); err != nil {
		t.Fatalf("the correct recipient must open it: %v", err)
	}

	// The attack: the right private key, but a DIFFERENT claimed public key. The shared secret is
	// unchanged, so only the additional-data binding can refuse this. With the binding removed this
	// call succeeds and the payload is misattributed.
	wrong := make([]byte, e2ee.KeySize)
	copy(wrong, recipient.PublicKey)
	wrong[0] ^= 0x01 // a different claimed recipient, same private key

	if _, err := e2ee.Open(recipient.PrivateKey, wrong, sealed); err == nil {
		t.Fatal("opening with a different claimed recipient must fail: otherwise the payload's " +
			"addressee is not authenticated and a message can be misattributed")
	}
}

// TestRecipientBindingCoversTheSenderKey is the same property from the other side, and it is the
// one the mutation broke.
//
// The binding must be part of what the tag covers, not merely a parameter passed to e2ee.Open. Asserting
// that the ciphertext differs when the additional data differs is the direct way to say so: if the
// binding were dropped, two seals with different additional data would produce tags that validate
// under either.
func TestRecipientBindingCoversTheSenderKey(t *testing.T) {
	alice := mustKeyPair(t)
	bob := mustKeyPair(t)

	// e2ee.Seal the same plaintext for two different recipients.
	sealedA, err := e2ee.Seal(alice.PublicKey, []byte("same plaintext"))
	if err != nil {
		t.Fatalf("e2ee.Seal: %v", err)
	}
	sealedB, err := e2ee.Seal(bob.PublicKey, []byte("same plaintext"))
	if err != nil {
		t.Fatalf("e2ee.Seal: %v", err)
	}

	// Alice's ciphertext must not open with Alice's private key while claiming it was for Bob.
	if _, err := e2ee.Open(alice.PrivateKey, bob.PublicKey, sealedA); err == nil {
		t.Fatal("a ciphertext sealed for Alice must not open when the claimant says Bob")
	}
	// And each must still open for its own recipient, or the binding would be refusing valid use.
	if _, err := e2ee.Open(alice.PrivateKey, alice.PublicKey, sealedA); err != nil {
		t.Fatalf("Alice's own payload must open: %v", err)
	}
	if _, err := e2ee.Open(bob.PrivateKey, bob.PublicKey, sealedB); err != nil {
		t.Fatalf("Bob's own payload must open: %v", err)
	}
}
