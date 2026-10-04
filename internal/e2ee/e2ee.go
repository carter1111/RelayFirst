// Package e2ee implements the encryption layer for private task payloads (S13-1, S13-2).
//
// # What this is for, and the constraint that shapes it
//
// A RelayFirst task may be private. "Anyone can run a node" means a stranger may be carrying your
// payload, and MVP.md §6 records the consequence: an A2A protocol whose outward identity includes
// agent-to-agent work must be able to carry work nobody else can read.
//
// # Why the primitives are all library calls
//
// Invariant A1: keccak256, secp256k1, ecrecover, X25519 and ChaCha20 are never hand-written. So
// this package is thin on purpose — it composes golang.org/x/crypto and adds only the framing and
// the rules about what must be authenticated. A hand-rolled X25519 or a hand-rolled Poly1305
// would be a catastrophic vulnerability with no compensating benefit, and there is no version of
// this package where writing them would be justified.
//
// # The one design decision that is not a library call
//
// Which fields must be inside the signature. A recipient needs the ephemeral public key and the
// nonce to decrypt; an attacker who can change either can redirect the plaintext. And an attacker
// who can change the ALGORITHM can mount a downgrade. So `alg`, `epk` and `nonce` are all covered
// by the signature (see the outer receipt payload in internal/receipt), and this package's
// `Sealed` type documents that its fields are authenticated rather than being merely present.
//
// # What is deliberately absent
//
// ARCHITECTURE.md §6.4 defers forward secrecy (per-message ratchet), group encryption, metadata
// obfuscation and anonymous identity. None is here, and their absence is a schedule rather than an
// oversight: adding a ratchet changes the wire format, and doing so before the recipient-side
// semantics are settled would produce a format nobody has implemented.
package e2ee

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
)

// Algorithm identifiers.
//
// # Why these are strings and not an enum validated elsewhere
//
// A9 §② forbids putting a version constant inside the signed bytes and demanding an exact match:
// that makes an unknown version unverifiable rather than merely undecryptable. So the algorithm is
// a string a reader compares against what it supports, and a reader that does not recognize one
// still VERIFIES the receipt — it just cannot read the payload.
const (
	// AlgXChaCha20Poly1305 identifies the only algorithm this build implements.
	//
	// XChaCha20 rather than ChaCha20 for the 24-byte nonce: with a 12-byte nonce, randomly chosen
	// nonces collide at a rate that becomes dangerous at scale, and a nonce collision in a
	// stream cipher is catastrophic rather than merely inconvenient (ARCHITECTURE.md §6.1).
	AlgXChaCha20Poly1305 = "X25519-XChaCha20Poly1305"

	// KeySize is an X25519 key's length.
	KeySize = 32

	// NonceSize is XChaCha20-Poly1305's nonce length.
	NonceSize = chacha20poly1305.NonceSizeX
)

// ErrUnsupportedAlgorithm reports an algorithm this build cannot decrypt.
//
// # Why this is distinct from a decryption failure
//
// "I do not know this algorithm" and "this ciphertext is wrong" have different fixes: the first
// needs a newer build, the second means tampering or a wrong key. Collapsing them would send an
// operator hunting for an attacker when the answer is an upgrade. The same split as
// unsupported-versus-invalid in internal/receipt.
var ErrUnsupportedAlgorithm = errors.New("e2ee: unsupported algorithm")

// KeyPair is an X25519 key pair used only for encryption.
//
// # Why this is separate from the signing key, stated on the type
//
// ARCHITECTURE.md §6.1 requires the encryption key and the signing key to be independent, and
// §6.3 of the S13 plan states the invariant: leaking an encryption key must not confer signing or
// asset-moving ability. A derived encryption key would break that — a signing-key leak would let
// an attacker derive it and decrypt every ciphertext ever sent, including ones already archived.
//
// So `PrivateKey` is generated independently and never derived from an agent id or a signing key.
// The cost is one more secret to store, and it is the price of the property.
type KeyPair struct {
	PrivateKey []byte
	PublicKey  []byte
}

// GenerateKeyPair returns a fresh X25519 key pair from crypto/rand.
func GenerateKeyPair() (KeyPair, error) {
	priv := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, priv); err != nil {
		return KeyPair{}, fmt.Errorf("e2ee: generate key: %w", err)
	}
	return KeyPairFromPrivate(priv)
}

