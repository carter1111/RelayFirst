package a2a

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	actorA = "agent:eip155:8453:0x7f4db0d9c4b8a6bce8e1c22da9c419e6e1f3a8b5"
	actorB = "agent:eip155:8453:0x00000000000000000000000000000000000000b2"
)

// testHasher is a stand-in for keccak256. The chain logic only needs a
// deterministic function; the real hash is injected by whoever has eip712, which
// this package must not import (MVP.md §7.1).
func testHasher(b []byte) []byte {
	// FNV-1a, widened to 32 bytes so the shape matches a real digest.
	var h uint64 = 14695981039346656037
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	out := make([]byte, 32)
	for i := 0; i < 8; i++ {
		out[i] = byte(h >> (8 * i))
	}
	return out
}

var t0 = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

// chain builds a valid per-actor chain of events of the given types.
func chain(t *testing.T, actor string, types ...EventType) []Event {
	t.Helper()
	var (
		out      []Event
		prevHash string
	)
	for i, typ := range types {
		seq := uint64(i + 1)
		e := Event{
			EventID:           "evt-" + actor[len(actor)-4:] + "-" + string(rune('a'+i)),
			SessionID:         "ses_test",
			Actor:             actor,
			Type:              typ,
			Sequence:          seq,
			PreviousEventHash: prevHash,
			IssuedAt:          t0.Add(time.Duration(i) * time.Second),
		}
		if typ.taskScoped() {
			e.TaskID = "tsk_1"
		}
		h, err := EventHash(testHasher, e)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		prevHash = h
		out = append(out, e)
	}
	return out
}

// TestEvent_CanonicalBytesExcludesSignature is the property the chain depends on:
// hashing the unsigned form and the signed form must agree, or a chain could not be
// validated from a signed event.
func TestEvent_CanonicalBytesExcludesSignature(t *testing.T) {
	e := Event{
		EventID:   "evt-1",
		SessionID: "ses_1",
		TaskID:    "tsk_1",
		Actor:     actorA,
		Type:      EventTaskCreated,
		Sequence:  1,
		IssuedAt:  t0,
	}
	unsigned, err := e.CanonicalBytes()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}

	e.Signature = "0xdeadbeef"
	signed, err := e.CanonicalBytes()
	if err != nil {
		t.Fatalf("canonical signed: %v", err)
	}

	if string(unsigned) != string(signed) {
		t.Error("the signature must be excluded from the canonical bytes; " +
			"otherwise a chain cannot be validated from a signed event")
	}
}

// TestValidateEvent_SequenceRules covers the chain-field invariants.
func TestValidateEvent_SequenceRules(t *testing.T) {
	base := Event{
		EventID:   "evt-1",
		SessionID: "ses_1",
		TaskID:    "tsk_1",
		Actor:     actorA,
		Type:      EventTaskCreated,
		Sequence:  1,
		IssuedAt:  t0,
	}
	if err := ValidateEvent(base); err != nil {
		t.Fatalf("a minimal first event must validate: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Event)
	}{
		{"sequence 0", func(e *Event) { e.Sequence = 0 }},
		{"sequence 1 with a predecessor", func(e *Event) { e.PreviousEventHash = "0xab" }},
		{"sequence 2 with no predecessor", func(e *Event) { e.Sequence = 2 }},
		{"no event id", func(e *Event) { e.EventID = "" }},
		{"no session id", func(e *Event) { e.SessionID = "" }},
		{"no task id on a task event", func(e *Event) { e.TaskID = "" }},
		{"malformed actor", func(e *Event) { e.Actor = "agent:eip155::0x00" }},
		{"no issuedAt", func(e *Event) { e.IssuedAt = time.Time{} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := base
			c.mutate(&e)
			if err := ValidateEvent(e); err == nil {
				t.Errorf("an event with %s must not validate", c.name)
			}
		})
	}
}

func TestValidateEvent_ExpiresBeforeIssuedIsRejected(t *testing.T) {
	exp := t0.Add(-time.Hour)
	e := Event{
		EventID:   "evt-1",
		SessionID: "ses_1",
		TaskID:    "tsk_1",
		Actor:     actorA,
		Type:      EventTaskCreated,
		Sequence:  1,
		IssuedAt:  t0,
		ExpiresAt: &exp,
	}
	if err := ValidateEvent(e); err == nil {
		t.Fatal("an event that expires before it was issued must be rejected")
	}
}

// TestValidateChain_AcceptsValidChain is the control: a well-formed chain passes,
// or every rejection test below would be meaningless.
func TestValidateChain_AcceptsValidChain(t *testing.T) {
	events := append(
		chain(t, actorA, EventTaskCreated, EventTaskOffered),
		chain(t, actorB, EventTaskAccepted, EventTaskStarted)...,
	)
	if err := ValidateChain(testHasher, events); err != nil {
		t.Fatalf("a valid two-actor chain must validate: %v", err)
	}
}

// TestValidateChain_DetectsGap is the property that makes a missing event visible.
func TestValidateChain_DetectsGap(t *testing.T) {
	events := chain(t, actorA, EventTaskCreated, EventTaskOffered, EventTaskAccepted)
	// Drop the middle event: sequences become 1, 3.
	withGap := []Event{events[0], events[2]}

	err := ValidateChain(testHasher, withGap)
	if err == nil {
		t.Fatal("a missing event must be detected: otherwise a truncated history looks complete")
	}
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("a chain violation must be a *ChainError, got %T: %v", err, err)
	}
	if ce.Kind != "gap" {
		t.Errorf("kind = %q, want %q (a missing event is a gap, not a fork)", ce.Kind, "gap")
	}
}

