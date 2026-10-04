package eventsign_test

import (
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/a2a"
	"github.com/relayfirst/relayfirst/internal/delegation"
	"github.com/relayfirst/relayfirst/internal/delegationsign"
	"github.com/relayfirst/relayfirst/internal/eip712"
	"github.com/relayfirst/relayfirst/internal/eventsign"
)

// These tests cover the event signing layer and the delegation wiring it enables.
//
// Two things matter here beyond correctness. The first is that delegation is now REACHABLE: before
// this, AuthorizeEvent existed with nothing able to call it, so the mechanism was complete and
// unusable. The second is that the two questions — "did this actor sign it" and "was this actor
// allowed to" — stay distinguishable, because they fail for different reasons and an operator needs
// to know which one they have.

const (
	ownerKey   = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	sessionKey = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	otherKey   = "0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
	chainID    = uint64(8453)
)

func idFor(t *testing.T, key string) string {
	t.Helper()
	priv, err := eip712.PrivateKeyFromHex(key)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	addr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	return "agent:eip155:8453:" + eip712.AddressToHex(addr)
}

// eventFor builds a valid unsigned event for an actor.
func eventFor(t *testing.T, actor string, typ a2a.EventType, seq uint64, prev string) a2a.Event {
	t.Helper()
	e := a2a.Event{
		EventID:           "evt-" + string(typ),
		SessionID:         "ses-1",
		Actor:             actor,
		Type:              typ,
		Sequence:          seq,
		PreviousEventHash: prev,
		IssuedAt:          time.Unix(1791015800, 0),
	}
	if typ != a2a.EventSessionOpen && typ != a2a.EventSessionClose {
		e.TaskID = "tsk-1"
	}
	return e
}

// TestSignAndVerify is the control. Without it the rejection tests would pass for a scheme that
// never works.
func TestSignAndVerify(t *testing.T) {
	e := eventFor(t, idFor(t, sessionKey), a2a.EventSessionOpen, 1, "")
	signed, err := eventsign.Sign(e, sessionKey, chainID)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if signed.Signature == "" {
		t.Fatal("the event must carry a signature")
	}

	signer, err := eventsign.Verify(signed)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// And the recovered signer is the session key's address.
	want, err := eip712.HexToAddress(strings.TrimPrefix(idFor(t, sessionKey), "agent:eip155:8453:"))
	if err != nil {
		t.Fatalf("parse want: %v", err)
	}
	if len(signer) != len(want) {
		t.Fatalf("signer length %d, want %d", len(signer), len(want))
	}
	for i := range want {
		if signer[i] != want[i] {
			t.Fatal("the recovered signer must be the signing key's address")
		}
	}
}

// TestSign_RefusesMismatchedKey keeps an unverifiable event from being produced.
func TestSign_RefusesMismatchedKey(t *testing.T) {
	e := eventFor(t, idFor(t, sessionKey), a2a.EventSessionOpen, 1, "")
	if _, err := eventsign.Sign(e, ownerKey, chainID); err == nil {
		t.Fatal("signing with a key that does not match the event's actor must fail")
	}
}

// TestVerify_RejectsTampering is the integrity property: any change invalidates the signature.
func TestVerify_RejectsTampering(t *testing.T) {
	e := eventFor(t, idFor(t, sessionKey), a2a.EventSessionOpen, 1, "")
	signed, err := eventsign.Sign(e, sessionKey, chainID)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*a2a.Event)
	}{
		{"type changed", func(x *a2a.Event) { x.Type = a2a.EventSessionClose }},
		{"session changed", func(x *a2a.Event) { x.SessionID = "ses-other" }},
		{"actor changed", func(x *a2a.Event) { x.Actor = idFor(t, ownerKey) }},
		{"issuedAt changed", func(x *a2a.Event) { x.IssuedAt = x.IssuedAt.Add(time.Hour) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := signed
			c.mutate(&x)
			if _, err := eventsign.Verify(x); err == nil {
				t.Errorf("an event with %s must not verify", c.name)
			}
		})
	}
}