// KeyPairFromPrivate derives the public key for an existing private key.
//
// curve25519 clamps internally, so a key generated elsewhere is usable without this package
// reimplementing the clamping — which is exactly the kind of arithmetic A1 forbids writing by hand.
func KeyPairFromPrivate(priv []byte) (KeyPair, error) {
	if len(priv) != KeySize {
		return KeyPair{}, fmt.Errorf("e2ee: private key must be %d bytes, got %d", KeySize, len(priv))
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return KeyPair{}, fmt.Errorf("e2ee: derive public key: %w", err)
	}
	return KeyPair{PrivateKey: append([]byte(nil), priv...), PublicKey: pub}, nil
}

// SharedSecret computes the X25519 shared secret.
//
// # Why the error from X25519 is not ignored
//
// X25519 refuses a public key that produces an all-zero output, which is what a small-order point
// yields. Ignoring that error would mean accepting a peer's key and deriving a secret the peer
// also knows trivially — the shared secret would provide no confidentiality at all. The library
// checks it, and this returns the failure rather than swallowing it.
func SharedSecret(priv, peerPub []byte) ([]byte, error) {
	if len(priv) != KeySize {
		return nil, fmt.Errorf("e2ee: private key must be %d bytes, got %d", KeySize, len(priv))
	}
	if len(peerPub) != KeySize {
		return nil, fmt.Errorf("e2ee: peer public key must be %d bytes, got %d", KeySize, len(peerPub))
	}
	secret, err := curve25519.X25519(priv, peerPub)
	if err != nil {
		return nil, fmt.Errorf("e2ee: shared secret: %w", err)
	}
	return secret, nil
}

// Sealed is an encrypted payload as it travels.
//
// # Why every field here is inside the signature
//
// A recipient needs `EPK` and `Nonce` to decrypt, so an attacker who can change either can direct
// the plaintext somewhere useless or reuse a nonce. And `Alg` is the downgrade target: a reader
// told to use a weaker algorithm might do so. So all three are covered by the outer receipt's
// signature — this type describes what is authenticated, it does not perform the authentication.
//
// The consequence worth stating: a `Sealed` value is meaningless without the signature over it. It
// is not self-protecting, and a caller that stored one without the signature would have no way to
// tell a genuine ciphertext from a substituted one.
type Sealed struct {
	// Alg identifies the algorithm.
	Alg string `json:"alg"`

	// EPK is the sender's ephemeral X25519 public key, base64.
	//
	// # Why ephemeral
	//
	// Using a fresh key per message means a compromise of the sender's long-term encryption key
	// does not decrypt past messages that used an ephemeral key — a partial forward secrecy that
	// costs nothing. ARCHITECTURE.md §6.4 defers full ratcheting; this is the part that is free.
	EPK string `json:"epk"`

	// Nonce is the 24-byte XChaCha20 nonce, base64.
	Nonce string `json:"nonce"`

	// CT is the AEAD ciphertext with its Poly1305 tag, base64.
	CT string `json:"ct"`

	// Encrypted marks the payload as sealed, so a reader does not mistake ciphertext for
	// plaintext and try to parse it.
	Encrypted bool `json:"encrypted"`
}

// Seal encrypts plaintext for a recipient's public key.
//
// # What is authenticated, and what that protects
//
// The recipient's public key goes into the AEAD's additional data, so a ciphertext sealed for one
// agent cannot be replayed to another: the tag covers who it was meant for. Without that, a relay
// could forward a message to the wrong agent and the mistake would surface as a decryption
// failure rather than as a refusal.
//
// A fresh ephemeral key is used per call, so two seals of the same plaintext produce different
// ciphertexts. That matters beyond hygiene: identical ciphertexts would leak that two messages are
// the same, which is exactly the metadata leakage §6.4 defers and this does not need to introduce.
func Seal(recipientPub, plaintext []byte) (Sealed, error) {
	if len(recipientPub) != KeySize {
		return Sealed{}, fmt.Errorf("e2ee: recipient public key must be %d bytes, got %d", KeySize, len(recipientPub))
	}

	eph, err := GenerateKeyPair()
	if err != nil {
		return Sealed{}, err
	}
	secret, err := SharedSecret(eph.PrivateKey, recipientPub)
	if err != nil {
		return Sealed{}, err
	}

	aead, err := chacha20poly1305.NewX(secret)
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
		EPK:       base64.StdEncoding.EncodeToString(eph.PublicKey),
		Nonce:     base64.StdEncoding.EncodeToString(nonce),
		CT:        base64.StdEncoding.EncodeToString(ct),
		Encrypted: true,
	}, nil
}