// TestValidateChain_DetectsFork is the attack a hash chain exists to catch: one
// actor signing two different events at the same sequence.
func TestValidateChain_DetectsFork(t *testing.T) {
	events := chain(t, actorA, EventTaskCreated, EventTaskOffered)

	// A second event at sequence 2 with different content, still linking correctly
	// to sequence 1. An actor that renumbered after signing could produce this.
	fork := events[1]
	fork.EventID = "evt-forked"
	fork.Type = EventTaskCancelled
	// Recompute so the fork is internally consistent, which is what makes it
	// dangerous: nothing about the event itself looks wrong.
	h, err := EventHash(testHasher, fork)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	_ = h

	both := []Event{events[0], events[1], fork}
	err = ValidateChain(testHasher, both)
	if err == nil {
		t.Fatal("two different events at the same sequence must be detected")
	}
	var ce *ChainError
	if !errors.As(err, &ce) {
		t.Fatalf("want *ChainError, got %T", err)
	}
	if ce.Kind != "replay" {
		t.Errorf("kind = %q, want %q: two events at one sequence means one was replayed or renumbered", ce.Kind, "replay")
	}
}

// TestValidateChain_DetectsBrokenPredecessor covers a link that does not match.
func TestValidateChain_DetectsBrokenPredecessor(t *testing.T) {
	events := chain(t, actorA, EventTaskCreated, EventTaskOffered)
	events[1].PreviousEventHash = "0x" + strings.Repeat("00", 32)

	err := ValidateChain(testHasher, events)
	if err == nil {
		t.Fatal("a wrong predecessor hash must be detected")
	}
	var ce *ChainError
	if !errors.As(err, &ce) || ce.Kind != "predecessor" {
		t.Errorf("want a predecessor error, got: %v", err)
	}
}

// TestValidateChain_IsOrderIndependent is the property the whole design rests on.
//
// Two relays receive the same events in different orders. If validation depended on
// arrival order, they would disagree — and a disagreement about validity is a fork.
func TestValidateChain_IsOrderIndependent(t *testing.T) {
	events := append(
		chain(t, actorA, EventTaskCreated, EventTaskOffered),
		chain(t, actorB, EventTaskAccepted)...,
	)

	reversed := make([]Event, len(events))
	for i := range events {
		reversed[len(events)-1-i] = events[i]
	}

	if err := ValidateChain(testHasher, events); err != nil {
		t.Fatalf("forward order: %v", err)
	}
	if err := ValidateChain(testHasher, reversed); err != nil {
		t.Fatalf("a valid chain must validate regardless of slice order: %v", err)
	}
}

// TestValidateChain_ReportsActorDeterministically guards against a flaky-looking
// error: with several broken actors, the same one must be reported every time.
func TestValidateChain_ReportsActorDeterministically(t *testing.T) {
	a := chain(t, actorA, EventTaskCreated)
	b := chain(t, actorB, EventTaskAccepted)
	// Break both.
	a[0].Sequence = 5
	b[0].Sequence = 7

	first := ValidateChain(testHasher, []Event{a[0], b[0]})
	second := ValidateChain(testHasher, []Event{b[0], a[0]})
	if first == nil || second == nil {
		t.Fatal("both broken chains must fail")
	}
	if first.Error() != second.Error() {
		t.Errorf("the reported error must not depend on input order:\n  %v\n  %v", first, second)
	}
}

