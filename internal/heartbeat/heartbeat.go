// Package heartbeat implements the Layer 0 liveness attestation (Q1 = A, F1).
//
// # The shape of the problem
//
// A node has NO key (ADR-0004), so "node N was alive in slot t" cannot be said by the
// node itself -- that would be self-attestation, which incentive.md forbids. A verifier
// says it instead, on the node's behalf. This package is the verifier's message and the
// rule that makes it worth something.
//
// # Why the same EIP-712 domain as a verdict
//
// It reuses assertion-1. EIP-712 hashes the struct type, so a RelayNodeHeartbeat cannot
// be replayed as a RelayVerifierAssertion even under the same domain; the two are
// distinct messages. Reusing the domain means the per-key domain whitelist (ADR-0009 D3)
// is unchanged -- a verifier that may sign assertion-1 may sign heartbeats, and no new
// capability is granted.
//
// # The two holes F1 closes, and the one it accepts
//
// A verifier that attests a node could (1) attest a node it operates itself, dressing up
// self-attestation under a second identity, or (2) attest a NodeID that does not exist.
// F1 answers (1) directly: Qualified refuses any attestation whose signer IS the node's
// operator, the same A != B rule S4-6 uses to refuse self-verification. Hole (2) is
// reduced to collusion with an outside verifier, which is the single trust assumption
// Q1=A already accepts and which S4-6 has a pattern for. So F1 turns two holes into one
// already-priced risk rather than leaving both open.
package heartbeat

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/assertion"
	"github.com/relayfirst/relayfirst/internal/eip712"
)

// DomainName and DomainVersion are assertion's, referenced rather than copied so the two
// can never drift. EIP-712's type hash is what keeps a heartbeat from being a verdict.
const (
	DomainName    = assertion.DomainName
	DomainVersion = assertion.DomainVersion
)

// Attestation is a verifier saying "node N was alive in this slot of this epoch".
type Attestation struct {
	// NodeID is the node the claim is about, in the canonical agent id form. The node
	// has no key; this id is published (S10-0), not signed by the node.
	NodeID string `json:"nodeId"`

	// Epoch is the settlement epoch the slot belongs to.
	Epoch uint64 `json:"epoch"`

	// Slot is the slot index within the epoch, in [0, SlotsPerEpoch).
	Slot uint32 `json:"slot"`

	// VerifierID is the attesting verifier's own agentId.
	VerifierID string `json:"verifierId"`

	// Signature is the verifier's EIP-712 signature over the attestation.
	Signature string `json:"signature,omitempty"`
}

var types = eip712.Types{
	"RelayNodeHeartbeat": {
		{Name: "nodeId", Type: "string"},
		{Name: "epoch", Type: "uint64"},
		{Name: "slot", Type: "uint32"},
		{Name: "verifierId", Type: "string"},
	},
}

func (a Attestation) typedData() eip712.TypedData {
	return eip712.TypedData{
		Types:       types,
		PrimaryType: "RelayNodeHeartbeat",
		Domain:      eip712.Domain{Name: DomainName, Version: DomainVersion},
		Message: map[string]any{
			"nodeId":     a.NodeID,
			"epoch":      a.Epoch,
			"slot":       uint64(a.Slot),
			"verifierId": a.VerifierID,
		},
	}
}

// Sign attaches the verifier's signature.
//
// The key is checked against the declared verifier id here, where both are in hand: a
// mismatch would otherwise surface later as an unverifiable attestation, at a place with
// less context to explain it. This mirrors assertion.Sign.
func (a *Attestation) Sign(privKeyHex string, chainID uint64) error {
	if err := a.validateShape(); err != nil {
		return err
	}

	priv, err := eip712.PrivateKeyFromHex(privKeyHex)
	if err != nil {
		return fmt.Errorf("heartbeat: parse private key: %w", err)
	}
	derivedAddr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	derivedID, err := agentid.Format(chainID, eip712.AddressToHex(derivedAddr))
	if err != nil {
		return fmt.Errorf("heartbeat: derive verifier id: %w", err)
	}
	declared, err := agentid.Parse(a.VerifierID)
	if err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	if declared.String() != derivedID {
		return fmt.Errorf(
			"heartbeat: the key derives identity %s but the attestation claims %s; "+
				"signing would produce an attestation that cannot verify",
			derivedID, declared.String())
	}

	sig, err := eip712.Sign(priv, a.typedData())
	if err != nil {
		return fmt.Errorf("heartbeat: sign: %w", err)
	}
	a.Signature = "0x" + hex.EncodeToString(sig)
	return nil
}

