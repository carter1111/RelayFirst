// Package delegation implements session delegation: scoped, revocable authority over a hot key
// (S13-3, ARCHITECTURE.md §3.4).
//
// # The decision that shapes this package, and why it is the safe one
//
// A session key exists so an agent does not sign every message by hand, and so a node or MCP server
// can act without holding the owner's key. The obvious design gives the session key a SCOPE and lets
// it sign whatever the scope allows — including receipts.
//
// That design is rejected here, deliberately, because it would require weakening the one invariant
// that currently makes forgery impossible. `Receipt.Validate` requires the signing key's address to
// equal the address inside `agentId`, so a session key cannot sign a receipt today: a different key
// recovers to a different address and the receipt is refused. That is not a rule anyone wrote; it is
// a consequence of the check.
//
// Allowing a receipt to be delegated would mean relaxing that check, and the relaxation cannot limit
// itself to "only receipts, not events" — the accepting code must decide per signature whether this
// key could sign THIS kind of object. Get that wrong once and a leaked hot key mints unlimited
// points, because a receipt is the only thing that earns any.
//
// So: RECEIPTS ARE NEVER DELEGABLE. Not "forbidden by a check" — there is no scope value that
// includes them, so the question cannot be asked. See Scope below.
//
// # Why this package has no cryptography
//
// It defines what a delegation IS and whether a given action falls inside one. Signing and verifying
// the owner's grant live in internal/delegationsign, which links eip712. Keeping them apart means a
// NODE can include this package to understand scopes without becoming able to verify — the same
// separation as internal/agentid, and for the same reason (MVP.md §7.1).
package delegation

import (
	"fmt"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/agentid"
)

// Scope names one category of action a session key may perform.
//
// # Why this is a closed enumeration and not a string pattern
//
// A pattern would let a future caller write a scope that happens to match a receipt, and the mistake
// would be invisible until it was exploited. A closed set means "can this key sign a receipt" has a
// structural answer: there is no value for it.
//
// # Why receipts are absent rather than present-and-forbidden
//
// A `ScopeReceipt` value that validation rejects would work, and it would also be one refactor away
// from being accepted. Its absence is stronger: no code can grant it, and a test asserts that no
// scope value names a receipt.
type Scope string

const (
	// ScopeSessionEvent allows signing session lifecycle events (open/close).
	ScopeSessionEvent Scope = "session:event"

	// ScopeTaskProgress allows signing task progress and input-waiting events.
	//
	// It is separate from session events because a node that relays task traffic has no need to
	// open or close sessions on a user's behalf, and a scope that covers both would grant more than
	// the intended use.
	ScopeTaskProgress Scope = "task:progress"

	// ScopeTaskLifecycle allows signing task state transitions (offer/accept/start/complete).
	//
	// It is the broadest task scope and still excludes receipts, which is the boundary that matters.
	ScopeTaskLifecycle Scope = "task:lifecycle"
)

// allScopes is the complete set. Anything not here cannot be granted.
var allScopes = []Scope{ScopeSessionEvent, ScopeTaskProgress, ScopeTaskLifecycle}

// Valid reports whether s is a grantable scope.
func (s Scope) Valid() bool {
	for _, known := range allScopes {
		if s == known {
			return true
		}
	}
	return false
}

// AllScopes returns the complete, ordered set so a caller can enumerate what exists.
//
// It is a copy so a caller cannot mutate the package's view of what is grantable.
func AllScopes() []Scope { return append([]Scope(nil), allScopes...) }

// ScopeForReceipt is deliberately not defined.
//
// # Why this is a comment rather than a stub
//
// A stub returning "" would be a value a caller could pass around, compare, and eventually store —
// and then a future change would be tempted to make it mean something. Its absence means the
// question "which scope covers receipts" has no answer a program can express, which is the design
// (see the package comment). The test TestNoScopeCoversReceipts pins it.

// Grant is an owner's delegation of limited authority to a session key.
//
// # What each field is for, and what is deliberately not here
//
// There is no allowance, budget or amount. A budget would only matter for something that could be
// over-spent, and the scopes here cannot move value or mint points: receipts are excluded, so there
// is nothing to count. Adding a budget would imply a scope that spends, which does not exist.
type Grant struct {
	// Owner is the delegating agent's identity.
	Owner string `json:"owner"`

	// SessionKey is the delegated signer's identity, derived from its own key.
	//
	// It is an agentId rather than a raw address so that "who is this session" answers the same way
	// everything else does, and so the Go side and the contract side derive it identically.
	SessionKey string `json:"sessionKey"`

	// Scopes is what the session key may do. At least one is required.
	Scopes []Scope `json:"scopes"`

	// ValidFrom and ValidUntil bound the grant in the owner's own account.
	//
	// # Why the times are the owner's and not the verifier's
	//
	// A verifier that applied its own clock would let two nodes disagree about whether a grant was
	// live, which is the fork ARCHITECTURE.md §4.2 warns about. The times are inside the signed
	// grant, so every verifier computes the same window.
	ValidFrom  time.Time `json:"validFrom"`
	ValidUntil time.Time `json:"validUntil"`

	// Nonce is the owner's monotonic counter for this session key.
	//
	// # How revocation works with a counter
	//
	// The owner publishes a revocation by incrementing its nonce for that session key. A verifier
	// that knows the current nonce rejects any grant with a lower one, so revocation needs no
	// on-chain transaction and no registry — the latest nonce is the truth, and it can be
	// distributed however the owner already distributes things.
	//
	// The cost is honest and must be stated: a verifier that does NOT have a fresh nonce cannot tell
	// a revoked grant from a live one. Revocation is therefore as fresh as the nonce's distribution,
	// and nothing here pretends otherwise.
	Nonce uint64 `json:"nonce"`
}