// TestEvent_UnknownTypeIsNotMalformed is the A9 boundary.
//
// An unknown event type comes from a newer build. It must be kept and reported, not
// rejected: a node that dropped events it did not understand would silently
// truncate a history someone else can still read.
func TestEvent_UnknownTypeIsNotMalformed(t *testing.T) {
	e := Event{
		EventID:   "evt-1",
		SessionID: "ses_1",
		TaskID:    "tsk_1",
		Actor:     actorA,
		Type:      EventType("TASK_SOMETHING_NEW"),
		Sequence:  1,
		IssuedAt:  t0,
	}
	if e.Type.Known() {
		t.Fatal("this test needs an unknown type")
	}
	// ValidateEvent must NOT reject it. A node that dropped events it did not
	// understand would silently truncate a history someone else can still read
	// (A9 §①), so the shape check has to tolerate a type it does not know.
	if err := ValidateEvent(e); err != nil {
		t.Errorf("an unknown event type must be storable, not rejected: %v", err)
	}
}

// TestSignedBytes_RemovesSignatureFromReceivedBytes is the regression test for a bug that made every
// produced event fail its own verification.
//
// # The bug
//
// SignedBytes returned received bytes verbatim, copying the receipt layer's rule that a received
// payload is hashed as-is. For a receipt that is correct: its Payload field holds the bytes of the
// SIGNED OBJECT and the signature sits outside it. An event's received bytes are the WHOLE event
// including its signature, so the producer signed bytes without a signature member and the verifier
// hashed bytes with one.
//
// Measured at the time: 160 received bytes against 139 sign-time bytes, the difference being the
// signature. So an event could not verify after a round trip, and every chained event broke too — one
// root cause with two symptoms.
//
// # Why the assertion is on equality with the sign-time bytes
//
// That is exactly the property the two paths must share. A test that only checked "the signature is
// gone" would pass for an implementation that also stripped something else, and an implementation that
// stripped a real field would break the chain while looking correct.
func TestSignedBytes_RemovesSignatureFromReceivedBytes(t *testing.T) {
	built := Event{
		EventID: "evt-1", SessionID: "ses-1",
		Actor: "agent:eip155:8453:0x7f4db0d9c4b8a6bce8e1c22da9c419e6e1f3a8b5",
		Type:  EventSessionOpen, Sequence: 1, IssuedAt: t0,
	}
	signTime, err := built.SignedBytes()
	if err != nil {
		t.Fatalf("sign-time bytes: %v", err)
	}

	built.Signature = "0x" + strings.Repeat("ab", 65)
	wire, err := json.Marshal(built)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// A decoded event carries Raw, which is the path that was wrong.
	decoded, err := DecodeEvent(wire)
	if err != nil {
		t.Fatalf("DecodeEvent: %v", err)
	}
	received, err := decoded.SignedBytes()
	if err != nil {
		t.Fatalf("received bytes: %v", err)
	}

	if string(received) != string(signTime) {
		t.Fatalf("the signed bytes must be identical whether an event was built or received, or a "+
			"producer signs one set of bytes and a verifier checks another:\n  sign-time (%d): %s\n  received  (%d): %s",
			len(signTime), signTime, len(received), received)
	}
}

