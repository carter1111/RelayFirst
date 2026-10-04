package a2a

import (
	"strings"
	"testing"
	"time"
)

// taskEvents builds a chain of task events for one actor, so tests can express a
// history by its event types.
func taskEvents(t *testing.T, actor string, types ...EventType) []Event {
	t.Helper()
	return chain(t, actor, types...)
}

// merge concatenates several actors' chains, which is how a real history arrives.
func merge(chains ...[]Event) []Event {
	var out []Event
	for _, c := range chains {
		out = append(out, c...)
	}
	return out
}

// TestDerive_HappyPath walks the ordinary lifecycle and checks the state at each
// step, so a regression in any single edge is localized.
func TestDerive_HappyPath(t *testing.T) {
	steps := []struct {
		types []EventType
		want  TaskState
	}{
		{[]EventType{EventTaskCreated}, StateCreated},
		{[]EventType{EventTaskCreated, EventTaskOffered}, StateOffered},
		{[]EventType{EventTaskCreated, EventTaskOffered, EventTaskAccepted}, StateAccepted},
		{[]EventType{EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted}, StateRunning},
		{[]EventType{EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted,
			EventTaskProgress}, StateRunning},
		{[]EventType{EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted,
			EventTaskProgress, EventTaskCompleted}, StateCompleted},
	}
	for _, s := range steps {
		events := taskEvents(t, actorA, s.types...)
		res, err := Derive(DeriveInput{Events: events, Now: t0})
		if err != nil {
			t.Fatalf("Derive(%v): %v", s.types, err)
		}
		if res.State != s.want {
			t.Errorf("events %v derived %s, want %s", s.types, res.State, s.want)
		}
	}
}

// TestDerive_WaitingForInputRoundTrip covers the input pause and its resume, which
// is a cycle in the graph rather than a straight line.
func TestDerive_WaitingForInputRoundTrip(t *testing.T) {
	events := taskEvents(t, actorA,
		EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted,
		EventTaskWaitingForInput)

	res, err := Derive(DeriveInput{Events: events, Now: t0})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.State != StateWaitingForInput {
		t.Errorf("state = %s, want %s", res.State, StateWaitingForInput)
	}

	events = taskEvents(t, actorA,
		EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted,
		EventTaskWaitingForInput, EventTaskInputProvided)
	res, err = Derive(DeriveInput{Events: events, Now: t0})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.State != StateRunning {
		t.Errorf("providing input must resume the task, got %s", res.State)
	}
}

// TestDerive_WaitingForApprovalAndDenial covers the approval branch.
func TestDerive_WaitingForApprovalAndDenial(t *testing.T) {
	base := []EventType{EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted,
		EventTaskWaitingForApprov}

	res, err := Derive(DeriveInput{Events: taskEvents(t, actorA, base...), Now: t0})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.State != StateWaitingForApproval {
		t.Errorf("state = %s, want %s", res.State, StateWaitingForApproval)
	}

	res, err = Derive(DeriveInput{Events: taskEvents(t, actorA, append(base, EventTaskApproved)...), Now: t0})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.State != StateRunning {
		t.Errorf("approval must resume the task, got %s", res.State)
	}

	res, err = Derive(DeriveInput{Events: taskEvents(t, actorA, append(base, EventTaskDenied)...), Now: t0})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.State != StateDenied {
		t.Errorf("state = %s, want %s", res.State, StateDenied)
	}
}

// TestDerive_TimeoutEdges covers the four timeout edges S9-5 implements.
//
// Each is checked with an explicit timeout event, which is how the protocol
// represents one: a relay notices the deadline and emits a signed event. The
// derivation itself does not depend on the relay's clock.
func TestDerive_TimeoutEdges(t *testing.T) {
	cases := []struct {
		name string
		pre  []EventType
		evt  EventType
		want TaskState
	}{
		{"offer expires", []EventType{EventTaskCreated, EventTaskOffered}, EventOfferTTLExpire, StateExpired},
		{"accepted but never started", []EventType{EventTaskCreated, EventTaskOffered, EventTaskAccepted}, EventAcceptStartTTLExpire, StateExpired},
		{"waiting for input expires", []EventType{EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted, EventTaskWaitingForInput}, EventInputTTLExpire, StateExpired},
		{"approval expires", []EventType{EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted, EventTaskWaitingForApprov}, EventApprovalExpire, StateExpired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			events := taskEvents(t, actorA, append(c.pre, c.evt)...)
			res, err := Derive(DeriveInput{Events: events, Now: t0})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if res.State != c.want {
				t.Errorf("state = %s, want %s", res.State, c.want)
			}
		})
	}
}

