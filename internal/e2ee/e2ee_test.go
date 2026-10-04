package e2ee_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/e2ee"
)

// These tests cover S13-1 (X25519) and S13-2 (XChaCha20-Poly1305).
//
// The properties that matter are all negative — things an attacker must NOT be able to do — so most
// of these assertions are about failures:
//
//   - a third party cannot read the payload;
//   - a tampered ciphertext is refused rather than silently producing wrong plaintext;
//   - a ciphertext for one recipient cannot be opened by another;
//   - and the scheme is not a broken one dressed up, e.g. two encryptions of the same plaintext
//     must differ.

func mustKeyPair(t *testing.T) e2ee.KeyPair {
	t.Helper()
	kp, err := e2ee.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	return kp
}

// TestRoundTrip is the control. Without it the rejection tests below would pass for a scheme that
// never works at all.
func TestRoundTrip(t *testing.T) {
	recipient := mustKeyPair(t)
	plaintext := []byte(`{"url":"https://private.example/secret"}`)

	sealed, err := e2ee.Seal(recipient.PublicKey, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !sealed.Encrypted {
		t.Error("a sealed payload must be marked encrypted")
	}
	if sealed.Alg != e2ee.AlgXChaCha20Poly1305 {
		t.Errorf("alg = %q", sealed.Alg)
	}

	got, err := e2ee.Open(recipient.PrivateKey, recipient.PublicKey, sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("plaintext did not survive:\n  got  %s\n  want %s", got, plaintext)
	}
}

// TestCiphertextDoesNotContainThePlaintext is the point of the whole exercise: a relay carries the
// bytes, so the bytes must not be readable.
func TestCiphertextDoesNotContainThePlaintext(t *testing.T) {
	recipient := mustKeyPair(t)
	// A distinctive string so a substring search is meaningful.
	secret := "SUPER-SECRET-MARKER-9f3a"
	sealed, err := e2ee.Seal(recipient.PublicKey, []byte(secret))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	raw, err := sealed.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("the plaintext must not appear anywhere in the encrypted payload")
	}
	// And not merely base64-of-plaintext either, which would be an encoding mistake rather than
	// encryption.
	if bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString([]byte(secret)))) {
		t.Fatal("the payload must not contain a base64 encoding of the plaintext")
	}
}

