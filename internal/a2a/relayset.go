package a2a

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	a2asdk "github.com/a2aproject/a2a-go/v2/a2a"
)

// Relay-set extension (P1 #3, ARCHITECTURE.md §17.1).
//
// # What it is for
//
// §17.1 calls this the most important discovery layer, and the reason is that "anyone can run a
// node" does not mean "anyone knows where it is". A card that lists only where to reach the
// agent does not say where the agent's MESSAGES go, and those are different questions: the
// endpoint is the agent's inbox, while the relay set is where it publishes and where a reader
// should look for what it sent.
//
// # The part that makes it trustless
//
// The relay set is inside the agent's own card, which the agent signs. So it is valid because of
// that signature and not because any directory says so. A reader that trusts a directory's copy
// of a relay set has reintroduced the single official directory §17 forbids; a reader that
// verifies the card does not need one.
//
// # Why this is an extension rather than a new card field
//
// A2A has no field for it, and adding one would fork the card schema — which A9 forbids. A2A's
// extension mechanism exists for exactly this: a URI-keyed object an implementation adds and
// others ignore. So the card stays a valid standard card, and a RelayFirst-aware reader finds
// the relay set in it.
//
// # What is NOT here, deliberately
//
// Not the signature. The card already carries a proof, and a signature over the relay set alone
// would be a second thing to verify that could disagree with the first. The relay set is covered
// because the card is covered.

// RelaySetExtensionURI identifies the relay-set extension.
const RelaySetExtensionURI = "https://relayfirst.dev/a2a/relay-set/v1"

// RelayRole describes what a relay is for.
//
// # Why roles rather than one flat list
//
// A reader needs to know which relay to send TO and which to look in. An inbox is where the
// agent accepts messages; a backup is a mirror that may lag. Collapsing them into one list would
// make a client guess, and guessing wrong means publishing to a mirror or reading from an inbox
// that never receives.
type RelayRole string

const (
	// RelayRoleInbox is where the agent accepts messages.
	RelayRoleInbox RelayRole = "inbox"

	// RelayRoleBackup is a mirror the agent also publishes to, for availability.
	RelayRoleBackup RelayRole = "backup"
)

// Valid reports whether r is a role this build understands.
//
// # Why an unknown role is not an error
//
// The set of roles will grow — Archiver reveals itself in §7.3's node capabilities, and a newer
// agent may declare one this build has never heard of. Rejecting the card would make an old
// reader call a newer agent broken, the same mistake as demanding an exact schema match. A
// reader skips what it does not understand and reports it.
func (r RelayRole) Valid() bool {
	switch r {
	case RelayRoleInbox, RelayRoleBackup:
		return true
	default:
		return false
	}
}

// RelayEndpoint is one relay an agent publishes to.
//
// # Why priority and not order
//
// A JSON array's order is not a contract: a reader that re-serialized the card could reorder it,
// and one that stored the array in a set would lose the order entirely. An explicit priority is
// a value inside the signed bytes, so it survives any transport and means the same thing to
// every reader.
type RelayEndpoint struct {
	// URL is the relay's endpoint.
	URL string `json:"url"`

	// Role is what this relay is for.
	Role RelayRole `json:"role"`

	// Priority orders the candidates. Lower is preferred.
	//
	// # Why lower is preferred, stated because guessing is easy
	//
	// §17.1's example uses 1 for the primary and 20 for a backup, so lower-first is the
	// documented convention. Writing it down matters: a reader that assumed higher-first would
	// publish everything to the backup.
	Priority int `json:"priority"`

	// Regions is an optional hint about where the relay is reachable from.
	//
	// It is a HINT and not a filter. A client selecting by region would be making a latency
	// decision on the agent's word, which the agent has no way to verify — so this is
	// informational, and a client that ignores it loses nothing but a possible optimization.
	Regions []string `json:"regions,omitempty"`
}

// RelaySetExtension is the parameter object carried under RelaySetExtensionURI.
type RelaySetExtension struct {
	// Endpoints are the relays the agent publishes to.
	Endpoints []RelayEndpoint `json:"relayEndpoints"`

	// UpdatedAt and ExpiresAt are the agent's own timestamps, in unix seconds.
	//
	// # Why ExpiresAt exists and what it is not
	//
	// It is a hint that the set may be stale, not an authorization rule. A reader that refuses a
	// lapsed set would stop being able to reach an agent whose card it simply has not refreshed —
	// and §17.1's example carries one, so the field is expected. The correct response to a lapse
	// is to REFETCH the card, not to deny service.
	UpdatedAt int64 `json:"updatedAt,omitempty"`
	ExpiresAt int64 `json:"expiresAt,omitempty"`
}

