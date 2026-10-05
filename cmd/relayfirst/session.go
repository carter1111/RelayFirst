package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/a2a"
	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/delegation"
	"github.com/relayfirst/relayfirst/internal/delegationsign"
	"github.com/relayfirst/relayfirst/internal/eip712"
	"github.com/relayfirst/relayfirst/internal/eventsign"
	"github.com/relayfirst/relayfirst/internal/protocol"
)

// The `session` command: the first producer of signed A2A events.
//
// # Why this exists, stated plainly
//
// Three layers of A2A machinery were built, tested, and unreachable: internal/a2a defines events and
// the session and task state machines, internal/eventsign signs and authorizes them, and no
// production code ever created an event. The delegation mechanism in particular had nothing to
// authorize.
//
// A fourth library would not have changed that. What was missing was a PRODUCER: something that
// takes an agent's key, builds an event, signs it, and sends it to a relay. That is this command, and
// it is deliberately the smallest thing that closes the loop rather than a general event framework.
//
// # What it does NOT do
//
// It does not implement the full session lifecycle. It emits an open or a close, which is enough to
// exercise signing, chaining and delegation end to end. Task events, message exchange and the timeout
// edges belong to S9-4..S9-6's state machine, and emitting them needs a producer for each — a
// decision about who produces what, not another library.
//
// # Why the sequence is tracked in a file rather than a database
//
// A per-actor sequence must be monotonic and must not repeat (a repeat is a replay the chain rejects).
// The chain is only as good as the producer's bookkeeping, and the bookkeeping has to survive between
// invocations because each command is a separate process. A small state file beside the config is the
// minimum that works, and it is deliberately inspectable so an operator can see and fix it — a
// database would hide the one thing that goes wrong.

const sessionUsage = `relayfirst session — emit signed session events, and authorize a session key

  relayfirst session open   [flags]
  relayfirst session close  [flags]
  relayfirst session show   [flags]    Print the local chain state and the next event
  relayfirst session grant  [flags]    OWNER-side: sign a delegation grant for a session key
  relayfirst session verify [flags]    READER-side: pull events and authorize them under a grant

Flags:
  --key <hex>        Session signing key for open/close. Also RELAYFIRST_SESSION_KEY.
                     OWNER's key for grant. Also RELAYFIRST_OWNER_KEY.
  --chain-id <n>     EVM chain id for the identity (default 8453).
  --session <id>     Session id. Derived from the key and --nonce when omitted.
  --nonce <text>     Nonce for deriving a session id. Default: "default".
  --task <id>        Task id. Required for task-scoped events; unused for session ones.
  --relay <url>      Relay to POST to. Also RELAYFIRST_RELAY. Omit to print without sending.
  --state <path>     Chain state file. Default: .relayfirst-session-state.json

Grant flags (OWNER-side; the owner authorizes a session key's scopes):
  --session-key <id>  The delegated session key's agentId (from ` + "`relayfirst id`" + `). REQUIRED.
  --scopes <list>     Comma-separated scopes to grant. Default: session:event
                      (grantable: session:event, task:progress, task:lifecycle)
  --valid-for <dur>   Window length, e.g. 24h. Default: 24h.
  --grant-nonce <n>   Owner's monotonic nonce for this key. Default: 1.
  --out <path>        Also write the signed grant JSON here.

Verify flags (READER-side; needs the grant the actor claims to act under):
  --relay <url>          Relay to pull from. REQUIRED. Also RELAYFIRST_RELAY.
  --agent <agentId>      The actor whose events to pull. REQUIRED (the session key).
  --grant <path|json>    The signed grant. A file path, or inline JSON. REQUIRED.
  --grant-nonce <n>      FRESHNESS: the owner's latest nonce. A grant at a lower nonce is
                         refused as revoked. Default: the grant's own nonce, and the output
                         says so, because a reader that believes revocation is instantaneous
                         would be wrong. Also RELAYFIRST_GRANT_NONCE.
  --as-of <rfc3339>      Clock to test the grant window against. Default: now.

A reader authorizes with this one command: it pulls the actor's messages, keeps the
` + "`event`" + ` ones, and runs the same check an accepting peer would — signature first
(who signed), then authority (was that signer permitted). An event signed by a key the
grant did not name, or outside the granted scopes, is refused and named. This is the
consumer the delegation mechanism was missing; the relay cannot do it, because a node
that could verify could forge (MVP.md §7.1).

The chain matters: each actor's events carry a per-actor sequence and a link to the previous event's
hash, and a repeat or a gap is rejected by every verifier. This command keeps that bookkeeping in a
state file so it survives between invocations.

A grant is the OWNER's signature over the delegation; it travels to whoever will authorize the
session key's events. This command signs it and never holds the session key: the session key signs
its own events, and the owner's grant is what makes them acceptable. A delegate can never sign a
receipt (there is no such scope; invariant A9).

Keys are NEVER written to the config file; --key would leak into shell history and the process table,
so prefer the environment variable.
`

