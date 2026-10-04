// Package eventsign signs and verifies A2A protocol events (S13-3 wiring).
//
// # Why this had to exist before delegation could be wired
//
// `internal/a2a` defines an Event with a Signature field and a SignedBytes method, and it has no
// signing or verification of its own — deliberately, because the node imports that package and a
// node must not be able to verify anything (MVP.md §7.1). The consequence was that nothing signed an
// event, so `delegationsign.AuthorizeEvent` had nothing to authorize: the delegation mechanism was
// complete and unreachable.
//
// So this is the missing layer, not a new feature. Wiring delegation without it would have produced
// an authorization check that no code path could reach.
//
// # The same split as everything else here
//
// The format lives in internal/a2a (dependency-free, node-safe). Signing and verifying live here,
// where linking eip712 is expected. That is the shape used for the receipt, the agent card, the
// assertion and the delegation grant, and it exists because the node's inability to verify is a
// property of the import graph rather than of anyone remembering.
//
// # What a signed event establishes
//
// That the actor named in the event signed THESE bytes. It does not establish that the actor was
// authorized to perform the action: that is what a delegation grant is for, and
// `delegationsign.AuthorizeEvent` is what checks it. Keeping the two apart matters because they fail
// differently — an unsigned event is a transport problem, while an unauthorized one is an authority
// problem, and a caller needs to know which it has.
package eventsign

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/relayfirst/relayfirst/internal/a2a"
	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/eip712"
)

// DomainName and DomainVersion scope the EIP-712 domain for events.
//
// # Why a fifth domain rather than reusing one
//
// EIP-712 includes the domain AND the struct type in the digest, so a replay across purposes needs
// both to coincide. Keeping the domains distinct means even a struct-name collision would not
// suffice. The versions in use are now: receipt "1"/"2", card "1", assertion "assertion-1",
// delegation "delegation-1", event "event-1".
const (
	DomainName    = "RelayFirst"
	DomainVersion = "event-1"
)

// eventTypes is the EIP-712 struct for an event.
//
// # Why it signs the payload HASH rather than listing the fields
//
// An event's payload is arbitrary JSON whose shape depends on the event type, and EIP-712 cannot
// express "some JSON". Hashing the canonical event bytes into a single bytes32 keeps the signed
// struct a fixed shape, which is the same reasoning as the receipt signing a payloadHash rather than
// the payload — and it means a new event type needs no change here.
var eventTypes = eip712.Types{
	"RelayEvent": {
		{Name: "actor", Type: "string"},
		{Name: "eventHash", Type: "bytes32"},
	},
}

// Sign attaches the actor's EIP-712 signature to an event.
//
// # Why the actor id is derived from the key rather than trusted
//
// A caller passing an actorId its key does not control would produce a signature that cannot verify,
// and the failure would surface later at a place with less context to explain it. Deriving here puts
// the error where both values are in hand — the same arrangement as the card proof, the assertion and
// the delegation grant.
//
// # Why the signature is over SignedBytes
//
// `Event.SignedBytes` returns the received bytes when the event came off the wire and a
// reconstruction otherwise. Signing must use the SAME function that verification uses, or a signed
// event would fail to verify against itself — which is the trap the receipt layer documents.
func Sign(e a2a.Event, privKeyHex string, chainID uint64) (a2a.Event, error) {
	if err := a2a.ValidateEvent(e); err != nil {
		return a2a.Event{}, err
	}

	priv, err := eip712.PrivateKeyFromHex(privKeyHex)
	if err != nil {
		return a2a.Event{}, fmt.Errorf("eventsign: parse key: %w", err)
	}
	derivedAddr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	derivedID, err := agentid.Format(chainID, eip712.AddressToHex(derivedAddr))
	if err != nil {
		return a2a.Event{}, fmt.Errorf("eventsign: derive actor id: %w", err)
	}
	declared, err := agentid.Parse(e.Actor)
	if err != nil {
		return a2a.Event{}, fmt.Errorf("eventsign: %w", err)
	}
	if declared.String() != derivedID {
		return a2a.Event{}, fmt.Errorf(
			"eventsign: the key derives identity %s but the event names %s; "+
				"signing would produce an event that cannot verify",
			derivedID, declared.String())
	}

	raw, err := e.SignedBytes()
	if err != nil {
		return a2a.Event{}, fmt.Errorf("eventsign: %w", err)
	}

	sig, err := eip712.Sign(priv, typedData(e.Actor, raw))
	if err != nil {
		return a2a.Event{}, fmt.Errorf("eventsign: sign: %w", err)
	}

	signed := e
	signed.Signature = "0x" + hex.EncodeToString(sig)
	return signed, nil
}

// Verify checks an event's signature and returns the recovered signer address.
//
// # Why it returns the address rather than only an error
//
// The caller's next question is almost always "and is that signer allowed to do this" — which for a
// delegated event means comparing against a grant. Returning the address means the caller does not
// recover it a second time, and two recoveries could only disagree by being wrong.
//
// # What it does not check
//
// Authority. See the package comment: an event signed by an actor who was not permitted to emit it
// verifies here and is rejected by `delegationsign.AuthorizeEvent`. Conflating the two would make an
// authority failure look like a transport one.
func Verify(e a2a.Event) ([]byte, error) {
	if err := a2a.ValidateEvent(e); err != nil {
		return nil, err
	}
	if strings.TrimSpace(e.Signature) == "" {
		return nil, fmt.Errorf("eventsign: event %s has no signature; an unsigned event proves nothing", e.EventID)
	}

	sig, err := hex.DecodeString(strings.TrimPrefix(e.Signature, "0x"))
	if err != nil {
		return nil, fmt.Errorf("eventsign: event %s signature is not valid hex: %w", e.EventID, err)
	}
	if len(sig) != eip712.SignatureLength {
		return nil, fmt.Errorf("eventsign: event %s signature is %d bytes, want %d",
			e.EventID, len(sig), eip712.SignatureLength)
	}

	raw, err := e.SignedBytes()
	if err != nil {
		return nil, fmt.Errorf("eventsign: event %s: %w", e.EventID, err)
	}

	recovered, err := eip712.RecoverAddress(typedData(e.Actor, raw), sig)
	if err != nil {
		return nil, fmt.Errorf("eventsign: event %s signature recovery failed: %w", e.EventID, err)
	}

	declared, err := agentid.Parse(e.Actor)
	if err != nil {
		return nil, fmt.Errorf("eventsign: %w", err)
	}
	declaredAddr, err := eip712.HexToAddress(declared.Address)
	if err != nil {
		return nil, fmt.Errorf("eventsign: actor address: %w", err)
	}
	if !bytesEqual(declaredAddr, recovered) {
		return nil, fmt.Errorf(
			"eventsign: event %s actor mismatch: the event names %s but the signature recovers %s",
			e.EventID, declared.Address, eip712.AddressToHex(recovered))
	}
	return recovered, nil
}

// typedData builds the signed structure from an actor and the event's signed bytes.
func typedData(actor string, signedBytes []byte) eip712.TypedData {
	return eip712.TypedData{
		Types:       eventTypes,
		PrimaryType: "RelayEvent",
		Domain: eip712.Domain{
			Name:    DomainName,
			Version: DomainVersion,
		},
		Message: map[string]any{
			"actor":     actor,
			"eventHash": "0x" + hex.EncodeToString(eip712.Keccak256(signedBytes)),
		},
	}
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
