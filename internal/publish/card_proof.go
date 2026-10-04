// Agent Card signing and proof verification (S9-3).
//
// # Why the proof is not the card's own JWS signature
//
// A2A defines an optional JWS signature on a card, and the SDK implements it with
// ES256 (P-256) or RS256. RelayFirst cannot use it, and the reason is identity,
// not convenience: an agent's identity here is an EVM account
// (`agent:eip155:<chainId>:<address>`, MVP.md §17.2 — "identity is anchored in EVM
// accounts ... this cannot be dropped"). Its key is secp256k1, which ES256 cannot
// express. Signing a card with a second, unrelated key would give every agent two
// identities that nothing keeps in agreement, and the whole point of a signed card
// is to say which identity published it.
//
// So the binding is explicit instead of implicit: the card declares an agentId,
// and this proof carries a signature that only the holder of that agentId's key
// could produce, over a digest that commits to both the identity and the exact
// card bytes.
//
// # Why the digest is over the card's bytes, not its fields
//
// Same reasoning as the receipt's frozen payload (A9 §④). If the digest were
// computed by re-serializing parsed fields, then any difference between this
// build's serializer and the signer's — a field order, a number format, an added
// field — would silently change the digest and the signature would fail for a
// card that is perfectly fine. Hashing the bytes the publisher actually signed
// removes that entire class of failure, and lets a card carry fields this build
// does not know about.
//
// # What a valid proof establishes
//
// That whoever produced the proof held the key for the declared agentId, and that
// they signed THIS card. It does not establish that the card is honest, that the
// skills are real, or that the endpoint answers. Those are separate questions and
// nothing here should be read as answering them.
package publish

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	a2asdk "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/relayfirst/relayfirst/internal/a2a"
	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/eip712"
)

// CardProof is a detached signature binding a card to an agent identity.
//
// It is a separate object rather than a field inside the card so the card stays a
// byte-exact standard A2A document. Embedding the proof would change the card's
// bytes, which the proof hashes — a circularity that would make the signature
// impossible to verify after transport.
type CardProof struct {
	// AgentID is the identity being claimed. It is repeated from the card's
	// extension on purpose: verification checks the two agree, and that agreement
	// is the binding. If they could not differ, there would be nothing to check.
	AgentID string `json:"agentId"`

	// CardHash is keccak256 of the exact card bytes that were signed.
	CardHash string `json:"cardHash"`

	// Signature is a 65-byte secp256k1 signature over the EIP-712 digest, hex.
	Signature string `json:"signature"`
}

// CardDomainVersion is the EIP-712 domain version for card proofs.
//
// It is separate from the receipt domain versions so a signature for one can
// never be replayed as the other. That separation is not decorative: without it, a
// receipt digest and a card digest for the same agent could in principle collide,
// and a signature meant for a card would validate as a receipt.
const CardDomainVersion = "1"

// cardDomainName is the EIP-712 domain name. Kept identical to the receipt
// domain's name so a verifier has one name to recognize, with the version
// distinguishing the two uses.
const cardDomainName = "RelayFirst"

// cardTypes is the EIP-712 struct for a card proof.
//
// The fields are the two things being bound: who, and which bytes. Nothing else
// belongs here — adding a field that the card already contains would duplicate
// data that could then disagree.
var cardTypes = eip712.Types{
	"RelayAgentCard": {
		{Name: "agentId", Type: "string"},
		{Name: "cardHash", Type: "bytes32"},
	},
}

// CardHash returns keccak256 of raw card bytes.
//
// It is exported so a verifier can check a proof without re-marshalling the card,
// which is what keeps verification independent of any serializer.
func CardHash(raw []byte) string {
	return "0x" + hex.EncodeToString(eip712.Keccak256(raw))
}

// SignCard produces a proof for raw card bytes using the agent's private key.
//
// # Why the agentId is derived rather than trusted
//
// The caller passes an agentId and a key. If those disagreed, the proof would
// claim an identity the key cannot sign for, and verification would fail later at
// a place with less context to explain why. So this derives the address from the
// key and refuses to sign a mismatch — the failure belongs here, where both values
// are in hand.
func SignCard(privKeyHex string, chainID uint64, claimedAgentID string, raw []byte) (CardProof, error) {
	if len(raw) == 0 {
		return CardProof{}, fmt.Errorf("publish: cannot sign an empty card")
	}

	priv, err := eip712.PrivateKeyFromHex(privKeyHex)
	if err != nil {
		return CardProof{}, fmt.Errorf("publish: parse private key: %w", err)
	}
	derivedAddr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	derivedID, err := agentid.Format(chainID, eip712.AddressToHex(derivedAddr))
	if err != nil {
		return CardProof{}, fmt.Errorf("publish: derive agent id: %w", err)
	}

	claimed, err := agentid.Parse(claimedAgentID)
	if err != nil {
		return CardProof{}, fmt.Errorf("publish: %w", err)
	}
	if claimed.String() != derivedID {
		return CardProof{}, fmt.Errorf(
			"publish: the key derives identity %s but the card claims %s; "+
				"signing would produce a proof that cannot verify",
			derivedID, claimed.String())
	}

	hash := CardHash(raw)
	td := eip712.TypedData{
		Types:       cardTypes,
		PrimaryType: "RelayAgentCard",
		Domain: eip712.Domain{
			Name:    cardDomainName,
			Version: CardDomainVersion,
		},
		Message: map[string]any{
			"agentId":  claimed.String(),
			"cardHash": hash,
		},
	}

	sig, err := eip712.Sign(priv, td)
	if err != nil {
		return CardProof{}, fmt.Errorf("publish: sign card: %w", err)
	}

	return CardProof{
		AgentID:   claimed.String(),
		CardHash:  hash,
		Signature: "0x" + hex.EncodeToString(sig),
	}, nil
}

