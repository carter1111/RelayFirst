package a2a

import (
	"encoding/json"
	"testing"

	a2asdk "github.com/a2aproject/a2a-go/v2/a2a"
)

// These tests cover P1 #3, the relay-set extension.
//
// The properties that matter:
//
//   - the set travels in the CARD, so its validity comes from the agent's signature rather than
//     from any directory (§17.1's whole point);
//   - it is an EXTENSION, so the card stays a valid standard A2A card (A9: no schema fork);
//   - a reader can tell which relay to SEND to, since an inbox and a mirror mean different
//     things and a client that guessed would publish to a mirror;
//   - and an unknown role does not make an old reader call a newer agent broken.

func testRelaySet() RelaySetExtension {
	return RelaySetExtension{
		Endpoints: []RelayEndpoint{
			{URL: "wss://backup.example", Role: RelayRoleBackup, Priority: 20},
			{URL: "wss://inbox.example", Role: RelayRoleInbox, Priority: 1, Regions: []string{"ap-southeast-1"}},
		},
		UpdatedAt: 1791015800,
		ExpiresAt: 1793607800,
	}
}

// TestRelaySet_RoundTripsThroughTheCard is the basic path: publish and read back.
func TestRelaySet_RoundTripsThroughTheCard(t *testing.T) {
	spec := testSpec()
	set := testRelaySet()
	spec.RelaySet = &set

	card, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Serialize and re-read, because that is what a peer actually does.
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded a2asdk.AgentCard
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got, ok := RelaySetFromCard(&decoded)
	if !ok {
		t.Fatal("the relay set must survive a card round trip")
	}
	if len(got.Endpoints) != 2 {
		t.Fatalf("endpoints = %d, want 2", len(got.Endpoints))
	}
	if got.UpdatedAt != 1791015800 {
		t.Errorf("updatedAt = %d, want 1791015800", got.UpdatedAt)
	}
}

// TestRelaySet_IsAnExtensionNotASchemaFork is the A9 requirement.
//
// The card must remain a standard A2A card: no new top-level field, and the set inside an
// extension. A forked field would make an unmodified A2A client unable to read the card, which is
// the one thing the "use the official SDK" decision was for.
func TestRelaySet_IsAnExtensionNotASchemaFork(t *testing.T) {
	spec := testSpec()
	set := testRelaySet()
	spec.RelaySet = &set

	card, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// No forked top-level fields.
	for _, forked := range []string{"relaySet", "relayEndpoints", "relays"} {
		if _, present := doc[forked]; present {
			t.Errorf("%q must not be a top-level card field: it belongs in the extension so the "+
				"card stays a valid standard A2A card", forked)
		}
	}
	// And the standard fields must still be intact.
	for _, must := range []string{"name", "supportedInterfaces", "capabilities", "skills"} {
		if _, present := doc[must]; !present {
			t.Errorf("the standard field %q must still be present", must)
		}
	}
}

// TestPreferredRelays_InboxBeforeBackup is the ordering rule, and it is the part a caller must
// get right: publishing to a mirror means the agent never receives anything.
func TestPreferredRelays_InboxBeforeBackup(t *testing.T) {
	ordered := PreferredRelays(testRelaySet())
	if len(ordered) != 2 {
		t.Fatalf("endpoints = %d, want 2", len(ordered))
	}
	if ordered[0].Role != RelayRoleInbox {
		t.Fatalf("the inbox must come first, got %s: a client that published to a backup would "+
			"never reach the agent", ordered[0].Role)
	}
	if ordered[1].Role != RelayRoleBackup {
		t.Errorf("the backup must come second, got %s", ordered[1].Role)
	}
}

// TestPreferredRelays_LowerPriorityFirst pins the convention, since guessing the direction is
// easy and guessing wrong sends everything to the least preferred relay.
func TestPreferredRelays_LowerPriorityFirst(t *testing.T) {
	set := RelaySetExtension{
		Endpoints: []RelayEndpoint{
			{URL: "wss://a.example", Role: RelayRoleBackup, Priority: 20},
			{URL: "wss://b.example", Role: RelayRoleBackup, Priority: 1},
			{URL: "wss://c.example", Role: RelayRoleBackup, Priority: 10},
		},
	}
	ordered := PreferredRelays(set)
	want := []string{"wss://b.example", "wss://c.example", "wss://a.example"}
	for i, w := range want {
		if ordered[i].URL != w {
			t.Errorf("position %d = %s, want %s: lower priority numbers are preferred", i, ordered[i].URL, w)
		}
	}
}

// TestPreferredRelays_DoesNotMutateTheInput matters because a caller may hold the set inside a
// signed document and must not reorder what it will re-verify.
func TestPreferredRelays_DoesNotMutateTheInput(t *testing.T) {
	set := testRelaySet()
	originalFirst := set.Endpoints[0].URL

	_ = PreferredRelays(set)

	if set.Endpoints[0].URL != originalFirst {
		t.Error("PreferredRelays must not reorder the input: the caller may re-verify those bytes")
	}
}

