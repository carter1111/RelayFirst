// Package assertion implements attributable verification (S10-5, MVP.md §7.3).
//
// # The problem this solves
//
// A node can make verification cheap: it has the receipts, it can re-check them, and a
// client that trusts it saves work. But a node that verifies and is believed is a node that
// can forge a verdict — it could say "valid" about a receipt it never checked, and the
// client would never know.
//
// §7.3's answer is not to forbid the node from verifying. It is to make the node's verdict
// ATTRIBUTABLE: the node signs its own conclusion, so a lie is a signed lie. That does not
// make the node honest; it makes dishonesty provable, which is what a later slashing stage
// needs and what a client needs in order to decide how much to trust it.
//
// # Why this is a separate component and not part of the node
//
// This is the resolution of the §7.1/§7.3 conflict recorded in TASKS.md §10.2b. The minimal
// node must remain unable to verify — that is a structural guarantee enforced by the import
// graph, and it is what makes "a node cannot forge work" true. A verifier is a different
// object with a different trust position: it holds a key, its conclusions are signed, and it
// is therefore accountable in a way a dumb relay is not.
//
// Keeping them separate means both properties hold at once: a node that cannot forge, and a
// verifier whose lies are evidence. Folding them together would have traded the first for
// the second.
//
// # What a client must still do
//
// Verify independently (S10-6). An assertion is an accelerator, not an authority. A client
// that reads an assertion and stops checking has replaced a verifiable system with a trusted
// one, and the signature on the assertion only tells it WHO claimed the work was valid — not
// that the claim is true.
package assertion

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/eip712"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// Verdict is what a verifier concluded.
//
// # Why this is a closed set and not a score
//
// A recompute verdict is binary: the result reproduces or it does not. There is no partial
// credit, and adding a numeric score here would invite a client to treat a weak opinion as a
// measurement. Weaker modes exist (`evaluator`) and live elsewhere, with their own honest
// labelling.
type Verdict string

const (
	// VerdictValid means the verifier re-checked and the receipt held up.
	VerdictValid Verdict = "valid"

	// VerdictInvalid means the verifier checked and it did not.
	VerdictInvalid Verdict = "invalid"

	// VerdictUnsupported means the verifier could not check it — a schema major or a
	// verification mode it does not implement.
	//
	// # Why this is a verdict and not an error
	//
	// It is a signed statement about the verifier, and it is the honest answer for a receipt
	// from a newer build. Collapsing it into "invalid" would turn an out-of-date verifier
	// into an accuser, which is the same mistake the receipt layer's unsupported/invalid
	// split exists to prevent.
	VerdictUnsupported Verdict = "unsupported"
)

// Valid reports whether v is a verdict this build defines.
func (v Verdict) Valid() bool {
	switch v {
	case VerdictValid, VerdictInvalid, VerdictUnsupported:
		return true
	default:
		return false
	}
}

// Assertion is a signed claim by a verifier about a receipt.
//
// # Why the receipt hash is inside the signed bytes
//
// An assertion that named only a receipt id would be reusable against any receipt that
// happened to share that id — and ids are chosen by the producer, so a dishonest one could
// mint a receipt whose id collides with a receipt that has a valid assertion. Binding the
// canonical bytes makes the claim about THIS receipt, and makes it fail if a single byte of
// it changes.
//
// # Why the verifier identity is not stored separately
//
// It is recoverable from the signature, and storing it as well would create a second source
// of truth that could disagree with the signature. The id is a field because the EIP-712
// struct needs it to be signed; verification compares the two rather than trusting either.
type Assertion struct {
	// ReceiptID is the id of the receipt being judged.
	ReceiptID string `json:"receiptId"`

	// ReceiptHash is keccak256 of the receipt's canonical bytes.
	ReceiptHash string `json:"receiptHash"`

	// VerifierID is the verifier's own agentId.
	VerifierID string `json:"verifierId"`

	// Verdict is the conclusion.
	Verdict Verdict `json:"verdict"`

	// Reason explains a non-valid verdict, and is required for one.
	//
	// # Why it is required rather than optional
	//
	// An accusation with no reason cannot be reviewed, and review is the only thing that
	// makes a negative verdict usable. A verifier that said "invalid" and nothing else would
	// be asking to be believed on the strength of its own assertion, which is exactly the
	// trust it is supposed to be replacing.
	Reason string `json:"reason,omitempty"`

	// AssertedAt is when the verifier made the claim.
	AssertedAt time.Time `json:"assertedAt"`

	// Signature is the verifier's EIP-712 signature over the assertion.
	Signature string `json:"signature,omitempty"`
}