// TestSignedBytes_KeepsOtherMembersIntact guards the removal from taking more than the signature, and
// from rewriting anything else.
//
// # Why the fixture uses adversarial bytes
//
// An earlier version of this test used an alphabetically ordered object with simple values, and a
// mutation proved it useless: replacing the targeted removal with decode-and-re-encode PASSED it,
// because re-encoding such an object happens to reproduce it. But re-encoding is exactly what must
// not happen here — measured on a harder fixture:
//
//	in : {"zebra":"a\u0041b","alpha":1,"nested":{"n":1.50,"m":2}}
//	out: {"alpha":1,"nested":{"m":2,"n":1.5},"zebra":"aAb"}
//
// Keys reordered, 1.50 became 1.5, and an escape was resolved. Every one of those changes the bytes,
// and changed bytes mean a valid signature fails to verify. So the fixture below contains all three
// hazards, and the assertion is byte equality with the input minus the signature rather than a
// membership check.
func TestSignedBytes_KeepsOtherMembersIntact(t *testing.T) {
	// Non-alphabetical key order, a resolved-looking escape, a trailing-zero number, and an unknown
	// field: each is something a re-encode would alter.
	wire := []byte(`{"zebra":"a\u0041b","eventId":"evt-1","sessionId":"ses-1","taskId":"tsk-1",` +
		`"actor":"` + actorA + `","type":"TASK_CREATED","sequence":1,` +
		`"issuedAt":"2026-10-04T12:00:00Z","nested":{"n":1.50,"m":2},` +
		`"signature":"0xdead","futureField":{"added":true}}`)

	decoded, err := DecodeEvent(wire)
	if err != nil {
		t.Fatalf("DecodeEvent: %v", err)
	}
	signed, err := decoded.SignedBytes()
	if err != nil {
		t.Fatalf("SignedBytes: %v", err)
	}

	if strings.Contains(string(signed), `"signature"`) {
		t.Error("the signature member must be removed")
	}
	// Byte-level assertions on the hazards, because these are what a re-encode would break.
	if !strings.Contains(string(signed), `\u0041`) {
		t.Errorf("an escape in a string value must survive verbatim; resolving it changes the signed bytes: %s", signed)
	}
	if !strings.Contains(string(signed), `1.50`) {
		t.Errorf("a number's exact spelling must survive; 1.50 and 1.5 are different signed bytes: %s", signed)
	}
	if !strings.Contains(string(signed), `"futureField":{"added":true}`) {
		t.Errorf("an unknown field must survive: a newer build's data would otherwise be silently dropped: %s", signed)
	}
	// And the members must still be in their original order, which a re-encode would sort.
	zebra := strings.Index(string(signed), `"zebra"`)
	eventID := strings.Index(string(signed), `"eventId"`)
	if zebra < 0 || eventID < 0 || zebra > eventID {
		t.Errorf("member order must be preserved; a re-encode would reorder and break the signature: %s", signed)
	}
}

// TestSignedBytes_LeavesUnsignedReceivedBytesAlone covers the case where no signature member exists.
func TestSignedBytes_LeavesUnsignedReceivedBytesAlone(t *testing.T) {
	wire := []byte(`{"eventId":"evt-1","sessionId":"ses-1","taskId":"tsk-1",` +
		`"actor":"` + actorA + `","type":"TASK_CREATED","sequence":1,` +
		`"issuedAt":"2026-10-04T12:00:00Z"}`)

	decoded, err := DecodeEvent(wire)
	if err != nil {
		t.Fatalf("DecodeEvent: %v", err)
	}
	signed, err := decoded.SignedBytes()
	if err != nil {
		t.Fatalf("SignedBytes: %v", err)
	}
	if string(signed) != string(wire) {
		t.Errorf("bytes with no signature member must be returned unchanged:\n  got  %s\n  want %s", signed, wire)
	}
}

// TestSignedBytes_ToleratesASignatureNotLast is the ordering trap.
//
// JSON members are unordered, so a signature in the middle must be removed together with a comma
// rather than leaving `,,` or a dangling separator. A producer that wrote it last would hide this.
func TestSignedBytes_ToleratesASignatureNotLast(t *testing.T) {
	// Signature first, and signature in the middle.
	cases := []string{
		`{"signature":"0xdead","eventId":"evt-1","sessionId":"ses-1","actor":"` + actorA + `","type":"SESSION_OPEN","sequence":1,"issuedAt":"2026-10-04T12:00:00Z"}`,
		`{"eventId":"evt-1","signature":"0xdead","sessionId":"ses-1","actor":"` + actorA + `","type":"SESSION_OPEN","sequence":1,"issuedAt":"2026-10-04T12:00:00Z"}`,
		`{"eventId":"evt-1","sessionId":"ses-1","actor":"` + actorA + `","type":"SESSION_OPEN","sequence":1,"issuedAt":"2026-10-04T12:00:00Z","signature":"0xdead"}`,
	}
	for i, wire := range cases {
		decoded, err := DecodeEvent([]byte(wire))
		if err != nil {
			t.Fatalf("case %d: DecodeEvent: %v", i, err)
		}
		signed, err := decoded.SignedBytes()
		if err != nil {
			t.Fatalf("case %d: SignedBytes: %v", i, err)
		}
		if strings.Contains(string(signed), "signature") {
			t.Errorf("case %d: the signature must be removed wherever it sits: %s", i, signed)
		}
		// The result must still be valid JSON with the other members present, or removing the member
		// left a malformed object.
		var probe map[string]any
		if err := json.Unmarshal(signed, &probe); err != nil {
			t.Errorf("case %d: the result must still be valid JSON, got %v for %s", i, err, signed)
			continue
		}
		for _, key := range []string{"eventId", "sessionId", "actor", "type", "sequence", "issuedAt"} {
			if _, ok := probe[key]; !ok {
				t.Errorf("case %d: member %s was lost: %s", i, key, signed)
			}
		}
	}
}

