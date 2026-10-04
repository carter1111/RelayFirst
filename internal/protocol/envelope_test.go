package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEnvelope_ZeroChainFieldsAreAbsentFromTheWire is the A9 property that makes
// reserving the chain fields safe (S9-0e).
//
// The whole point of reserving them with `omitempty` is that a message that does
// not join a chain must serialize to the bytes it did before the fields existed.
// If they appeared as `"sequence":0,"previousEventHash":""`, every stored and
// in-flight envelope would change shape the moment this struct changed, which is
// exactly the "adding a field forks the readers" failure A9 exists to prevent.
//
// This asserts on the raw bytes, not on the round-tripped struct: a round-trip
// would pass even if the keys were present, because decoding them back gives the
// same zero values.
func TestEnvelope_ZeroChainFieldsAreAbsentFromTheWire(t *testing.T) {
	env := Envelope{
		ID:      "0x" + strings.Repeat("ab", 32),
		AgentID: "agent:eip155:8453:0x0000000000000000000000000000000000000001",
		Kind:    KindReceipt,
		Payload: []byte("body"),
	}

	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// These are the keys a pre-S9-0e reader knows. Their presence is required;
	// the new keys' absence is what keeps the wire compatible.
	for _, must := range []string{`"id"`, `"agentId"`, `"kind"`, `"payload"`} {
		if !strings.Contains(string(raw), must) {
			t.Errorf("existing key %s must still be emitted, wire was: %s", must, raw)
		}
	}
	for _, mustNot := range []string{`"sequence"`, `"previousEventHash"`} {
		if strings.Contains(string(raw), mustNot) {
			t.Errorf("unset chain field %s must not appear on the wire, or every "+
				"existing envelope changes shape; wire was: %s", mustNot, raw)
		}
	}
}

// TestEnvelope_ChainFieldsRoundTrip confirms the reserved positions actually
// carry data when a message opts into a chain. Reserving a field that silently
// drops its value would be worse than not reserving it.
func TestEnvelope_ChainFieldsRoundTrip(t *testing.T) {
	env := Envelope{
		ID:                "0x" + strings.Repeat("cd", 32),
		AgentID:           "agent:eip155:8453:0x0000000000000000000000000000000000000002",
		Kind:              KindReceipt,
		Payload:           []byte("body"),
		Sequence:          7,
		PreviousEventHash: "0x" + strings.Repeat("ef", 32),
	}

	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Envelope
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Sequence != env.Sequence {
		t.Errorf("sequence round-trip: got %d, want %d", back.Sequence, env.Sequence)
	}
	if back.PreviousEventHash != env.PreviousEventHash {
		t.Errorf("previousEventHash round-trip: got %q, want %q",
			back.PreviousEventHash, env.PreviousEventHash)
	}
}

// TestEnvelope_AcceptsUnknownFields pins the forward-compatibility half.
//
// A reader on an older build will receive envelopes from a newer one. If
// decoding rejected keys it did not know, the first new field would make old
// nodes fail on valid traffic — the other fork A9 forbids. This is the same
// tolerant-decode rule the receipt layer already follows (S9-0b).
func TestEnvelope_AcceptsUnknownFields(t *testing.T) {
	raw := []byte(`{"id":"x","agentId":"a","kind":"receipt","payload":"AA==",` +
		`"sequence":3,"futureField":{"nested":true}}`)

	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("an envelope with an unknown field must decode, not fail: %v", err)
	}
	if env.Sequence != 3 {
		t.Errorf("known field after an unknown one must still parse: got sequence %d, want 3", env.Sequence)
	}
}