// Extension returns the A2A extension entry carrying this relay set.
//
// Required is false: a client that speaks only A2A can still talk to the agent, and marking this
// required would make a standard client refuse a card it is perfectly able to use. The same
// reasoning as the identity extension.
func (r RelaySetExtension) Extension() a2asdk.AgentExtension {
	params := map[string]any{}
	raw, err := json.Marshal(r)
	if err == nil {
		var asMap map[string]any
		if json.Unmarshal(raw, &asMap) == nil {
			params = asMap
		}
	}
	return a2asdk.AgentExtension{
		URI:         RelaySetExtensionURI,
		Description: "RelayFirst relay set: which relays this agent publishes to",
		Required:    false,
		Params:      params,
	}
}

// RelaySetFromCard reads an agent's relay set out of a card.
//
// It returns ok=false when the extension is absent or malformed, which is not the same as an
// agent with no relays. A caller that merged the two would report "this agent is unreachable"
// for a card that simply does not declare a set, and in A2A-only deployments that is the normal
// case.
func RelaySetFromCard(card *a2asdk.AgentCard) (RelaySetExtension, bool) {
	if card == nil {
		return RelaySetExtension{}, false
	}
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != RelaySetExtensionURI {
			continue
		}
		raw, err := json.Marshal(ext.Params)
		if err != nil {
			return RelaySetExtension{}, false
		}
		var set RelaySetExtension
		if err := json.Unmarshal(raw, &set); err != nil {
			return RelaySetExtension{}, false
		}
		if len(set.Endpoints) == 0 {
			return RelaySetExtension{}, false
		}
		return set, true
	}
	return RelaySetExtension{}, false
}

// ValidateRelaySet checks a relay set's shape.
//
// # What it checks, and what it cannot
//
// It checks the things a reader would otherwise have to guard against itself: that every entry
// has a URL, that the roles are ones this build understands, and that there is at most one
// inbox. It cannot check that the relays exist or that they accept the agent's messages — only
// trying one answers that, and claiming otherwise would be the kind of unverifiable assurance
// this codebase keeps removing.
func ValidateRelaySet(set RelaySetExtension) error {
	if len(set.Endpoints) == 0 {
		return fmt.Errorf("a2a: relay set has no endpoints")
	}
	inboxes := 0
	for i, e := range set.Endpoints {
		if strings.TrimSpace(e.URL) == "" {
			return fmt.Errorf("a2a: relay set endpoint[%d] has no url", i)
		}
		if !e.Role.Valid() {
			// An unknown role is rejected by Validate even though a reader should SKIP it. The
			// asymmetry is deliberate: this function is for a producer checking its own set, and
			// a producer that emits a role nobody understands has made a mistake. A reader uses
			// RelaySetFromCard, which tolerates it.
			return fmt.Errorf("a2a: relay set endpoint[%d] has unknown role %q", i, e.Role)
		}
		if e.Role == RelayRoleInbox {
			inboxes++
		}
	}
	if inboxes == 0 {
		return fmt.Errorf("a2a: relay set declares no inbox, so a reader would not know where to send")
	}
	if inboxes > 1 {
		// Two inboxes would make "where do I send" ambiguous, and a client picking one is a
		// silent coin flip. One inbox plus backups is the shape that keeps the answer unique.
		return fmt.Errorf("a2a: relay set declares %d inboxes, but a reader needs exactly one to know where to send", inboxes)
	}
	return nil
}

// PreferredRelays returns the endpoints ordered by role then priority.
//
// # Why the ordering is a function and not the caller's problem
//
// The rule "inbox before backup, then lower priority first" is the part of this extension that a
// caller must get right, and getting it wrong means publishing everything to a mirror. Encoding
// it once means every caller orders the same way, and a test pins it.
//
// # Why the result is sorted rather than the input reordered
//
// A caller may hold the slice in a signed document and must not mutate what it will re-verify.
func PreferredRelays(set RelaySetExtension) []RelayEndpoint {
	out := append([]RelayEndpoint(nil), set.Endpoints...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		ai, bi := a.Role == RelayRoleInbox, b.Role == RelayRoleInbox
		if ai != bi {
			// Inbox first, always: it is where messages are accepted.
			return ai
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		// URL as a final tiebreak so the order is total and stable. Two endpoints with equal
		// priority would otherwise come back in map order, which changes between runs.
		return a.URL < b.URL
	})
	return out
}