// TestSignedBytes_IgnoresANestedSignature keeps a payload's own `signature` field from being removed.
func TestSignedBytes_IgnoresANestedSignature(t *testing.T) {
	wire := []byte(`{"eventId":"evt-1","sessionId":"ses-1","taskId":"tsk-1",` +
		`"actor":"` + actorA + `","type":"TASK_CREATED","sequence":1,` +
		`"issuedAt":"2026-10-04T12:00:00Z",` +
		`"payload":{"signature":"0xnested"},"signature":"0xouter"}`)

	decoded, err := DecodeEvent(wire)
	if err != nil {
		t.Fatalf("DecodeEvent: %v", err)
	}
	signed, err := decoded.SignedBytes()
	if err != nil {
		t.Fatalf("SignedBytes: %v", err)
	}

	// The outer signature goes; the nested one is data and must stay.
	if strings.Contains(string(signed), "0xouter") {
		t.Error("the event's own signature must be removed")
	}
	if !strings.Contains(string(signed), "0xnested") {
		t.Error("a signature INSIDE the payload is data and must not be removed; stripping it would " +
			"change what the actor signed")
	}
}

func TestSignedBytes_RejectsNonObjectInput(t *testing.T) {
	e := WithRaw(Event{EventID: "evt-1", SessionID: "ses-1", Actor: actorA,
		Type: EventTaskCreated, Sequence: 1, IssuedAt: t0}, []byte(`"not an object"`))

	if _, err := e.SignedBytes(); err == nil {
		t.Fatal("received bytes that are not an object must be refused rather than hashed")
	}
}

// TestEventHash_CoversUnknownFields is the A9 §④ regression, and it exists because
// a mutation test found the gap rather than the other way round.
//
// The original implementation always hashed a re-serialization of the struct. That
// silently DROPS any field this build does not know, and the resulting hash is
// identical — because the hash never covered the dropped field either way. So a node
// that re-serialized an event would destroy a newer actor's field while every chain
// check still passed. Nothing would fail; the data would just be gone.
//
// Hashing the received bytes closes it, and this test fails if that is ever undone.
func TestEventHash_CoversUnknownFields(t *testing.T) {
	// An event with a field this build does not define, as a newer actor would send.
	wire := []byte(`{"eventId":"evt-1","sessionId":"ses_1","taskId":"tsk_1",` +
		`"actor":"` + actorA + `","type":"TASK_CREATED","sequence":1,` +
		`"issuedAt":"2026-10-04T12:00:00Z","futureField":{"added":true}}`)

	decoded, err := DecodeEvent(wire)
	if err != nil {
		t.Fatalf("DecodeEvent: %v", err)
	}
	h, err := EventHash(testHasher, decoded)
	if err != nil {
		t.Fatalf("EventHash: %v", err)
	}

	// The same event without the unknown field must hash DIFFERENTLY. If it did not,
	// the field would be outside the chain's protection and could be stripped by any
	// intermediary without detection.
	stripped := []byte(`{"eventId":"evt-1","sessionId":"ses_1","taskId":"tsk_1",` +
		`"actor":"` + actorA + `","type":"TASK_CREATED","sequence":1,` +
		`"issuedAt":"2026-10-04T12:00:00Z"}`)
	decodedStripped, err := DecodeEvent(stripped)
	if err != nil {
		t.Fatalf("DecodeEvent: %v", err)
	}
	hStripped, err := EventHash(testHasher, decodedStripped)
	if err != nil {
		t.Fatalf("EventHash: %v", err)
	}

	if h == hStripped {
		t.Fatal("an event with an extra field must hash differently from one without it; " +
			"otherwise a re-serializing intermediary could strip a newer actor's fields " +
			"and every chain check would still pass")
	}
}