// Verify checks the signature and returns the attesting verifier's 20-byte address.
//
// It answers "who said this", never "is the node actually alive" -- that is a question
// about reality, and no signature answers it. The caller decides what a set of
// signed "alive" claims is worth.
func (a Attestation) Verify() ([]byte, error) {
	if err := a.validateShape(); err != nil {
		return nil, err
	}

	sig, err := hex.DecodeString(strings.TrimPrefix(a.Signature, "0x"))
	if err != nil {
		return nil, fmt.Errorf("heartbeat: signature is not valid hex: %w", err)
	}
	if len(sig) != eip712.SignatureLength {
		return nil, fmt.Errorf("heartbeat: signature is %d bytes, want %d", len(sig), eip712.SignatureLength)
	}

	recovered, err := eip712.RecoverAddress(a.typedData(), sig)
	if err != nil {
		return nil, fmt.Errorf("heartbeat: signature recovery failed: %w", err)
	}

	declared, err := agentid.Parse(a.VerifierID)
	if err != nil {
		return nil, fmt.Errorf("heartbeat: %w", err)
	}
	declaredAddr, err := eip712.HexToAddress(declared.Address)
	if err != nil {
		return nil, fmt.Errorf("heartbeat: verifier address: %w", err)
	}
	if !bytes.Equal(declaredAddr, recovered) {
		return nil, fmt.Errorf(
			"heartbeat: verifier mismatch: the attestation names %s but the signature recovers %s",
			declared.Address, eip712.AddressToHex(recovered))
	}
	return recovered, nil
}

func (a Attestation) validateShape() error {
	if _, err := agentid.Parse(a.NodeID); err != nil {
		return fmt.Errorf("heartbeat: nodeId: %w", err)
	}
	if _, err := agentid.Parse(a.VerifierID); err != nil {
		return fmt.Errorf("heartbeat: verifierId: %w", err)
	}
	return nil
}

// Spec is the qualifying rule for one epoch. Every field is a knob; the defaults come
// from incentive.md §3.
type Spec struct {
	// SlotsPerEpoch is how many slots an epoch is divided into. A 10-minute slot over a
	// 7-day epoch is 1008.
	SlotsPerEpoch int

	// RequiredOnline is the fraction of slots a node must be seen in to qualify.
	RequiredOnline float64

	// MaxGap is the longest run of consecutive missed slots allowed, so a node cannot
	// sit offline for hours and still pass on total count alone.
	MaxGap int
}

// DefaultSpec is the 95% / 18-slot rule from incentive.md §3 (10-minute slots, a
// 3-hour max gap). The slot length is not fixed there; it is a knob, not a claim.
func DefaultSpec() Spec {
	return Spec{SlotsPerEpoch: 1008, RequiredOnline: 0.95, MaxGap: 18}
}

// OperatorAddress extracts the 20-byte address from a node operator's agent id.
func OperatorAddress(operatorID string) ([]byte, error) {
	id, err := agentid.Parse(operatorID)
	if err != nil {
		return nil, fmt.Errorf("heartbeat: operator id: %w", err)
	}
	return eip712.HexToAddress(id.Address)
}

// Qualified decides whether a node passed an epoch, from the attestations it collected.
//
// # The A != B rule, enforced here
//
// nodeOperator is the address that registered the node. Every attestation's signer must
// differ from it: a verifier signing for a node it operates is self-attestation under a
// second identity, and it is refused rather than counted. This is the same rule S4-6
// applies to self-verification, for the same reason.
//
// # What it does not catch, and why that is accepted
//
// A verifier that is a genuinely separate party, colluding with the node, is not caught
// here. That is the single trust assumption Q1=A takes on; F1 does not remove it, it
// stops it from being also achievable with no outside party at all.
//
// It returns an error (not false) on a bad or self-signed attestation: a malformed input
// is a bug or an attack to surface, not simply an unhealthy node.
func Qualified(nodeOperator []byte, atts []Attestation, s Spec) (bool, error) {
	if s.SlotsPerEpoch <= 0 {
		return false, fmt.Errorf("heartbeat: SlotsPerEpoch must be positive")
	}

	seen := make(map[uint32]bool, len(atts))
	for i := range atts {
		a := atts[i]
		signer, err := a.Verify()
		if err != nil {
			return false, fmt.Errorf("heartbeat: attestation %d: %w", i, err)
		}
		if bytes.Equal(signer, nodeOperator) {
			return false, fmt.Errorf(
				"heartbeat: self-attestation: the signer %s is the node's own operator; "+
					"a node cannot attest its own liveness",
				eip712.AddressToHex(signer))
		}
		if int(a.Slot) >= s.SlotsPerEpoch {
			return false, fmt.Errorf("heartbeat: slot %d is outside the epoch (0..%d)", a.Slot, s.SlotsPerEpoch-1)
		}
		seen[a.Slot] = true
	}

	// Online = distinct slots seen, as a fraction of the epoch.
	online := float64(len(seen)) / float64(s.SlotsPerEpoch)
	if online < s.RequiredOnline {
		return false, nil
	}

	// Longest run of consecutive missed slots.
	slots := make([]int, 0, len(seen))
	for slot := range seen {
		slots = append(slots, int(slot))
	}
	sort.Ints(slots)

	missed := 0
	for i := 1; i < len(slots); i++ {
		if gap := slots[i] - slots[i-1] - 1; gap > missed {
			missed = gap
		}
	}
	// The run before the first seen slot and after the last is a gap too: a node seen
	// only in the middle of an epoch was offline at the edges.
	if len(slots) > 0 {
		if head := slots[0]; head > missed {
			missed = head
		}
		if tail := s.SlotsPerEpoch - 1 - slots[len(slots)-1]; tail > missed {
			missed = tail
		}
	} else {
		missed = s.SlotsPerEpoch
	}

	return missed <= s.MaxGap, nil
}
