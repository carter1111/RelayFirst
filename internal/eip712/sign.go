package eip712

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// Signature is a 65-byte r||s||v signature with an Ethereum-style recovery id
// (v = 27 + recoveryID), as produced by eth_signTypedData_v4.
const SignatureLength = 65

// recoveryOffset is the EIP-155 / geth convention for v.
const recoveryOffset = 27

// Sign signs the EIP-712 digest of td with the given secp256k1 private key and
// returns a 65-byte r||s||v signature.
//
// The message digest is always 32 bytes, so the low-s normalisation performed
// by decred's SignCompact already yields a canonical signature.
func Sign(privKey *secp256k1.PrivateKey, td TypedData) ([]byte, error) {
	digest, err := HashTypedData(td)
	if err != nil {
		return nil, err
	}
	return SignDigest(privKey, digest)
}

// SignDigest signs an already-computed 32-byte digest.
//
// decred's compact format is [header ‖ R ‖ S] where header is already
// 27 + recoveryID for uncompressed keys — which is exactly Ethereum's v. So the
// only conversion needed is moving v from the front to the back:
//
//	Ethereum:    r ‖ s ‖ v
//	decred:  v ‖ r ‖ s
func SignDigest(privKey *secp256k1.PrivateKey, digest []byte) ([]byte, error) {
	if privKey == nil {
		return nil, fmt.Errorf("eip712: nil private key")
	}
	if len(digest) != 32 {
		return nil, fmt.Errorf("eip712: digest must be 32 bytes, got %d", len(digest))
	}

	compact := ecdsa.SignCompact(privKey, digest, false)

	v := compact[0]
	if v < recoveryOffset || v > recoveryOffset+1 {
		return nil, fmt.Errorf("eip712: unexpected recovery header %d from SignCompact", v)
	}

	sig := make([]byte, SignatureLength)
	copy(sig[0:64], compact[1:])
	sig[64] = v

	return sig, nil
}

// RecoverAddress recovers the signer address for td from an Ethereum-style
// 65-byte signature. This is pure local computation — no RPC, no chain access
// (CODING_RULES.md, invariant A1/A3).
func RecoverAddress(td TypedData, signature []byte) ([]byte, error) {
	digest, err := HashTypedData(td)
	if err != nil {
		return nil, err
	}
	return RecoverAddressFromDigest(digest, signature)
}

// RecoverAddressFromDigest recovers the 20-byte signer address from a digest
// and a 65-byte r||s||v signature.
func RecoverAddressFromDigest(digest, signature []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, fmt.Errorf("eip712: digest must be 32 bytes, got %d", len(digest))
	}
	if len(signature) != SignatureLength {
		return nil, fmt.Errorf("eip712: signature must be %d bytes, got %d", SignatureLength, len(signature))
	}

	v := signature[64]
	if v < recoveryOffset || v > recoveryOffset+1 {
		return nil, fmt.Errorf("eip712: invalid recovery byte %d (want %d or %d)", v, recoveryOffset, recoveryOffset+1)
	}

	// RecoverCompact expects the same [header ‖ r ‖ s] layout, and its header
	// is already the 27-offset value Ethereum uses.
	compact := make([]byte, 65)
	compact[0] = v
	copy(compact[1:], signature[0:64])

	pub, _, err := ecdsa.RecoverCompact(compact, digest)
	if err != nil {
		return nil, fmt.Errorf("eip712: recover: %w", err)
	}

	serialized := pub.SerializeUncompressed() // 65 bytes: 0x04 ‖ X ‖ Y
	hash := Keccak256(serialized[1:])
	return hash[12:], nil // last 20 bytes
}

// AddressToHex renders a 20-byte address as 0x-prefixed lowercase hex.
func AddressToHex(addr []byte) string {
	return "0x" + hex.EncodeToString(addr)
}

// HexToAddress parses a 0x-prefixed (or bare) 20-byte address.
func HexToAddress(s string) ([]byte, error) {
	raw, err := hexStrip(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	if len(raw) != 20 {
		return nil, fmt.Errorf("eip712: address must be 20 bytes, got %d", len(raw))
	}
	return raw, nil
}

// PrivateKeyFromHex parses a 32-byte secp256k1 private key from hex.
func PrivateKeyFromHex(s string) (*secp256k1.PrivateKey, error) {
	raw, err := hexStrip(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("eip712: private key must be 32 bytes, got %d", len(raw))
	}
	return secp256k1.PrivKeyFromBytes(raw), nil
}
