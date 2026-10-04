// Receipt adapters: turning a signed receipt into a wire envelope, and back.
//
// These live in internal/publish rather than internal/protocol on purpose. A
// dependency is transitive, so anything that imported a package holding these would
// link internal/receipt, and therefore eip712 and secp256k1. A forwarding node must
// not (MVP.md §7.1), and a node only ever needs internal/protocol.
//
// The distinction the two directions embody:
//
//   - A SENDER may choose the bytes, so it may serialize a receipt.
//   - A FORWARDER must only carry the bytes, because a receipt's signature covers
//     an exact sequence and re-serializing it would break the signature while
//     leaving the receipt looking perfectly valid.
//
// Keeping the sender-only half here is what makes that enforceable.
package publish

import (
	"fmt"
	"strings"

	"github.com/relayfirst/relayfirst/internal/protocol"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// ReceiptEnvelope wraps a signed receipt as a relay message.
//
// The receipt is marshalled canonically so the bytes carried on the wire are the
// same bytes the standalone verifier expects.
func ReceiptEnvelope(r *receipt.Receipt) (protocol.Envelope, error) {
	if r == nil {
		return protocol.Envelope{}, fmt.Errorf("publish: nil receipt")
	}
	if strings.TrimSpace(r.ReceiptID) == "" {
		return protocol.Envelope{}, fmt.Errorf("publish: receipt has no id")
	}
	if strings.TrimSpace(r.AgentID) == "" {
		return protocol.Envelope{}, fmt.Errorf("publish: receipt has no agent id")
	}

	raw, err := r.MarshalCanonical()
	if err != nil {
		return protocol.Envelope{}, fmt.Errorf("publish: marshal receipt: %w", err)
	}

	return protocol.Envelope{
		ID:      r.ReceiptID,
		AgentID: r.AgentID,
		Kind:    protocol.KindReceipt,
		Payload: raw,
	}, nil
}

// DecodeReceipt extracts a receipt from a pulled envelope.
//
// It parses the stored bytes rather than trusting the envelope's indexing fields,
// so a node that mislabeled a message cannot make the client accept the wrong
// thing.
func DecodeReceipt(env protocol.Envelope) (*receipt.Receipt, error) {
	if env.Kind != protocol.KindReceipt {
		return nil, fmt.Errorf("publish: envelope kind %q is not %q", env.Kind, protocol.KindReceipt)
	}
	return receipt.Unmarshal(env.Payload)
}
