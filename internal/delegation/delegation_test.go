package delegation_test

import (
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/delegation"
)

// These tests cover S13-3's shape and scope rules, which are the part with no cryptography.
//
// The property that carries the most weight is the ABSENCE of a receipt scope. It is tested by
// searching the grantable set rather than by testing a rejection, because the design chose absence
// over a forbidden value: a value that validation rejects is one refactor from being accepted, while
// a value that does not exist cannot be granted at all.

func agentA() string { return "agent:eip155:8453:0x00000000000000000000000000000000000000a1" }
func agentB() string { return "agent:eip155:8453:0x00000000000000000000000000000000000000b2" }

func validGrant() delegation.Grant {
	return delegation.Grant{
		Owner:      agentA(),
		SessionKey: agentB(),
		Scopes:     []delegation.Scope{delegation.ScopeSessionEvent, delegation.ScopeTaskProgress},
		ValidFrom:  time.Unix(1791015800, 0),
		ValidUntil: time.Unix(1791019400, 0),
		Nonce:      1,
	}
}

// TestNoScopeCoversReceipts is the hard gate the user asked for.
//
// # Why this is a search rather than a rejection test
//
// The design decision was that receipts are NEVER delegable, and the implementation expresses that as
// the ABSENCE of a scope value rather than as a value that validation forbids. Absence is stronger: a
// forbidden value lives in the type, gets passed around, and is one refactor away from being accepted,
// while an absent value cannot be granted by any code.
//
// So this searches the grantable set for anything that could name a receipt. If a future change adds
// one, this fails — and it fails at the point where the decision is made rather than after an exploit.
func TestNoScopeCoversReceipts(t *testing.T) {
	for _, s := range delegation.AllScopes() {
		lower := strings.ToLower(string(s))
		for _, forbidden := range []string{"receipt", "points", "mint", "award", "credit"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("scope %q names %q: scopes must not be able to cover receipts, because a "+
					"delegable receipt is how one leaked hot key mints unlimited points", s, forbidden)
			}
		}
	}
	// And the absence is real: there is no exported scope constant this test failed to enumerate.
	// A caller cannot construct one either, since Validate rejects anything outside the set.
	if err := (delegation.Grant{
		Owner: agentA(), SessionKey: agentB(),
		Scopes:     []delegation.Scope{"receipt"},
		ValidFrom:  time.Unix(1791015800, 0),
		ValidUntil: time.Unix(1791019400, 0),
	}).Validate(); err == nil {
		t.Fatal("a grant naming a receipt scope must not validate")
	}
}

// TestValidate_AcceptsAWellFormedGrant is the control.
func TestValidate_AcceptsAWellFormedGrant(t *testing.T) {
	if err := validGrant().Validate(); err != nil {
		t.Fatalf("a well-formed grant must validate: %v", err)
	}
}

// TestValidate_RejectsNoScopes: a grant granting nothing is more likely a mistake than an intent.
func TestValidate_RejectsNoScopes(t *testing.T) {
	g := validGrant()
	g.Scopes = nil
	if err := g.Validate(); err == nil {
		t.Fatal("a grant with no scopes must be rejected")
	}
}

func TestValidate_RejectsUnknownAndRepeatedScopes(t *testing.T) {
	g := validGrant()
	g.Scopes = []delegation.Scope{"task:everything"}
	if err := g.Validate(); err == nil {
		t.Error("an ungrantable scope must be rejected")
	}

	g = validGrant()
	g.Scopes = []delegation.Scope{delegation.ScopeSessionEvent, delegation.ScopeSessionEvent}
	if err := g.Validate(); err == nil {
		t.Error("a repeated scope must be rejected")
	}
}

// TestValidate_RejectsSelfDelegation: a session key equal to its owner is the owner wearing a second
// name, which would pass every scope check while granting nothing new.
func TestValidate_RejectsSelfDelegation(t *testing.T) {
	g := validGrant()
	g.SessionKey = g.Owner
	if err := g.Validate(); err == nil {
		t.Fatal("a delegation to the owner itself must be rejected")
	}
}

func TestValidate_RejectsBadWindow(t *testing.T) {
	g := validGrant()
	g.ValidUntil = g.ValidFrom
	if err := g.Validate(); err == nil {
		t.Error("a zero-length window must be rejected")
	}

	g = validGrant()
	g.ValidUntil = g.ValidFrom.Add(-time.Second)
	if err := g.Validate(); err == nil {
		t.Error("a backwards window must be rejected")
	}

	g = validGrant()
	g.ValidFrom = time.Time{}
	if err := g.Validate(); err == nil {
		t.Error("a missing window must be rejected")
	}
}