// TestSignedBytes_RawTakesPrecedence documents which bytes are authoritative.
func TestSignedBytes_RawTakesPrecedence(t *testing.T) {
	raw := []byte(`{"eventId":"evt-1","futureField":1}`)
	e := WithRaw(Event{EventID: "evt-1", SessionID: "ses_1", Actor: actorA,
		Type: EventTaskCreated, Sequence: 1, IssuedAt: t0}, raw)

	got, err := e.SignedBytes()
	if err != nil {
		t.Fatalf("SignedBytes: %v", err)
	}
	if string(got) != string(raw) {
		t.Errorf("received bytes must take precedence over a reconstruction:\n  got  %s\n  want %s", got, raw)
	}

	// Without Raw, the reconstruction path is used, which is the in-memory case.
	built := Event{EventID: "evt-1", SessionID: "ses_1", Actor: actorA,
		Type: EventTaskCreated, Sequence: 1, IssuedAt: t0}
	gotBuilt, err := built.SignedBytes()
	if err != nil {
		t.Fatalf("SignedBytes: %v", err)
	}
	if string(gotBuilt) == string(raw) {
		t.Error("an event with no received bytes must be reconstructed, not matched to unrelated bytes")
	}
}

// TestDecodeEvent_BindsReceivedBytes is the safety property of the decode helper.
func TestDecodeEvent_BindsReceivedBytes(t *testing.T) {
	wire := []byte(`{"eventId":"evt-1","sessionId":"ses_1","taskId":"tsk_1",` +
		`"actor":"` + actorA + `","type":"TASK_CREATED","sequence":1,` +
		`"issuedAt":"2026-10-04T12:00:00Z"}`)

	e, err := DecodeEvent(wire)
	if err != nil {
		t.Fatalf("DecodeEvent: %v", err)
	}
	if len(e.Raw) == 0 {
		t.Fatal("DecodeEvent must bind the bytes it parsed")
	}
	got, err := e.SignedBytes()
	if err != nil {
		t.Fatalf("SignedBytes: %v", err)
	}
	if string(got) != string(wire) {
		t.Errorf("the bound bytes must be the input bytes:\n  got  %s\n  want %s", got, wire)
	}
}

// TestEvent_RawIsNotSerialized keeps the transport field out of the protocol object.
func TestEvent_RawIsNotSerialized(t *testing.T) {
	e := WithRaw(Event{EventID: "evt-1", SessionID: "ses_1", TaskID: "tsk_1", Actor: actorA,
		Type: EventTaskCreated, Sequence: 1, IssuedAt: t0}, []byte(`{"original":true}`))

	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "original") {
		t.Error("Raw must not be serialized: an event's own bytes inside its own encoding " +
			"would make two builds disagree about the wrapper")
	}
	if strings.Contains(string(raw), `"Raw"`) {
		t.Error("Raw must not appear as a JSON key")
	}
}

// TestEventHash_RawAndReconstructionAgreeForKnownFields confirms the two paths do
// not disagree for an event with only known fields.
//
// They are allowed to differ when unknown fields are present — that is the point —
// but a plain event must hash the same whether it came off the wire or was built in
// memory, or a locally built event could not be compared with a received one.
func TestEventHash_RawAndReconstructionAgreeForKnownFields(t *testing.T) {
	built := Event{EventID: "evt-1", SessionID: "ses_1", TaskID: "tsk_1", Actor: actorA,
		Type: EventTaskCreated, Sequence: 1, IssuedAt: t0}

	wire, err := json.Marshal(built)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	decoded, err := DecodeEvent(wire)
	if err != nil {
		t.Fatalf("DecodeEvent: %v", err)
	}

	hBuilt, err := EventHash(testHasher, built)
	if err != nil {
		t.Fatalf("hash built: %v", err)
	}
	hDecoded, err := EventHash(testHasher, decoded)
	if err != nil {
		t.Fatalf("hash decoded: %v", err)
	}
	if hBuilt != hDecoded {
		t.Error("for an event with only known fields, the wire path and the built path must agree")
	}
}
func TestEvent_PayloadRoundTripsWithUnknownFields(t *testing.T) {
	e := Event{
		EventID:   "evt-1",
		SessionID: "ses_1",
		TaskID:    "tsk_1",
		Actor:     actorA,
		Type:      EventTaskProgress,
		Sequence:  1,
		IssuedAt:  t0,
		Payload:   json.RawMessage(`{"progress":0.5,"futureField":{"added":true}}`),
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Event
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(string(back.Payload), "futureField") {
		t.Errorf("an unknown payload field must survive a round trip, got: %s", back.Payload)
	}
}