func runSession(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, sessionUsage)
		return fmt.Errorf("session needs a subcommand (open, close, show, grant or verify)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "open", "close", "show":
		return runSessionEmit(sub, rest)
	case "grant":
		return runSessionGrant(rest)
	case "verify":
		return runSessionVerify(rest)
	case "-h", "--help", "help":
		fmt.Print(sessionUsage)
		return nil
	default:
		fmt.Fprint(os.Stderr, sessionUsage)
		return fmt.Errorf("unknown session subcommand %q", sub)
	}
}

// chainState is the per-actor bookkeeping a producer needs.
//
// # Why the previous hash is stored rather than recomputed
//
// It could be recomputed by re-deriving the last event, which would mean storing the event too. Storing
// the hash is the smaller record and makes the file readable: an operator debugging a rejected chain
// wants to see the sequence and the hash, not a serialized event.
type chainState struct {
	SessionID string `json:"sessionId"`
	Actor     string `json:"actor"`
	Sequence  uint64 `json:"sequence"`
	LastHash  string `json:"lastHash"`
	UpdatedAt string `json:"updatedAt"`
}

func runSessionEmit(sub string, args []string) error {
	key := os.Getenv("RELAYFIRST_SESSION_KEY")
	relay := os.Getenv("RELAYFIRST_RELAY")
	chainID := uint64(8453)
	sessionID := ""
	nonce := "default"
	taskID := ""
	statePath := ".relayfirst-session-state.json"

	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			return fmt.Errorf("unexpected argument %q", a)
		}
		if i+1 >= len(args) {
			return fmt.Errorf("%s needs a value", a)
		}
		v := args[i+1]
		i++
		switch a {
		case "--key":
			key = v
		case "--relay":
			relay = v
		case "--session":
			sessionID = v
		case "--nonce":
			nonce = v
		case "--task":
			taskID = v
		case "--state":
			statePath = v
		case "--chain-id":
			var n uint64
			if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
				return fmt.Errorf("--chain-id must be a number, got %q", v)
			}
			chainID = n
		default:
			return fmt.Errorf("unknown flag %q", a)
		}
	}

	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("no key: pass --key or set RELAYFIRST_SESSION_KEY. Without one the event " +
			"cannot be signed, and an unsigned event proves nothing")
	}

	actor, err := agentIDFromKey(key, chainID)
	if err != nil {
		return err
	}

	if sessionID == "" {
		sessionID, err = a2a.SessionIDFor(actor, nonce)
		if err != nil {
			return err
		}
	}

	state, err := readChainState(statePath)
	if err != nil {
		return err
	}
	// A state file for a different session or actor would make the sequence wrong in a way the chain
	// rejects later, so it is refused here where the mismatch is visible.
	if state.SessionID != "" && (state.SessionID != sessionID || state.Actor != actor) {
		return fmt.Errorf("state file %s belongs to session %s / actor %s, not %s / %s; "+
			"point --state elsewhere or remove it to start a new chain",
			statePath, state.SessionID, state.Actor, sessionID, actor)
	}

	if sub == "show" {
		next := state.Sequence + 1
		return printJSON(map[string]any{
			"sessionId":    sessionID,
			"actor":        actor,
			"sequence":     state.Sequence,
			"nextSequence": next,
			"lastHash":     state.LastHash,
			"note":         "the next event this key would emit; a repeat or a gap is rejected by every verifier",
		})
	}

	typ := a2a.EventSessionOpen
	if sub == "close" {
		typ = a2a.EventSessionClose
	}

	// The event id must be unique per event, or the relay's dedup treats a second one as a retry and
	// silently drops it — which would look like a lost event rather than a collision.
	eventID, err := newEventID()
	if err != nil {
		return err
	}

	e, err := a2a.SessionEvent(eventID, sessionID, actor, typ, state.Sequence+1, state.LastHash, time.Now(), 0)
	if err != nil {
		return err
	}
	if strings.TrimSpace(taskID) != "" {
		e.TaskID = taskID
	}

	signed, err := eventsign.Sign(e, key, chainID)
	if err != nil {
		return err
	}

	// Self-check before sending, the same as the card command: an event that does not verify would be
	// stored by a relay that cannot check it, and the mistake would surface at a reader.
	if _, err := eventsign.Verify(signed); err != nil {
		return fmt.Errorf("the event we just built does not verify, refusing to send it: %w", err)
	}

	// Advance the chain only after a successful send, so a failed send does not burn a sequence number
	// and leave a gap that every verifier would reject.
	nextHash, err := a2a.EventHash(keccakHasher, signed)
	if err != nil {
		return err
	}

	if strings.TrimSpace(relay) == "" {
		// No relay: print, and do NOT advance the state. Advancing here would make the printed event
		// unsendable afterwards, which is a trap for anyone inspecting before sending.
		return printJSON(map[string]any{
			"event":     signed,
			"sessionId": sessionID,
			"sequence":  signed.Sequence,
			"sent":      false,
			"note": "no --relay given, so nothing was sent and the chain was not advanced; " +
				"send it yourself or re-run with --relay",
		})
	}

	if err := postEvent(strings.TrimRight(relay, "/"), signed); err != nil {
		return err
	}

	state.SessionID = sessionID
	state.Actor = actor
	state.Sequence = signed.Sequence
	state.LastHash = nextHash
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeChainState(statePath, state); err != nil {
		return err
	}

	return printJSON(map[string]any{
		"event":     signed,
		"sessionId": sessionID,
		"sequence":  signed.Sequence,
		"sent":      true,
		"relay":     relay,
		"nextHash":  nextHash,
		"note":      "the chain advanced; the relay stored this verbatim and did not verify it",
	})
}

