package a2a

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/agentid"
)

// This file defines the RelayFirst event layer (S9-6, ARCHITECTURE.md §4.4).
//
// # Why the event is the unit, and the state is derived
//
// A task's state is not stored; it is computed from the signed events that
// describe it. That is what makes the protocol verifiable: a client that has the
// events can derive the same state as anyone else, without trusting a server. A
// stored state would be a claim; a derived state is a proof.
//
// # The ordering problem, stated honestly
//
// ARCHITECTURE.md §4.2 is explicit that there is NO global order across relays,
// and that the authoritative ordering comes from:
//
//	1. per-actor `sequence`   — dedup and replay detection within one actor
//	2. per-actor hash chain   — the order of one actor's own events
//	3. the protocol rules     — applied to that partial order
//
// So this package never reads an arrival time or a relay sequence. The only time
// it trusts is the `IssuedAt` inside the signed event. If it used arrival order,
// two relays would derive two different task states from the same events, which
// is a state-machine fork — the exact failure A9 exists to prevent.
//
// # What is not claimed
//
// Cross-actor causality is NOT captured: a per-actor chain says nothing about
// whether actor A's event happened before actor B's. Derive() therefore resolves
// concurrent events with a deterministic tiebreak (see task.go) rather than
// pretending to know the truth. The tiebreak is stable and reproducible, which is
// what determinism requires; it is not a claim about physical ordering.

// EventType is one entry of the event table in ARCHITECTURE.md §4.4.
//
// The set here is the subset S9-6 needs for a full task lifecycle. The remaining
// entries in that table (delegation, payment, settlement) belong to stages that do
// not exist yet, and inventing their constants now would create names with no
// behaviour behind them.
type EventType string

// Session lifecycle (S9-4).
const (
	EventSessionOpen  EventType = "SESSION_OPEN"
	EventSessionClose EventType = "SESSION_CLOSE"
)

// Task lifecycle (S9-5).
const (
	EventTaskCreated  EventType = "TASK_CREATED"
	EventTaskOffered  EventType = "TASK_OFFERED"
	EventTaskAccepted EventType = "TASK_ACCEPTED"
	EventTaskRejected EventType = "TASK_REJECTED"
	EventTaskStarted  EventType = "TASK_STARTED"
	EventTaskProgress EventType = "TASK_PROGRESS"

	EventTaskWaitingForInput  EventType = "TASK_WAITING_FOR_INPUT"
	EventTaskInputProvided    EventType = "TASK_INPUT_PROVIDED"
	EventTaskWaitingForApprov EventType = "TASK_WAITING_FOR_APPROVAL"
	EventTaskApproved         EventType = "TASK_APPROVED"
	EventTaskDenied           EventType = "TASK_DENIED"

	EventTaskCompleted EventType = "TASK_COMPLETED"
	EventTaskFailed    EventType = "TASK_FAILED"
	EventTaskCancelled EventType = "TASK_CANCELLED"
)

// Timeout events. ARCHITECTURE.md §4.5 requires that every non-terminal state has
// a timeout edge, because a task that can hang forever is a task whose stake can
// hang forever. S9-5 implements four of them; the names exist for all six so the
// state machine can name the missing ones explicitly rather than silently
// omitting them.
const (
	EventOfferTTLExpire         EventType = "OFFER_TTL_EXPIRE"
	EventAcceptStartTTLExpire   EventType = "ACCEPT_START_TTL_EXPIRE"
	EventRunningHeartbeatExpire EventType = "RUNNING_HEARTBEAT_TTL_EXPIRE"
	EventInputTTLExpire         EventType = "INPUT_TTL_EXPIRE"
	EventApprovalExpire         EventType = "APPROVAL_EXPIRE"
	EventDisputeTTLExpire       EventType = "DISPUTE_TTL_EXPIRE"
)

// Hasher hashes canonical event bytes.
//
// # Why this is a parameter and not a call
//
// A hash chain needs keccak256, which lives in internal/eip712 — a package that
// links secp256k1. This package must not import it, because the node serves events
// and a node that linked signing code could forge (MVP.md §7.1). Injecting the
// function keeps the dependency out while still letting the chain be validated by
// whoever has a real hasher.
type Hasher func([]byte) []byte

