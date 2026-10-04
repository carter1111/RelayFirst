package e2ee

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"hash"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// Session key derivation, following ARCHITECTURE.md §6.2.
//
// # Why an X25519 shared secret is not used directly as a cipher key
//
// The shared secret from one key pair is THE SAME for every message between those two parties. Using
// it as the AEAD key would mean one key for the entire relationship: a compromise anywhere reveals
// everything, and there is no way to scope access to a single task.
//
// §6.2 specifies the fix: derive a per-session key with HKDF-SHA256, salted by the session and
// informed by the task. Then one shared secret yields a different key per task, so a key disclosed
// for one task says nothing about any other.
//
// # Why the salt and info are the caller's, and what that means
//
// HKDF's separation only helps if the inputs differ per session. This package cannot know what a
// session is, so both are parameters — and a caller that passed constants would get one key for
// everything, silently losing the property while the code still looked correct. The ceremony of
// passing them is the reminder.
//
// # Why this is not key ratcheting
//
// ARCHITECTURE.md §6.4 defers forward secrecy: a ratchet makes each message's key depend on the
// previous one, so compromising a key does not reveal earlier messages. HKDF here gives per-SESSION
// separation, not per-message. The difference matters when reading a security claim: a compromised
// session key reveals that session, and no others.

// SessionKey derives a per-session key from an X25519 shared secret.
//
// The result is usable as an XChaCha20-Poly1305 key. It is 32 bytes, matching the AEAD's key size.
func SessionKey(shared, salt, info []byte) ([]byte, error) {
	if len(shared) != KeySize {
		return nil, fmt.Errorf("e2ee: shared secret must be %d bytes, got %d", KeySize, len(shared))
	}
	if len(salt) == 0 {
		// A nil salt is legal in HKDF and would still derive a key, so refusing is a deliberate
		// choice: an empty salt means the same shared secret and the same info give the same key
		// across every deployment, which is the exact collision the salt exists to prevent.
		return nil, fmt.Errorf("e2ee: a session salt is required, or every session with the same " +
			"info would derive the same key")
	}

	// HKDF-Extract then HKDF-Expand, which is what hkdf.New does. SHA-256 per §6.2.
	reader := hkdf.New(func() hash.Hash { return sha256.New() }, shared, salt, info)
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("e2ee: derive session key: %w", err)
	}
	return key, nil
}

// SealWithSession encrypts under a per-session key derived from the shared secret.
//
// # Why this is recommended over Seal, and why Seal still exists
//
// This is the §6.2 path: derive, then encrypt. `Seal` uses the raw shared secret as the key, which is
// correct but gives one key for the whole relationship.
//
// `Seal` is kept because it is the minimal case — useful in tests and for a caller that genuinely has
// one session — and because removing it would be a silent behaviour change for anyone using it. The
// trade is stated rather than hidden: a caller wanting per-session separation uses this one.
//
// # What is authenticated
//
// The recipient's public key goes into the AEAD's additional data, exactly as in Seal, so a ciphertext
// for one principal cannot be presented as one for another. The SALT and INFO are NOT in the
// additional data, because they are inputs to the key: a changed salt derives a different key and the
// tag fails anyway. Putting them in both places would be redundant rather than wrong.
func SealWithSession(recipientPub, plaintext, salt, info []byte) (Sealed, error) {
	if len(recipientPub) != KeySize {
		return Sealed{}, fmt.Errorf("e2ee: recipient public key must be %d bytes, got %d", KeySize, len(recipientPub))
	}

	eph, err := GenerateKeyPair()
	if err != nil {
		return Sealed{}, err
	}
	shared, err := SharedSecret(eph.PrivateKey, recipientPub)
	if err != nil {
		return Sealed{}, err
	}
	key, err := SessionKey(shared, salt, info)
	if err != nil {
		return Sealed{}, err
	}

	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return Sealed{}, fmt.Errorf("e2ee: new aead: %w", err)
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Sealed{}, fmt.Errorf("e2ee: generate nonce: %w", err)
	}

	ct := aead.Seal(nil, nonce, plaintext, recipientPub)
	return Sealed{
		Alg:       AlgXChaCha20Poly1305,
		EPK:       base64Encode(eph.PublicKey),
		Nonce:     base64Encode(nonce),
		CT:        base64Encode(ct),
		Encrypted: true,
	}, nil
}

// OpenWithSession decrypts a payload sealed by SealWithSession.
//
// # Why the salt and info must be supplied again, and what that enforces
//
// They are not carried in the Sealed value: they are the caller's notion of which session this is, and
// a payload that carried its own salt would let a sender choose one. Requiring the recipient to name
// the session is what makes "this message belongs to task X" a fact the recipient already knows
// rather than one the sender asserts.
//
// A wrong salt or info derives a different key, so the tag fails and the error is an authentication
// failure — the same path as a wrong recipient, which is why the message says so.
func OpenWithSession(recipientPriv, recipientPub []byte, s Sealed, salt, info []byte) ([]byte, error) {
	if s.Alg != AlgXChaCha20Poly1305 {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, s.Alg)
	}
	if !s.Encrypted {
		return nil, fmt.Errorf("e2ee: payload is not marked encrypted")
	}

	epk, err := base64Decode(s.EPK)
	if err != nil {
		return nil, fmt.Errorf("e2ee: epk is not valid base64: %w", err)
	}
	if len(epk) != KeySize {
		return nil, fmt.Errorf("e2ee: epk must be %d bytes, got %d", KeySize, len(epk))
	}
	nonce, err := base64Decode(s.Nonce)
	if err != nil {
		return nil, fmt.Errorf("e2ee: nonce is not valid base64: %w", err)
	}
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("e2ee: nonce must be %d bytes, got %d", NonceSize, len(nonce))
	}
	ct, err := base64Decode(s.CT)
	if err != nil {
		return nil, fmt.Errorf("e2ee: ct is not valid base64: %w", err)
	}

	shared, err := SharedSecret(recipientPriv, epk)
	if err != nil {
		return nil, err
	}
	key, err := SessionKey(shared, salt, info)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("e2ee: new aead: %w", err)
	}

	plaintext, err := aead.Open(nil, nonce, ct, recipientPub)
	if err != nil {
		return nil, fmt.Errorf("e2ee: decryption failed (wrong key, wrong session, wrong recipient, "+
			"or tampered ciphertext): %w", err)
	}
	return plaintext, nil
}

// base64Encode and base64Decode exist so the two sealing paths use one spelling of the encoding, since
// a difference between them would make one path's payloads unreadable by the other.
func base64Encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func base64Decode(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
