package a2a

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/agentid"
)

// This file implements the session layer (S9-4).
//
// # What a session is, and what it is not
//
// A session is the scope two agents agree to talk in: it groups the events of a
// conversation and gives them a common id. It is deliberately thin. It is not a
// connection (that is the transport's business), not an authentication context
// (that is each event's signature), and not a subscription (that is the node's
// directory).
//
// Keeping it thin matters because a session is the one piece of state both sides
// must agree on, and every field added to it is a field they can disagree about.
//
// # Open and close are symmetric
//
// Either participant may open, and either may close. A session is not owned by its
// initiator: an executor that could not close a session an abandoned requester left
// open would leak a session per abandoned task. So SESSION_CLOSE is legal from
// either side, and re-closing is a no-op rather than an error — a peer retrying a
// close must not be told it did something wrong.

// SessionState is the derived session lifecycle.
type SessionState string

const (
	SessionOpenState   SessionState = "SESSION_OPEN"
	SessionClosedState SessionState = "SESSION_CLOSED"
)

// Terminal reports whether the session is over.
func (s SessionState) Terminal() bool { return s == SessionClosedState }

// SessionResult is the derived session state and its audit trail.
type SessionResult struct {
	State   SessionState
	Applied []Event
	Ignored []IgnoredEvent
	Unknown []Event

	// Participants are the distinct actors seen, sorted. It is derived rather
	// than stored so it cannot disagree with the events.
	Participants []string
}

// DeriveSession computes a session's state from its events.
//
// # Why this shares the ordering rules with Derive
//
// Because it must. If sessions ordered events one way and tasks another, a task
// nested in a session could be derived with a different history than the session
// containing it — the kind of inconsistency that produces a plausible-looking
// wrong answer. Both use protocolOrder.
//
// # Why an event from an unknown actor is not an error
//
// A session may legitimately involve more participants than the opener expected
// (an approval from a third party, a verification from an assigned verifier).
// DeriveSession records who spoke rather than restricting who may.
func DeriveSession(events []Event, now time.Time) (SessionResult, error) {
	res := SessionResult{}
	if len(events) == 0 {
		return res, fmt.Errorf("a2a: cannot derive a session state from no events")
	}

	seen := map[string]struct{}{}
	for _, e := range events {
		if err := ValidateEvent(e); err != nil {
			return res, err
		}
		seen[e.Actor] = struct{}{}
	}
	for a := range seen {
		res.Participants = append(res.Participants, a)
	}
	sort.Strings(res.Participants)

	// Only session-level events belong to the session's own state machine. Task
	// events are carried in the same stream but belong to Derive.
	var sessionEvents []Event
	for _, e := range events {
		switch e.Type {
		case EventSessionOpen, EventSessionClose:
			sessionEvents = append(sessionEvents, e)
			continue
		}
		if !e.Type.Known() {
			res.Unknown = append(res.Unknown, e)
			continue
		}
		res.Ignored = append(res.Ignored, IgnoredEvent{
			Event:  e,
			Reason: fmt.Sprintf("%s is a task event, not a session event", e.Type),
		})
	}

	if len(sessionEvents) == 0 {
		return res, fmt.Errorf("a2a: no SESSION_OPEN or SESSION_CLOSE among %d events", len(events))
	}

	state := SessionState("")
	opened := false
	for _, e := range protocolOrder(sessionEvents) {
		switch {
		case e.Type == EventSessionOpen:
			if opened {
				// A second open is not an error: it is a peer retrying a message
				// whose acknowledgement was lost. Idempotence here is what makes
				// retrying safe.
				res.Ignored = append(res.Ignored, IgnoredEvent{
					Event:  e,
					Reason: "session is already open; a repeated open is treated as a retry, not a conflict",
				})
				continue
			}
			opened = true
			state = SessionOpenState
			res.Applied = append(res.Applied, e)
		case e.Type == EventSessionClose:
			if !opened {
				res.Ignored = append(res.Ignored, IgnoredEvent{
					Event:  e,
					Reason: "cannot close a session that was never opened",
				})
				continue
			}
			if state.Terminal() {
				res.Ignored = append(res.Ignored, IgnoredEvent{
					Event:  e,
					Reason: "session is already closed; a repeated close is treated as a retry, not a conflict",
				})
				continue
			}
			state = SessionClosedState
			res.Applied = append(res.Applied, e)
		}
	}

	if !opened {
		return res, fmt.Errorf("a2a: no SESSION_OPEN among %d session events", len(sessionEvents))
	}
	res.State = state
	return res, nil
}

// SessionEvent builds the opening event for a session.
//
// # Why construction is a helper and not left to callers
//
// The chain fields (Sequence, PreviousEventHash) and the times have exact rules,
// and a caller assembling them by hand is how an off-by-one in a sequence number
// becomes a chain that will not validate. This is the same reasoning as the receipt
// builder: put the invariants where the value is made.
//
// prevHash is the actor's previous event hash, or "" for their first event. nextSeq
// is the actor's next sequence number (1 for their first).
func SessionEvent(
	eventID, sessionID, actor string,
	typ EventType,
	nextSeq uint64,
	prevHash string,
	issuedAt time.Time,
	ttl time.Duration,
) (Event, error) {
	if typ != EventSessionOpen && typ != EventSessionClose {
		return Event{}, fmt.Errorf("a2a: SessionEvent is for session events, got %s", typ)
	}
	e := Event{
		EventID:           eventID,
		SessionID:         sessionID,
		Actor:             actor,
		Type:              typ,
		Sequence:          nextSeq,
		PreviousEventHash: prevHash,
		IssuedAt:          issuedAt.UTC(),
	}
	if ttl > 0 {
		exp := issuedAt.UTC().Add(ttl)
		e.ExpiresAt = &exp
	}
	if err := ValidateEvent(e); err != nil {
		return Event{}, err
	}
	return e, nil
}

// SessionIDFor derives a session id from its opener and a nonce.
//
// # Why the id is derived rather than random
//
// Two agents that both try to open a session for the same task must not end up
// with two sessions. Deriving the id from the opener's identity and a nonce they
// choose makes the id stable for a given intent, so a retried open collides with
// the first rather than creating a second. A purely random id would make every
// retry a new session.
//
// The nonce is the caller's, so the caller decides the granularity: one session
// per task, per conversation, or per day.
func SessionIDFor(actor, nonce string) (string, error) {
	if _, err := agentid.Parse(actor); err != nil {
		return "", fmt.Errorf("a2a: session opener: %w", err)
	}
	if strings.TrimSpace(nonce) == "" {
		return "", fmt.Errorf("a2a: a session id needs a nonce; without one, retries would create new sessions")
	}
	// The id is a readable composition rather than a hash, because it appears in
	// logs and in a node's index, and a hash there would make debugging a session
	// needlessly hard. It carries no authority: nothing validates a session id by
	// recomputing it.
	slug := strings.ReplaceAll(actor, ":", "-")
	return "ses_" + slug + "_" + nonce, nil
}
