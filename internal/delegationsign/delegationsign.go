// Package delegationsign signs and verifies session delegation grants (S13-3).
//
// # Why this is separate from internal/delegation
//
// The grant's SHAPE and the rules for what a scope permits have no cryptography in them, so a node
// can include internal/delegation to understand a scope without gaining the ability to verify
// anything (MVP.md §7.1). Signing and verifying need eip712, and eip712 links secp256k1, so they live
// here where the import graph can keep them away from the node.
//
// The split is the same one used for the agent card: the format is shared, the signature is
// sender-side.
//
// # What a signed grant establishes, stated precisely
//
// That the OWNER signed this grant. It does not establish that the session key is honest, that the
// scopes are a good idea, or that the grant has not been revoked — revocation is a nonce comparison
// the caller performs with state this package does not have (see delegation.Grant.Nonce).
package delegationsign

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/delegation"
	"github.com/relayfirst/relayfirst/internal/eip712"
)

// DomainName and DomainVersion scope the EIP-712 domain.
//
// The version is distinct from the receipt's, the card proof's and the assertion's, so a signature
// made for one purpose cannot be replayed as another. The struct type also differs, and EIP-712
// includes the type hash, so the two protections are independent — the same arrangement as the
// verifier assertion.
const (
	DomainName    = "RelayFirst"
	DomainVersion = "delegation-1"
)

// SignedGrant is a grant plus the owner's signature over it.
type SignedGrant struct {
	Grant delegation.Grant `json:"grant"`

	// Signature is the owner's EIP-712 signature, hex.
	Signature string `json:"signature,omitempty"`
}

// types is the EIP-712 struct for a grant.
//
// # Why the scopes are hashed rather than listed as a string array
//
// EIP-712 can express arrays, but the encoding of a dynamic array is where implementations most
// often disagree, and a disagreement here would be a signature that verifies under one build and not
// another. Hashing the canonical scope list into a bytes32 keeps the signed struct a fixed shape,
// which is the same reasoning as the receipt signing a payload HASH rather than the payload.
//
// # Why the nonce is included
//
// Without it, an owner could not revoke: revocation works by publishing a higher nonce, and a
// signature that did not cover the nonce could be presented with any value the presenter chose.
var types = eip712.Types{
	"RelaySessionDelegation": {
		{Name: "owner", Type: "string"},
		{Name: "sessionKey", Type: "string"},
		{Name: "scopesHash", Type: "bytes32"},
		{Name: "validFrom", Type: "uint256"},
		{Name: "validUntil", Type: "uint256"},
		{Name: "nonce", Type: "uint256"},
	},
}

// ScopesHash returns keccak256 of the canonical scope list.
//
// # Why the order is normalized before hashing
//
// Two grants listing the same scopes in different orders are the same grant. Hashing the caller's
// order would make them different, so a verifier would reject a re-serialization that reordered an
// array — which is exactly the fragility the receipt layer avoids by signing bytes rather than
// structures. Sorting first removes the order from the signed data entirely.
func ScopesHash(scopes []delegation.Scope) string {
	names := make([]string, 0, len(scopes))
	for _, s := range scopes {
		names = append(names, string(s))
	}
	// Insertion sort: the lists are tiny and a dependency for three elements would be noise.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return "0x" + hex.EncodeToString(eip712.Keccak256([]byte(strings.Join(names, ","))))
}

func (s SignedGrant) typedData() eip712.TypedData {
	return eip712.TypedData{
		Types:       types,
		PrimaryType: "RelaySessionDelegation",
		Domain: eip712.Domain{
			Name:    DomainName,
			Version: DomainVersion,
		},
		Message: map[string]any{
			"owner":      s.Grant.Owner,
			"sessionKey": s.Grant.SessionKey,
			"scopesHash": ScopesHash(s.Grant.Scopes),
			"validFrom":  fmt.Sprintf("%d", s.Grant.ValidFrom.Unix()),
			"validUntil": fmt.Sprintf("%d", s.Grant.ValidUntil.Unix()),
			"nonce":      fmt.Sprintf("%d", s.Grant.Nonce),
		},
	}
}

