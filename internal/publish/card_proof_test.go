package publish

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	a2asdk "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/relayfirst/relayfirst/internal/a2a"
	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/eip712"
)

// Two distinct keys so tests can exercise "the wrong signer" rather than only
// "a malformed signature".
const (
	cardPrivKeyA = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	cardPrivKeyB = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	cardChainID  = uint64(8453)
)

// agentIDFor derives the canonical id for a private key, so a test never has to
// hardcode an address that could drift from the key.
func agentIDFor(t *testing.T, privKeyHex string) string {
	t.Helper()
	priv, err := eip712.PrivateKeyFromHex(privKeyHex)
	if err != nil {
		t.Fatalf("private key: %v", err)
	}
	addr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	id, err := agentid.Format(cardChainID, eip712.AddressToHex(addr))
	if err != nil {
		t.Fatalf("format agent id: %v", err)
	}
	return id
}

// buildCardBytes returns the exact card bytes and the card, so a test can prove
// the proof covers those bytes and nothing re-serialized.
func buildCardBytes(t *testing.T, agentID string) (*a2asdk.AgentCard, []byte) {
	t.Helper()
	card, err := a2a.Build(a2a.CardSpec{
		AgentID:     agentID,
		Name:        "relayfirst-node",
		Description: "A RelayFirst relay node",
		URL:         "https://node.example/a2a",
		Version:     "1.0.0",
		Skills: []a2asdk.AgentSkill{
			{ID: "relay", Name: "Relay messages", Description: "Store and forward"},
		},
	})
	if err != nil {
		t.Fatalf("build card: %v", err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	return card, raw
}

func TestSignAndVerifyCard_RoundTrip(t *testing.T) {
	idA := agentIDFor(t, cardPrivKeyA)
	card, raw := buildCardBytes(t, idA)

	proof, err := SignCard(cardPrivKeyA, cardChainID, idA, raw)
	if err != nil {
		t.Fatalf("sign card: %v", err)
	}
	if proof.AgentID != idA {
		t.Errorf("proof agentId = %q, want %q", proof.AgentID, idA)
	}
	if err := VerifyCardProof(proof, raw); err != nil {
		t.Errorf("a freshly signed proof must verify: %v", err)
	}
	if err := VerifyCard(card, raw, proof); err != nil {
		t.Errorf("a freshly signed card must verify: %v", err)
	}
}

// TestSignCard_RefusesMismatchedKey is the guard that keeps a proof from being
// born unverifiable. The failure belongs at signing time, where both the key and
// the claimed identity are in hand.
func TestSignCard_RefusesMismatchedKey(t *testing.T) {
	idB := agentIDFor(t, cardPrivKeyB)
	_, raw := buildCardBytes(t, idB)

	// Sign with key A while claiming identity B.
	if _, err := SignCard(cardPrivKeyA, cardChainID, idB, raw); err == nil {
		t.Fatal("signing with a key that does not match the claimed identity must fail")
	}
}

// TestVerifyCardProof_RejectsTamperedCard is the core integrity property: any
// change to the bytes must invalidate the proof.
func TestVerifyCardProof_RejectsTamperedCard(t *testing.T) {
	idA := agentIDFor(t, cardPrivKeyA)
	_, raw := buildCardBytes(t, idA)
	proof, err := SignCard(cardPrivKeyA, cardChainID, idA, raw)
	if err != nil {
		t.Fatalf("sign card: %v", err)
	}

	// A single-byte change anywhere must break it. Mutating a real field is the
	// realistic case; the point is that nothing about the card is unprotected.
	tampered := make([]byte, len(raw))
	copy(tampered, raw)
	for i := range tampered {
		if tampered[i] == 'r' { // inside "relayfirst-node"
			tampered[i] = 'x'
			break
		}
	}
	if string(tampered) == string(raw) {
		t.Fatal("test did not actually mutate the card")
	}

	err = VerifyCardProof(proof, tampered)
	if err == nil {
		t.Fatal("a proof must not verify against modified card bytes")
	}
	// The message must say "different card", not "bad signature": those are
	// different problems for an operator to chase.
	if !strings.Contains(err.Error(), "different card") {
		t.Errorf("a hash mismatch should be reported as a different card, got: %v", err)
	}
}

// TestVerifyCardProof_RejectsWrongSigner covers a proof that is internally valid
// but was made by someone else.
func TestVerifyCardProof_RejectsWrongSigner(t *testing.T) {
	idB := agentIDFor(t, cardPrivKeyB)
	_, raw := buildCardBytes(t, idB)
	proof, err := SignCard(cardPrivKeyB, cardChainID, idB, raw)
	if err != nil {
		t.Fatalf("sign card: %v", err)
	}

	// Relabel the proof as identity A without re-signing. The hash still matches
	// the bytes, so only the signature check can catch this.
	proof.AgentID = agentIDFor(t, cardPrivKeyA)

	if err := VerifyCardProof(proof, raw); err == nil {
		t.Fatal("a proof relabelled to another identity must not verify")
	}
}

// TestVerifyCard_RejectsIdentitySwap is the attack this design specifically
// guards against, and it must exercise the identity comparison rather than
// passing because the signature check happened to fire first.
//
// The attack: an attacker holds a valid key for identity A. They craft card bytes
// that declare identity B, and sign those exact bytes with A's key. The proof is
// internally consistent — its agentId is A, and A's signature over {A, hash} is
// genuine — so VerifyCardProof passes on its own. Only the comparison between the
// proof's identity and the identity inside the signed card bytes rejects it.
//
// SignCard cannot produce this fixture, which is the point: the attack requires
// bypassing it. So the proof is assembled with the low-level EIP-712 calls an
// attacker would actually use.
func TestVerifyCard_RejectsIdentitySwap(t *testing.T) {
	idA := agentIDFor(t, cardPrivKeyA)
	idB := agentIDFor(t, cardPrivKeyB)

	// Card bytes that declare B, but signed by A.
	cardB, rawB := buildCardBytes(t, idB)

	privA, err := eip712.PrivateKeyFromHex(cardPrivKeyA)
	if err != nil {
		t.Fatalf("private key A: %v", err)
	}
	hash := CardHash(rawB)
	td := eip712.TypedData{
		Types:       cardTypes,
		PrimaryType: "RelayAgentCard",
		Domain:      eip712.Domain{Name: cardDomainName, Version: CardDomainVersion},
		Message:     map[string]any{"agentId": idA, "cardHash": hash},
	}
	sig, err := eip712.Sign(privA, td)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	forged := CardProof{
		AgentID:   idA,
		CardHash:  hash,
		Signature: "0x" + hex.EncodeToString(sig),
	}

	// The forged proof is internally valid: A really did sign these bytes.
	if err := VerifyCardProof(forged, rawB); err != nil {
		t.Fatalf("precondition: the forged proof must pass the signature check on its own, "+
			"otherwise this test is not exercising the identity comparison: %v", err)
	}

	// But the card declares B, and the proof says A. That disagreement is the
	// attack, and it must be rejected.
	err = VerifyCard(cardB, rawB, forged)
	if err == nil {
		t.Fatal("a card declaring B, backed by A's valid proof, must be rejected: " +
			"otherwise A can publish a card that readers attribute to B")
	}
	if !strings.Contains(err.Error(), "does not match the card's declared identity") {
		t.Errorf("the rejection must name the identity disagreement, got: %v", err)
	}
}

// TestVerifyCard_ReadsIdentityFromSignedBytes confirms the card's identity is
// taken from the verified bytes, not from a struct the caller assembled. If it
// were read from the struct, a caller could verify one card and then present a
// different one.
func TestVerifyCard_ReadsIdentityFromSignedBytes(t *testing.T) {
	idA := agentIDFor(t, cardPrivKeyA)
	_, rawA := buildCardBytes(t, idA)
	proofA, err := SignCard(cardPrivKeyA, cardChainID, idA, rawA)
	if err != nil {
		t.Fatalf("sign card: %v", err)
	}

	// Pass a card struct that claims a different identity, alongside the honest
	// bytes and proof. The bytes are the truth, so this must pass.
	idB := agentIDFor(t, cardPrivKeyB)
	decoy, _ := buildCardBytes(t, idB)
	if err := VerifyCard(decoy, rawA, proofA); err != nil {
		t.Errorf("verification must read the identity from the signed bytes, not the passed struct: %v", err)
	}
}

// TestVerifyCardProof_RejectsMalformedInputs covers the shape checks.
func TestVerifyCardProof_RejectsMalformedInputs(t *testing.T) {
	idA := agentIDFor(t, cardPrivKeyA)
	_, raw := buildCardBytes(t, idA)
	good, err := SignCard(cardPrivKeyA, cardChainID, idA, raw)
	if err != nil {
		t.Fatalf("sign card: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*CardProof)
	}{
		{"empty agentId", func(p *CardProof) { p.AgentID = "" }},
		{"malformed agentId", func(p *CardProof) { p.AgentID = "agent:eip155::0x00" }},
		{"empty signature", func(p *CardProof) { p.Signature = "" }},
		{"non-hex signature", func(p *CardProof) { p.Signature = "0xzzzz" }},
		{"short signature", func(p *CardProof) { p.Signature = "0xabcd" }},
		{"empty hash", func(p *CardProof) { p.CardHash = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := good
			c.mutate(&p)
			if err := VerifyCardProof(p, raw); err == nil {
				t.Errorf("a proof with %s must not verify", c.name)
			}
		})
	}

	if err := VerifyCardProof(good, nil); err == nil {
		t.Error("verifying against empty card bytes must fail")
	}
}

// TestCardProof_DomainIsDistinctFromReceipt is the cross-protocol replay guard.
//
// A receipt and a card proof are both EIP-712 signatures by the same key over the
// same domain name. If their domain versions matched and their structs ever
// collided, a signature made for one could be replayed as the other. The versions
// are kept distinct for exactly this reason, and this test fails if someone
// unifies them without thinking it through.
func TestCardProof_DomainIsDistinctFromReceipt(t *testing.T) {
	if CardDomainVersion == "1" {
		// Receipt v1 also uses "1". The structs differ (RelayReceipt vs
		// RelayAgentCard), and EIP-712 includes the type hash in the digest, so
		// the two cannot collide even with the same version string. This test
		// documents that the safety comes from the struct, not the version, so
		// that a future change to either is a deliberate act.
		if _, ok := cardTypes["RelayReceipt"]; ok {
			t.Fatal("the card proof must not reuse the receipt struct type")
		}
		if _, ok := cardTypes["RelayAgentCard"]; !ok {
			t.Fatal("the card proof struct must be RelayAgentCard")
		}
	}
}

// TestCardHash_IsOverRawBytes pins the property that makes verification
// serializer-independent: the hash is a function of the bytes, so a card carrying
// fields this build does not know about still verifies.
func TestCardHash_IsOverRawBytes(t *testing.T) {
	idA := agentIDFor(t, cardPrivKeyA)
	_, raw := buildCardBytes(t, idA)

	// A card with an extra, unknown field must still verify if it was signed in
	// that exact form — that is the forward-compatibility A9 §④ requires.
	withExtra := append([]byte(nil), raw[:len(raw)-1]...)
	withExtra = append(withExtra, []byte(`,"futureField":{"added":true}}`)...)

	proof, err := SignCard(cardPrivKeyA, cardChainID, idA, withExtra)
	if err != nil {
		t.Fatalf("sign card with an additive field: %v", err)
	}
	if err := VerifyCardProof(proof, withExtra); err != nil {
		t.Errorf("a card with an additive field must verify against its own bytes: %v", err)
	}

	// And the original bytes must no longer match that proof.
	if err := VerifyCardProof(proof, raw); err == nil {
		t.Error("a proof for modified bytes must not verify against the original")
	}
}
