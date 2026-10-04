package a2a

import (
	"fmt"
	"strings"

	a2asdk "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/relayfirst/relayfirst/internal/agentid"
)

// This file holds the RelayFirst profile of the A2A data model.
//
// # The rule it follows
//
// Wire types are the SDK's. Where A2A already defines a structure — AgentCard,
// Message, Task, the task state machine — we use it rather than restating it, so
// a card we publish is readable by an unmodified A2A client and a card we receive
// cannot be subtly different from one the standard describes.
//
// What this file adds is the RelayFirst binding: the pieces A2A leaves to the
// implementation. There are two, and only two:
//
//   - Identity. A2A says an agent has a card; it does not say the card is tied to
//     an EVM account. RelayFirst's identity is `agent:eip155:<chainId>:<address>`
//     and MVP.md §17.2 keeps it ("identity is anchored in EVM accounts ... this
//     cannot be dropped"), so the binding has to be explicit.
//   - Endpoints. Which transports a RelayFirst agent exposes, and at which paths.
//
// # Why this package stays crypto-free
//
// Nothing here imports internal/eip712 or internal/receipt, and that is load
// bearing rather than tidy. A node serves cards, and MVP.md §7.1 requires a node
// to be unable to verify signatures — enforced by the import graph, not by
// review. If card *signing* lived here, the node would link secp256k1 the moment
// it served a card. Signing lives in internal/publish instead, the same split
// internal/protocol already uses for receipts.

// RelayIdentityExtensionURI identifies the RelayFirst identity extension.
//
// A2A has no first-class field for "this agent's EVM address", and adding one
// would mean forking the card schema — the thing A9 forbids. Extensions are the
// standard's own answer: a URI-keyed object an implementation can add, which
// other implementations ignore. A RelayFirst card therefore stays a valid A2A
// card, and a RelayFirst-aware reader can still find the identity.
const RelayIdentityExtensionURI = "https://relayfirst.dev/a2a/identity/v1"

// IdentityExtension is the parameter object carried under
// RelayIdentityExtensionURI.
//
// # Why the agentId appears here and also in the proof
//
// The duplication is deliberate. Here it makes the card self-describing: a reader
// can see who published it without fetching anything else. The proof carries it
// again so a verifier can check that the two agree — and that agreement IS the
// binding. If they could not disagree, there would be nothing to check.
type IdentityExtension struct {
	// AgentID is the canonical RelayFirst id, `agent:eip155:<chainId>:<address>`.
	AgentID string `json:"agentId"`
}

// Extension returns the A2A extension entry carrying id.
//
// Required is false on purpose: an A2A-only client that ignores this extension
// can still talk to the agent. Marking it required would make a standard client
// refuse a card it is perfectly able to use.
func (i IdentityExtension) Extension() a2asdk.AgentExtension {
	return a2asdk.AgentExtension{
		URI:         RelayIdentityExtensionURI,
		Description: "RelayFirst EVM identity (agent:eip155:<chainId>:<address>)",
		Required:    false,
		Params:      map[string]any{"agentId": i.AgentID},
	}
}

// AgentIDFromCard returns the RelayFirst identity a card declares.
//
// It returns ok=false when the extension is absent or malformed, which is not the
// same as a card that declares an identity we reject. A caller that treats the
// two as one will report "forged card" for a card that merely came from a
// different implementation.
func AgentIDFromCard(card *a2asdk.AgentCard) (string, bool) {
	if card == nil {
		return "", false
	}
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != RelayIdentityExtensionURI {
			continue
		}
		raw, ok := ext.Params["agentId"]
		if !ok {
			return "", false
		}
		s, ok := raw.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return "", false
		}
		return s, true
	}
	return "", false
}

// CardSpec is the RelayFirst input for building an A2A card.
//
// It is a spec rather than the card itself because a published card has fields
// the caller should not have to fill in correctly (the protocol version on every
// interface, the input/output modes) and fields a caller must not be able to
// contradict (the identity extension).
type CardSpec struct {
	// AgentID is the publisher's canonical RelayFirst id.
	AgentID string

	// Name and Description are human-readable.
	Name        string
	Description string

	// URL is the endpoint clients reach this agent at, e.g.
	// "https://node.example/a2a".
	URL string

	// Version is the agent's own version string, not the protocol version.
	Version string

	// Skills are the capabilities the agent advertises. A card with no skills
	// advertises an agent that can do nothing, so Build rejects it.
	Skills []a2asdk.AgentSkill
}

// Build assembles an A2A AgentCard from a RelayFirst spec.
//
// The protocol version stamped on each interface comes from SchemaVersion, so
// every interface a RelayFirst agent publishes agrees with the version this build
// actually negotiates. A caller cannot set it, and therefore cannot publish a
// card claiming a version the agent does not speak — which would be a
// self-inflicted version lie, and exactly the kind of drift A9 exists to prevent.
func Build(spec CardSpec) (*a2asdk.AgentCard, error) {
	if err := validateSpec(spec); err != nil {
		return nil, err
	}

	identity := IdentityExtension{AgentID: spec.AgentID}
	iface := a2asdk.NewAgentInterface(spec.URL, TransportJSONRPC)
	iface.ProtocolVersion = a2asdk.ProtocolVersion(SchemaVersion)

	return &a2asdk.AgentCard{
		Name:        spec.Name,
		Description: spec.Description,
		Version:     spec.Version,
		SupportedInterfaces: []*a2asdk.AgentInterface{
			iface,
		},
		Capabilities: a2asdk.AgentCapabilities{
			Extensions: []a2asdk.AgentExtension{identity.Extension()},
		},
		// The default modes describe what the agent accepts and returns when a
		// skill does not narrow it. RelayFirst tasks exchange JSON, so that is
		// the honest default rather than text.
		DefaultInputModes:  []string{"application/json"},
		DefaultOutputModes: []string{"application/json"},
		Skills:             spec.Skills,
	}, nil
}

