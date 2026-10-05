// Package protocol defines the on-the-wire format shared by relay nodes and their
// clients (MVP.md §7.1).
//
// # Why the format is separate from the node, and from token construction
//
// A node must not be able to verify signatures. That is not a stylistic
// preference: MVP.md §7.1 specifies a "dumb" store-and-forward node, and §7.3
// explains why — verification happens on the client (§5.4), which is exactly what
// stops a hostile node from being able to forge work.
//
// Making that a property of the import graph takes two separations, not one:
//
//   - The format lives here, so internal/node does not import internal/receipt.
//   - This package itself does not import internal/receipt either, because a
//     dependency is transitive. The helpers that turn a receipt into an envelope
//     need the receipt package, so they live in internal/publish, which only a
//     sender uses.
//
// The result is checkable rather than merely intended: the address token types
// below are all that a forwarding node needs, and `go list -deps` on the node
// binary shows no signing code at all.
//
// The direction is deliberate. Envelope is a *shared* type that both a forwarder
// and a sender need. Building one from a receipt is a sender-only concern.
package protocol

import "strings"

// Envelope is the wire format for a store-and-forward message.
//
// A node reads only ID, AgentID and Kind. Payload is opaque bytes, carried through
// untouched.
type Envelope struct {
	// ID is unique per message. For a receipt it is the receipt id. It is the
	// idempotency key, so a retry is harmless (S5-3).
	//
	// Named `id` on the wire rather than `receiptId` so the same format can carry
	// non-receipt messages — the node is payload-agnostic by design.
	ID string `json:"id"`

	// AgentID is the addressee.
	AgentID string `json:"agentId"`

	// Kind names the payload type, e.g. "receipt".
	Kind string `json:"kind"`

	// Payload is the opaque body, base64 in JSON. encoding/json handles the
	// conversion for []byte.
	Payload []byte `json:"payload"`

	// Sequence and PreviousEventHash reserve the positions a message chain will
	// occupy (S9-0e, invariant A9 §⑥).
	//
	// # Why reserve them before anything uses them
	//
	// These are message-layer fields, not receipt fields: they order *deliveries*
	// relative to an agent, whereas a receipt's own ordering is inside its signed
	// payload. Keeping them out here means adding a chain later does not touch the
	// signed bytes of any receipt, which is the whole reason A9 can promise old
	// receipts stay verifiable.
	//
	// Reserving now rather than when the chain ships is the cheap half of that
	// promise. The JSON keys, their types and their zero-value semantics are
	// decided here, in one place, so a node or client written against this struct
	// does not have to be revisited to introduce the chain — it stays additive.
	//
	// # Both are zero for now, and that is the contract
	//
	// `omitempty` is load-bearing, not cosmetic. With it, an envelope that does not
	// participate in a chain serializes to exactly the bytes it did before these
	// fields existed, so no stored or in-flight message changes shape. A chain, when
	// it arrives, is opted into by setting them; nothing is reinterpreted.
	//
	// A forwarder must never require these. A node is payload-agnostic (the header
	// of this file), and a message with no chain is a complete, valid message — not
	// a truncated one.
	Sequence uint64 `json:"sequence,omitempty"`

	// PreviousEventHash links this message to the one before it in the same
	// agent's stream, forming an append-only chain a reader can check for gaps.
	//
	// Empty means "no predecessor claimed", which is the only honest value for a
	// first message. It is a hash of the predecessor envelope's canonical bytes,
	// not of a receipt payload: the chain orders deliveries, and a delivery is the
	// envelope.
	//
	// It is not signed here, and it does not need to be for the chain to be useful:
	// the value a reader cares about is that consecutive messages agree, which is
	// checkable from the bytes the node stored. What it deliberately does not
	// provide is proof against a node that drops messages — a chain can only show
	// that what is present is consistent, never that nothing was withheld.
	PreviousEventHash string `json:"previousEventHash,omitempty"`
}

// KindReceipt is the envelope kind used for a mined receipt.
const KindReceipt = "receipt"

// KindEvent is the envelope kind used for a signed protocol event (S9-6).
//
// # Why an event travels as an envelope and not as a new message type
//
// An event is a signed, opaque body with an id and an addressee — exactly what an
// envelope carries. Giving it its own transport would mean a second store, a
// second dedup rule and a second pull path, all of which already exist and are
// already tested. The node needs no new capability: it indexes by id and agent and
// carries the bytes.
//
// # What the node still must not do
//
// Nothing here lets a node interpret an event. It does not parse the payload, does
// not check the chain and does not verify the signature — the same rule as receipts
// (MVP.md §7.1). An event's chain is validated by whoever holds the events, with a
// hasher the node does not have.
const KindEvent = "event"

// KindGrant is the envelope kind used for a signed session delegation grant (S13-3d).
//
// # Why a grant travels as its own envelope kind
//
// A grant is an owner's signed authorization for a session key, and it is checked
// alongside the events that key signs. It is not itself an event — it has no
// sequence and no chain — so giving it the `event` kind would invite a reader to
// feed it to the event parser, where it would fail in a confusing way.
//
// It is carried through a node exactly like an event or a receipt, and for the same
// reason: one store, one dedup rule and one pull path already exist and are tested.
// The node needs no new capability, and it still does not parse or verify the
// payload (MVP.md §7.1). A node that cannot verify a grant cannot be tricked by one.
const KindGrant = "grant"

// LooksLikeEventID reports whether id has the shape of an event id.
//
// Event ids are opaque strings chosen by the actor, unlike receipt ids which are
// derived hashes. So this is a weaker check than LooksLikeReceiptID: it only
// rejects an empty or absurdly long id, which is enough to keep the index sane
// without constraining a newer actor's id scheme.
func LooksLikeEventID(id string) bool {
	if id == "" {
		return false
	}
	// The bound is generous and exists only so a hostile sender cannot use an id
	// as an unbounded storage key.
	const maxEventIDLength = 256
	return len(id) <= maxEventIDLength
}

// MaxPayloadBytes caps a single accepted message.
//
// A node is a public endpoint, so an uncapped body is an unbounded memory and
// disk commitment offered to anyone who can reach the port. 1 MiB is far above a
// real receipt (a probe receipt is a few KB) and far below anything that would
// trouble the host.
const MaxPayloadBytes = 1 << 20

// LooksLikeReceiptID reports whether id has the shape of a receipt id
// (0x-prefixed, 32 bytes of hex).
//
// It is a shape check, not validation: it exists so a caller gets an early, clear
// error about a malformed id instead of discovering it at verification time. It
// deliberately does not establish that the id corresponds to any real receipt, and
// it needs no crypto to answer — which is why a node can perform it.
func LooksLikeReceiptID(id string) bool {
	s := strings.TrimPrefix(id, "0x")
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
