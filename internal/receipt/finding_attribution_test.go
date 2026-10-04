package receipt

import (
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/eip712"
)

// This file records a security finding about SIGNER ATTRIBUTION, found while planning S13-3
// (Session Delegation). It exists because the finding is the entire reason S13-3 is dangerous, and
// a claim that load-bearing belongs in a test rather than in prose.

// TestFinding_OnlyTheAgentIdKeyCanSign shows the invariant that currently holds, and why it is
// simultaneously the protection and the obstacle.
//
// # What holds today
//
// `Validate` requires the address inside `agentId` to equal the address recovered from the
// signature. So one agent cannot mint receipts attributed to another: an impostor's key does not
// recover to the victim's address, and the receipt is rejected.
//
// # Why that matters for S13-3, and is the finding
//
// The same check means a SESSION KEY CANNOT SIGN A RECEIPT TODAY. Not because a scope rule forbids
// it, but because a session key is a different key, so its recovered address does not match the
// `agentId` — and validation rejects it as a signer mismatch.
//
// So the property "a compromised session key cannot mint receipts" is currently FREE, enforced
// structurally. Making Session Delegation work means RELAXING this check so a delegated key is
// accepted...
//
// # ...and THAT is the danger
//
// The relaxation cannot be scoped to session-level events by itself. Whatever mechanism accepts a
// delegated signer has to decide, per signature, whether this key was allowed to sign THIS kind of
// object — and if that decision is wrong, or missing, or defaulted, a session key can sign a
// RECEIPT. A receipt is the only thing that earns points, so the failure mode is: one leaked hot
// key mints unlimited points.
//
// Concretely, the danger is that the relaxation is a CHANGE TO THE ONE INVARIANT THAT CURRENTLY
// MAKES FORGERY IMPOSSIBLE. It is not an additive feature.
func TestFinding_OnlyTheAgentIdKeyCanSign(t *testing.T) {
	// The receipt declares the fixture key's identity and is signed by it: accepted.
	ownerID, err := DeriveAgentID(testPrivKey, 8453)
	if err != nil {
		t.Fatalf("derive owner id: %v", err)
	}

	r := validReceipt(t)
	r.AgentID = ownerID
	id, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	r.ReceiptID = id
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := r.Validate(nil); err != nil {
		t.Fatalf("the owner signing its own receipt must validate: %v", err)
	}

	// Now the same receipt, with the agentId pointing at a DIFFERENT identity while the signature
	// stays the owner's. This is the shape a session key would produce: a valid signature by a key
	// that is not the declared agent.
	//
	// It is REJECTED, and that rejection is exactly what S13-3 must weaken.
	const otherKey = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	otherID, err := DeriveAgentID(otherKey, 8453)
	if err != nil {
		t.Fatalf("derive other id: %v", err)
	}

	sessionSigned := validReceipt(t)
	sessionSigned.AgentID = otherID // declares the agent, as a delegated signature would
	id2, err := sessionSigned.DerivedReceiptID()
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	sessionSigned.ReceiptID = id2
	// Signed by the OWNER's key, not by the declared agent's key. This stands in for a session key.
	if err := sessionSigned.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}

	err = sessionSigned.Validate(nil)
	if err == nil {
		t.Fatal("a receipt whose declared agent did not sign must be rejected TODAY; if this " +
			"passes, the attribution invariant has already been weakened and S13-3's risk is live")
	}
	if !strings.Contains(err.Error(), "signer mismatch") {
		t.Errorf("the rejection must be a signer mismatch, which is the check S13-3 has to relax, got: %v", err)
	}
}

// TestFinding_ValidateWithNilSkipsAttribution shows the mechanism by which a caller can lose even the
// expectation check.
//
// `Validate(nil)` is documented as "do not check the signer" and is correct for the offline
// verifier, which has no expectation to compare against. The finding is not that the API is wrong;
// it is that `nil` is the ONLY argument any production caller passes, so the check that could tie a
// receipt to a caller's expectation is never reached in practice.
func TestFinding_ValidateWithNilSkipsAttribution(t *testing.T) {
	r := validReceipt(t)
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}

	foreign, err := eip712.HexToAddress("0x00000000000000000000000000000000000000ff")
	if err != nil {
		t.Fatalf("parse address: %v", err)
	}

	// With an expectation, the check fires.
	expectErr := r.Validate(foreign)
	if expectErr == nil {
		t.Fatal("passing a foreign expectation must fail, or the parameter does nothing")
	}
	if !strings.Contains(expectErr.Error(), "does not match expected") {
		t.Errorf("the failure must name the mismatch, got: %v", expectErr)
	}

	// With nil, it is skipped — the documented behaviour, and the gap.
	if err := r.Validate(nil); err != nil {
		t.Fatalf("Validate(nil) must not check the signer: %v", err)
	}
}

// TestFinding_ProductionCallersPassNil is the reason the previous test names a gap rather than a
// quirk: the check exists and is never used. If a future caller starts passing an expectation, this
// test's premise changes and someone should look at why.
func TestFinding_ProductionCallersPassNil(t *testing.T) {
	// The assertion is documentary: the search is in the accompanying analysis, and the point of
	// this test is that the finding has a place in the suite rather than only in a note.
	//
	// Concretely checked here: passing nil succeeds, which is what every production call site does.
	r := validReceipt(t)
	if err := r.Sign(testPrivKey); err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := r.Validate(nil); err != nil {
		t.Fatalf("Validate(nil) is the production path and must succeed for a well-formed receipt: %v", err)
	}
	t.Log("production call sites using Validate(nil): internal/mining/loop.go:137, :278; " +
		"internal/mining/verdict.go:82. None supplies an expectation.")
}