func TestVerify_RejectsUnsignedAndMalformed(t *testing.T) {
	e := eventFor(t, idFor(t, sessionKey), a2a.EventSessionOpen, 1, "")
	if _, err := eventsign.Verify(e); err == nil {
		t.Error("an unsigned event must not verify")
	}

	signed, err := eventsign.Sign(e, sessionKey, chainID)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	signed.Signature = "not-hex"
	if _, err := eventsign.Verify(signed); err == nil {
		t.Error("a non-hex signature must not verify")
	}
	signed.Signature = "0xabcd"
	if _, err := eventsign.Verify(signed); err == nil {
		t.Error("a short signature must not verify")
	}
}

// grantFor issues a grant from the owner to the session key with the given scopes.
func grantFor(t *testing.T, scopes []delegation.Scope) delegationsign.SignedGrant {
	t.Helper()
	g := delegationsign.SignedGrant{
		Grant: delegation.Grant{
			Owner:      idFor(t, ownerKey),
			SessionKey: idFor(t, sessionKey),
			Scopes:     scopes,
			ValidFrom:  time.Unix(1791015800, 0),
			ValidUntil: time.Unix(1791019400, 0),
			Nonce:      1,
		},
	}
	if err := g.Sign(ownerKey, chainID); err != nil {
		t.Fatalf("sign grant: %v", err)
	}
	return g
}

// TestAuthorizeEventWithGrant_AcceptsADelegatedEvent is the wiring proof.
//
// Before this layer existed, AuthorizeEvent had no caller: the delegation mechanism was complete and
// unreachable. This is the first path that reaches it.
func TestAuthorizeEventWithGrant_AcceptsADelegatedEvent(t *testing.T) {
	e := eventFor(t, idFor(t, sessionKey), a2a.EventSessionOpen, 1, "")
	signed, err := eventsign.Sign(e, sessionKey, chainID)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	grant := grantFor(t, []delegation.Scope{delegation.ScopeSessionEvent})
	if _, err := eventsign.AuthorizeEventWithGrant(eventsign.AuthorizeInput{
		Event: signed, Grant: grant, Scope: delegation.ScopeSessionEvent,
		CurrentNonce: 1, Now: time.Unix(1791015900, 0),
	}); err != nil {
		t.Fatalf("a properly delegated session event must be authorized: %v", err)
	}
}

// TestAuthorizeEventWithGrant_RefusesAnUndelegatedScope is the authority check.
func TestAuthorizeEventWithGrant_RefusesAnUndelegatedScope(t *testing.T) {
	// A session event, but the grant covers only task progress.
	e := eventFor(t, idFor(t, sessionKey), a2a.EventSessionOpen, 1, "")
	signed, err := eventsign.Sign(e, sessionKey, chainID)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	grant := grantFor(t, []delegation.Scope{delegation.ScopeTaskProgress})
	_, err = eventsign.AuthorizeEventWithGrant(eventsign.AuthorizeInput{
		Event: signed, Grant: grant, Scope: delegation.ScopeSessionEvent,
		CurrentNonce: 1, Now: time.Unix(1791015900, 0),
	})
	if err == nil {
		t.Fatal("an event outside the granted scopes must be refused")
	}
	if !strings.Contains(err.Error(), "not granted") {
		t.Errorf("the refusal must name the scope problem, got: %v", err)
	}
}