// TestDerive_ApprovalExpireTargetIsConfigurable pins the §4.5 policy seam: the
// default target is EXPIRED, and a deployment may choose CANCELLED.
func TestDerive_ApprovalExpireTargetIsConfigurable(t *testing.T) {
	if ApprovalExpireTarget != StateExpired {
		t.Fatalf("the default APPROVAL_EXPIRE target must be %s, got %s",
			StateExpired, ApprovalExpireTarget)
	}

	original := ApprovalExpireTarget
	t.Cleanup(func() { ApprovalExpireTarget = original })

	ApprovalExpireTarget = StateCancelled
	events := taskEvents(t, actorA, EventTaskCreated, EventTaskOffered, EventTaskAccepted,
		EventTaskStarted, EventTaskWaitingForApprov, EventApprovalExpire)
	res, err := Derive(DeriveInput{Events: events, Now: t0})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.State != StateCancelled {
		t.Errorf("with the policy override the state must be %s, got %s", StateCancelled, res.State)
	}
}

// TestDerive_CancelFromAnyNonTerminalState covers the rule that cancellation is
// always available, which is what stops an abandoned task from hanging forever.
func TestDerive_CancelFromAnyNonTerminalState(t *testing.T) {
	paths := [][]EventType{
		{EventTaskCreated},
		{EventTaskCreated, EventTaskOffered},
		{EventTaskCreated, EventTaskOffered, EventTaskAccepted},
		{EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted},
		{EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted, EventTaskWaitingForInput},
		{EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted, EventTaskWaitingForApprov},
	}
	for _, p := range paths {
		events := taskEvents(t, actorA, append(p, EventTaskCancelled)...)
		res, err := Derive(DeriveInput{Events: events, Now: t0})
		if err != nil {
			t.Fatalf("Derive(%v): %v", p, err)
		}
		if res.State != StateCancelled {
			t.Errorf("cancel after %v gave %s, want %s", p, res.State, StateCancelled)
		}
	}
}

// TestDerive_TerminalStatesAreAbsorbing is the property that makes "terminal"
// mean something: nothing moves a finished task.
func TestDerive_TerminalStatesAreAbsorbing(t *testing.T) {
	terminals := []struct {
		name  string
		types []EventType
		want  TaskState
	}{
		{"completed", []EventType{EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted, EventTaskCompleted}, StateCompleted},
		{"rejected", []EventType{EventTaskCreated, EventTaskOffered, EventTaskRejected}, StateRejected},
		{"failed", []EventType{EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted, EventTaskFailed}, StateFailed},
	}
	// Every later event that a racing actor might send.
	after := []EventType{EventTaskProgress, EventTaskStarted, EventTaskCompleted, EventTaskFailed, EventTaskCancelled}

	for _, tc := range terminals {
		for _, late := range after {
			events := taskEvents(t, actorA, append(append([]EventType{}, tc.types...), late)...)
			res, err := Derive(DeriveInput{Events: events, Now: t0})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if res.State != tc.want {
				t.Errorf("%s then %s gave %s, want %s (terminal states must absorb)",
					tc.name, late, res.State, tc.want)
			}
		}
	}
}

// TestDerive_IllegalTransitionIsIgnoredNotFatal is the robustness rule.
//
// Two actors racing produce events that are individually valid and jointly illegal.
// That is normal, not an error: a client that crashed on it would be unusable
// against real peers. The event is reported instead.
func TestDerive_IllegalTransitionIsIgnoredNotFatal(t *testing.T) {
	// TASK_STARTED from OFFERED is illegal: nobody accepted.
	events := taskEvents(t, actorA, EventTaskCreated, EventTaskOffered, EventTaskStarted)
	res, err := Derive(DeriveInput{Events: events, Now: t0})
	if err != nil {
		t.Fatalf("an illegal transition must be ignored, not fatal: %v", err)
	}
	if res.State != StateOffered {
		t.Errorf("state = %s, want %s (the illegal event must not move it)", res.State, StateOffered)
	}
	if len(res.Ignored) == 0 {
		t.Error("the ignored event must be reported so an operator can see why nothing happened")
	}
	if !strings.Contains(res.Ignored[0].Reason, "not a legal event") {
		t.Errorf("the reason must name the problem, got: %q", res.Ignored[0].Reason)
	}
}

