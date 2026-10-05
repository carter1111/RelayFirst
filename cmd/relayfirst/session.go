package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

  relayfirst session open  [flags]
  relayfirst session close [flags]
  relayfirst session show  [flags]     Print the local chain state and the next event
  relayfirst session grant [flags]     OWNER-side: sign a delegation grant for a session key

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
		return fmt.Errorf("session needs a subcommand (open, close, show or grant)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "open", "close", "show":
		return runSessionEmit(sub, rest)
	case "grant":
		return runSessionGrant(rest)
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