// Event is one signed protocol event.
//
// # What is inside the signature, and what is not
//
// Everything here is covered by the event's signature except the signature itself
// and the chain fields the *receiver* verifies (Sequence and PreviousEventHash are
// inside — they are the actor's own claims about order, and an actor that could
// renumber its events after signing could rewrite its own history).
//
// The fields deliberately absent are the relay's: arrival time and relay sequence.
// They are not part of the protocol object (ARCHITECTURE.md §4.4, ephemeral
// signals), so nothing here can accidentally depend on them.
type Event struct {
	// EventID is unique per event. It is the idempotency key.
	EventID string `json:"eventId"`

	// SessionID and TaskID scope the event. TaskID is empty for session-level
	// events such as SESSION_OPEN.
	SessionID string `json:"sessionId"`
	TaskID    string `json:"taskId,omitempty"`

	// Actor is the agentId of the signer. Every event has exactly one actor: the
	// party that signed it.
	Actor string `json:"actor"`

	// Type is the event type.
	Type EventType `json:"type"`

	// Sequence is the actor's own counter. It must start at 1 and increase by one
	// per event from that actor, which is what makes a gap or a replay detectable.
	//
	// Starting at 1 rather than 0 makes "unset" distinguishable from "first": a
	// zero value here is an error, not a valid first event.
	Sequence uint64 `json:"sequence"`

	// PreviousEventHash links to the actor's preceding event, or is empty for
	// sequence 1. It chains one actor's own events, which is the only ordering
	// the protocol can prove on its own.
	PreviousEventHash string `json:"previousEventHash,omitempty"`

	// IssuedAt and ExpiresAt are the signed times.
	//
	// ExpiresAt is the deadline for the state this event creates, and it is what
	// the timeout edges are computed from — never the receiver's clock. A relay
	// may only *notice* that a deadline passed and emit a new signed timeout
	// event; it cannot decide the transition itself (ARCHITECTURE.md §4.5).
	IssuedAt  time.Time  `json:"issuedAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`

	// Payload is the event-specific body. It is carried as raw JSON so an unknown
	// event type from a newer actor can still be stored and re-served without this
	// build having to understand it (the same forward-compatibility rule as the
	// envelope and the receipt).
	Payload json.RawMessage `json:"payload,omitempty"`

	// Signature is the actor's signature over the canonical bytes. It is excluded
	// from those bytes, for the obvious reason.
	Signature string `json:"signature,omitempty"`

	// Raw holds the bytes this event was received as, when it came off the wire.
	//
	// It is unexported and json:"-" on purpose: it is transport state, not protocol
	// state. Serializing it would put an event's own bytes inside its own encoding,
	// and the two builds would then disagree about how to wrap them. Use WithRaw or
	// DecodeEvent to set it; SignedBytes explains why it takes precedence.
	Raw json.RawMessage `json:"-"`
}

// CanonicalBytes renders the event deterministically for hashing and signing.
//
// # Why this is safe to compute with encoding/json
//
// Go marshals struct fields in declaration order and map keys in sorted order, so
// the output is a pure function of the value. That is enough for a hash chain
// within one build. It is NOT the same guarantee as the receipt's frozen payload,
// and it does not need to be: an event is hashed only by the actor that just
// created it and by a verifier that received its bytes, and the verifier checks
// the signature over the bytes it received rather than re-deriving them.
//
// Signature is blanked rather than omitted so the field list is identical whether
// the event is signed or not, which keeps the unsigned form and the signed form
// hashable by the same code path.
func (e Event) CanonicalBytes() ([]byte, error) {
	clone := e
	clone.Signature = ""
	return json.Marshal(clone)
}

// EventHash returns the chain hash of an event, using h.
//
// Returning an error only for a serialization failure keeps call sites honest:
// a nil Hasher is a programming error and panics rather than silently producing an
// unverifiable chain.
//
// # Why this hashes the verbatim bytes when they are present (A9 §④)
//
// CanonicalBytes re-serializes the struct, which means it hashes a *reconstruction*
// of the event rather than the event. That is fine for an event this build just
// created, and wrong for one it received, for a reason that is easy to miss:
// re-serializing silently DROPS any field this build does not know. A newer actor's
// event carrying an extra field would decode, re-encode without that field, and
// hash to the same value as the original — because the hash was never computed over
// the field either way.
//
// The consequence is that a node which re-serialized an event would destroy the
// newer field while every chain check still passed. Nothing would fail; the data
// would just be gone. So when the event carries the bytes it was received as, those
// bytes are what get hashed, and the chain covers what was actually sent.
//
// This mirrors the receipt's frozen payload exactly. The reconstruction path stays
// for events built in memory, where there are no received bytes to prefer.
func EventHash(h Hasher, e Event) (string, error) {
	if h == nil {
		panic("a2a: EventHash requires a Hasher; a nil hasher would produce an unverifiable chain")
	}
	raw, err := e.SignedBytes()
	if err != nil {
		return "", err
	}
	return "0x" + hexLower(h(raw)), nil
}