// TestAuthorizeEventWithGrant_RefusesASignerTheGrantDidNotName is the boundary that makes the grant
// meaningful: another key's valid signature is not authority.
func TestAuthorizeEventWithGrant_RefusesASignerTheGrantDidNotName(t *testing.T) {
	// An event signed by a THIRD key, with a grant naming the session key.
	e := eventFor(t, idFor(t, otherKey), a2a.EventSessionOpen, 1, "")
	signed, err := eventsign.Sign(e, otherKey, chainID)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	grant := grantFor(t, []delegation.Scope{delegation.ScopeSessionEvent})
	_, err = eventsign.AuthorizeEventWithGrant(eventsign.AuthorizeInput{
		Event: signed, Grant: grant, Scope: delegation.ScopeSessionEvent,
		CurrentNonce: 1, Now: time.Unix(1791015900, 0),
	})
	if err == nil {
		t.Fatal("a signature from a key the grant did not name must be refused")
	}
	if !strings.Contains(err.Error(), "not this grant's session key") {
		t.Errorf("the refusal must name the signer problem, got: %v", err)
	}
}

// TestAuthorizeEventWithGrant_RefusesSignedButRevoked keeps the nonce check reachable through the
// wiring, since a caller could otherwise forget to pass a fresh nonce.
func TestAuthorizeEventWithGrant_RefusesSignedButRevoked(t *testing.T) {
	e := eventFor(t, idFor(t, sessionKey), a2a.EventSessionOpen, 1, "")
	signed, err := eventsign.Sign(e, sessionKey, chainID)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	grant := grantFor(t, []delegation.Scope{delegation.ScopeSessionEvent})

	// The owner has published nonce+1.
	_, err = eventsign.AuthorizeEventWithGrant(eventsign.AuthorizeInput{
		Event: signed, Grant: grant, Scope: delegation.ScopeSessionEvent,
		CurrentNonce: 2, Now: time.Unix(1791015900, 0),
	})
	if err == nil {
		t.Fatal("a revoked grant must be refused")
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Errorf("the refusal must name revocation, got: %v", err)
	}
}

// TestAuthorizeEventWithGrant_SignatureProblemIsNotAnAuthorityProblem keeps the two questions
// distinguishable, because they fail for different reasons.
func TestAuthorizeEventWithGrant_SignatureProblemIsNotAnAuthorityProblem(t *testing.T) {
	e := eventFor(t, idFor(t, sessionKey), a2a.EventSessionOpen, 1, "")
	signed, err := eventsign.Sign(e, sessionKey, chainID)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Break the signature AFTER signing, so the grant is fine and only the signature is wrong.
	signed.IssuedAt = signed.IssuedAt.Add(time.Second)

	grant := grantFor(t, []delegation.Scope{delegation.ScopeSessionEvent})
	_, err = eventsign.AuthorizeEventWithGrant(eventsign.AuthorizeInput{
		Event: signed, Grant: grant, Scope: delegation.ScopeSessionEvent,
		CurrentNonce: 1, Now: time.Unix(1791015900, 0),
	})
	if err == nil {
		t.Fatal("a tampered event must be refused")
	}
	if strings.Contains(err.Error(), "not granted") || strings.Contains(err.Error(), "revoked") {
		t.Errorf("a signature failure must not be reported as an authority failure, got: %v", err)
	}
}

// TestAuthorizeEventWithGrant_HasTwoIndependentRefusals records a defence-in-depth property that a
// mutation revealed.
//
// # What the mutation showed
//
// Removing the `if err != nil { return }` after Verify did NOT fail any test. The reason is that
// Verify returns a nil signer on failure, and AuthorizeEvent then refuses because a nil signer is not
// the grant's session key — so the authority check catches what the signature check let through.
//
// That is good design rather than a gap: two independent refusals mean a bug in one is not an
// authorization bypass. But it also means "skip Verify" is not by itself observable, so the property
// is asserted here explicitly rather than left implicit. Anything that made BOTH paths permissive
// fails loudly, which M1b confirmed.
func TestAuthorizeEventWithGrant_HasTwoIndependentRefusals(t *testing.T) {
	// An unsigned event. Verify rejects it; if that rejection were ignored, the nil signer would not
	// match the grant's session key either.
	unsigned := eventFor(t, idFor(t, sessionKey), a2a.EventSessionOpen, 1, "")
	grant := grantFor(t, []delegation.Scope{delegation.ScopeSessionEvent})

	_, err := eventsign.AuthorizeEventWithGrant(eventsign.AuthorizeInput{
		Event: unsigned, Grant: grant, Scope: delegation.ScopeSessionEvent,
		CurrentNonce: 1, Now: time.Unix(1791015900, 0),
	})
	if err == nil {
		t.Fatal("an unsigned event must be refused")
	}
	// And the refusal names the signature, not the authority: the first check to fail should be the
	// one reported, or an operator would look for a grant problem that is not there.
	if !strings.Contains(err.Error(), "no signature") {
		t.Errorf("the refusal should come from the signature check first, got: %v", err)
	}
}