// runSessionGrant signs a delegation grant with the OWNER's key.
//
// # Why this is the producer that was missing
//
// A session key signs its own events, and `eventsign.AuthorizeEventWithGrant` decides
// whether those events were authorized. That check needs a signed grant, and until now
// nothing produced one — the delegation mechanism was complete and had nothing to
// authorize. This is the owner's half: it signs the grant and stops. It never sees or
// uses the session key, which is the point of a session key.
//
// # Why the owner and the session key are separate flags
//
// `--key` is the owner (the identity doing the delegating) and `--session-key` is the
// delegated agentId. They are deliberately different names so a caller cannot pass one
// key and have the tool quietly do the other thing, which is the failure that would
// make a grant name the wrong signer.
//
// # What it validates before signing
//
// The grant's own shape (delegation.Grant.Validate) and that the owner key derives the
// identity it claims. delegationsign.Sign enforces the latter too, but the error is
// clearer when both values are in hand here.
func runSessionGrant(args []string) error {
	ownerKey := os.Getenv("RELAYFIRST_OWNER_KEY")
	sessionKeyID := ""
	relay := os.Getenv("RELAYFIRST_RELAY")
	scopesArg := "session:event"
	validFor := 24 * time.Hour
	grantNonce := uint64(1)
	chainID := uint64(8453)
	outPath := ""

	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			return fmt.Errorf("unexpected argument %q", a)
		}
		if i+1 >= len(args) {
			return fmt.Errorf("%s needs a value", a)
		}
		v := args[i+1]
		i++
		switch a {
		case "--key":
			ownerKey = v
		case "--session-key":
			sessionKeyID = v
		case "--relay":
			relay = v
		case "--scopes":
			scopesArg = v
		case "--out":
			outPath = v
		case "--valid-for":
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("--valid-for must be a duration like 24h, got %q", v)
			}
			validFor = d
		case "--grant-nonce":
			var n uint64
			if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
				return fmt.Errorf("--grant-nonce must be a number, got %q", v)
			}
			grantNonce = n
		case "--chain-id":
			var n uint64
			if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
				return fmt.Errorf("--chain-id must be a number, got %q", v)
			}
			chainID = n
		default:
			return fmt.Errorf("unknown flag %q", a)
		}
	}

	if strings.TrimSpace(ownerKey) == "" {
		return fmt.Errorf("no owner key: pass --key or set RELAYFIRST_OWNER_KEY. A grant must be " +
			"signed by the owner it delegates from, or it authorizes nothing")
	}
	if strings.TrimSpace(sessionKeyID) == "" {
		return fmt.Errorf("--session-key <agentId> is required: it is the identity being delegated " +
			"to, and a grant that named the wrong one would authorize nothing while looking valid")
	}

	// Normalize the scopes, rejecting an unknown one here rather than letting a
	// typo produce a grant whose scope covers nothing.
	scopes, err := parseScopes(scopesArg)
	if err != nil {
		return err
	}

	// The owner's declared identity comes from the key, so the grant names an owner
	// that can actually sign it. This mirrors every other signing path (card, receipt,
	// assertion): the identity is derived, never chosen.
	ownerID, err := agentIDFromKey(ownerKey, chainID)
	if err != nil {
		return err
	}

	// Validate the session key's shape up front so the error names the bad input rather
	// than surfacing from inside Sign.
	if _, err := agentid.Parse(sessionKeyID); err != nil {
		return fmt.Errorf("--session-key %q is not an agentId (derive it with `relayfirst id <hex>`): %w",
			sessionKeyID, err)
	}
	if sessionKeyID == ownerID {
		return fmt.Errorf("--session-key equals the owner key; a delegation must name a DIFFERENT " +
			"key, or it grants nothing new and only creates a confusing second name for the owner")
	}

	now := time.Now().UTC().Truncate(time.Second)
	g := delegationsign.SignedGrant{
		Grant: delegation.Grant{
			Owner:      ownerID,
			SessionKey: sessionKeyID,
			Scopes:     scopes,
			ValidFrom:  now,
			ValidUntil: now.Add(validFor),
			Nonce:      grantNonce,
		},
	}
	if err := g.Sign(ownerKey, chainID); err != nil {
		return err
	}

	// Self-check before handing it out: a grant that does not verify would authorize
	// nothing, and the mistake would surface far from here at an authorizer.
	if err := g.Verify(); err != nil {
		return fmt.Errorf("the grant we just signed does not verify, refusing to emit it: %w", err)
	}

	encoded, err := json.Marshal(g)
	if err != nil {
		return fmt.Errorf("marshal grant: %w", err)
	}

	if outPath != "" {
		if err := os.WriteFile(outPath, append(encoded, '\n'), 0o644); err != nil {
			return fmt.Errorf("write grant to %s: %w", outPath, err)
		}
	}

	result := map[string]any{
		"grant":      g,
		"owner":      ownerID,
		"sessionKey": sessionKeyID,
		"scopes":     scopeNames(scopes),
		"validFrom":  g.Grant.ValidFrom.Format(time.RFC3339),
		"validUntil": g.Grant.ValidUntil.Format(time.RFC3339),
		"nonce":      grantNonce,
		"sent":       false,
		"note": "this grant travels OUT OF BAND (the same bytes over any channel), so a relay " +
			"cannot withhold or shred it and an authorizer reads it from wherever it trusts",
	}

	if strings.TrimSpace(relay) != "" {
		// The id is derived from the grant's own bytes, so a retried publish is the same
		// envelope and the node dedups it rather than storing a second copy. `grantId` is
		// a reserved word elsewhere in SQL, hence `delegationId`.
		envelopeID := "0x" + hex.EncodeToString(eip712.Keccak256(encoded))
		if err := postEnvelope(strings.TrimRight(relay, "/"), envelopeID, ownerID, protocol.KindGrant, encoded); err != nil {
			return err
		}
		result["sent"] = true
		result["relay"] = relay
		result["envelopeId"] = envelopeID
	}

	if outPath != "" {
		result["writtenTo"] = outPath
	}
	return printJSON(result)
}