// Validate checks a grant's shape, without verifying its signature.
//
// Signature verification needs eip712 and lives in internal/delegationsign, so this can be used by a
// node that must not be able to verify anything.
func (g Grant) Validate() error {
	if _, err := agentid.Parse(g.Owner); err != nil {
		return fmt.Errorf("delegation: owner: %w", err)
	}
	if _, err := agentid.Parse(g.SessionKey); err != nil {
		return fmt.Errorf("delegation: sessionKey: %w", err)
	}

	// A session key equal to its own owner is not a delegation, it is the owner itself wearing a
	// second name — and it would pass every scope check while granting nothing new, which is a
	// confusing state to allow.
	if g.Owner == g.SessionKey {
		return fmt.Errorf("delegation: sessionKey is the owner; a delegation must name a different key")
	}

	if len(g.Scopes) == 0 {
		return fmt.Errorf("delegation: no scopes; a delegation without scopes grants nothing and " +
			"is more likely a mistake than an intent")
	}
	seen := map[Scope]bool{}
	for _, s := range g.Scopes {
		if !s.Valid() {
			// An unknown scope is a defect in the producer, the same asymmetry as relay roles: a
			// producer emitting one has made a mistake, while a READER should skip it.
			return fmt.Errorf("delegation: scope %q is not grantable (known: %s)", s, knownScopeNames())
		}
		if seen[s] {
			return fmt.Errorf("delegation: scope %q is repeated", s)
		}
		seen[s] = true
	}

	if g.ValidFrom.IsZero() || g.ValidUntil.IsZero() {
		return fmt.Errorf("delegation: validFrom and validUntil are required")
	}
	if !g.ValidUntil.After(g.ValidFrom) {
		return fmt.Errorf("delegation: validUntil must be after validFrom")
	}
	return nil
}

// Allows reports whether the grant permits an action in the given scope at the given time.
//
// # What this checks, and the two things it cannot
//
// It checks that the scope was granted and that the time is inside the window. It does NOT check the
// signature (that is internal/delegationsign) and does NOT check the nonce against the owner's
// current value, because that requires state this type does not have — see Nonce.
//
// # Why the time is a parameter rather than read here
//
// Reading time.Now() would make the function untestable at the boundaries and, worse, would hide
// which clock a caller used. Passing it forces the caller to say, and the same reasoning as Derive
// taking a `Now`.
func (g Grant) Allows(scope Scope, at time.Time) error {
	if !scope.Valid() {
		return fmt.Errorf("delegation: scope %q is not grantable", scope)
	}
	if at.Before(g.ValidFrom) {
		return fmt.Errorf("delegation: not valid until %s", g.ValidFrom.UTC().Format(time.RFC3339))
	}
	if !at.Before(g.ValidUntil) {
		return fmt.Errorf("delegation: expired at %s", g.ValidUntil.UTC().Format(time.RFC3339))
	}
	for _, s := range g.Scopes {
		if s == scope {
			return nil
		}
	}
	return fmt.Errorf("delegation: scope %q was not granted (granted: %s)", scope, joinScopes(g.Scopes))
}

// IsRevokedBy reports whether a newer nonce supersedes this grant.
//
// # Why the comparison is strictly greater
//
// A nonce equal to the grant's means no revocation has been issued, so the grant stands. Only a
// HIGHER nonce revokes, which is what makes "increment to revoke" work: the owner publishes nonce+1
// and every grant at nonce is immediately stale. Treating equal as revoked would revoke every grant
// the moment it was created.
func (g Grant) IsRevokedBy(currentNonce uint64) bool {
	return currentNonce > g.Nonce
}

// knownScopeNames renders the grantable set for an error message, so a producer is told what it may
// use rather than only that it was wrong.
func knownScopeNames() string {
	names := make([]string, 0, len(allScopes))
	for _, s := range allScopes {
		names = append(names, string(s))
	}
	return strings.Join(names, ", ")
}

func joinScopes(scopes []Scope) string {
	names := make([]string, 0, len(scopes))
	for _, s := range scopes {
		names = append(names, string(s))
	}
	return strings.Join(names, ", ")
}
