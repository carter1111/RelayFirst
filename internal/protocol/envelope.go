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
}

// KindReceipt is the envelope kind used for a mined receipt.
const KindReceipt = "receipt"

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