// TestValidateRelaySet_RequiresExactlyOneInbox is the rule that keeps "where do I send"
// unambiguous.
func TestValidateRelaySet_RequiresExactlyOneInbox(t *testing.T) {
	valid := RelaySetExtension{Endpoints: []RelayEndpoint{
		{URL: "wss://a", Role: RelayRoleInbox, Priority: 1},
		{URL: "wss://b", Role: RelayRoleBackup, Priority: 2},
	}}
	if err := ValidateRelaySet(valid); err != nil {
		t.Fatalf("a valid set must pass: %v", err)
	}

	noInbox := RelaySetExtension{Endpoints: []RelayEndpoint{
		{URL: "wss://a", Role: RelayRoleBackup, Priority: 1},
	}}
	if err := ValidateRelaySet(noInbox); err == nil {
		t.Error("a set with no inbox must be rejected: a reader would not know where to send")
	}

	twoInboxes := RelaySetExtension{Endpoints: []RelayEndpoint{
		{URL: "wss://a", Role: RelayRoleInbox, Priority: 1},
		{URL: "wss://b", Role: RelayRoleInbox, Priority: 2},
	}}
	if err := ValidateRelaySet(twoInboxes); err == nil {
		t.Error("two inboxes must be rejected: picking one would be a silent coin flip")
	}

	empty := RelaySetExtension{}
	if err := ValidateRelaySet(empty); err == nil {
		t.Error("an empty set must be rejected")
	}
}

func TestValidateRelaySet_RequiresURLAndKnownRole(t *testing.T) {
	noURL := RelaySetExtension{Endpoints: []RelayEndpoint{{Role: RelayRoleInbox}}}
	if err := ValidateRelaySet(noURL); err == nil {
		t.Error("an endpoint with no url must be rejected")
	}

	badRole := RelaySetExtension{Endpoints: []RelayEndpoint{{URL: "wss://a", Role: "archiver"}}}
	if err := ValidateRelaySet(badRole); err == nil {
		t.Error("a producer emitting an unknown role has made a mistake and must be told")
	}
}

// TestBuild_RejectsAnInvalidRelaySet keeps a bad set from being published, where it would be
// signed and then permanent.
func TestBuild_RejectsAnInvalidRelaySet(t *testing.T) {
	spec := testSpec()
	bad := RelaySetExtension{Endpoints: []RelayEndpoint{{URL: "wss://a", Role: "archiver"}}}
	spec.RelaySet = &bad

	if _, err := Build(spec); err == nil {
		t.Fatal("Build must reject a relay set that would be invalid once signed")
	}
}

// TestBuild_RelaySetIsOptional keeps an A2A-only deployment working: a card with no relay set is
// still a complete card.
func TestBuild_RelaySetIsOptional(t *testing.T) {
	card, err := Build(testSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := RelaySetFromCard(card); ok {
		t.Error("a card with no relay set must not report one")
	}
	if err := ValidateCard(card); err != nil {
		t.Errorf("a card without a relay set must still be valid: %v", err)
	}
}

// TestRelaySet_UnknownRoleIsSkippedByAReaderButRejectedByvalidate is the asymmetry, and it is
// deliberate.
//
// A PRODUCER emitting a role nobody understands has made a mistake and must be told. A READER
// encountering one must skip it, because rejecting the card would make an old reader call a newer
// agent broken — the same mistake as demanding an exact schema match.
func TestRelaySet_UnknownRoleIsSkippedByAReaderButRejectedByValidate(t *testing.T) {
	// A producer-side check rejects it.
	if err := ValidateRelaySet(RelaySetExtension{Endpoints: []RelayEndpoint{
		{URL: "wss://a", Role: RelayRoleInbox},
		{URL: "wss://b", Role: "archiver"},
	}}); err == nil {
		t.Error("a producer must be told its role is unknown")
	}

	// A reader tolerates it and still finds the readable entries.
	raw, err := json.Marshal(map[string]any{
		"relayEndpoints": []map[string]any{
			{"url": "wss://inbox.example", "role": "inbox", "priority": 1},
			{"url": "wss://new.example", "role": "archiver", "priority": 5},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	card := &a2asdk.AgentCard{
		Capabilities: a2asdk.AgentCapabilities{
			Extensions: []a2asdk.AgentExtension{{
				URI: RelaySetExtensionURI,
			}},
		},
	}
	// Fill the params through JSON so the unknown role survives into the map the reader sees.
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	card.Capabilities.Extensions[0].Params = params

	set, ok := RelaySetFromCard(card)
	if !ok {
		t.Fatal("a reader must find the set even when it contains a role it does not know")
	}
	// It is read, and a reader that wants only what it understands can filter. What matters is
	// that the card was not rejected wholesale.
	if len(set.Endpoints) != 2 {
		t.Errorf("endpoints = %d, want 2: an unknown role must not discard the whole set", len(set.Endpoints))
	}
}

// TestRelaySet_SurvivesSDKDecoding is the interop check: an unmodified SDK client must still
// serve the card, and a RelayFirst reader must still find the set afterwards.
func TestRelaySet_SurvivesSDKDecoding(t *testing.T) {
	spec := testSpec()
	set := testRelaySet()
	spec.RelaySet = &set

	card, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var sdkCard a2asdk.AgentCard
	if err := json.Unmarshal(raw, &sdkCard); err != nil {
		t.Fatalf("an unmodified SDK client must decode the card: %v", err)
	}

	got, ok := RelaySetFromCard(&sdkCard)
	if !ok {
		t.Fatal("the relay set must be readable after SDK decoding")
	}
	ordered := PreferredRelays(got)
	if len(ordered) == 0 || ordered[0].Role != RelayRoleInbox {
		t.Error("the ordering must survive the round trip too")
	}
}

// TestRelaySet_RegionIsAHintNotAFilter shows a client can ignore regions without losing anything.
func TestRelaySet_RegionIsAHintNotAFilter(t *testing.T) {
	ordered := PreferredRelays(testRelaySet())
	for _, e := range ordered {
		// No filtering happens here, so every endpoint is present regardless of regions.
		if e.URL == "" {
			t.Error("an endpoint with no regions must still be returned")
		}
	}
	if len(ordered) != 2 {
		t.Errorf("region hints must not filter the set, got %d endpoints", len(ordered))
	}
}
