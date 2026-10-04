package delegationsign_test

import (
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/delegation"
	"github.com/relayfirst/relayfirst/internal/delegationsign"
	"github.com/relayfirst/relayfirst/internal/eip712"
)

// These tests cover S13-3's signing layer.
//
// The property that matters most is that AuthorizeEvent refuses by default: it folds four checks into
// one call because a caller assembling them itself will eventually omit one, and the omission would be
// an authorization bypass rather than a visible failure.

const (
	ownerKey   = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	sessionKey = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	otherKey   = "0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
	chainID    = uint64(8453)
)

func idFor(t *testing.T, key string) string {
	t.Helper()
	priv, err := eip712.PrivateKeyFromHex(key)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	addr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	return "agent:eip155:8453:" + eip712.AddressToHex(addr)
}

func addrFor(t *testing.T, key string) []byte {
	t.Helper()
	priv, err := eip712.PrivateKeyFromHex(key)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	return eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
}

func signedGrant(t *testing.T) delegationsign.SignedGrant {
	t.Helper()
	s := delegationsign.SignedGrant{
		Grant: delegation.Grant{
			Owner:      idFor(t, ownerKey),
			SessionKey: idFor(t, sessionKey),
			Scopes:     []delegation.Scope{delegation.ScopeSessionEvent, delegation.ScopeTaskProgress},
			ValidFrom:  time.Unix(1791015800, 0),
			ValidUntil: time.Unix(1791019400, 0),
			Nonce:      1,
		},
	}
	if err := s.Sign(ownerKey, chainID); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return s
}

// TestSignAndVerify is the control.
func TestSignAndVerify(t *testing.T) {
	s := signedGrant(t)
	if s.Signature == "" {
		t.Fatal("the grant must be signed")
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("a freshly signed grant must verify: %v", err)
	}
}

// TestSign_RefusesMismatchedOwner keeps an unverifiable grant from being produced.
func TestSign_RefusesMismatchedOwner(t *testing.T) {
	s := delegationsign.SignedGrant{
		Grant: delegation.Grant{
			Owner:      idFor(t, sessionKey), // names the SESSION key's identity
			SessionKey: idFor(t, otherKey),
			Scopes:     []delegation.Scope{delegation.ScopeSessionEvent},
			ValidFrom:  time.Unix(1791015800, 0),
			ValidUntil: time.Unix(1791019400, 0),
		},
	}
	// Signed by the owner key, which does not match the declared owner.
	if err := s.Sign(ownerKey, chainID); err == nil {
		t.Fatal("signing with a key that does not match the declared owner must fail")
	}
}

// TestVerify_RejectsTampering is the integrity property: any change to the grant invalidates the
// signature.
func TestVerify_RejectsTampering(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*delegationsign.SignedGrant)
	}{
		{"scopes widened", func(s *delegationsign.SignedGrant) {
			s.Grant.Scopes = append(s.Grant.Scopes, delegation.ScopeTaskLifecycle)
		}},
		{"session key swapped", func(s *delegationsign.SignedGrant) { s.Grant.SessionKey = idFor(t, otherKey) }},
		{"window extended", func(s *delegationsign.SignedGrant) {
			s.Grant.ValidUntil = s.Grant.ValidUntil.Add(24 * time.Hour)
		}},
		{"nonce changed", func(s *delegationsign.SignedGrant) { s.Grant.Nonce = 999 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := signedGrant(t)
			c.mutate(&s)
			if err := s.Verify(); err == nil {
				t.Errorf("a grant with %s must not verify: the signature covers it", c.name)
			}
		})
	}
}

// TestScopesHash_IsOrderIndependent is why the hash normalizes first.
//
// Two grants listing the same scopes in different orders are the same grant. Hashing the caller's
// order would make them different, so a verifier would reject a re-serialization that reordered an
// array — the fragility the receipt layer avoids by signing bytes.
func TestScopesHash_IsOrderIndependent(t *testing.T) {
	a := delegationsign.ScopesHash([]delegation.Scope{delegation.ScopeSessionEvent, delegation.ScopeTaskProgress})
	b := delegationsign.ScopesHash([]delegation.Scope{delegation.ScopeTaskProgress, delegation.ScopeSessionEvent})
	if a != b {
		t.Fatal("the scope hash must not depend on order, or a reordered array would break a valid grant")
	}
	// And a different set must hash differently, or the hash would be decorative.
	c := delegationsign.ScopesHash([]delegation.Scope{delegation.ScopeSessionEvent})
	if a == c {
		t.Fatal("different scope sets must hash differently")
	}
}

// TestAuthorizeEvent_AcceptsAValidSessionSignature is the control for the gate.
func TestAuthorizeEvent_AcceptsAValidSessionSignature(t *testing.T) {
	s := signedGrant(t)
	at := s.Grant.ValidFrom.Add(time.Minute)

	if err := s.AuthorizeEvent(delegation.ScopeSessionEvent, addrFor(t, sessionKey), s.Grant.Nonce, at); err != nil {
		t.Fatalf("a valid session signature must be authorized: %v", err)
	}
}

// TestAuthorizeEvent_RefusesANonSessionSigner is the authorization property.
//
// The grant must only authorize ITS OWN session key. A different signer — including the owner — is
// not the delegated key, and accepting it would mean the grant's session key field was decorative.
func TestAuthorizeEvent_RefusesANonSessionSigner(t *testing.T) {
	s := signedGrant(t)
	at := s.Grant.ValidFrom.Add(time.Minute)

	if err := s.AuthorizeEvent(delegation.ScopeSessionEvent, addrFor(t, otherKey), s.Grant.Nonce, at); err == nil {
		t.Fatal("a signature from a key that is not the session key must be refused")
	}
	if err := s.AuthorizeEvent(delegation.ScopeSessionEvent, addrFor(t, ownerKey), s.Grant.Nonce, at); err == nil {
		t.Fatal("the owner is not the delegated session key either; only the named key may act for it")
	}
}