// runSessionVerify is the reader that consumes a grant: it pulls an actor's events and
// authorizes each under the grant the actor claims to act with.
//
// # Why this cannot live on the node
//
// The check needs the actor's signature and the grant's signature, which means eip712, and a
// node that could verify could forge (MVP.md §7.1). So the consumer is a client: it pulls the
// stored bytes from a node and judges them here. The node's job ends at carrying them.
//
// # What this proves, and the honest limit
//
// For each event: that the signer is the actor the event names (eventsign.Verify), and that
// the grant covers that scope for that signer at that time and is not superseded (the
// delegation check). It does NOT prove the event's content is true, and a stale --grant-nonce
// cannot distinguish a revoked grant from a live one — the output says so rather than implying
// instantaneous revocation.
func runSessionVerify(args []string) error {
	relay := os.Getenv("RELAYFIRST_RELAY")
	agent := ""
	grantSpec := ""
	asOf := ""
	nonceRaw := os.Getenv("RELAYFIRST_GRANT_NONCE")

	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			return fmt.Errorf("unexpected argument %q", a)
		}
		if i+1 >= len(args) {
			return fmt.Errorf("%s needs a value", a)
		}
		v := args[i+1]
		i++
		switch a {
		case "--relay":
			relay = v
		case "--agent":
			agent = v
		case "--grant":
			grantSpec = v
		case "--grant-nonce":
			nonceRaw = v
		case "--as-of":
			asOf = v
		default:
			return fmt.Errorf("unknown flag %q", a)
		}
	}

	if strings.TrimSpace(relay) == "" {
		return fmt.Errorf("--relay <url> is required: a reader has to pull the events to judge them")
	}
	if strings.TrimSpace(agent) == "" {
		return fmt.Errorf("--agent <agentId> is required: name the actor whose events to authorize")
	}
	if strings.TrimSpace(grantSpec) == "" {
		return fmt.Errorf("--grant <path|json> is required: without the grant there is no authority to check, " +
			"and every delegated event must be judged against one")
	}

	grant, err := loadGrant(grantSpec)
	if err != nil {
		return err
	}
	// The grant's own signature is checked first: an unsigned or tampered grant authorizes
	// nothing, and reporting that before any event avoids blaming the events for it.
	if err := grant.Verify(); err != nil {
		return fmt.Errorf("the grant does not verify on its own, so nothing can be authorized under it: %w", err)
	}

	now := time.Now().UTC()
	if strings.TrimSpace(asOf) != "" {
		t, err := time.Parse(time.RFC3339, asOf)
		if err != nil {
			return fmt.Errorf("--as-of must be RFC3339 (e.g. 2026-10-05T12:00:00Z), got %q", asOf)
		}
		now = t
	}

	// Freshness is the caller's: the reader supplies the owner's latest nonce. Defaulting to
	// the grant's own nonce means "no revocation observed", which is the only honest default
	// when the reader has nothing fresher — and it is stated in the output.
	nonce := grant.Grant.Nonce
	nonceMode := "the grant's own nonce (no fresher nonce supplied, so a revocation would not be seen)"
	if strings.TrimSpace(nonceRaw) != "" {
		var n uint64
		if _, err := fmt.Sscanf(nonceRaw, "%d", &n); err != nil {
			return fmt.Errorf("--grant-nonce must be a number, got %q", nonceRaw)
		}
		nonce = n
		nonceMode = fmt.Sprintf("supplied current nonce %d", n)
	}

	events, nonEvents, err := pullEvents(strings.TrimRight(relay, "/"), agent)
	if err != nil {
		return err
	}

	type verdict struct {
		EventID  string `json:"eventId"`
		Type     string `json:"type"`
		Sequence uint64 `json:"sequence"`
		Signer   string `json:"signer,omitempty"`
		Scope    string `json:"scope,omitempty"`
		Status   string `json:"status"`
		Reason   string `json:"reason,omitempty"`
	}
	results := make([]verdict, 0, len(events))
	authorized, refused := 0, 0

	for _, e := range events {
		signer, err := eventsign.AuthorizeDerivedScope(eventsign.AuthorizeInput{
			Event: e, Grant: grant, CurrentNonce: nonce, Now: now,
		})
		v := verdict{EventID: e.EventID, Type: string(e.Type), Sequence: e.Sequence}
		// The scope is reported even when it is the reason for refusal, so an operator does
		// not have to re-derive the mapping by hand to understand the verdict.
		if scope, serr := eventsign.ScopeOfEvent(e.Type); serr == nil {
			v.Scope = string(scope)
		}
		if err != nil {
			refused++
			v.Status = "refused"
			v.Reason = err.Error()
		} else {
			authorized++
			v.Status = "authorized"
			v.Signer = "0x" + hex.EncodeToString(signer)
		}
		results = append(results, v)
	}

	out := map[string]any{
		"relay":       relay,
		"agent":       agent,
		"owner":       grant.Grant.Owner,
		"sessionKey":  grant.Grant.SessionKey,
		"grantScopes": scopeNames(grant.Grant.Scopes),
		"asOf":        now.Format(time.RFC3339),
		"nonceMode":   nonceMode,
		"summary": map[string]any{
			"events":     len(events),
			"authorized": authorized,
			"refused":    refused,
			"nonEvents":  nonEvents,
			"note": "authorized means the event was signed by the grant's session key AND its " +
				"scope was granted; it is not a claim that the event's content is true",
		},
		"results": results,
	}
	return printJSON(out)
}