func validateSpec(spec CardSpec) error {
	if strings.TrimSpace(spec.AgentID) == "" {
		return fmt.Errorf("a2a: card spec has no agentId")
	}
	if _, err := agentid.Parse(spec.AgentID); err != nil {
		return fmt.Errorf("a2a: %w", err)
	}
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("a2a: card spec has no name")
	}
	if strings.TrimSpace(spec.URL) == "" {
		return fmt.Errorf("a2a: card spec has no URL")
	}
	if len(spec.Skills) == 0 {
		return fmt.Errorf("a2a: card spec advertises no skills, so the agent can do nothing")
	}
	for i, s := range spec.Skills {
		if strings.TrimSpace(s.ID) == "" {
			return fmt.Errorf("a2a: skills[%d] has no id", i)
		}
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("a2a: skills[%d] has no name", i)
		}
	}
	return nil
}

// ValidateCard checks a received card before anything relies on it.
//
// # What this does and does not establish
//
// It checks shape: the card names itself, exposes at least one interface, and
// carries a well-formed RelayFirst identity. It does NOT establish that the
// publisher holds the key for that identity — a card can claim any agentId. That
// is the proof's job (internal/publish), and the two must not be confused: a
// caller that reads an agentId out of a validated card without checking a proof
// has authenticated nothing.
func ValidateCard(card *a2asdk.AgentCard) error {
	if card == nil {
		return fmt.Errorf("a2a: nil card")
	}
	if strings.TrimSpace(card.Name) == "" {
		return fmt.Errorf("a2a: card has no name")
	}
	if len(card.SupportedInterfaces) == 0 {
		return fmt.Errorf("a2a: card exposes no interfaces, so it is unreachable")
	}
	for i, iface := range card.SupportedInterfaces {
		if iface == nil {
			return fmt.Errorf("a2a: card interface[%d] is nil", i)
		}
		if strings.TrimSpace(iface.URL) == "" {
			return fmt.Errorf("a2a: card interface[%d] has no URL", i)
		}
		if strings.TrimSpace(string(iface.ProtocolVersion)) == "" {
			return fmt.Errorf("a2a: card interface[%d] declares no protocol version", i)
		}
		// A well-formed version is required here, but NOT membership in our
		// supported set: a card advertising a version we do not speak is a card
		// we cannot use, not a broken card. Rejecting it would make an old reader
		// call a newer agent invalid, which is the mistake A9 §② forbids.
		if _, err := ParseVersion(string(iface.ProtocolVersion)); err != nil {
			return fmt.Errorf("a2a: card interface[%d] protocol version is malformed: %w", i, err)
		}
	}

	agentID, ok := AgentIDFromCard(card)
	if !ok {
		return fmt.Errorf("a2a: card carries no %s identity extension", RelayIdentityExtensionURI)
	}
	if _, err := agentid.Parse(agentID); err != nil {
		return fmt.Errorf("a2a: card identity extension is malformed: %w", err)
	}
	return nil
}

// SelectInterface picks the interface to use for a client that supports the given
// protocol versions, newest first in preference.
//
// # Why selection is not "take the first"
//
// A card lists every endpoint an agent exposes, and the spec expects a client to
// choose based on what it supports. Taking the first entry would work until an
// agent lists its WebSocket binding first, at which point every HTTP-only client
// would fail against a card that told it exactly what to do.
//
// A binding we cannot speak is skipped rather than treated as an error, so an
// agent that adds a transport does not break clients that predate it.
func SelectInterface(card *a2asdk.AgentCard, supported []Version) (*a2asdk.AgentInterface, Version, error) {
	if card == nil {
		return nil, Version{}, fmt.Errorf("a2a: nil card")
	}
	if len(supported) == 0 {
		return nil, Version{}, fmt.Errorf("a2a: no supported versions given")
	}

	var (
		best    *a2asdk.AgentInterface
		bestVer Version
	)
	for _, iface := range card.SupportedInterfaces {
		if iface == nil {
			continue
		}
		// Only JSON-RPC for now. The WebSocket binding (S9-12) adds a second
		// value here; anything else is a transport this build cannot speak.
		if iface.ProtocolBinding != TransportJSONRPC {
			continue
		}
		v, err := ParseVersion(string(iface.ProtocolVersion))
		if err != nil {
			// A malformed version on one interface must not hide a usable one.
			continue
		}
		for _, s := range supported {
			if s.Compare(v) != 0 {
				continue
			}
			if best == nil || v.Compare(bestVer) > 0 {
				best, bestVer = iface, v
			}
			break
		}
	}
	if best == nil {
		return nil, Version{}, fmt.Errorf(
			"card exposes no interface for the versions this client supports (%s): %w",
			formatVersions(supported), ErrVersionNotSupported)
	}
	return best, bestVer, nil
}

func ptr[T any](v T) *T { return &v }
