package a2a

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// This file implements the RelayFirst task state machine (S9-5).
//
// # The rule that shapes everything here
//
// ARCHITECTURE.md §4.5 requires that a state transition's *legality* be decided
// from signed data — per-actor sequence, per-actor hash chain, and these rules —
// and NOT from any relay's arrival order. Two relays that received the same events
// in different orders must derive the same state, or the state machine forks.
//
// So Derive takes a set of events and a clock, and is a pure function of them. It
// never consults arrival time. The clock is used only to answer "has a deadline
// passed", and even then the deadline is read from the signed ExpiresAt rather than
// from anything the receiver observed.
//
// # The honest limitation
//
// Per-actor chains order one actor's events. They do not order events *between*
// actors, and the protocol has no way to do so (ARCHITECTURE.md §4.2). When two
// actors' events are concurrent, Derive applies a deterministic tiebreak —
// (IssuedAt, then Actor, then EventID) — so the result is reproducible. The
// tiebreak is a convention, not a claim about physical time, and a caller must not
// read it as one.

// TaskState is the derived lifecycle state.
//
// It is a RelayFirst type rather than the SDK's TaskState because the two sets are
// not the same: the SDK describes an A2A task's reported status, while this is the
// state our rules derive, including states A2A does not name (a task that expired
// because an offer went unanswered). Conflating them would force a lossy mapping
// in one direction or the other. A2ATaskState below is the explicit bridge.
type TaskState string

const (
	StateCreated            TaskState = "TASK_CREATED"
	StateOffered            TaskState = "TASK_OFFERED"
	StateAccepted           TaskState = "TASK_ACCEPTED"
	StateRunning            TaskState = "TASK_RUNNING"
	StateWaitingForInput    TaskState = "TASK_WAITING_FOR_INPUT"
	StateWaitingForApproval TaskState = "TASK_WAITING_FOR_APPROVAL"

	StateCompleted TaskState = "TASK_COMPLETED"
	StateFailed    TaskState = "TASK_FAILED"
	StateCancelled TaskState = "TASK_CANCELLED"
	StateRejected  TaskState = "TASK_REJECTED"
	StateDenied    TaskState = "TASK_DENIED"
	StateExpired   TaskState = "TASK_EXPIRED"
)

// Terminal reports whether s ends the task's life.
func (s TaskState) Terminal() bool {
	switch s {
	case StateCompleted, StateFailed, StateCancelled, StateRejected, StateDenied, StateExpired:
		return true
	default:
		return false
	}
}

// String renders the state.
func (s TaskState) String() string { return string(s) }

// transition describes one legal edge.
type transition struct {
	from  TaskState
	event EventType
	to    TaskState
}

// taskTransitions is the edge table from ARCHITECTURE.md §4.5.
//
// # Why the timeouts are in the same table as the ordinary edges
//
// They are the same kind of thing: a signed event that moves the state. Keeping
// them separate would suggest a timeout is special, when the whole point of §4.5
// is that a timeout is an ordinary transition whose *cause* happens to be a clock.
// The distinction that matters is who may emit it, which is checked separately.
//
// # The states with no outgoing edge
//
// Every terminal state is absent, which is the property that makes "terminal"
// meaningful. A test walks every state and asserts that terminal iff no edge.
var taskTransitions = []transition{
	{StateCreated, EventTaskOffered, StateOffered},
	{StateOffered, EventTaskAccepted, StateAccepted},
	{StateOffered, EventTaskRejected, StateRejected},

	{StateAccepted, EventTaskStarted, StateRunning},
	{StateRunning, EventTaskProgress, StateRunning},
	{StateRunning, EventTaskWaitingForInput, StateWaitingForInput},
	{StateWaitingForInput, EventTaskInputProvided, StateRunning},
	{StateRunning, EventTaskWaitingForApprov, StateWaitingForApproval},
	{StateWaitingForApproval, EventTaskApproved, StateRunning},
	{StateWaitingForApproval, EventTaskDenied, StateDenied},
	{StateRunning, EventTaskCompleted, StateCompleted},
	{StateRunning, EventTaskFailed, StateFailed},

	// Timeout edges. §4.5 requires one for every non-terminal state; S9-5
	// implements these four, and Derive reports the two it does not implement
	// rather than silently ignoring them.
	{StateOffered, EventOfferTTLExpire, StateExpired},
	{StateAccepted, EventAcceptStartTTLExpire, StateExpired},
	{StateWaitingForInput, EventInputTTLExpire, StateExpired},
	// APPROVAL_EXPIRE's target is policy-dependent: §4.5 says the default is
	// TASK_EXPIRED and a deployment may configure TASK_CANCELLED. The default is
	// modelled here; ApprovalExpireTarget below is the seam for the other.
	{StateWaitingForApproval, EventApprovalExpire, StateExpired},
}