// Open decrypts a Sealed payload with the recipient's private key.
//
// # Why the additional data is the recipient's own public key
//
// It must match what Seal used, or the tag will not verify. So this takes the recipient's public
// key as well as the private one: the public half is what the sender authenticated, and a caller
// that supplied the wrong one would get an authentication failure rather than a wrong plaintext.
//
// # Why this refuses an unknown algorithm rather than trying anyway
//
// Guessing would mean a reader that cannot recognize a format attempts to interpret it, and the
// failure mode of guessing wrong about a cipher is not a bad error message. It is
// ErrUnsupportedAlgorithm so a caller can tell "upgrade your build" from "this is not for you".
func Open(recipientPriv, recipientPub []byte, s Sealed) ([]byte, error) {
	if s.Alg != AlgXChaCha20Poly1305 {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, s.Alg)
	}
	if !s.Encrypted {
		return nil, fmt.Errorf("e2ee: payload is not marked encrypted")
	}
	if len(recipientPub) != KeySize {
		return nil, fmt.Errorf("e2ee: recipient public key must be %d bytes, got %d", KeySize, len(recipientPub))
	}

	epk, err := base64.StdEncoding.DecodeString(s.EPK)
	if err != nil {
		return nil, fmt.Errorf("e2ee: epk is not valid base64: %w", err)
	}
	if len(epk) != KeySize {
		return nil, fmt.Errorf("e2ee: epk must be %d bytes, got %d", KeySize, len(epk))
	}
	nonce, err := base64.StdEncoding.DecodeString(s.Nonce)
	if err != nil {
		return nil, fmt.Errorf("e2ee: nonce is not valid base64: %w", err)
	}
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("e2ee: nonce must be %d bytes, got %d", NonceSize, len(nonce))
	}
	ct, err := base64.StdEncoding.DecodeString(s.CT)
	if err != nil {
		return nil, fmt.Errorf("e2ee: ct is not valid base64: %w", err)
	}

	secret, err := SharedSecret(recipientPriv, epk)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.NewX(secret)
	if err != nil {
		return nil, fmt.Errorf("e2ee: new aead: %w", err)
	}

	// Any tampering with the ciphertext or the recipient binding fails HERE, as an authentication
	// error rather than as a wrong plaintext. That is the property the AEAD provides and the reason
	// the recipient's key goes into the additional data.
	plaintext, err := aead.Open(nil, nonce, ct, recipientPub)
	if err != nil {
		return nil, fmt.Errorf("e2ee: decryption failed (wrong key, wrong recipient, or tampered ciphertext): %w", err)
	}
	return plaintext, nil
}

// Marshal renders a Sealed payload as JSON bytes for the receipt's payload field.
func (s Sealed) Marshal() ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("e2ee: marshal sealed payload: %w", err)
	}
	return raw, nil
}

// UnmarshalSealed parses a payload, reporting whether it is a sealed one.
//
// # Why ok rather than an error for "not sealed"
//
// A payload is sealed or it is not, and both are normal: a public task is not. Returning an error
// for the unsealed case would make every caller handle a condition that is not exceptional, and
// callers under that pressure write `if err != nil { return nil }` and lose the distinction
// entirely.
func UnmarshalSealed(raw []byte) (Sealed, bool) {
	var s Sealed
	if err := json.Unmarshal(raw, &s); err != nil {
		return Sealed{}, false
	}
	// `Encrypted` is the marker. Checking it rather than merely parseability matters: any JSON
	// object would unmarshal into this struct with zero fields, and treating a bare `{}` as a
	// sealed payload would send a recipient into a decryption failure for what is really a
	// different payload shape.
	if !s.Encrypted || strings.TrimSpace(s.CT) == "" {
		return Sealed{}, false
	}
	return s, true
}