// TestAuthorizeEvent_RefusesRevokedGrant covers the nonce check.
func TestAuthorizeEvent_RefusesRevokedGrant(t *testing.T) {
	s := signedGrant(t)
	at := s.Grant.ValidFrom.Add(time.Minute)

	// The owner has published nonce+1, so the grant is stale.
	err := s.AuthorizeEvent(delegation.ScopeSessionEvent, addrFor(t, sessionKey), s.Grant.Nonce+1, at)
	if err == nil {
		t.Fatal("a revoked grant must be refused")
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Errorf("the refusal must name revocation, got: %v", err)
	}
}

// TestAuthorizeEvent_RefusesOutsideTheWindow covers the time check.
func TestAuthorizeEvent_RefusesOutsideTheWindow(t *testing.T) {
	s := signedGrant(t)
	signer := addrFor(t, sessionKey)

	if err := s.AuthorizeEvent(delegation.ScopeSessionEvent, signer, s.Grant.Nonce, s.Grant.ValidFrom.Add(-time.Hour)); err == nil {
		t.Error("a time before the window must be refused")
	}
	if err := s.AuthorizeEvent(delegation.ScopeSessionEvent, signer, s.Grant.Nonce, s.Grant.ValidUntil.Add(time.Hour)); err == nil {
		t.Error("a time after the window must be refused")
	}
}

// TestAuthorizeEvent_RefusesUngrantedScope covers the scope check.
func TestAuthorizeEvent_RefusesUngrantedScope(t *testing.T) {
	s := signedGrant(t)
	at := s.Grant.ValidFrom.Add(time.Minute)

	err := s.AuthorizeEvent(delegation.ScopeTaskLifecycle, addrFor(t, sessionKey), s.Grant.Nonce, at)
	if err == nil {
		t.Fatal("an ungranted scope must be refused")
	}
}

// TestAuthorizeEvent_RefusesAReceiptScope is the endpoint of the whole design decision.
//
// Receipts are not delegable, and the implementation expresses that as the ABSENCE of a receipt scope
// rather than as a value validation forbids. So this asks for one the type cannot express, and the
// gate must refuse it — meaning there is no path by which a session key signs a receipt.
func TestAuthorizeEvent_RefusesAReceiptScope(t *testing.T) {
	s := signedGrant(t)
	at := s.Grant.ValidFrom.Add(time.Minute)

	// A scope the type does not define. Even if a caller reaches past the constants with a literal,
	// the gate refuses it because it is not grantable.
	err := s.AuthorizeEvent(delegation.Scope("receipt"), addrFor(t, sessionKey), s.Grant.Nonce, at)
	if err == nil {
		t.Fatal("a receipt scope must be refused: a delegable receipt is how one leaked hot key " +
			"mints unlimited points")
	}
	if !strings.Contains(err.Error(), "not grantable") {
		t.Errorf("the refusal must say the scope does not exist, got: %v", err)
	}

	// And a grant cannot even be constructed with one, which is the stronger half of the design.
	bad := delegationsign.SignedGrant{
		Grant: delegation.Grant{
			Owner:      idFor(t, ownerKey),
			SessionKey: idFor(t, sessionKey),
			Scopes:     []delegation.Scope{"receipt"},
			ValidFrom:  time.Unix(1791015800, 0),
			ValidUntil: time.Unix(1791019400, 0),
		},
	}
	if err := bad.Sign(ownerKey, chainID); err == nil {
		t.Fatal("a grant naming a receipt scope must not even be signable")
	}
}

// TestAuthorizeEvent_RefusesAForgedGrant keeps a fabricated grant from authorizing anything.
func TestAuthorizeEvent_RefusesAForgedGrant(t *testing.T) {
	s := signedGrant(t)
	at := s.Grant.ValidFrom.Add(time.Minute)
	// Preserve the shape but break the signature.
	s.Signature = "0x" + strings.Repeat("00", 65)

	if err := s.AuthorizeEvent(delegation.ScopeSessionEvent, addrFor(t, sessionKey), s.Grant.Nonce, at); err == nil {
		t.Fatal("a grant with an invalid signature must not authorize anything")
	}
}

// TestVerify_RejectsUnsignedAndMalformed covers the shape checks.
func TestVerify_RejectsUnsignedAndMalformed(t *testing.T) {
	if err := (delegationsign.SignedGrant{Grant: signedGrant(t).Grant}).Verify(); err == nil {
		t.Error("an unsigned grant must not verify")
	}

	s := signedGrant(t)
	s.Signature = "not-hex"
	if err := s.Verify(); err == nil {
		t.Error("a non-hex signature must not verify")
	}

	s = signedGrant(t)
	s.Signature = "0xabcd"
	if err := s.Verify(); err == nil {
		t.Error("a short signature must not verify")
	}
}

// TestDomainIsDistinctFromReceipt keeps a delegation signature from being replayable as a receipt.
func TestDomainIsDistinctFromReceipt(t *testing.T) {
	if delegationsign.DomainVersion == "" {
		t.Fatal("the domain version must be set")
	}
	// The struct types also differ, and EIP-712 includes the type hash, so the protection is
	// independent of the version string.
	if delegationsign.DomainVersion == "1" {
		t.Error("the delegation domain should not reuse the receipt's version")
	}
}