// Sign attaches the owner's signature.
//
// # Why the key is checked against the declared owner
//
// A mismatch would produce a grant that cannot verify, and the failure would surface later at a place
// with less context to explain it. Refusing here puts the error where both values are in hand, the
// same reasoning as the agent card proof and the verifier assertion.
func (s *SignedGrant) Sign(ownerPrivKeyHex string, chainID uint64) error {
	if err := s.Grant.Validate(); err != nil {
		return err
	}

	priv, err := eip712.PrivateKeyFromHex(ownerPrivKeyHex)
	if err != nil {
		return fmt.Errorf("delegation: parse owner key: %w", err)
	}
	derivedAddr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	derivedID, err := agentid.Format(chainID, eip712.AddressToHex(derivedAddr))
	if err != nil {
		return fmt.Errorf("delegation: derive owner id: %w", err)
	}
	declared, err := agentid.Parse(s.Grant.Owner)
	if err != nil {
		return fmt.Errorf("delegation: %w", err)
	}
	if declared.String() != derivedID {
		return fmt.Errorf(
			"delegation: the key derives identity %s but the grant names %s; "+
				"signing would produce a grant that cannot verify",
			derivedID, declared.String())
	}

	sig, err := eip712.Sign(priv, s.typedData())
	if err != nil {
		return fmt.Errorf("delegation: sign: %w", err)
	}
	s.Signature = "0x" + hex.EncodeToString(sig)
	return nil
}

// Verify checks the grant's shape and the owner's signature.
//
// # What it establishes, and the two things it does not
//
// It establishes that the owner signed THIS grant, with these scopes and this nonce and window. It
// does not check the window against a clock (call delegation.Grant.Allows for that, which forces the
// caller to name the clock) and does not check revocation (see delegation.Grant.Nonce).
//
// Keeping those out is not a gap: folding them in would make this function's answer depend on state
// it does not have, and a caller would not know which of three different problems it had just hit.
func (s SignedGrant) Verify() error {
	if err := s.Grant.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(s.Signature) == "" {
		return fmt.Errorf("delegation: no signature; an unsigned grant proves nothing")
	}

	sig, err := hex.DecodeString(strings.TrimPrefix(s.Signature, "0x"))
	if err != nil {
		return fmt.Errorf("delegation: signature is not valid hex: %w", err)
	}
	if len(sig) != eip712.SignatureLength {
		return fmt.Errorf("delegation: signature is %d bytes, want %d", len(sig), eip712.SignatureLength)
	}

	recovered, err := eip712.RecoverAddress(s.typedData(), sig)
	if err != nil {
		return fmt.Errorf("delegation: signature recovery failed: %w", err)
	}

	declared, err := agentid.Parse(s.Grant.Owner)
	if err != nil {
		return fmt.Errorf("delegation: %w", err)
	}
	declaredAddr, err := eip712.HexToAddress(declared.Address)
	if err != nil {
		return fmt.Errorf("delegation: owner address: %w", err)
	}
	if !bytesEqual(declaredAddr, recovered) {
		return fmt.Errorf(
			"delegation: owner mismatch: the grant names %s but the signature recovers %s",
			declared.Address, eip712.AddressToHex(recovered))
	}
	return nil
}

// SignerIsSessionKey reports whether an address is the grant's delegated session key.
//
// # Why this exists rather than a caller comparing addresses
//
// Verifying a session-signed event means answering "is this signer the key this grant delegates to".
// A caller doing that comparison itself would have to derive the session key's address the same way
// this package does, and any difference would be a silent authorization bypass — an event accepted
// because two address derivations disagreed about case, say. Providing the comparison keeps the rule
// in one place.
func (s SignedGrant) SignerIsSessionKey(recovered []byte) (bool, error) {
	session, err := agentid.Parse(s.Grant.SessionKey)
	if err != nil {
		return false, fmt.Errorf("delegation: %w", err)
	}
	addr, err := eip712.HexToAddress(session.Address)
	if err != nil {
		return false, fmt.Errorf("delegation: session key address: %w", err)
	}
	return bytesEqual(addr, recovered), nil
}

// AuthorizeEvent is the one function a caller should use to decide whether a session-signed event is
// acceptable.
//
// # Why one function rather than four checks
//
// Accepting a delegated signature requires four things to be true: the grant verifies, it is not
// revoked, the clock is inside the window, and the scope covers the action. A caller assembling those
// itself will eventually omit one, and the omission will be an authorization bypass rather than a
// visible failure. Folding them together means the default is refusal.
//
// # Why the recovered signer is an argument
//
// The caller has already recovered the signer from the event's own signature — that is how it knows
// who signed. Passing the address in keeps this function from needing the event, so it can stay about
// authorization rather than about event parsing.
func (s SignedGrant) AuthorizeEvent(scope delegation.Scope, recoveredSigner []byte, currentNonce uint64, at time.Time) error {
	if err := s.Verify(); err != nil {
		return err
	}
	if s.Grant.IsRevokedBy(currentNonce) {
		return fmt.Errorf("delegation: revoked (grant nonce %d, current %d)", s.Grant.Nonce, currentNonce)
	}
	isSessionKey, err := s.SignerIsSessionKey(recoveredSigner)
	if err != nil {
		return err
	}
	if !isSessionKey {
		return fmt.Errorf("delegation: the signer is not this grant's session key")
	}
	return s.Grant.Allows(scope, at)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