// SignedBytes returns the bytes an event's hash and signature cover.
//
// # The two paths, and which one is authoritative
//
//   - When Raw is set — the event was received, and Raw is the bytes it arrived as
//     — Raw wins. Hashing the received bytes is what makes the chain cover unknown
//     fields and survive a re-serializing intermediary (A9 §④).
//   - Otherwise the event was built in memory and CanonicalBytes reconstructs it.
//
// Raw is deliberately NOT part of the struct's JSON: it is transport state, not
// protocol state. If it were serialized, an event's own bytes would contain its
// own bytes, and two builds would disagree about the encoding of the wrapper.
func (e Event) SignedBytes() ([]byte, error) {
	if len(e.Raw) > 0 {
		return e.Raw, nil
	}
	raw, err := e.CanonicalBytes()
	if err != nil {
		return nil, fmt.Errorf("a2a: canonicalize event: %w", err)
	}
	return raw, nil
}

// WithRaw attaches the received bytes to a decoded event.
//
// # Why decoding does not do this automatically
//
// Because a caller may decode an event it intends to modify, and a stale Raw would
// then be hashed instead of the modification — a silent wrong answer. Making the
// attachment explicit forces the caller to say "these are the bytes I received",
// which is the only situation where it is true.
func WithRaw(e Event, raw []byte) Event {
	e.Raw = append([]byte(nil), raw...)
	return e
}

// DecodeEvent parses an event and binds the bytes it was parsed from.
//
// It is the safe way to read an event off the wire: the returned value hashes and
// verifies against exactly the bytes supplied, including any field this build does
// not understand.
func DecodeEvent(raw []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(raw, &e); err != nil {
		return Event{}, fmt.Errorf("a2a: decode event: %w", err)
	}
	return WithRaw(e, raw), nil
}

// ValidateEvent checks an event's own shape, without looking at the chain.
//
// It does not verify the signature: that needs the actor's key and belongs to the
// signing layer, for the same reason card proofs live in internal/publish.
func ValidateEvent(e Event) error {
	if strings.TrimSpace(e.EventID) == "" {
		return fmt.Errorf("a2a: event has no id")
	}
	if strings.TrimSpace(e.SessionID) == "" {
		return fmt.Errorf("a2a: event %s has no session id", e.EventID)
	}
	if _, err := agentid.Parse(e.Actor); err != nil {
		return fmt.Errorf("a2a: event %s actor: %w", e.EventID, err)
	}
	if e.Sequence == 0 {
		return fmt.Errorf("a2a: event %s has sequence 0; sequences start at 1", e.EventID)
	}
	// An unknown event TYPE is not checked here on purpose. A type this build does
	// not recognize comes from a newer one, and rejecting it would make a node
	// silently truncate a history a newer reader can still parse (A9 §①). Whether
	// the type is *usable* is a different question, answered by Type.Known() and
	// reported by Derive as Unknown rather than as an error.
	//
	// Task-scoping is only enforced for types we do understand, because we cannot
	// know a newer type's scoping rules. Applying ours to it would be a guess.
	if e.Sequence == 1 && e.PreviousEventHash != "" {
		return fmt.Errorf("a2a: event %s is sequence 1 but claims a predecessor", e.EventID)
	}
	if e.Sequence > 1 && e.PreviousEventHash == "" {
		return fmt.Errorf("a2a: event %s is sequence %d but claims no predecessor", e.EventID, e.Sequence)
	}
	if e.IssuedAt.IsZero() {
		return fmt.Errorf("a2a: event %s has no issuedAt", e.EventID)
	}
	if e.ExpiresAt != nil && e.ExpiresAt.Before(e.IssuedAt) {
		return fmt.Errorf("a2a: event %s expires before it was issued", e.EventID)
	}
	// Task-scoped events must name a task; session-scoped ones must not pretend to.
	// Only for types we understand: we cannot know a newer type's scoping rules.
	if e.Type.Known() && e.Type.taskScoped() && strings.TrimSpace(e.TaskID) == "" {
		return fmt.Errorf("a2a: event %s (%s) is task-scoped but has no task id", e.EventID, e.Type)
	}
	return nil
}

// Known reports whether t is an event type this build understands.
//
// An unknown type is not malformed — it is a type from a newer build. Callers that
// store events must keep them (A9 §①: a node that dropped events it did not
// understand would silently truncate a history someone else can still read), and
// callers that derive state must report the unknown rather than guess.
func (t EventType) Known() bool {
	_, ok := knownEventTypes[t]
	return ok
}

// taskScoped reports whether t must name a task.
func (t EventType) taskScoped() bool {
	switch t {
	case EventSessionOpen, EventSessionClose:
		return false
	default:
		return true
	}
}

