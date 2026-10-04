// Package a2a adapts the official Agent2Agent primitives to RelayFirst.
//
// # Why this exists instead of a hand-rolled negotiation
//
// The versioning plan (docs/notes/upgrade-architecture-plan.md §2.6) decided not
// to invent any negotiation primitive: the A2A SDK already defines the service
// parameter name, the error sentinel and the interface descriptor, and a second
// implementation of "what does A2A-Version mean" is a second thing to keep in
// sync — against the standard, which is the one party that will not bend.
//
// So this package re-exports the SDK's primitives under names RelayFirst code
// uses, and adds only the one piece the SDK does not ship: choosing a version
// two sides can agree on. The A2A-Version key, the ErrVersionNotSupported
// sentinel and the AgentInterface shape are the SDK's, not copies.
//
// # The distinction this package keeps
//
// A malformed version string and a version we do not support are different
// answers, and callers must not merge them:
//
//   - malformed  → the client sent something that is not a version at all
//   - unsupported → the client sent a well-formed version, and we share none
//
// This mirrors the receipt layer's UnsupportedError/ValidationError split
// (internal/receipt/schema.go). The reason is the same: collapsing the two makes
// a peer that is merely newer look like a peer that is broken.
package a2a

import (
	a2asdk "github.com/a2aproject/a2a-go/v2/a2a"
)

// HeaderVersion is the service parameter carrying the peer's A2A protocol
// version, e.g. "1.0".
//
// Clients MUST send it on every request so an agent can keep working after it
// upgrades. It is the SDK's constant, not a copy, so it cannot drift from the
// standard's spelling.
const HeaderVersion = a2asdk.SvcParamVersion

// HeaderExtensions is the service parameter listing the extension URIs a client
// wants active. Declared here so callers have one place to look; RelayFirst does
// not require any extension yet.
const HeaderExtensions = a2asdk.SvcParamExtensions

// ErrVersionNotSupported reports that the caller's version is well-formed but
// shares nothing with ours.
//
// It is the SDK's sentinel, aliased rather than redefined, so `errors.Is` works
// against either name and the transport bindings keep mapping it to their own
// VERSION_NOT_SUPPORTED error codes.
var ErrVersionNotSupported = a2asdk.ErrVersionNotSupported

// AgentInterface is one transport endpoint an agent exposes.
//
// It is a type alias, not a new struct: a RelayFirst agent card must be readable
// by an unmodified A2A client, and a parallel struct would eventually disagree
// about a JSON key or an optional field. The alias makes that impossible.
type AgentInterface = a2asdk.AgentInterface

// TransportProtocol is the binding identifier ("JSONRPC", "GRPC", ...).
type TransportProtocol = a2asdk.TransportProtocol

// Transport bindings we intend to expose. HTTP is the MVP binding (MVP.md §7.3);
// the WebSocket binding is added in S9-12.
const (
	TransportJSONRPC = a2asdk.TransportProtocolJSONRPC
	TransportGRPC    = a2asdk.TransportProtocolGRPC
)

// TransportWebSocket is the WebSocket binding identifier (S9-12).
//
// A2A's TransportProtocol is an open string, and the spec says a custom binding SHOULD
// be identified by a URI. RelayFirst does not need a URI here because "WEBSOCKET" is not
// a custom binding — it is the transport WebSocket, reached over the standard upgrade —
// and the standard value is clearer to a human reading a card than a URL would be.
const TransportWebSocket TransportProtocol = "WEBSOCKET"

// WellKnownAgentCardPath is where an A2A agent publishes its card (RFC 8615).
//
// RelayFirst already serves /.well-known/relayfirst for its own node document
// (RFN-04); the agent card is a separate, standard document and does not replace
// it.
const WellKnownAgentCardPath = "/.well-known/agent-card.json"
