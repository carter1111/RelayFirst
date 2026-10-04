package a2a

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	a2asdk "github.com/a2aproject/a2a-go/v2/a2a"
)

const testAgentID = "agent:eip155:8453:0x7f4db0d9c4b8a6bce8e1c22da9c419e6e1f3a8b5"

func testSpec() CardSpec {
	return CardSpec{
		AgentID:     testAgentID,
		Name:        "relayfirst-node",
		Description: "A RelayFirst relay node",
		URL:         "https://node.example/a2a",
		Version:     "1.0.0",
		Skills: []a2asdk.AgentSkill{
			{ID: "relay", Name: "Relay messages", Description: "Store and forward"},
		},
	}
}

func TestBuild_CarriesIdentityAndVersion(t *testing.T) {
	card, err := Build(testSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	gotID, ok := AgentIDFromCard(card)
	if !ok {
		t.Fatal("a built card must carry the RelayFirst identity extension")
	}
	if gotID != testAgentID {
		t.Errorf("card identity = %q, want %q", gotID, testAgentID)
	}

	// Every interface must advertise the version this build actually negotiates.
	// A caller cannot set it, so a card cannot claim a version the agent does not
	// speak.
	for i, iface := range card.SupportedInterfaces {
		if string(iface.ProtocolVersion) != SchemaVersion {
			t.Errorf("interface[%d] protocol version = %q, want %q (the negotiated version)",
				i, iface.ProtocolVersion, SchemaVersion)
		}
		if iface.ProtocolBinding != TransportJSONRPC {
			t.Errorf("interface[%d] binding = %q, want %q", i, iface.ProtocolBinding, TransportJSONRPC)
		}
	}
}

// TestBuild_RejectsIncompleteSpec confirms the builder refuses to produce a card
// that would be useless or invalid. A card with no skills advertises an agent
// that can do nothing; one with no URL is unreachable.
func TestBuild_RejectsIncompleteSpec(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CardSpec)
	}{
		{"no agentId", func(s *CardSpec) { s.AgentID = "" }},
		{"malformed agentId", func(s *CardSpec) { s.AgentID = "agent:eip155::0x00" }},
		{"no name", func(s *CardSpec) { s.Name = "" }},
		{"no url", func(s *CardSpec) { s.URL = "" }},
		{"no skills", func(s *CardSpec) { s.Skills = nil }},
		{"skill without id", func(s *CardSpec) { s.Skills = []a2asdk.AgentSkill{{Name: "x"}} }},
		{"skill without name", func(s *CardSpec) { s.Skills = []a2asdk.AgentSkill{{ID: "x"}} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := testSpec()
			c.mutate(&spec)
			if _, err := Build(spec); err == nil {
				t.Errorf("Build must reject a spec with %s", c.name)
			}
		})
	}
}

