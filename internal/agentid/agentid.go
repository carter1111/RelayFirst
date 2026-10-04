// Package agentid parses and formats the canonical RelayFirst agent identifier.
//
// # Why this is its own package
//
// The identifier appears on both sides of a boundary that must not be crossed:
// the receipt layer (which may do cryptography) and the A2A layer (which must
// not, so that a node serving agent cards never links signing code — MVP.md §7.1).
// Neither may import the other.
//
// Before this package, that left two options, both bad: duplicate the parser, or
// let one side reach across. Duplication is the worse one, and not
// hypothetically: finding L1 was a bug in exactly this parsing, where the chainId
// component was never checked. Two copies of a parser that subtle means two
// chances to reintroduce it, and a difference between them would show up as a
// consumer routing on an identity that validation approved but that means
// something else.
//
// So the syntax lives here, with no dependencies at all. Callers that need an
// address as bytes convert it themselves, and the shape rules are stated once.
//
// # What "parse" means here
//
// This is syntax only. It does not establish that anyone controls the address —
// that is what a signature proves, and it is a different question answered in a
// different place. A parser that returned "valid identity" would be claiming an
// authentication it never performed.
package agentid

import (
	"fmt"
	"strconv"
	"strings"
)

// Prefix is the fixed scheme before the chain id.
const Prefix = "agent:eip155:"

// ID is a parsed identifier.
type ID struct {
	// ChainID is the EVM chain the identity is anchored to.
	ChainID uint64

	// Address is the lowercase 0x-prefixed 20-byte address.
	Address string
}

// String renders the canonical form. The round trip through Parse and String is
// the identity function, which is what lets callers compare identifiers as
// strings without a normalization step that could disagree with the parser.
func (id ID) String() string {
	return Prefix + strconv.FormatUint(id.ChainID, 10) + ":" + id.Address
}

// Parse validates an identifier and returns its components.
//
// # The checks, and why each is not optional
//
//   - The prefix must match exactly. A near-miss scheme is a different scheme.
//   - The chain id must be present and a decimal uint64. This is the L1 fix:
//     an empty, negative, hex or overflowing chain id used to be accepted
//     because the field was skipped entirely. A signature does not help — the
//     chain id sits inside the signed string, so it is authenticated as *that
//     string*, never as a chain id. Binding is not validation.
//   - The address must be 0x followed by exactly 40 hex digits, and is
//     normalized to lowercase so two spellings of one address cannot look like
//     two agents.
//
// Rejecting rather than repairing is deliberate. A consumer that routes by chain
// must be able to trust that a parsed identifier means what it says; silently
// fixing a malformed one would move the error to wherever the value is used.
func Parse(s string) (ID, error) {
	rest, ok := strings.CutPrefix(s, Prefix)
	if !ok {
		return ID{}, fmt.Errorf("agent id %q must start with %q", s, Prefix)
	}

	chainStr, addrStr, found := strings.Cut(rest, ":")
	if !found {
		return ID{}, fmt.Errorf("agent id %q must have form %s<chainId>:<address>", s, Prefix)
	}

	if chainStr == "" {
		return ID{}, fmt.Errorf("agent id %q has an empty chainId", s)
	}
	chainID, err := strconv.ParseUint(chainStr, 10, 64)
	if err != nil {
		return ID{}, fmt.Errorf("agent id chainId %q is not a decimal uint64: %w", chainStr, err)
	}

	addr, err := normalizeAddress(s, addrStr)
	if err != nil {
		return ID{}, err
	}

	return ID{ChainID: chainID, Address: addr}, nil
}

// AddressHexDigits is the number of hex digits in an EVM address (20 bytes).
const AddressHexDigits = 40

// normalizeAddress lowercases and shape-checks an address.
//
// The shape check is here rather than left to the caller because it is part of
// what the identifier means: `agent:eip155:1:not-an-address` is not a slightly
// wrong identifier, it is not an identifier at all.
func normalizeAddress(whole, addr string) (string, error) {
	if addr == "" {
		return "", fmt.Errorf("agent id %q has an empty address", whole)
	}
	hexPart, ok := strings.CutPrefix(addr, "0x")
	if !ok {
		return "", fmt.Errorf("agent id address %q must be 0x-prefixed", addr)
	}
	if len(hexPart) != AddressHexDigits {
		return "", fmt.Errorf("agent id address %q must be 0x followed by %d hex digits, got %d",
			addr, AddressHexDigits, len(hexPart))
	}
	for i := 0; i < len(hexPart); i++ {
		c := hexPart[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return "", fmt.Errorf("agent id address %q contains a non-hex character %q", addr, string(c))
		}
	}
	return "0x" + strings.ToLower(hexPart), nil
}

// Format builds the canonical identifier from components.
//
// It validates the address so a caller cannot construct an identifier that Parse
// would reject. An identifier that cannot survive a round trip is a latent bug in
// whatever stores it.
func Format(chainID uint64, address string) (string, error) {
	addr, err := normalizeAddress("agent id", address)
	if err != nil {
		return "", err
	}
	return ID{ChainID: chainID, Address: addr}.String(), nil
}