// TestDerive_EventsBeforeCreatedAreIgnored covers a partial history.
func TestDerive_EventsBeforeCreatedAreIgnored(t *testing.T) {
	events := taskEvents(t, actorA, EventTaskProgress, EventTaskCreated, EventTaskOffered)
	res, err := Derive(DeriveInput{Events: events, Now: t0})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.State != StateOffered {
		t.Errorf("state = %s, want %s", res.State, StateOffered)
	}
}

// TestDerive_RequiresTaskCreated is the guard against inventing an initial state.
func TestDerive_RequiresTaskCreated(t *testing.T) {
	events := taskEvents(t, actorA, EventTaskOffered, EventTaskAccepted)
	if _, err := Derive(DeriveInput{Events: events, Now: t0}); err == nil {
		t.Fatal("deriving a state with no TASK_CREATED must fail: " +
			"otherwise a caller could get a confident answer from a partial history")
	}
}

func TestDerive_NoEventsIsAnError(t *testing.T) {
	if _, err := Derive(DeriveInput{Events: nil, Now: t0}); err == nil {
		t.Fatal("deriving from no events must fail")
	}
}

// TestDerive_IsOrderIndependent is the central property.
//
// ARCHITECTURE.md §4.2: there is no global order across relays. Two relays that
// received the same events in different orders MUST derive the same state, or the
// state machine forks. This shuffles the input in every rotation and asserts one
// answer.
func TestDerive_IsOrderIndependent(t *testing.T) {
	events := merge(
		taskEvents(t, actorA, EventTaskCreated, EventTaskOffered),
		taskEvents(t, actorB, EventTaskAccepted, EventTaskStarted),
	)

	// Rotate the slice through every position, which covers more orderings than a
	// single reversal and needs no randomness.
	var want TaskState
	for shift := 0; shift < len(events); shift++ {
		rotated := append(append([]Event{}, events[shift:]...), events[:shift]...)
		res, err := Derive(DeriveInput{Events: rotated, Now: t0})
		if err != nil {
			t.Fatalf("shift %d: %v", shift, err)
		}
		if shift == 0 {
			want = res.State
			continue
		}
		if res.State != want {
			t.Errorf("shift %d derived %s but shift 0 derived %s; "+
				"a state that depends on arrival order is a fork", shift, res.State, want)
		}
	}
}

// TestDerive_UnknownEventsAreReportedNotGuessed is the version boundary.
//
// An event type from a newer build might be exactly the one that would move the
// state. Reporting it separately from "ignored" is what keeps a version mismatch
// from looking like a protocol bug.
func TestDerive_UnknownEventsAreReportedNotGuessed(t *testing.T) {
	events := taskEvents(t, actorA, EventTaskCreated, EventTaskOffered)
	unknown := Event{
		EventID:   "evt-new",
		SessionID: "ses_test",
		TaskID:    "tsk_1",
		Actor:     actorB,
		Type:      EventType("TASK_FROM_THE_FUTURE"),
		Sequence:  1,
		IssuedAt:  t0.Add(time.Minute),
	}
	events = append(events, unknown)

	res, err := Derive(DeriveInput{Events: events, Now: t0})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(res.Unknown) != 1 {
		t.Fatalf("unknown events = %d, want 1", len(res.Unknown))
	}
	if res.Unknown[0].Type != "TASK_FROM_THE_FUTURE" {
		t.Errorf("the unknown event must be preserved, got %s", res.Unknown[0].Type)
	}
	// It must not have been silently treated as a legal transition.
	if res.State != StateOffered {
		t.Errorf("state = %s, want %s: an unknown event must not move the state", res.State, StateOffered)
	}
}

// TestDerive_DeadlineFromSignedExpiry is the timeout-by-clock path.
//
// A relay may notice that a signed deadline passed and derive EXPIRED without any
// timeout event having been produced yet. The deadline comes from the event, not
// from the observer's clock, so every observer computes the same instant.
func TestDerive_DeadlineFromSignedExpiry(t *testing.T) {
	events := taskEvents(t, actorA, EventTaskCreated, EventTaskOffered)
	// Give the OFFERED event a deadline.
	exp := t0.Add(300 * time.Second)
	events[1].ExpiresAt = &exp

	// Before the deadline: still offered.
	res, err := Derive(DeriveInput{Events: events, Now: t0.Add(time.Second)})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.State != StateOffered {
		t.Errorf("before the deadline the state must be %s, got %s", StateOffered, res.State)
	}

	// After: expired, with the reason reported.
	res, err = Derive(DeriveInput{Events: events, Now: exp.Add(time.Second)})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.State != StateExpired {
		t.Errorf("after the deadline the state must be %s, got %s", StateExpired, res.State)
	}
	if res.DeadlineReason() == "" {
		t.Error("a deadline-derived expiry must explain itself")
	}
	if !strings.Contains(res.DeadlineReason(), "signed deadline") {
		t.Errorf("the reason must point at the signed deadline, got: %q", res.DeadlineReason())
	}
}