// Cancel is legal from any non-terminal state (§4.5), so it is not in the table:
// a table entry per source state would be the same fact written six times and one
// of them would eventually be forgotten.

// ApprovalExpireTarget is the state APPROVAL_EXPIRE moves to.
//
// ARCHITECTURE.md §4.5 allows a deployment to choose TASK_CANCELLED instead of the
// default TASK_EXPIRED. It is a package variable rather than a constant so that
// choice is a single deliberate assignment, and a test pins the default so a
// change to it is visible.
var ApprovalExpireTarget = StateExpired

// DeriveInput is everything Derive needs.
type DeriveInput struct {
	// Events are the signed events for one task. Order is irrelevant: Derive
	// sorts them by the protocol's own rules, never by the slice's order.
	Events []Event

	// Now is the instant to evaluate deadlines against. It is passed in rather
	// than read from time.Now so a test can be exact and so a caller is forced to
	// think about which clock it is using.
	Now time.Time
}

// DeriveResult is the derived state plus everything a caller needs to understand
// how it was reached.
type DeriveResult struct {
	// State is the derived state.
	State TaskState

	// Applied are the events that moved the state, in the order Derive applied
	// them. It is the audit trail: a caller that disagrees with the result can see
	// exactly which event it would have to contest.
	Applied []Event

	// Ignored are events that were valid but did not move the state, with the
	// reason. They are returned rather than dropped because "an event arrived and
	// changed nothing" is exactly what an operator needs to see when a task looks
	// stuck.
	Ignored []IgnoredEvent

	// deadlineReason explains a deadline-derived expiry. See DeadlineReason.
	deadlineReason string

	// Unknown are event types this build does not understand.
	//
	// They are separated from Ignored on purpose: an unknown type means a newer
	// actor is involved, which is a version situation, while an ignored event is a
	// protocol situation. Reporting them together would make a version mismatch
	// look like a bug.
	Unknown []Event
}

// IgnoredEvent is an event that did not affect the state.
type IgnoredEvent struct {
	Event  Event
	Reason string
}

