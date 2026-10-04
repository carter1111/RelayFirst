package eventsign

import (
	"fmt"
	"time"

	"github.com/relayfirst/relayfirst/internal/a2a"
	"github.com/relayfirst/relayfirst/internal/delegation"
	"github.com/relayfirst/relayfirst/internal/delegationsign"
)

// This file wires delegation to events, which is what S13-3 was for.
//
// # Why it lives here and not in internal/a2a
//
// a2a has no cryptography and must keep it that way, because the node imports it. This function needs
// both the event signature and the grant's signature, so it belongs on the side that links eip712 —
// alongside Verify, whose output it consumes.
//
// # The two questions, and why the order matters
//
//  1. Did the actor named in this event sign it?    -> eventsign.Verify
//  2. Was that actor permitted to emit this event?  -> delegationsign.AuthorizeEvent
//
// They are checked in that order because the second needs the first's answer: authority is a question
// about a specific signer, so establishing which signer before asking whether it may act is the only
// coherent sequence. It also means a forged signature is reported as a signature problem rather than
// as an authority one, which is what an operator needs to know.

// AuthorizeInput is what AuthorizeEventWithGrant needs.
type AuthorizeInput struct {
	// Event is the event to authorize.
	Event a2a.Event

	// Grant is the delegation the actor claims to act under.
	//
	// # Why a grant and not a session key
	//
	// The grant carries the scopes, the window and the nonce, so an authorization decision needs all
	// of it. A caller that passed only a key would have to re-derive the rest, and the re-derivation
	// is where an authorization bypass hides.
	Grant delegationsign.SignedGrant

	// Scope is the action the event represents.
	//
	// # Why the caller supplies it rather than the event implying it
	//
	// Mapping every event type to a scope inside this package would mean adding a case each time an
	// event type is added, and a forgotten case is an unclassified event that some default would
	// then cover. Making the caller state the scope means an unclassified event is a compile-time
	// absence rather than a runtime default.
	Scope delegation.Scope

	// CurrentNonce is the owner's latest nonce for the grant's session key.
	//
	// # Why revocation needs it and cannot be checked without it
	//
	// Revocation is a counter comparison, so this package cannot know whether a grant is live: the
	// caller supplies the freshest nonce it has. A caller with a stale one cannot tell a revoked
	// grant from a live one, which is stated on the field rather than hidden, because a caller that
	// believed otherwise would treat revocation as instantaneous.
	CurrentNonce uint64

	// Now is the instant to test the grant's window against.
	//
	// It is passed in rather than read here for the same reason a2a.Derive takes it: reading a clock
	// hides which one was used, and two verifiers applying different clocks would disagree about
	// whether a grant was live.
	Now time.Time
}

// AuthorizeEventWithGrant checks a delegated event and returns the signer.
//
// # What a caller should do with the error
//
// Treat every failure as a refusal. The errors are distinguishable enough to log usefully — a
// signature problem reads differently from an expired grant — but none of them means "proceed".
//
// # What this does NOT establish
//
// That the event's content is true or that the actor's work is valid. It establishes that a specific
// actor signed this event and that the owner of the identity had delegated that actor the authority
// to emit this class of event. Everything else is separate, and the separation is the point: an
// authorization check that also claimed to validate content would be doing two jobs and would get
// one of them wrong.
func AuthorizeEventWithGrant(in AuthorizeInput) ([]byte, error) {
	// Order matters: the signer must be established before asking whether it may act.
	signer, err := Verify(in.Event)
	if err != nil {
		return nil, err
	}

	// The grant's own signature, revocation and window are all checked inside AuthorizeEvent, which
	// exists as one function precisely so a caller cannot omit one of them.
	if err := in.Grant.AuthorizeEvent(in.Scope, signer, in.CurrentNonce, in.Now); err != nil {
		return nil, fmt.Errorf("eventsign: event %s by %s: %w", in.Event.EventID, in.Event.Actor, err)
	}
	return signer, nil
}

// ScopeOfEvent maps an event type to the scope that covers it.
//
// # Why this is a function with no default
//
// Every branch returns an explicit scope or an error. There is deliberately no fallback: a default
// would silently cover a new event type with whatever scope the default named, and the failure would
// be an event authorized by a grant that was never meant to cover it. An unrecognized type is an
// error the caller must resolve, which is the same refusal-by-default as AuthorizeEventWithGrant.
//
// # Why unknown types are an error rather than a skip
//
// `a2a.Event.Type.Known()` reports false for a type from a newer build, and those must still be
// STORABLE (A9 §①). But they cannot be AUTHORIZED, because this build does not know what authority
// they require. So the distinction is: storage tolerates the unknown, authorization refuses it.
func ScopeOfEvent(t a2a.EventType) (delegation.Scope, error) {
	switch t {
	case a2a.EventSessionOpen, a2a.EventSessionClose:
		return delegation.ScopeSessionEvent, nil

	case a2a.EventTaskProgress, a2a.EventTaskWaitingForInput,
		a2a.EventTaskInputProvided, a2a.EventTaskWaitingForApprov:
		return delegation.ScopeTaskProgress, nil

	case a2a.EventTaskCreated, a2a.EventTaskOffered, a2a.EventTaskAccepted,
		a2a.EventTaskRejected, a2a.EventTaskStarted, a2a.EventTaskApproved,
		a2a.EventTaskDenied, a2a.EventTaskCompleted, a2a.EventTaskFailed,
		a2a.EventTaskCancelled:
		return delegation.ScopeTaskLifecycle, nil

	default:
		// Timeout events land here too, and that is intentional. A timeout is emitted by a relay
		// noticing a signed deadline passed (ARCHITECTURE.md §4.5), not on an owner's behalf, so no
		// session scope covers it — a delegated key must not be able to expire someone's task.
		return "", fmt.Errorf(
			"eventsign: event type %q has no delegable scope; a delegated key may not emit it "+
				"(if it should be delegable, add it to ScopeOfEvent deliberately)", t)
	}
}