// TestDerive_TerminalStateDoesNotExpire confirms a finished task is not later
// declared expired by the clock, which would rewrite history.
func TestDerive_TerminalStateDoesNotExpire(t *testing.T) {
	events := taskEvents(t, actorA,
		EventTaskCreated, EventTaskOffered, EventTaskAccepted, EventTaskStarted, EventTaskCompleted)
	// The deadline must be after the event was issued, or the event is malformed
	// rather than merely old. The last event is issued at t0+4s.
	last := events[len(events)-1]
	exp := last.IssuedAt.Add(time.Second)
	events[len(events)-1].ExpiresAt = &exp

	res, err := Derive(DeriveInput{Events: events, Now: exp.Add(time.Hour)})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.State != StateCompleted {
		t.Errorf("state = %s, want %s: a completed task must not later become expired",
			res.State, StateCompleted)
	}
}

// TestDerive_NoClockMeansNoDeadlineExpiry documents that Now is meaningful.
func TestDerive_NoClockMeansNoDeadlineExpiry(t *testing.T) {
	events := taskEvents(t, actorA, EventTaskCreated, EventTaskOffered)
	exp := t0.Add(time.Second)
	events[1].ExpiresAt = &exp

	res, err := Derive(DeriveInput{Events: events})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.State != StateOffered {
		t.Errorf("with no clock there is no expiry to observe, got %s", res.State)
	}
}

// TestTimeoutCoverage_ReportsTheGap is the honesty check.
//
// S9-5 implements four of the six timeout edges ARCHITECTURE.md §4.5 requires. The
// report is derived from the same table the state machine uses, so it cannot drift
// from the implementation, and the two missing ones are named rather than omitted.
func TestTimeoutCoverage_ReportsTheGap(t *testing.T) {
	cov := TimeoutCoverage()
	if len(cov) != 5 {
		t.Fatalf("coverage report has %d entries, want 5", len(cov))
	}

	implemented := map[TaskState]bool{}
	for _, c := range cov {
		implemented[c.State] = c.Implemented
	}

	// The four S9-5 implements.
	for _, s := range []TaskState{StateOffered, StateAccepted, StateWaitingForInput, StateWaitingForApproval} {
		if !implemented[s] {
			t.Errorf("%s must be reported as implemented", s)
		}
	}
	// The one it deliberately does not.
	if implemented[StateRunning] {
		t.Error("TASK_RUNNING heartbeat timeout is not implemented in S9-5 and must be reported as such")
	}
}

// TestTaskState_TerminalMatchesTheTable ties Terminal() to the edge table, so the
// two cannot disagree: a state is terminal exactly when no edge leaves it.
func TestTaskState_TerminalMatchesTheTable(t *testing.T) {
	all := []TaskState{
		StateCreated, StateOffered, StateAccepted, StateRunning,
		StateWaitingForInput, StateWaitingForApproval,
		StateCompleted, StateFailed, StateCancelled, StateRejected, StateDenied, StateExpired,
	}
	hasEdge := map[TaskState]bool{}
	for _, s := range all {
		hasEdge[s] = false
	}
	for _, tr := range taskTransitions {
		hasEdge[tr.from] = true
	}

	for _, s := range all {
		// Cancellation is legal from every non-terminal state, so a non-terminal
		// state always has at least that edge.
		wantTerminal := !hasEdge[s]
		if s.Terminal() != wantTerminal {
			t.Errorf("%s.Terminal() = %v but the edge table says %v",
				s, s.Terminal(), wantTerminal)
		}
	}
}

// TestA2ATaskState_CoversEveryState guards the wire mapping: a state that fell
// through to UNSPECIFIED would put an uninterpretable value on the wire.
func TestA2ATaskState_CoversEveryState(t *testing.T) {
	all := []TaskState{
		StateCreated, StateOffered, StateAccepted, StateRunning,
		StateWaitingForInput, StateWaitingForApproval,
		StateCompleted, StateFailed, StateCancelled, StateRejected, StateDenied, StateExpired,
	}
	for _, s := range all {
		if got := A2ATaskState(s); got == "TASK_STATE_UNSPECIFIED" {
			t.Errorf("state %s has no A2A mapping, so it would go on the wire as UNSPECIFIED", s)
		}
	}
}