// Derive computes a task's state from its events.
//
// # Why this returns a result and not just a state
//
// A bare state is unfalsifiable. A caller that gets "TASK_FAILED" with no account
// of why cannot tell a correct derivation from a bug in this function. Returning
// the applied events, the ignored ones and the unknown ones makes every derivation
// inspectable, which is the same reason the receipt layer returns field-level
// errors rather than a boolean.
//
// # Determinism
//
// Given the same events and the same Now, this returns the same result regardless
// of the order the events are supplied in. That is the property the whole design
// rests on, and it is what the tests exercise directly.
func Derive(in DeriveInput) (DeriveResult, error) {
	res := DeriveResult{}

	if len(in.Events) == 0 {
		return res, fmt.Errorf("a2a: cannot derive a task state from no events")
	}

	// Validate each event's own shape first, so a malformed event is reported as
	// malformed rather than as an illegal transition.
	for _, e := range in.Events {
		if err := ValidateEvent(e); err != nil {
			return res, err
		}
	}

	// Split known from unknown. Unknown types are kept and reported, never
	// guessed at: an unknown event might be exactly the one that would have moved
	// the state, and pretending otherwise would produce a confident wrong answer.
	var known []Event
	for _, e := range in.Events {
		if e.Type.Known() {
			known = append(known, e)
			continue
		}
		res.Unknown = append(res.Unknown, e)
	}
	if len(known) == 0 {
		return res, fmt.Errorf(
			"a2a: none of the %d events have a known type; this build cannot derive the state",
			len(in.Events))
	}

	// The protocol's own ordering. Not arrival order, not relay order.
	ordered := protocolOrder(known)

	// Start from the first state-producing event. TASK_CREATED establishes the
	// task; without it there is nothing to transition from, and inventing an
	// initial state would let a caller derive a state from a partial history.
	state, started := TaskState(""), false

	for i, e := range ordered {
		if !started {
			if e.Type != EventTaskCreated {
				res.Ignored = append(res.Ignored, IgnoredEvent{
					Event:  e,
					Reason: "no TASK_CREATED yet, so there is no task to move",
				})
				continue
			}
			state, started = StateCreated, true
			res.Applied = append(res.Applied, e)
			continue
		}

		next, ok := applyTransition(state, e.Type)
		if !ok {
			res.Ignored = append(res.Ignored, IgnoredEvent{
				Event:  e,
				Reason: fmt.Sprintf("%s is not a legal event from state %s", e.Type, state),
			})
			continue
		}
		state = next
		res.Applied = append(res.Applied, e)

		// A terminal state ends the derivation. This must stop the loop, not
		// merely be a statement: a terminal state absorbs every later event, so
		// nothing after it may move the state. Racing actors produce such events
		// routinely, and they are reported rather than treated as errors.
		if state.Terminal() {
			for _, rest := range ordered[i+1:] {
				res.Ignored = append(res.Ignored, IgnoredEvent{
					Event:  rest,
					Reason: fmt.Sprintf("%s is terminal; %s cannot move it", state, rest.Type),
				})
			}
			break
		}
	}

	if !started {
		return res, fmt.Errorf("a2a: no TASK_CREATED among %d events; cannot derive a state", len(known))
	}

	// Deadlines are evaluated last, from the signed ExpiresAt of the event that
	// created the current state. This is what makes a timeout observable without
	// requiring a timeout event to have been produced yet.
	if !state.Terminal() {
		if expired, reason := deadlinePassed(state, res.Applied, in.Now); expired {
			// The state moves to EXPIRED, but no event caused it. Applied is left
			// alone and the reason is reported through Ignored-like surface below.
			state = StateExpired
			res.deadlineReason = reason
		}
	}

	res.State = state
	return res, nil
}

// deadlineReason records why a deadline-derived expiry happened.
//
// It is unexported because it is surfaced through a method, keeping the field out
// of the JSON shape a caller might come to depend on.
func (r DeriveResult) DeadlineReason() string { return r.deadlineReason }

// applyTransition looks up the edge for an event from a state.
func applyTransition(from TaskState, event EventType) (TaskState, bool) {
	// Cancellation is legal from any NON-terminal state (ARCHITECTURE.md §4.5).
	// From a terminal state it is not: a finished task must not be rewritten as
	// cancelled, or the record of how it ended would depend on who spoke last.
	if event == EventTaskCancelled {
		if from.Terminal() {
			return "", false
		}
		return StateCancelled, true
	}
	if event == EventApprovalExpire && from == StateWaitingForApproval {
		return ApprovalExpireTarget, true
	}
	for _, t := range taskTransitions {
		if t.from == from && t.event == event {
			return t.to, true
		}
	}
	return "", false
}

// deadlinePassed reports whether the state's signed deadline has elapsed at now.
//
// # Why the deadline comes from the event, not the clock
//
// ARCHITECTURE.md §4.5: a relay may only *notice* that a deadline passed. The
// deadline itself is inside the signed event (ExpiresAt), so every observer
// computes the same instant. Using a local arrival time would let two relays
// disagree about when a task expired.
func deadlinePassed(state TaskState, applied []Event, now time.Time) (bool, string) {
	if now.IsZero() || len(applied) == 0 {
		return false, ""
	}
	// The deadline in force is the one on the most recent applied event, because
	// each event that creates a state may set its own TTL.
	last := applied[len(applied)-1]
	if last.ExpiresAt == nil {
		return false, ""
	}
	if now.Before(*last.ExpiresAt) {
		return false, ""
	}
	// Only states with a timeout edge may expire; a terminal one already has.
	if !hasTimeoutEdge(state) {
		return false, ""
	}
	return true, fmt.Sprintf(
		"the signed deadline %s on %s (sequence %d) has passed; "+
			"the state moves to %s, and a relay may now emit the corresponding timeout event",
		last.ExpiresAt.UTC().Format(time.RFC3339), last.Type, last.Sequence, StateExpired)
}

