package a2a

import (
	"strings"
	"testing"
	"time"
)

// sessionChain builds a session-level event chain for one actor. It cannot use the
// shared chain helper because session events carry no TaskID.
func sessionChain(t *testing.T, actor string, types ...EventType) []Event {
	t.Helper()
	var (
		out      []Event
		prevHash string
	)
	for i, typ := range types {
		e := Event{
			EventID:           "sev-" + string(rune('a'+i)),
			SessionID:         "ses_test",
			Actor:             actor,
			Type:              typ,
			Sequence:          uint64(i + 1),
			PreviousEventHash: prevHash,
			IssuedAt:          t0.Add(time.Duration(i) * time.Second),
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

func TestDeriveSession_OpenAndClose(t *testing.T) {
	res, err := DeriveSession(sessionChain(t, actorA, EventSessionOpen), t0)
	if err != nil {
		t.Fatalf("DeriveSession: %v", err)
	}
	if res.State != SessionOpenState {
		t.Errorf("state = %s, want %s", res.State, SessionOpenState)
	}
	if res.State.Terminal() {
		t.Error("an open session is not terminal")
	}

	res, err = DeriveSession(sessionChain(t, actorA, EventSessionOpen, EventSessionClose), t0)
	if err != nil {
		t.Fatalf("DeriveSession: %v", err)
	}
	if res.State != SessionClosedState {
		t.Errorf("state = %s, want %s", res.State, SessionClosedState)
	}
	if !res.State.Terminal() {
		t.Error("a closed session is terminal")
	}
}

// TestDeriveSession_EitherSideMayClose is the property that stops a session leak.
//
// A session is not owned by its opener. If only the opener could close it, every
// abandoned task would leak a session its executor could not clean up.
func TestDeriveSession_EitherSideMayClose(t *testing.T) {
	open := sessionChain(t, actorA, EventSessionOpen)
	// The close happens after the open, as it must in reality.
	closeEvents := sessionChain(t, actorB, EventSessionClose)
	closeEvents[0].IssuedAt = open[0].IssuedAt.Add(time.Second)

	events := merge(open, closeEvents)
	res, err := DeriveSession(events, t0)
	if err != nil {
		t.Fatalf("DeriveSession: %v", err)
	}
	if res.State != SessionClosedState {
		t.Errorf("the other participant must be able to close, got %s", res.State)
	}
	if len(res.Participants) != 2 {
		t.Errorf("participants = %v, want both actors", res.Participants)
	}
}

// TestDeriveSession_SimultaneousEventsUseTheActorTiebreak documents the limit of
// what the protocol can know.
//
// Per-actor chains do not order events between actors (ARCHITECTURE.md §4.2), so
// two events at the same instant are genuinely ambiguous. DeriveSession resolves
// it deterministically — by actor id — which makes every verifier agree, and that
// agreement is the requirement. It is a convention, not a claim about which
// happened first, and this test states that plainly rather than pretending the
// ambiguity does not exist.
func TestDeriveSession_SimultaneousEventsUseTheActorTiebreak(t *testing.T) {
	// Both at t0, deliberately.
	events := merge(
		sessionChain(t, actorA, EventSessionOpen),
		sessionChain(t, actorB, EventSessionClose),
	)
	res, err := DeriveSession(events, t0)
	if err != nil {
		t.Fatalf("DeriveSession: %v", err)
	}

	// actorB's id sorts before actorA's, so the close is applied first and the
	// open then leaves the session open. The important property is that this is
	// reproducible: run it again, and with the input reversed.
	for _, order := range [][]Event{events, {events[1], events[0]}} {
		again, err := DeriveSession(order, t0)
		if err != nil {
			t.Fatalf("DeriveSession: %v", err)
		}
		if again.State != res.State {
			t.Errorf("the same-instant tiebreak must be stable: got %s then %s", res.State, again.State)
		}
	}
}

// TestDeriveSession_RepeatedOpenIsARetryNotAConflict is the idempotence rule.
//
// A peer whose acknowledgement was lost will retry the open. Telling it that it did
// something wrong would make retrying unsafe, and retrying is the only recovery a
// message-based protocol has.
func TestDeriveSession_RepeatedOpenIsARetryNotAConflict(t *testing.T) {
	events := sessionChain(t, actorA, EventSessionOpen, EventSessionOpen)
	res, err := DeriveSession(events, t0)
	if err != nil {
		t.Fatalf("a repeated open must not be an error: %v", err)
	}
	if res.State != SessionOpenState {
		t.Errorf("state = %s, want %s", res.State, SessionOpenState)
	}
	if len(res.Ignored) != 1 {
		t.Fatalf("the repeat must be reported as ignored, got %d ignored", len(res.Ignored))
	}
	if !strings.Contains(res.Ignored[0].Reason, "retry") {
		t.Errorf("the reason must say it is treated as a retry, got: %q", res.Ignored[0].Reason)
	}
}

func TestDeriveSession_RepeatedCloseIsARetryNotAConflict(t *testing.T) {
	events := sessionChain(t, actorA, EventSessionOpen, EventSessionClose, EventSessionClose)
	res, err := DeriveSession(events, t0)
	if err != nil {
		t.Fatalf("a repeated close must not be an error: %v", err)
	}
	if res.State != SessionClosedState {
		t.Errorf("state = %s, want %s", res.State, SessionClosedState)
	}
}

// TestDeriveSession_CloseBeforeOpenIsIgnored covers a malformed history.
func TestDeriveSession_CloseBeforeOpenIsIgnored(t *testing.T) {
	events := sessionChain(t, actorA, EventSessionClose, EventSessionOpen)
	res, err := DeriveSession(events, t0)
	if err != nil {
		t.Fatalf("DeriveSession: %v", err)
	}
	// The close cannot apply; the open still does.
	if res.State != SessionOpenState {
		t.Errorf("state = %s, want %s", res.State, SessionOpenState)
	}
	if len(res.Ignored) == 0 {
		t.Error("the impossible close must be reported")
	}
}

func TestDeriveSession_RequiresAnOpen(t *testing.T) {
	if _, err := DeriveSession(sessionChain(t, actorA, EventSessionClose), t0); err == nil {
		t.Fatal("a session with no open must fail")
	}
}

func TestDeriveSession_NoEventsIsAnError(t *testing.T) {
	if _, err := DeriveSession(nil, t0); err == nil {
		t.Fatal("deriving a session from no events must fail")
	}
}

// TestDeriveSession_TaskEventsAreNotSessionTransitions keeps the two state machines
// separate: a task event in the stream must not be read as a session event.
func TestDeriveSession_TaskEventsAreNotSessionTransitions(t *testing.T) {
	events := append(
		sessionChain(t, actorA, EventSessionOpen),
		taskEvents(t, actorA, EventTaskCreated, EventTaskOffered)...,
	)
	res, err := DeriveSession(events, t0)
	if err != nil {
		t.Fatalf("DeriveSession: %v", err)
	}
	if res.State != SessionOpenState {
		t.Errorf("a task event must not move the session state, got %s", res.State)
	}
	if len(res.Ignored) != 2 {
		t.Errorf("task events must be reported as not session events, got %d ignored", len(res.Ignored))
	}
}

// TestDeriveSession_IsOrderIndependent mirrors the task property.
func TestDeriveSession_IsOrderIndependent(t *testing.T) {
	events := merge(
		sessionChain(t, actorA, EventSessionOpen),
		sessionChain(t, actorB, EventSessionClose),
	)
	var want SessionState
	for shift := 0; shift < len(events); shift++ {
		rotated := append(append([]Event{}, events[shift:]...), events[:shift]...)
		res, err := DeriveSession(rotated, t0)
		if err != nil {
			t.Fatalf("shift %d: %v", shift, err)
		}
		if shift == 0 {
			want = res.State
			continue
		}
		if res.State != want {
			t.Errorf("shift %d gave %s but shift 0 gave %s", shift, res.State, want)
		}
	}
}

// TestDeriveSession_UnknownEventsAreReported keeps the A9 boundary in the session
// path too.
func TestDeriveSession_UnknownEventsAreReported(t *testing.T) {
	events := sessionChain(t, actorA, EventSessionOpen)
	events = append(events, Event{
		EventID:   "sev-future",
		SessionID: "ses_test",
		Actor:     actorB,
		Type:      EventType("SESSION_FROM_THE_FUTURE"),
		Sequence:  1,
		IssuedAt:  t0,
	})

	res, err := DeriveSession(events, t0)
	if err != nil {
		t.Fatalf("DeriveSession: %v", err)
	}
	if len(res.Unknown) != 1 {
		t.Errorf("unknown events = %d, want 1", len(res.Unknown))
	}
}

// TestSessionIDFor_IsStableForRetries is the property that prevents duplicate
// sessions: the same intent must derive the same id.
func TestSessionIDFor_IsStableForRetries(t *testing.T) {
	a, err := SessionIDFor(actorA, "task-42")
	if err != nil {
		t.Fatalf("SessionIDFor: %v", err)
	}
	b, err := SessionIDFor(actorA, "task-42")
	if err != nil {
		t.Fatalf("SessionIDFor: %v", err)
	}
	if a != b {
		t.Errorf("the same opener and nonce must derive the same id, got %q and %q", a, b)
	}

	// A different nonce is a different session.
	c, err := SessionIDFor(actorA, "task-43")
	if err != nil {
		t.Fatalf("SessionIDFor: %v", err)
	}
	if a == c {
		t.Error("a different nonce must derive a different session id")
	}

	// A different opener is a different session even for the same nonce, or two
	// agents working on "task-42" would collide.
	d, err := SessionIDFor(actorB, "task-42")
	if err != nil {
		t.Fatalf("SessionIDFor: %v", err)
	}
	if a == d {
		t.Error("different openers must not collide on a shared nonce")
	}
}

func TestSessionIDFor_RejectsBadInput(t *testing.T) {
	if _, err := SessionIDFor(actorA, ""); err == nil {
		t.Fatal("an empty nonce must be rejected: retries would create new sessions")
	}
	if _, err := SessionIDFor("not-an-agent", "n"); err == nil {
		t.Fatal("a malformed opener must be rejected")
	}
}

// TestSessionEvent_ValidatesItsInput keeps the builder from producing an event that
// ValidateEvent would reject.
func TestSessionEvent_ValidatesItsInput(t *testing.T) {
	e, err := SessionEvent("sev-1", "ses_1", actorA, EventSessionOpen, 1, "", t0, time.Hour)
	if err != nil {
		t.Fatalf("SessionEvent: %v", err)
	}
	if err := ValidateEvent(e); err != nil {
		t.Errorf("the builder produced an event that does not validate: %v", err)
	}
	if e.ExpiresAt == nil {
		t.Error("a positive TTL must set ExpiresAt")
	}
	if e.TaskID != "" {
		t.Error("a session event must not carry a task id")
	}

	// A task event through the session builder is a misuse.
	if _, err := SessionEvent("sev-2", "ses_1", actorA, EventTaskCreated, 1, "", t0, 0); err == nil {
		t.Error("SessionEvent must refuse a task event type")
	}
	// A malformed actor must be caught by the builder, not later.
	if _, err := SessionEvent("sev-3", "ses_1", "bad", EventSessionOpen, 1, "", t0, 0); err == nil {
		t.Error("SessionEvent must refuse a malformed actor")
	}
}