func TestValidateCard_AcceptsBuiltCard(t *testing.T) {
	card, err := Build(testSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := ValidateCard(card); err != nil {
		t.Errorf("a card this package built must validate: %v", err)
	}
}

func TestValidateCard_RejectsMissingIdentity(t *testing.T) {
	card, err := Build(testSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	card.Capabilities.Extensions = nil

	if err := ValidateCard(card); err == nil {
		t.Fatal("a card with no RelayFirst identity extension must not validate")
	}
}

// TestValidateCard_DoesNotRequireOurVersion is the A9 §② boundary.
//
// A card advertising a version we do not speak is a card we cannot use, not a
// broken card. Rejecting it would let an old reader call a newer agent invalid,
// which is the fork A9 forbids. So validation checks the version is well formed,
// and negotiation decides whether we can talk.
func TestValidateCard_DoesNotRequireOurVersion(t *testing.T) {
	card, err := Build(testSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	card.SupportedInterfaces[0].ProtocolVersion = "99.0"

	if err := ValidateCard(card); err != nil {
		t.Errorf("a well-formed future version must pass shape validation, got: %v", err)
	}

	// It must then fail at selection, with the version answer rather than a
	// validity answer.
	n, err := NewNegotiator(SchemaVersion)
	if err != nil {
		t.Fatalf("negotiator: %v", err)
	}
	_, _, err = SelectInterface(card, n.Supported())
	if !errors.Is(err, ErrVersionNotSupported) {
		t.Errorf("selecting against a future version must be ErrVersionNotSupported, got: %v", err)
	}
}

func TestValidateCard_RejectsMalformedVersion(t *testing.T) {
	card, err := Build(testSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	card.SupportedInterfaces[0].ProtocolVersion = "not-a-version"

	if err := ValidateCard(card); err == nil {
		t.Fatal("a malformed protocol version is a defect in the card and must be rejected")
	}
}

func TestValidateCard_RejectsEmptyInterfaceList(t *testing.T) {
	card, err := Build(testSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	card.SupportedInterfaces = nil

	if err := ValidateCard(card); err == nil {
		t.Fatal("a card with no interfaces is unreachable and must not validate")
	}
}

// TestBuild_DeclaresBothBindings is the S9-12 requirement: the card advertises the
// WebSocket interface alongside the JSON-RPC one (MVP.md §12.1).
func TestBuild_DeclaresBothBindings(t *testing.T) {
	spec := testSpec()
	spec.WebSocketURL = "wss://node.example/ws/messages/agent"

	card, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(card.SupportedInterfaces) != 2 {
		t.Fatalf("a card with a WebSocket URL must declare two interfaces, got %d",
			len(card.SupportedInterfaces))
	}
	// HTTP first: the documented client preference is to take the earlier entry it can
	// speak, and HTTP is the binding every existing client uses.
	if card.SupportedInterfaces[0].ProtocolBinding != TransportJSONRPC {
		t.Errorf("the JSON-RPC binding must be listed first, got %q",
			card.SupportedInterfaces[0].ProtocolBinding)
	}
	if card.SupportedInterfaces[1].ProtocolBinding != TransportWebSocket {
		t.Errorf("the second binding must be WebSocket, got %q",
			card.SupportedInterfaces[1].ProtocolBinding)
	}
	// Both must declare the negotiated version, or a client could not select either.
	for i, iface := range card.SupportedInterfaces {
		if string(iface.ProtocolVersion) != SchemaVersion {
			t.Errorf("interface[%d] version = %q, want %q", i, iface.ProtocolVersion, SchemaVersion)
		}
	}
}

// TestBuild_OmitsWebSocketWhenUnset is the honesty rule: a card must not advertise a
// binding the agent does not serve. A client that trusted it would connect to a URL that
// was never there.
func TestBuild_OmitsWebSocketWhenUnset(t *testing.T) {
	card, err := Build(testSpec()) // no WebSocketURL
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(card.SupportedInterfaces) != 1 {
		t.Fatalf("a card with no WebSocket URL must declare one interface, got %d",
			len(card.SupportedInterfaces))
	}
	for _, iface := range card.SupportedInterfaces {
		if iface.ProtocolBinding == TransportWebSocket {
			t.Error("a card must not advertise a WebSocket binding the agent does not serve")
		}
	}
}

// TestSelectInterface_SkipsWebSocketForJSONRPCClients is the practical consequence of the
// multi-binding card: a client that speaks only JSON-RPC must still find its endpoint.
func TestSelectInterface_SkipsWebSocketForJSONRPCClients(t *testing.T) {
	spec := testSpec()
	spec.WebSocketURL = "wss://node.example/ws/messages/agent"
	card, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	n, err := NewNegotiator(SchemaVersion)
	if err != nil {
		t.Fatalf("negotiator: %v", err)
	}
	iface, _, err := SelectInterface(card, n.Supported())
	if err != nil {
		t.Fatalf("SelectInterface: %v", err)
	}
	if iface.ProtocolBinding != TransportJSONRPC {
		t.Errorf("selection must land on the JSON-RPC binding this client can speak, got %q",
			iface.ProtocolBinding)
	}
}

// TestValidateCard_AcceptsBothBindings keeps the two-interface card valid.
func TestValidateCard_AcceptsBothBindings(t *testing.T) {
	spec := testSpec()
	spec.WebSocketURL = "wss://node.example/ws/messages/agent"
	card, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := ValidateCard(card); err != nil {
		t.Errorf("a two-binding card must validate: %v", err)
	}
}

// TestCard_SerializesAsStandardA2A is the compatibility requirement (criterion ⑧).
//
// The card must be an ordinary A2A card: standard field names, and the RelayFirst
// identity carried in an extension rather than a forked field. If this drifts, an
// unmodified A2A client stops being able to read our cards, which is the one thing
// the "use the official SDK" decision was for.
func TestCard_SerializesAsStandardA2A(t *testing.T) {
	card, err := Build(testSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Standard A2A field names must be present as-is.
	for _, key := range []string{
		`"supportedInterfaces"`, `"capabilities"`, `"defaultInputModes"`,
		`"defaultOutputModes"`, `"name"`, `"description"`, `"version"`, `"skills"`,
		`"protocolBinding"`, `"protocolVersion"`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("card JSON is missing the standard key %s; wire was: %s", key, raw)
		}
	}

	// The RelayFirst identity must live inside the extension, not as a top-level
	// field that would fork the schema.
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, forked := decoded["agentId"]; forked {
		t.Error("the agentId must not be a top-level card field; it belongs in the extension " +
			"so the card stays a valid standard A2A card")
	}

	// And it must round-trip through the extension.
	var back a2asdk.AgentCard
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal into card: %v", err)
	}
	gotID, ok := AgentIDFromCard(&back)
	if !ok || gotID != testAgentID {
		t.Errorf("identity did not survive the round trip: got %q ok=%v", gotID, ok)
	}
}

// TestSelectInterface_PicksMatchingBinding covers the multi-binding case that
// arrives with the WebSocket work: a card may list an endpoint this client cannot
// speak, and selection must skip it rather than fail.
func TestSelectInterface_PicksMatchingBinding(t *testing.T) {
	card, err := Build(testSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Prepend a binding this client does not speak, as a future card might.
	ws := a2asdk.NewAgentInterface("wss://node.example/a2a", TransportProtocol("WEBSOCKET"))
	ws.ProtocolVersion = a2asdk.ProtocolVersion(SchemaVersion)
	card.SupportedInterfaces = append([]*a2asdk.AgentInterface{ws}, card.SupportedInterfaces...)

	n, err := NewNegotiator(SchemaVersion)
	if err != nil {
		t.Fatalf("negotiator: %v", err)
	}
	iface, ver, err := SelectInterface(card, n.Supported())
	if err != nil {
		t.Fatalf("a card with one unusable binding must still yield the usable one: %v", err)
	}
	if iface.ProtocolBinding != TransportJSONRPC {
		t.Errorf("selected binding %q, want %q", iface.ProtocolBinding, TransportJSONRPC)
	}
	if ver.Compare(Version{1, 0}) != 0 {
		t.Errorf("selected version %s, want 1.0", ver)
	}
}

func TestSelectInterface_NoUsableInterfaceIsVersionNotSupported(t *testing.T) {
	card, err := Build(testSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	card.SupportedInterfaces[0].ProtocolVersion = "99.0"

	n, err := NewNegotiator(SchemaVersion)
	if err != nil {
		t.Fatalf("negotiator: %v", err)
	}
	_, _, err = SelectInterface(card, n.Supported())
	if !errors.Is(err, ErrVersionNotSupported) {
		t.Errorf("no usable interface is a version answer, got: %v", err)
	}
}