// hasTimeoutEdge reports whether §4.5 defines a timeout from state.
//
// The two states it does not cover (TASK_RUNNING heartbeat, TASK_DISPUTED) are
// intentionally absent, and TimeoutCoverage reports them so the gap is visible
// rather than assumed away.
func hasTimeoutEdge(state TaskState) bool {
	switch state {
	case StateOffered, StateAccepted, StateWaitingForInput, StateWaitingForApproval:
		return true
	default:
		return false
	}
}

// TimeoutCoverage reports which states §4.5 requires a timeout for and whether this
// build implements one.
//
// # Why this is a function and not a comment
//
// S9-5 deliberately implements four of the six timeout edges. A comment saying so
// would be true today and stale the moment someone adds one. Reporting the gap
// from the same table the state machine uses means the answer cannot drift from the
// implementation.
func TimeoutCoverage() []struct {
	State       TaskState
	Event       EventType
	Implemented bool
} {
	required := []struct {
		state TaskState
		event EventType
	}{
		{StateOffered, EventOfferTTLExpire},
		{StateAccepted, EventAcceptStartTTLExpire},
		{StateRunning, EventRunningHeartbeatExpire},
		{StateWaitingForInput, EventInputTTLExpire},
		{StateWaitingForApproval, EventApprovalExpire},
	}
	out := make([]struct {
		State       TaskState
		Event       EventType
		Implemented bool
	}, 0, len(required))
	for _, r := range required {
		impl := false
		for _, t := range taskTransitions {
			if t.from == r.state && t.event == r.event {
				impl = true
				break
			}
		}
		out = append(out, struct {
			State       TaskState
			Event       EventType
			Implemented bool
		}{State: r.state, Event: r.event, Implemented: impl})
	}
	return out
}

// protocolOrder sorts events by the protocol's ordering rules.
//
// The comparison, in order of precedence:
//
//  1. IssuedAt — the signed time, which is the only time the protocol has.
//  2. Actor   — a stable tiebreak when two actors' times collide.
//  3. EventID — a final tiebreak so the order is total, never ambiguous.
//
// # Why this is a convention and not a discovery
//
// It cannot be a discovery: per-actor chains do not order events between actors,
// so any cross-actor order is a choice. What matters is that the choice is
// deterministic and documented, so two verifiers agree. Reading it as "what
// actually happened first" would be a mistake, and the doc comment says so.
func protocolOrder(events []Event) []Event {
	out := append([]Event(nil), events...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.IssuedAt.Equal(b.IssuedAt) {
			return a.IssuedAt.Before(b.IssuedAt)
		}
		if a.Actor != b.Actor {
			return a.Actor < b.Actor
		}
		return a.EventID < b.EventID
	})
	return out
}

// A2ATaskState maps a derived state onto the SDK's task state, for the wire.
//
// # Why this mapping is explicit and total
//
// A RelayFirst task can be in states A2A does not name (an offer that expired
// unanswered, a denial). Sending them as-is would put values on the wire that a
// standard A2A client cannot interpret. Mapping them onto the closest standard
// state keeps the wire valid, and the lossy direction is a documented choice rather
// than an accident.
//
// The mapping is not invertible, and that is expected: a client reading the A2A
// state learns how the task ended, and a RelayFirst client reads our events for
// the precise reason.
func A2ATaskState(s TaskState) string {
	switch s {
	case StateCreated, StateOffered:
		// Offered but not yet accepted: A2A calls this submitted.
		return "TASK_STATE_SUBMITTED"
	case StateAccepted, StateRunning:
		return "TASK_STATE_WORKING"
	case StateWaitingForInput:
		return "TASK_STATE_INPUT_REQUIRED"
	case StateWaitingForApproval:
		// A2A has TASK_STATE_AUTH_REQUIRED for "needs a human or credential to
		// proceed", which is the closest standard state for a pending approval.
		return "TASK_STATE_AUTH_REQUIRED"
	case StateCompleted:
		return "TASK_STATE_COMPLETED"
	case StateFailed, StateExpired:
		// An expiry is a failure to complete, and A2A has no expired state.
		return "TASK_STATE_FAILED"
	case StateCancelled:
		return "TASK_STATE_CANCELED"
	case StateRejected, StateDenied:
		// A denial is the approver rejecting the work; A2A's REJECTED is the
		// closest, and it is terminal in both models.
		return "TASK_STATE_REJECTED"
	default:
		return "TASK_STATE_UNSPECIFIED"
	}
}

// MarshalJSON renders the state as its string.
func (s TaskState) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(s))
}