// TestScopeOfEvent_ClassifiesEveryDelegableType keeps the mapping from drifting.
func TestScopeOfEvent_ClassifiesEveryDelegableType(t *testing.T) {
	cases := map[a2a.EventType]delegation.Scope{
		a2a.EventSessionOpen:          delegation.ScopeSessionEvent,
		a2a.EventSessionClose:         delegation.ScopeSessionEvent,
		a2a.EventTaskProgress:         delegation.ScopeTaskProgress,
		a2a.EventTaskWaitingForInput:  delegation.ScopeTaskProgress,
		a2a.EventTaskInputProvided:    delegation.ScopeTaskProgress,
		a2a.EventTaskWaitingForApprov: delegation.ScopeTaskProgress,
		a2a.EventTaskCreated:          delegation.ScopeTaskLifecycle,
		a2a.EventTaskOffered:          delegation.ScopeTaskLifecycle,
		a2a.EventTaskAccepted:         delegation.ScopeTaskLifecycle,
		a2a.EventTaskCompleted:        delegation.ScopeTaskLifecycle,
	}
	for typ, want := range cases {
		got, err := eventsign.ScopeOfEvent(typ)
		if err != nil {
			t.Errorf("ScopeOfEvent(%s) errored: %v", typ, err)
			continue
		}
		if got != want {
			t.Errorf("ScopeOfEvent(%s) = %s, want %s", typ, got, want)
		}
	}
}

// TestScopeOfEvent_RefusesWhatIsNotDelegable is the refusal-by-default rule.
//
// There is no fallback scope, so an event type nobody classified cannot be authorized. A default
// would silently cover a new type with whatever scope it named, and the failure would be an event
// authorized by a grant never meant to cover it.
func TestScopeOfEvent_RefusesWhatIsNotDelegable(t *testing.T) {
	notDelegable := []a2a.EventType{
		// A timeout is emitted by a relay noticing a signed deadline passed, not on an owner's
		// behalf: a delegated key must not be able to expire someone's task.
		a2a.EventOfferTTLExpire,
		a2a.EventAcceptStartTTLExpire,
		a2a.EventApprovalExpire,
		a2a.EventInputTTLExpire,
		// A type from a newer build cannot be authorized by a build that does not know what
		// authority it requires. It must still be STORABLE (A9 §①), which is a different question.
		a2a.EventType("TASK_FROM_THE_FUTURE"),
	}
	for _, typ := range notDelegable {
		if _, err := eventsign.ScopeOfEvent(typ); err == nil {
			t.Errorf("ScopeOfEvent(%s) must refuse rather than fall back to a scope", typ)
		}
	}
}

// TestScopeOfEvent_NeverReturnsAReceiptScope is the hard gate reached through the wiring.
//
// The delegation package has no receipt scope at all, so this can only fail if someone added one.
// Checking it here as well means the two layers both refuse.
func TestScopeOfEvent_NeverReturnsAReceiptScope(t *testing.T) {
	for _, typ := range []a2a.EventType{
		a2a.EventTaskCompleted, a2a.EventTaskStarted, a2a.EventSessionOpen,
	} {
		scope, err := eventsign.ScopeOfEvent(typ)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(string(scope)), "receipt") {
			t.Fatalf("ScopeOfEvent(%s) = %q, which names a receipt: a delegable receipt is how one "+
				"leaked hot key mints unlimited points", typ, scope)
		}
	}
}