// DomainName and DomainVersion scope the EIP-712 domain.
//
// The version is distinct from the receipt's and the card proof's so a signature made for one
// purpose cannot be replayed as another. The struct type also differs, and EIP-712 includes
// the type hash, so the two protections are independent.
const (
	DomainName    = "RelayFirst"
	DomainVersion = "assertion-1"
)

// types is the EIP-712 struct for an assertion.
//
// Every field that carries meaning is included. The reason is not: it is prose, and hashing
// prose would make an assertion fail if a verifier's wording changed between signing and
// verification, which is a fragile thing to bind.
var types = eip712.Types{
	"RelayVerifierAssertion": {
		{Name: "receiptId", Type: "string"},
		{Name: "receiptHash", Type: "bytes32"},
		{Name: "verifierId", Type: "string"},
		{Name: "verdict", Type: "string"},
	},
}

// ReceiptHash returns keccak256 of a receipt's canonical bytes.
//
// # Why the caller passes bytes rather than a receipt
//
// So that verification can hash exactly what it received. A helper that took a *Receipt would
// re-serialize it, and a re-serialization that dropped an unknown field would change the hash
// — the same trap the receipt layer's frozen payload exists to avoid.
func ReceiptHash(canonical []byte) string {
	return "0x" + hex.EncodeToString(eip712.Keccak256(canonical))
}

// typedData builds the signed structure.
func (a Assertion) typedData() eip712.TypedData {
	return eip712.TypedData{
		Types:       types,
		PrimaryType: "RelayVerifierAssertion",
		Domain: eip712.Domain{
			Name:    DomainName,
			Version: DomainVersion,
		},
		Message: map[string]any{
			"receiptId":   a.ReceiptID,
			"receiptHash": a.ReceiptHash,
			"verifierId":  a.VerifierID,
			"verdict":     string(a.Verdict),
		},
	}
}

// Sign attaches the verifier's signature.
//
// # Why the key is checked against the declared verifier id
//
// A mismatch would produce an assertion that cannot verify, and the failure would surface
// later at a place with less context to explain it. Refusing here puts the error where both
// values are in hand, the same reasoning as the agent card proof.
func (a *Assertion) Sign(privKeyHex string, chainID uint64) error {
	if err := a.validateShape(); err != nil {
		return err
	}

	priv, err := eip712.PrivateKeyFromHex(privKeyHex)
	if err != nil {
		return fmt.Errorf("assertion: parse private key: %w", err)
	}
	derivedAddr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	derivedID, err := agentid.Format(chainID, eip712.AddressToHex(derivedAddr))
	if err != nil {
		return fmt.Errorf("assertion: derive verifier id: %w", err)
	}
	declared, err := agentid.Parse(a.VerifierID)
	if err != nil {
		return fmt.Errorf("assertion: %w", err)
	}
	if declared.String() != derivedID {
		return fmt.Errorf(
			"assertion: the key derives identity %s but the assertion claims %s; "+
				"signing would produce an assertion that cannot verify",
			derivedID, declared.String())
	}

	sig, err := eip712.Sign(priv, a.typedData())
	if err != nil {
		return fmt.Errorf("assertion: sign: %w", err)
	}
	a.Signature = "0x" + hex.EncodeToString(sig)
	return nil
}