func TestValidate_RejectsMalformedIdentities(t *testing.T) {
	g := validGrant()
	g.Owner = "not-an-agent"
	if err := g.Validate(); err == nil {
		t.Error("a malformed owner must be rejected")
	}

	g = validGrant()
	g.SessionKey = "agent:eip155::0x00"
	if err := g.Validate(); err == nil {
		t.Error("a malformed session key must be rejected")
	}
}

// TestAllows_IsInsideTheWindow covers the time rule, including both boundaries.
func TestAllows_IsInsideTheWindow(t *testing.T) {
	g := validGrant()

	// Inside.
	if err := g.Allows(delegation.ScopeSessionEvent, g.ValidFrom.Add(time.Minute)); err != nil {
		t.Errorf("a granted scope inside the window must be allowed: %v", err)
	}
	// Exactly at validFrom is inside, since the window is inclusive at the start.
	if err := g.Allows(delegation.ScopeSessionEvent, g.ValidFrom); err != nil {
		t.Errorf("validFrom must be inclusive: %v", err)
	}
	// Exactly at validUntil is OUTSIDE, because a window that includes its own end never closes.
	if err := g.Allows(delegation.ScopeSessionEvent, g.ValidUntil); err == nil {
		t.Error("validUntil must be exclusive, or the window never closes")
	}
	// Before and after are both refused.
	if err := g.Allows(delegation.ScopeSessionEvent, g.ValidFrom.Add(-time.Second)); err == nil {
		t.Error("before validFrom must be refused")
	}
	if err := g.Allows(delegation.ScopeSessionEvent, g.ValidUntil.Add(time.Second)); err == nil {
		t.Error("after validUntil must be refused")
	}
}

// TestAllows_RefusesAUngrantedScope: the scope list is the whole authority, so anything outside it
// must be refused even when the window is fine.
func TestAllows_RefusesAUngrantedScope(t *testing.T) {
	g := validGrant() // grants session events and task progress, NOT task lifecycle
	at := g.ValidFrom.Add(time.Minute)

	if err := g.Allows(delegation.ScopeTaskLifecycle, at); err == nil {
		t.Fatal("an ungranted scope must be refused")
	}
	if err := g.Allows(delegation.ScopeSessionEvent, at); err != nil {
		t.Errorf("a granted scope must be allowed: %v", err)
	}
}

// TestAllows_RefusesAnUngrantableScope keeps a typo from being treated as a scope that simply was not
// granted — those need different fixes.
func TestAllows_RefusesAnUngrantableScope(t *testing.T) {
	g := validGrant()
	err := g.Allows("receipt", g.ValidFrom.Add(time.Minute))
	if err == nil {
		t.Fatal("an ungrantable scope must be refused")
	}
	if !strings.Contains(err.Error(), "not grantable") {
		t.Errorf("the error must say the scope does not exist rather than that it was not granted, got: %v", err)
	}
}

// TestIsRevokedBy_OnlyAHigherNonceRevokes pins the comparison direction, which is what makes
// "increment to revoke" work.
func TestIsRevokedBy_OnlyAHigherNonceRevokes(t *testing.T) {
	g := validGrant()
	g.Nonce = 5

	if g.IsRevokedBy(5) {
		t.Error("an equal nonce means no revocation has been issued, so the grant stands")
	}
	if g.IsRevokedBy(4) {
		t.Error("a lower nonce must not revoke")
	}
	if !g.IsRevokedBy(6) {
		t.Error("a higher nonce must revoke: incrementing is how an owner revokes")
	}
}

// TestAllScopes_ReturnsACopy keeps a caller from mutating the package's view of what is grantable.
func TestAllScopes_ReturnsACopy(t *testing.T) {
	first := delegation.AllScopes()
	if len(first) == 0 {
		t.Fatal("there must be at least one grantable scope")
	}
	first[0] = "tampered"

	second := delegation.AllScopes()
	if second[0] == "tampered" {
		t.Fatal("AllScopes leaked the package's slice; a caller mutated what may be granted")
	}
}

// TestScopeValid_OnlyKnownScopes keeps the enumeration closed.
func TestScopeValid_OnlyKnownScopes(t *testing.T) {
	for _, s := range delegation.AllScopes() {
		if !s.Valid() {
			t.Errorf("an enumerated scope %q must be Valid", s)
		}
	}
	for _, bad := range []delegation.Scope{"", "receipt", "task:", "SESSION:EVENT"} {
		if bad.Valid() {
			t.Errorf("scope %q must not be valid", bad)
		}
	}
}