// TestThirdPartyCannotDecrypt is the confidentiality property.
func TestThirdPartyCannotDecrypt(t *testing.T) {
	recipient := mustKeyPair(t)
	attacker := mustKeyPair(t)

	sealed, err := e2ee.Seal(recipient.PublicKey, []byte("for the recipient only"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// The attacker's own key pair does not open it.
	if _, err := e2ee.Open(attacker.PrivateKey, attacker.PublicKey, sealed); err == nil {
		t.Fatal("a third party must not be able to decrypt a payload addressed to someone else")
	}

	// Nor does the attacker's private key with the RECIPIENT's public key, which is the mistake a
	// caller makes when it mixes halves from two key pairs.
	if _, err := e2ee.Open(attacker.PrivateKey, recipient.PublicKey, sealed); err == nil {
		t.Fatal("a mismatched key pair must not decrypt")
	}
}

// TestTamperedCiphertextIsRefused rather than producing wrong plaintext. The AEAD provides this;
// the test exists because the property is the reason to use an AEAD at all.
func TestTamperedCiphertextIsRefused(t *testing.T) {
	recipient := mustKeyPair(t)
	sealed, err := e2ee.Seal(recipient.PublicKey, []byte("authentic"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Flip one bit in the ciphertext.
	ct, err := base64.StdEncoding.DecodeString(sealed.CT)
	if err != nil {
		t.Fatalf("decode ct: %v", err)
	}
	ct[0] ^= 0x01
	tampered := sealed
	tampered.CT = base64.StdEncoding.EncodeToString(ct)

	if _, err := e2ee.Open(recipient.PrivateKey, recipient.PublicKey, tampered); err == nil {
		t.Fatal("a modified ciphertext must be refused, not decrypted into wrong plaintext")
	}
}

// TestTamperedNonceIsRefused covers the other field a recipient needs.
func TestTamperedNonceIsRefused(t *testing.T) {
	recipient := mustKeyPair(t)
	sealed, err := e2ee.Seal(recipient.PublicKey, []byte("authentic"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	nonce, err := base64.StdEncoding.DecodeString(sealed.Nonce)
	if err != nil {
		t.Fatalf("decode nonce: %v", err)
	}
	nonce[0] ^= 0x01
	tampered := sealed
	tampered.Nonce = base64.StdEncoding.EncodeToString(nonce)

	if _, err := e2ee.Open(recipient.PrivateKey, recipient.PublicKey, tampered); err == nil {
		t.Fatal("a modified nonce must be refused")
	}
}

// TestTamperedEpkIsRefused covers the third. An attacker who could substitute the ephemeral key
// would be choosing the shared secret, which is the whole game.
func TestTamperedEpkIsRefused(t *testing.T) {
	recipient := mustKeyPair(t)
	attacker := mustKeyPair(t)
	sealed, err := e2ee.Seal(recipient.PublicKey, []byte("authentic"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	tampered := sealed
	tampered.EPK = base64.StdEncoding.EncodeToString(attacker.PublicKey)

	if _, err := e2ee.Open(recipient.PrivateKey, recipient.PublicKey, tampered); err == nil {
		t.Fatal("a substituted ephemeral key must be refused: the attacker would be choosing the shared secret")
	}
}

// TestRecipientBindingCannotBeReplayedToAnother keeps the additional-data binding honest. The
// recipient's public key is authenticated, so a ciphertext for one agent cannot be handed to
// another and expected to work.
func TestRecipientBindingCannotBeReplayedToAnother(t *testing.T) {
	alice := mustKeyPair(t)
	bob := mustKeyPair(t)

	sealed, err := e2ee.Seal(alice.PublicKey, []byte("for alice"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Bob has his own key pair, and the payload was not sealed for him.
	if _, err := e2ee.Open(bob.PrivateKey, bob.PublicKey, sealed); err == nil {
		t.Fatal("a payload sealed for Alice must not open for Bob")
	}
}

// TestTwoSealsDiffer is why an ephemeral key is used per message.
//
// Identical ciphertexts would reveal that two messages are the same, which is metadata leakage —
// the kind ARCHITECTURE.md §6.4 defers, and this does not need to introduce for free.
func TestTwoSealsDiffer(t *testing.T) {
	recipient := mustKeyPair(t)
	plaintext := []byte("the same message twice")

	first, err := e2ee.Seal(recipient.PublicKey, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := e2ee.Seal(recipient.PublicKey, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if first.CT == second.CT {
		t.Error("two seals of the same plaintext must differ, or identical messages leak as identical")
	}
	if first.EPK == second.EPK {
		t.Error("the ephemeral key must be fresh per message")
	}
	// Both must still decrypt, since differing is only useful if the scheme still works.
	for i, s := range []e2ee.Sealed{first, second} {
		got, err := e2ee.Open(recipient.PrivateKey, recipient.PublicKey, s)
		if err != nil {
			t.Fatalf("seal %d: %v", i, err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Errorf("seal %d did not round trip", i)
		}
	}
}

// TestSharedSecretIsSymmetric pins the X25519 property the scheme relies on: both sides derive the
// same secret without exchanging it.
func TestSharedSecretIsSymmetric(t *testing.T) {
	a := mustKeyPair(t)
	b := mustKeyPair(t)

	ab, err := e2ee.SharedSecret(a.PrivateKey, b.PublicKey)
	if err != nil {
		t.Fatalf("a->b: %v", err)
	}
	ba, err := e2ee.SharedSecret(b.PrivateKey, a.PublicKey)
	if err != nil {
		t.Fatalf("b->a: %v", err)
	}
	if !bytes.Equal(ab, ba) {
		t.Fatal("X25519 must be symmetric, or no message could ever be decrypted")
	}
}

// TestSharedSecretRejectsSmallOrderPoint is why the library's error is propagated rather than
// ignored.
//
// An all-zero public key is a small-order point that yields an all-zero shared secret the peer
// also knows trivially, so the "encryption" would provide no confidentiality. The library refuses
// it and this must not swallow that.
func TestSharedSecretRejectsSmallOrderPoint(t *testing.T) {
	kp := mustKeyPair(t)
	zero := make([]byte, e2ee.KeySize)

	if _, err := e2ee.SharedSecret(kp.PrivateKey, zero); err == nil {
		t.Fatal("an all-zero public key must be refused: the shared secret would provide no confidentiality")
	}
}

// TestUnsupportedAlgorithmIsDistinguishable keeps "upgrade your build" apart from "this is not for
// you", the same split as unsupported-versus-invalid in the receipt layer.
func TestUnsupportedAlgorithmIsDistinguishable(t *testing.T) {
	recipient := mustKeyPair(t)
	sealed, err := e2ee.Seal(recipient.PublicKey, []byte("x"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	sealed.Alg = "X25519-FutureCipher"

	_, err = e2ee.Open(recipient.PrivateKey, recipient.PublicKey, sealed)
	if err == nil {
		t.Fatal("an unknown algorithm must not be guessed at")
	}
	if !errors.Is(err, e2ee.ErrUnsupportedAlgorithm) {
		t.Errorf("an unknown algorithm must be ErrUnsupportedAlgorithm so a caller can tell it needs upgrading, got: %v", err)
	}
}

// TestUnmarshalSealedDistinguishesPayloadShapes keeps a public payload from being mistaken for a
// sealed one.
func TestUnmarshalSealedDistinguishesPayloadShapes(t *testing.T) {
	recipient := mustKeyPair(t)
	sealed, err := e2ee.Seal(recipient.PublicKey, []byte(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	raw, err := sealed.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, ok := e2ee.UnmarshalSealed(raw)
	if !ok {
		t.Fatal("a sealed payload must be recognized")
	}
	if got.CT != sealed.CT {
		t.Error("the payload did not survive a marshal round trip")
	}

	// A public payload is not an error, it is simply not sealed.
	if _, ok := e2ee.UnmarshalSealed([]byte(`{"url":"https://example.com"}`)); ok {
		t.Error("a plain payload must not be reported as sealed")
	}
	// A bare object would unmarshal into the struct with zero fields, which is why `Encrypted` is
	// the marker rather than mere parseability.
	if _, ok := e2ee.UnmarshalSealed([]byte(`{}`)); ok {
		t.Error("an empty object must not be reported as sealed")
	}
	if _, ok := e2ee.UnmarshalSealed([]byte(`not json`)); ok {
		t.Error("non-JSON must not be reported as sealed")
	}
}

// TestSealRejectsBadKeyLengths keeps a mistyped key from producing a confusing decryption failure
// later.
func TestSealRejectsBadKeyLengths(t *testing.T) {
	if _, err := e2ee.Seal([]byte("short"), []byte("x")); err == nil {
		t.Error("a wrong-length recipient key must be rejected at seal time")
	}
	if _, err := e2ee.KeyPairFromPrivate([]byte("short")); err == nil {
		t.Error("a wrong-length private key must be rejected")
	}
	recipient := mustKeyPair(t)
	sealed, err := e2ee.Seal(recipient.PublicKey, []byte("x"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := e2ee.Open([]byte("short"), recipient.PublicKey, sealed); err == nil {
		t.Error("a wrong-length private key must be rejected at open time")
	}
}

// TestKeyPairIsNotDerivedFromAnything documents the independence requirement in code.
//
// ARCHITECTURE.md §6.1 and the S13 plan §2.2 require the encryption key to be independent of the
// signing key, because a derived one would mean a signing-key leak decrypts every archived
// ciphertext. The check here is that two generated pairs differ, which is the observable
// consequence of genuine independence.
func TestKeyPairIsNotDerivedFromAnything(t *testing.T) {
	a := mustKeyPair(t)
	b := mustKeyPair(t)

	if bytes.Equal(a.PrivateKey, b.PrivateKey) {
		t.Fatal("two generated key pairs must differ; a deterministic derivation is what the design forbids")
	}
	if bytes.Equal(a.PublicKey, b.PublicKey) {
		t.Fatal("two generated public keys must differ")
	}
	if len(a.PrivateKey) != e2ee.KeySize || len(a.PublicKey) != e2ee.KeySize {
		t.Errorf("key sizes are wrong: priv=%d pub=%d", len(a.PrivateKey), len(a.PublicKey))
	}
}

// TestSealedPayloadIsNotSelfProtecting states the design's limit as a test, so a caller cannot
// assume otherwise by accident.
//
// A Sealed value carries no signature. Its fields are authenticated by the OUTER receipt's
// signature, so a placeholder struct is trivially constructible — which means a caller that stored
// one without the signature has no way to tell a genuine ciphertext from a substituted one.
func TestSealedPayloadIsNotSelfProtecting(t *testing.T) {
	// A fabricated payload is constructible, and that is the point: it proves the security comes
	// from the signature over these bytes, not from the bytes themselves.
	forged := e2ee.Sealed{
		Alg:       e2ee.AlgXChaCha20Poly1305,
		EPK:       base64.StdEncoding.EncodeToString(make([]byte, e2ee.KeySize)),
		Nonce:     base64.StdEncoding.EncodeToString(make([]byte, e2ee.NonceSize)),
		CT:        base64.StdEncoding.EncodeToString([]byte("forged")),
		Encrypted: true,
	}
	raw, err := forged.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if _, ok := e2ee.UnmarshalSealed(raw); !ok {
		t.Fatal("a fabricated payload parses, which is expected — its authenticity comes from the outer signature")
	}
	// And it does not decrypt, because it was never a real ciphertext.
	recipient := mustKeyPair(t)
	if _, err := e2ee.Open(recipient.PrivateKey, recipient.PublicKey, forged); err == nil {
		t.Error("a fabricated payload must not decrypt")
	}
}

// TestNoAlgorithmDowngradePath confirms the algorithm is compared, not coerced.
func TestNoAlgorithmDowngradePath(t *testing.T) {
	recipient := mustKeyPair(t)
	sealed, err := e2ee.Seal(recipient.PublicKey, []byte("x"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// An empty algorithm must not fall through to a default.
	sealed.Alg = ""
	if _, err := e2ee.Open(recipient.PrivateKey, recipient.PublicKey, sealed); err == nil {
		t.Fatal("an empty algorithm must not default to anything")
	} else if !errors.Is(err, e2ee.ErrUnsupportedAlgorithm) {
		t.Errorf("want ErrUnsupportedAlgorithm, got: %v", err)
	}
	_ = strings.TrimSpace
}