// loadGrant reads a grant from a file path or from inline JSON.
//
// The inline form exists so a reader can paste the grant a peer handed it without first
// writing a file; the two are distinguished by the leading `{`, which a path cannot have.
func loadGrant(spec string) (delegationsign.SignedGrant, error) {
	raw := []byte(spec)
	if !strings.HasPrefix(strings.TrimSpace(spec), "{") {
		var err error
		raw, err = os.ReadFile(spec)
		if err != nil {
			return delegationsign.SignedGrant{}, fmt.Errorf("read grant %s: %w", spec, err)
		}
	}
	var g delegationsign.SignedGrant
	if err := json.Unmarshal(raw, &g); err != nil {
		return delegationsign.SignedGrant{}, fmt.Errorf("parse grant: %w", err)
	}
	return g, nil
}

// pullEvents fetches an actor's stored messages and keeps the event ones.
//
// # Why the reader filters by kind rather than trusting the node to
//
// A node carries any kind and does not interpret the payload (MVP.md §7.1), so a pull can
// return grants, receipts and events together. Filtering here means the node needs no new
// capability and the reader stays the only place that decides what an event is.
//
// A payload that does not decode as an event is an error rather than a skip: an event-kind
// envelope whose bytes are not an event is malformed, and silently dropping it would hide a
// truncated or corrupt history behind a clean-looking summary.
func pullEvents(relay, agent string) ([]a2a.Event, int, error) {
	req, err := http.NewRequest(http.MethodGet, relay+"/messages/"+url.PathEscape(agent), nil)
	if err != nil {
		return nil, 0, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("relay %s is unreachable: %w", relay, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, 0, fmt.Errorf("relay returned %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}

	var body struct {
		Messages []struct {
			Kind    string          `json:"kind"`
			Payload json.RawMessage `json:"payload"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, fmt.Errorf("decode pull response: %w", err)
	}

	events := make([]a2a.Event, 0, len(body.Messages))
	nonEvents := 0
	for _, m := range body.Messages {
		if m.Kind != protocol.KindEvent {
			nonEvents++
			continue
		}
		// The pull carries the payload as a base64 string ([]byte in JSON).
		var payload []byte
		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			return nil, 0, fmt.Errorf("decode event payload: %w", err)
		}
		e, err := a2a.DecodeEvent(payload)
		if err != nil {
			return nil, 0, fmt.Errorf("an event-kind message did not decode as an event: %w", err)
		}
		events = append(events, e)
	}
	return events, nonEvents, nil
}

// parseScopes splits a comma-separated scope list and rejects anything not grantable.
//
// # Why an unknown scope is an error rather than a skip
//
// A silently dropped scope produces a grant that authorizes less than the caller asked
// for, and the failure appears later as a rejected event with no hint about the grant.
// The grantable set is closed (delegation.Scope.Valid), so an unknown value is always a
// caller's typo or a mistaken assumption, and both deserve an immediate message.
func parseScopes(raw string) ([]delegation.Scope, error) {
	parts := strings.Split(raw, ",")
	scopes := make([]delegation.Scope, 0, len(parts))
	seen := map[delegation.Scope]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		s := delegation.Scope(p)
		if !s.Valid() {
			return nil, fmt.Errorf("scope %q is not grantable (grantable: %s)", p, strings.Join(scopeNames(delegation.AllScopes()), ", "))
		}
		if seen[s] {
			return nil, fmt.Errorf("scope %q is listed twice", p)
		}
		seen[s] = true
		scopes = append(scopes, s)
	}
	if len(scopes) == 0 {
		return nil, fmt.Errorf("no scopes given; a grant without scopes authorizes nothing")
	}
	return scopes, nil
}

// scopeNames renders scopes as strings for output and messages.
func scopeNames(scopes []delegation.Scope) []string {
	names := make([]string, 0, len(scopes))
	for _, s := range scopes {
		names = append(names, string(s))
	}
	return names
}

// keccakHasher adapts eip712.Keccak256 to a2a.Hasher.
//
// The signature must match exactly, and Keccak256 is variadic, so a direct reference does not
// convert. Wrapping it keeps the a2a package dependency-free while giving the chain its real hash.
func keccakHasher(b []byte) []byte { return eip712.Keccak256(b) }

// newEventID returns a unique event id.
//
// It is random rather than derived: the id is the relay's idempotency key, so two events must not
// collide. A counter would collide across machines, and a hash of the content would make a legitimate
// repeat look like a retry.
func newEventID() (string, error) {
	raw := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate event id: %w", err)
	}
	return "evt_" + hex.EncodeToString(raw), nil
}

// postEvent sends a signed event as an envelope.
func postEvent(relay string, e a2a.Event) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return postEnvelope(relay, e.EventID, e.Actor, protocol.KindEvent, raw)
}

// postEnvelope sends an opaque payload to a relay under an id/agentId/kind header.
//
// # Why this is shared with the event and grant producers
//
// A relay stores an envelope without interpreting its payload (MVP.md §7.1), so the
// delivery half is identical for any kind: id, addressee, kind, opaque bytes. Keeping
// one copy means a change to how a delivery is reported (a timeout, a status detail)
// cannot drift between the producers that use it.
//
// The payload is passed as bytes and is base64-encoded by encoding/json on the wire,
// exactly as before — the caller's bytes are not decoded, re-encoded or inspected.
func postEnvelope(relay, id, agentID, kind string, payload []byte) error {
	body, err := json.Marshal(map[string]any{
		"id": id, "agentId": agentID, "kind": kind, "payload": payload,
	})
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, relay+"/messages", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("relay %s is unreachable: %w", relay, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("relay returned %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

// readChainState loads the chain state, treating a missing file as an empty chain.
//
// A missing file is normal on the first run, so it is not an error. A malformed one IS, because
// silently starting over would reset the sequence to 1 and produce a chain every verifier rejects as
// a replay — the failure would appear far from the cause.
func readChainState(path string) (chainState, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return chainState{}, nil
	}
	if err != nil {
		return chainState{}, fmt.Errorf("read state %s: %w", path, err)
	}
	var s chainState
	if err := json.Unmarshal(raw, &s); err != nil {
		return chainState{}, fmt.Errorf("state file %s is malformed (%v); fix or remove it, because "+
			"starting over would reset the sequence and produce a chain every verifier rejects", path, err)
	}
	return s, nil
}

func writeChainState(path string, s chainState) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}