// Terminal reports whether t ends the task's life.
func (t EventType) Terminal() bool {
	switch t {
	case EventTaskCompleted, EventTaskFailed, EventTaskCancelled, EventTaskRejected, EventTaskDenied:
		return true
	default:
		return false
	}
}

var knownEventTypes = map[EventType]struct{}{
	EventSessionOpen: {}, EventSessionClose: {},
	EventTaskCreated: {}, EventTaskOffered: {}, EventTaskAccepted: {}, EventTaskRejected: {},
	EventTaskStarted: {}, EventTaskProgress: {},
	EventTaskWaitingForInput: {}, EventTaskInputProvided: {},
	EventTaskWaitingForApprov: {}, EventTaskApproved: {}, EventTaskDenied: {},
	EventTaskCompleted: {}, EventTaskFailed: {}, EventTaskCancelled: {},
	EventOfferTTLExpire: {}, EventAcceptStartTTLExpire: {}, EventRunningHeartbeatExpire: {},
	EventInputTTLExpire: {}, EventApprovalExpire: {}, EventDisputeTTLExpire: {},
}

// ChainError describes a per-actor chain violation.
//
// The cases are separated because they mean different things to an operator: a gap
// is a missing event, a replay is a duplicate, and a fork is one actor having
// signed two different histories at the same sequence. Collapsing them into one
// "invalid chain" would hide which of the three happened.
type ChainError struct {
	Actor  string
	Reason string
	// Kind is one of "gap", "replay", "fork", "predecessor".
	Kind string
}

func (e *ChainError) Error() string {
	return fmt.Sprintf("a2a: event chain for %s: %s (%s)", e.Actor, e.Reason, e.Kind)
}

// ValidateChain checks every actor's own sequence and hash links.
//
// # What this proves
//
// For each actor independently: the events are contiguous from 1, each links to
// its predecessor's hash, and no sequence number appears twice with different
// content. That is exactly the ordering the protocol can prove on its own
// (ARCHITECTURE.md §4.2) — nothing more, and nothing less.
//
// # What it deliberately does not check
//
// It does not order events across actors, because the protocol has no basis for
// that. A caller wanting a task state must call Derive, which applies the state
// machine's deterministic tiebreak.
//
// It does not verify signatures either: it hashes bytes with the supplied Hasher,
// which is a consistency check, not an authentication.
func ValidateChain(h Hasher, events []Event) error {
	byActor := map[string][]Event{}
	for _, e := range events {
		byActor[e.Actor] = append(byActor[e.Actor], e)
	}

	// Validate shape in a deterministic order, not in slice order.
	//
	// The error message must not depend on how the events arrived: a relay that
	// received them in a different order would report a different failure for the
	// same bad data, which reads as flakiness and sends an operator hunting a
	// problem that is not there. Sorting first makes the reported event a function
	// of the set, not of the slice.
	ordered := protocolOrder(events)
	for _, e := range ordered {
		if err := ValidateEvent(e); err != nil {
			return err
		}
	}

	// Iterate actors in sorted order so an error is reported for the same actor
	// every time, regardless of the order events arrived in. A non-deterministic
	// error message would make a flaky-looking failure out of a real one.
	actors := make([]string, 0, len(byActor))
	for a := range byActor {
		actors = append(actors, a)
	}
	sort.Strings(actors)

	for _, actor := range actors {
		chain := byActor[actor]
		sort.Slice(chain, func(i, j int) bool { return chain[i].Sequence < chain[j].Sequence })

		var prevHash string
		for i, e := range chain {
			wantSeq := uint64(i + 1)
			if e.Sequence != wantSeq {
				kind := "gap"
				if e.Sequence < wantSeq {
					kind = "replay"
				}
				return &ChainError{
					Actor: actor,
					Kind:  kind,
					Reason: fmt.Sprintf("expected sequence %d but found %d; "+
						"a gap means an event is missing and a repeat means one was replayed",
						wantSeq, e.Sequence),
				}
			}
			if e.PreviousEventHash != prevHash {
				// Sequence 1 must have an empty predecessor, which is also the
				// initial prevHash, so this single comparison covers both the
				// first event and every later link.
				return &ChainError{
					Actor: actor,
					Kind:  "predecessor",
					Reason: fmt.Sprintf("sequence %d links to %q but the predecessor hashes to %q",
						e.Sequence, e.PreviousEventHash, prevHash),
				}
			}
			h2, err := EventHash(h, e)
			if err != nil {
				return err
			}
			prevHash = h2
		}
	}
	return nil
}

// hexLower renders bytes as lowercase hex without a 0x prefix.
//
// It is local so this package does not import encoding/hex for one call and, more
// importantly, so there is one spelling of "how we render a hash" rather than two
// that could disagree about case.
func hexLower(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}