// Verify checks the assertion's signature against the receipt bytes it should cover.
//
// # The order of checks, and why
//
// Shape first, then the receipt hash, then the signature. A signature over the wrong bytes
// would fail anyway, but it would fail as "bad signature" and point an operator at a forgery.
// Comparing the hash first yields the accurate answer: this assertion is about a different
// receipt, which is what a stale or modified receipt looks like.
//
// # What this does not establish
//
// That the verdict is correct. It establishes that a named verifier made this claim about
// these bytes. Whether the claim is true is what independent verification (S10-6) answers,
// and the two must not be confused: this function answers "who said what", never "is it so".
func (a Assertion) Verify(canonicalReceipt []byte) error {
	if err := a.validateShape(); err != nil {
		return err
	}
	if len(canonicalReceipt) == 0 {
		return fmt.Errorf("assertion: no receipt bytes to verify against")
	}

	wantHash := ReceiptHash(canonicalReceipt)
	if !strings.EqualFold(wantHash, a.ReceiptHash) {
		return fmt.Errorf(
			"assertion: this assertion is about a different receipt: it covers %s but these bytes hash to %s",
			a.ReceiptHash, wantHash)
	}

	sig, err := hex.DecodeString(strings.TrimPrefix(a.Signature, "0x"))
	if err != nil {
		return fmt.Errorf("assertion: signature is not valid hex: %w", err)
	}
	if len(sig) != eip712.SignatureLength {
		return fmt.Errorf("assertion: signature is %d bytes, want %d", len(sig), eip712.SignatureLength)
	}

	recovered, err := eip712.RecoverAddress(a.typedData(), sig)
	if err != nil {
		return fmt.Errorf("assertion: signature recovery failed: %w", err)
	}

	declared, err := agentid.Parse(a.VerifierID)
	if err != nil {
		return fmt.Errorf("assertion: %w", err)
	}
	declaredAddr, err := eip712.HexToAddress(declared.Address)
	if err != nil {
		return fmt.Errorf("assertion: verifier address: %w", err)
	}
	if !bytesEqual(declaredAddr, recovered) {
		return fmt.Errorf(
			"assertion: verifier mismatch: the assertion names %s but the signature recovers %s",
			declared.Address, eip712.AddressToHex(recovered))
	}
	return nil
}

// validateShape checks the fields that are independent of the signature.
func (a Assertion) validateShape() error {
	if strings.TrimSpace(a.ReceiptID) == "" {
		return fmt.Errorf("assertion: no receiptId")
	}
	if strings.TrimSpace(a.ReceiptHash) == "" {
		return fmt.Errorf("assertion: no receiptHash; an assertion that does not bind the bytes " +
			"could be replayed against a different receipt with the same id")
	}
	if _, err := agentid.Parse(a.VerifierID); err != nil {
		return fmt.Errorf("assertion: verifierId: %w", err)
	}
	if !a.Verdict.Valid() {
		return fmt.Errorf("assertion: verdict %q is not one of valid/invalid/unsupported", a.Verdict)
	}
	if a.AssertedAt.IsZero() {
		return fmt.Errorf("assertion: no assertedAt")
	}
	// A non-valid verdict must explain itself. See the field comment.
	if a.Verdict != VerdictValid && strings.TrimSpace(a.Reason) == "" {
		return fmt.Errorf("assertion: a %s verdict must carry a reason; "+
			"an accusation with no reason cannot be reviewed", a.Verdict)
	}
	return nil
}

// MarshalCanonical renders the assertion for transport.
//
// encoding/json is sufficient here because the assertion is not itself signed — its
// signature covers the EIP-712 struct, which is a fixed field list. A re-serialization
// therefore cannot invalidate it, which is why no frozen-bytes machinery is needed.
func (a Assertion) MarshalCanonical() ([]byte, error) {
	return json.Marshal(a)
}

// UnmarshalAssertion parses an assertion.
func UnmarshalAssertion(raw []byte) (Assertion, error) {
	var a Assertion
	if err := json.Unmarshal(raw, &a); err != nil {
		return Assertion{}, fmt.Errorf("assertion: decode: %w", err)
	}
	return a, nil
}

// Assert builds and signs an assertion from a verification outcome.
//
// # Why this takes the receipt's canonical bytes and not the receipt
//
// So the hash is over exactly what the verifier checked. Passing a *Receipt would invite a
// re-serialization, and a re-serialization that dropped a field would produce an assertion
// about bytes nobody else has.
func Assert(
	canonicalReceipt []byte,
	r *receipt.Receipt,
	verifierID, privKeyHex string,
	chainID uint64,
	verdict Verdict,
	reason string,
	at time.Time,
) (Assertion, error) {
	if r == nil {
		return Assertion{}, fmt.Errorf("assertion: nil receipt")
	}
	if len(canonicalReceipt) == 0 {
		return Assertion{}, fmt.Errorf("assertion: no receipt bytes")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}

	a := Assertion{
		ReceiptID:   r.ReceiptID,
		ReceiptHash: ReceiptHash(canonicalReceipt),
		VerifierID:  verifierID,
		Verdict:     verdict,
		Reason:      reason,
		AssertedAt:  at.UTC(),
	}
	if err := a.Sign(privKeyHex, chainID); err != nil {
		return Assertion{}, err
	}
	return a, nil
}

// bytesEqual compares two byte slices.
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