// VerifyCardProof checks a proof against the exact card bytes it should cover.
//
// # The order of checks, and why it matters
//
// The hash is compared before the signature. A signature over the wrong bytes
// would fail anyway, but it would fail as "invalid signature", which points an
// operator at a forgery. Comparing the hash first yields the accurate answer: the
// proof is for a different card, which is what a stale card, a re-serialized card,
// or a swapped card all look like. Those are different problems from a forgery and
// deserve a different message.
//
// It does not verify that the card's own identity extension agrees with the proof;
// callers should do that separately (see VerifyCard), because a proof is
// meaningful on its own and a caller may legitimately hold only the proof.
func VerifyCardProof(proof CardProof, raw []byte) error {
	if len(raw) == 0 {
		return fmt.Errorf("publish: cannot verify a proof against an empty card")
	}
	if strings.TrimSpace(proof.AgentID) == "" {
		return fmt.Errorf("publish: proof has no agentId")
	}
	if _, err := agentid.Parse(proof.AgentID); err != nil {
		return fmt.Errorf("publish: proof agentId is malformed: %w", err)
	}

	gotHash := CardHash(raw)
	if !strings.EqualFold(gotHash, proof.CardHash) {
		return fmt.Errorf(
			"publish: proof is for a different card: proof covers %s but these bytes hash to %s",
			proof.CardHash, gotHash)
	}

	sig, err := hex.DecodeString(strings.TrimPrefix(proof.Signature, "0x"))
	if err != nil {
		return fmt.Errorf("publish: proof signature is not valid hex: %w", err)
	}
	if len(sig) != eip712.SignatureLength {
		return fmt.Errorf("publish: proof signature is %d bytes, want %d",
			len(sig), eip712.SignatureLength)
	}

	td := eip712.TypedData{
		Types:       cardTypes,
		PrimaryType: "RelayAgentCard",
		Domain: eip712.Domain{
			Name:    cardDomainName,
			Version: CardDomainVersion,
		},
		Message: map[string]any{
			"agentId":  proof.AgentID,
			"cardHash": proof.CardHash,
		},
	}

	recovered, err := eip712.RecoverAddress(td, sig)
	if err != nil {
		return fmt.Errorf("publish: card signature recovery failed: %w", err)
	}

	declared, err := agentid.Parse(proof.AgentID)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	declaredAddr, err := eip712.HexToAddress(declared.Address)
	if err != nil {
		return fmt.Errorf("publish: proof agentId address: %w", err)
	}
	if !bytesEqual(declaredAddr, recovered) {
		return fmt.Errorf(
			"publish: card proof signer mismatch: proof claims %s but the signature recovers %s",
			declared.Address, eip712.AddressToHex(recovered))
	}
	return nil
}

// VerifyCard checks that a card's declared identity matches a proof, and that the
// proof is valid over the card's exact bytes.
//
// # The check this function exists for
//
// A card declares an agentId in its extension. A proof carries an agentId too. If
// they can disagree, an attacker can take a valid proof for their own identity and
// attach it to a card that claims someone else's — the card would look signed, and
// the identity a reader trusts would be the wrong one. So the two must be
// compared, and the comparison is the whole reason both carry the field.
func VerifyCard(card *a2asdk.AgentCard, raw []byte, proof CardProof) error {
	if err := VerifyCardProof(proof, raw); err != nil {
		return err
	}

	// Parse the card's identity out of the bytes that were signed, not out of a
	// struct the caller assembled. Reading it from the verified bytes is what
	// makes this a check rather than a re-statement of the caller's input.
	var parsed a2asdk.AgentCard
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("publish: card bytes are not a valid A2A card: %w", err)
	}
	cardAgentID, ok := a2a.AgentIDFromCard(&parsed)
	if !ok {
		return fmt.Errorf("publish: card carries no RelayFirst identity extension")
	}
	if _, err := agentid.Parse(cardAgentID); err != nil {
		return fmt.Errorf("publish: card identity extension is malformed: %w", err)
	}

	proofID, err := agentid.Parse(proof.AgentID)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	cardID, err := agentid.Parse(cardAgentID)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	if proofID.String() != cardID.String() {
		return fmt.Errorf(
			"publish: proof identity %s does not match the card's declared identity %s; "+
				"a proof is only meaningful for the card that declares the same agent",
			proofID.String(), cardID.String())
	}

	// card is unused beyond the nil guard: verification deliberately reads the
	// bytes, not the struct. Keeping the parameter makes the caller's intent
	// explicit and prevents passing a card that was never serialized.
	if card == nil {
		return fmt.Errorf("publish: nil card")
	}
	return nil
}

// bytesEqual is a local comparison so this file does not depend on receipt's.
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
