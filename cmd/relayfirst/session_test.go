package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/a2a"
	"github.com/relayfirst/relayfirst/internal/delegationsign"
	"github.com/relayfirst/relayfirst/internal/eip712"
	"github.com/relayfirst/relayfirst/internal/eventsign"
)

// eventRelay is a relay that stores envelopes and serves them back, so a producer and a reader
// can be exercised against the same store the way two separate machines would.
//
// It is deliberately dumb, like the real node: it keys envelopes by the addressee and returns
// them verbatim, without decoding or checking the payload. That is the property the reader has
// to work with (MVP.md §7.1), so a test relay that validated would be testing the wrong thing.
type eventRelay struct {
	mu      sync.Mutex
	byAgent map[string][]map[string]json.RawMessage
}

func newEventRelay() *eventRelay {
	return &eventRelay{byAgent: map[string][]map[string]json.RawMessage{}}
}

func (r *eventRelay) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/messages":
			var env map[string]json.RawMessage
			if err := json.NewDecoder(req.Body).Decode(&env); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var agent string
			_ = json.Unmarshal(env["agentId"], &agent)
			r.mu.Lock()
			r.byAgent[agent] = append(r.byAgent[agent], env)
			r.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))

		case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/messages/"):
			agent := strings.TrimPrefix(req.URL.Path, "/messages/")
			r.mu.Lock()
			msgs := append([]map[string]json.RawMessage(nil), r.byAgent[agent]...)
			r.mu.Unlock()
			type outMsg struct {
				ID      string          `json:"id"`
				AgentID string          `json:"agentId"`
				Kind    string          `json:"kind"`
				Payload json.RawMessage `json:"payload"`
			}
			out := make([]outMsg, 0, len(msgs))
			for _, env := range msgs {
				var id, agentID, kind string
				_ = json.Unmarshal(env["id"], &id)
				_ = json.Unmarshal(env["agentId"], &agentID)
				_ = json.Unmarshal(env["kind"], &kind)
				out = append(out, outMsg{ID: id, AgentID: agentID, Kind: kind, Payload: env["payload"]})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"agentId": agent, "count": len(out), "messages": out,
			})

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// inject stores a raw envelope under an agent, so a test can place a non-event message in a
// stream without going through a producer.
func (r *eventRelay) inject(agent, kind string, payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byAgent[agent] = append(r.byAgent[agent], map[string]json.RawMessage{
		"id":      mustJSON(kind + "-1"),
		"agentId": mustJSON(agent),
		"kind":    mustJSON(kind),
		"payload": mustJSON(payload), // []byte marshals to base64, as on the wire
	})
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// captureOutput runs fn with stdout redirected and returns the decoded JSON it printed.
func captureOutput(t *testing.T, fn func() error) (map[string]any, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	if buf.Len() == 0 {
		return nil, runErr
	}
	var out map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &out); jerr != nil {
		return nil, fmt.Errorf("printed output is not JSON (%v): %s", jerr, buf.String())
	}
	return out, runErr
}

// These tests cover the session producer, which is the first thing in the repository that CREATES a
// signed A2A event.
//
// Before it, three layers of A2A machinery were complete, tested and unreachable: a2a defined events
// and the state machines, eventsign signed and authorized them, and nothing produced one. So the
// properties worth testing are the ones that make the producer usable rather than merely present:
// that it signs what it sends, that it chains correctly across invocations, and that a failed send
// does not corrupt the chain.

const sessionTestKey = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"

// ownerTestKey is a DIFFERENT key from sessionTestKey, which is what makes it a delegation
// rather than the owner wearing a second name.
const ownerTestKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"

// recordingRelay captures the envelopes a producer sends.
type recordingRelay struct {
	mu        sync.Mutex
	envelopes []map[string]json.RawMessage
	status    int
}

func (r *recordingRelay) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		_ = req
		r.mu.Lock()
		defer r.mu.Unlock()
		var env map[string]json.RawMessage
		if err := json.NewDecoder(req.Body).Decode(&env); err == nil {
			r.envelopes = append(r.envelopes, env)
		}
		status := r.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
}

func (r *recordingRelay) captured(t *testing.T) []a2a.Event {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]a2a.Event, 0, len(r.envelopes))
	for _, env := range r.envelopes {
		raw, ok := env["payload"]
		if !ok {
			t.Fatal("an envelope must carry a payload")
		}
		// The envelope's payload is base64 in JSON, so it decodes through []byte.
		var payload []byte
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		e, err := a2a.DecodeEvent(payload)
		if err != nil {
			t.Fatalf("decode event: %v", err)
		}
		out = append(out, e)
	}
	return out
}

// TestSession_OpenSignsAndSends is the round trip that closes the loop.
func TestSession_OpenSignsAndSends(t *testing.T) {
	relay := &recordingRelay{}
	srv := httptest.NewServer(relay.handler())
	defer srv.Close()

	state := filepath.Join(t.TempDir(), "state.json")
	if err := runSession([]string{"open", "--key", sessionTestKey, "--relay", srv.URL, "--state", state}); err != nil {
		t.Fatalf("session open: %v", err)
	}

	events := relay.captured(t)
	if len(events) != 1 {
		t.Fatalf("the relay received %d events, want 1", len(events))
	}
	e := events[0]

	// It is SIGNED, and the signature verifies. This is the property that was impossible before the
	// eventsign layer existed.
	if e.Signature == "" {
		t.Fatal("the produced event must be signed")
	}
	if _, err := eventsign.Verify(e); err != nil {
		t.Fatalf("the produced event's signature must verify: %v", err)
	}
	if e.Type != a2a.EventSessionOpen {
		t.Errorf("type = %s, want %s", e.Type, a2a.EventSessionOpen)
	}
	if e.Sequence != 1 {
		t.Errorf("the first event must be sequence 1, got %d", e.Sequence)
	}
	if e.PreviousEventHash != "" {
		t.Errorf("sequence 1 must claim no predecessor, got %q", e.PreviousEventHash)
	}
}

// TestSession_ChainsAcrossInvocations is the property that makes separate processes usable.
//
// Each command is its own process, so the chain state has to survive between them. A producer that
// restarted the sequence would emit two events at sequence 1, which every verifier rejects as a
// replay — and the failure would appear at a reader, far from the cause.
func TestSession_ChainsAcrossInvocations(t *testing.T) {
	relay := &recordingRelay{}
	srv := httptest.NewServer(relay.handler())
	defer srv.Close()

	state := filepath.Join(t.TempDir(), "state.json")
	if err := runSession([]string{"open", "--key", sessionTestKey, "--relay", srv.URL, "--state", state}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := runSession([]string{"close", "--key", sessionTestKey, "--relay", srv.URL, "--state", state}); err != nil {
		t.Fatalf("close: %v", err)
	}

	events := relay.captured(t)
	if len(events) != 2 {
		t.Fatalf("the relay received %d events, want 2", len(events))
	}

	// The sequence advances, and the second links to the first. VerifyChain is what a reader runs, so
	// passing it here means the producer's output is readable rather than merely well-formed.
	if events[1].Sequence != 2 {
		t.Fatalf("the second event must be sequence 2, got %d", events[1].Sequence)
	}
	if err := a2a.ValidateChain(keccakHasher, events); err != nil {
		t.Fatalf("the produced chain must validate as a reader would check it: %v", err)
	}
}

// TestSession_FailedSendDoesNotAdvanceTheChain is the correctness detail most easily got wrong.
//
// If the sequence advanced on a send that failed, the next event would claim a predecessor that no
// verifier has — a gap. So the state must only move after the relay accepted the event.
func TestSession_FailedSendDoesNotAdvanceTheChain(t *testing.T) {
	relay := &recordingRelay{status: http.StatusInternalServerError}
	srv := httptest.NewServer(relay.handler())
	defer srv.Close()

	state := filepath.Join(t.TempDir(), "state.json")
	err := runSession([]string{"open", "--key", sessionTestKey, "--relay", srv.URL, "--state", state})
	if err == nil {
		t.Fatal("a failing relay must produce an error")
	}

	// The state file must not exist, or must still say nothing was sent.
	raw, readErr := os.ReadFile(state)
	if readErr == nil && strings.Contains(string(raw), `"sequence": 1`) {
		t.Fatal("the chain must not advance when the send failed, or the next event would claim a " +
			"predecessor no verifier has")
	}
}

// TestSession_NoRelayPrintsWithoutAdvancing is the inspection path.
//
// Advancing here would make the printed event unsendable afterwards, which is a trap for anyone
// checking what would be sent before sending it.
func TestSession_NoRelayPrintsWithoutAdvancing(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	if err := runSession([]string{"open", "--key", sessionTestKey, "--state", state}); err != nil {
		t.Fatalf("open without a relay must succeed: %v", err)
	}
	if _, err := os.Stat(state); err == nil {
		t.Fatal("without a relay the chain must not advance: the printed event would then be " +
			"unsendable, because its sequence would be behind the state")
	}
}

// TestSession_RefusesAStateFileForAnotherSession keeps a mismatch from producing a chain that fails
// later.
func TestSession_RefusesAStateFileForAnotherSession(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")

	// A state file for a different actor.
	other := `{"sessionId":"ses-other","actor":"agent:eip155:8453:0x0000000000000000000000000000000000000009","sequence":5,"lastHash":"0xab"}`
	if err := os.WriteFile(state, []byte(other), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	err := runSession([]string{"open", "--key", sessionTestKey, "--state", state})
	if err == nil {
		t.Fatal("a state file belonging to another session or actor must be refused")
	}
	if !strings.Contains(err.Error(), "belongs to session") {
		t.Errorf("the error must name the mismatch, got: %v", err)
	}
}

// TestSession_MalformedStateIsAnErrorNotAFreshStart is the failure that would look like a replay.
func TestSession_MalformedStateIsAnErrorNotAFreshStart(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(state, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	err := runSession([]string{"open", "--key", sessionTestKey, "--state", state})
	if err == nil {
		t.Fatal("a malformed state file must be an error: starting over would reset the sequence and " +
			"produce a chain every verifier rejects as a replay")
	}
}

// TestSession_ShowReportsTheNextEvent is the inspection command, and it must not send or advance.
func TestSession_ShowReportsTheNextEvent(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	if err := runSession([]string{"show", "--key", sessionTestKey, "--state", state}); err != nil {
		t.Fatalf("show: %v", err)
	}
	if _, err := os.Stat(state); err == nil {
		t.Fatal("show must not write state")
	}
}

func TestSession_RequiresAKey(t *testing.T) {
	if err := runSession([]string{"open", "--state", filepath.Join(t.TempDir(), "s.json")}); err == nil {
		t.Fatal("without a key the event cannot be signed, and an unsigned event proves nothing")
	}
}

func TestSession_RejectsUnknownSubcommand(t *testing.T) {
	if err := runSession([]string{"reopen"}); err == nil {
		t.Fatal("an unknown subcommand must be refused")
	}
}

// TestSession_EventIDIsUnique keeps the relay's dedup from silently dropping a second event.
//
// The event id is the relay's idempotency key, so a collision would look like a lost event rather
// than a duplicate id.
func TestSession_EventIDIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id, err := newEventID()
		if err != nil {
			t.Fatalf("newEventID: %v", err)
		}
		if seen[id] {
			t.Fatalf("event id %q was produced twice in 200 attempts", id)
		}
		seen[id] = true
	}
}

// TestSession_ChainVerifiesWithTheRealHasher confirms the producer's chain survives the reader's own
// validation, which is the end-to-end property: a producer whose output a reader rejects is useless.
func TestSession_ChainVerifiesWithTheRealHasher(t *testing.T) {
	relay := &recordingRelay{}
	srv := httptest.NewServer(relay.handler())
	defer srv.Close()

	state := filepath.Join(t.TempDir(), "state.json")
	for _, sub := range []string{"open", "close"} {
		if err := runSession([]string{sub, "--key", sessionTestKey, "--relay", srv.URL, "--state", state}); err != nil {
			t.Fatalf("%s: %v", sub, err)
		}
	}

	events := relay.captured(t)
	// The reader validates with keccak256, the real hash, so the producer must have used the same one.
	if err := a2a.ValidateChain(func(b []byte) []byte { return eip712.Keccak256(b) }, events); err != nil {
		t.Fatalf("the produced chain must validate under the real hasher: %v", err)
	}
}

// TestSession_StateFileIsNotWorldReadable keeps a file that holds no secrets from being a habit that
// becomes one when a secret is added.
func TestSession_StateFileIsNotWorldReadable(t *testing.T) {
	relay := &recordingRelay{}
	srv := httptest.NewServer(relay.handler())
	defer srv.Close()

	state := filepath.Join(t.TempDir(), "state.json")
	if err := runSession([]string{"open", "--key", sessionTestKey, "--relay", srv.URL, "--state", state}); err != nil {
		t.Fatalf("open: %v", err)
	}
	info, err := os.Stat(state)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("state file mode is %v, want no group or world access; the file holds no secret today "+
			"but the habit is what matters", info.Mode().Perm())
	}
}

// idForTest derives the agentId an agent key corresponds to.
func idForTest(t *testing.T, key string) string {
	t.Helper()
	id, err := agentIDFromKey(key, 8453)
	if err != nil {
		t.Fatalf("derive id: %v", err)
	}
	return id
}

// TestSessionGrant_SignsAVerifiableGrant is the producer's basic contract.
func TestSessionGrant_SignsAVerifiableGrant(t *testing.T) {
	sessionID := idForTest(t, sessionTestKey)
	if err := runSession([]string{
		"grant", "--key", ownerTestKey, "--session-key", sessionID,
		"--scopes", "session:event", "--out", filepath.Join(t.TempDir(), "grant.json"),
	}); err != nil {
		t.Fatalf("session grant: %v", err)
	}
}

// TestSessionGrant_RoundTripsIntoAuthorization is the property that closes the loop.
//
// It is not enough for the grant to be signed; the whole point is that a session key's
// event, presented with this grant, is accepted by the authorizer. The grant's validity
// window starts at issuance, so the check runs with a clock inside that window.
func TestSessionGrant_RoundTripsIntoAuthorization(t *testing.T) {
	// Issue a grant valid from now, over a long window.
	dir := t.TempDir()
	out := filepath.Join(dir, "grant.json")
	sessionID := idForTest(t, sessionTestKey)
	if err := runSession([]string{
		"grant", "--key", ownerTestKey, "--session-key", sessionID,
		"--scopes", "session:event", "--valid-for", "1h", "--out", out,
	}); err != nil {
		t.Fatalf("session grant: %v", err)
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read grant: %v", err)
	}
	var g delegationsign.SignedGrant
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	if err := g.Verify(); err != nil {
		t.Fatalf("the emitted grant must verify on its own: %v", err)
	}

	// The session key signs an event whose actor is the session key (identity model (ii),
	// matching ARCHITECTURE.md §4.2: agentId is the signer, the grant carries the owner).
	e := eventForTest(t, sessionID)
	signed, err := eventsign.Sign(e, sessionTestKey, 8453)
	if err != nil {
		t.Fatalf("sign session event: %v", err)
	}

	scope, err := eventsign.ScopeOfEvent(e.Type)
	if err != nil {
		t.Fatalf("scope of event: %v", err)
	}
	if _, err := eventsign.AuthorizeEventWithGrant(eventsign.AuthorizeInput{
		Event: signed, Grant: g, Scope: scope,
		CurrentNonce: g.Grant.Nonce, Now: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("a session event under the grant this CLI produced must be authorized: %v", err)
	}
}

// TestSessionGrant_RefusesAnUndelegatedScope proves the grant's scope is enforced end to end:
// a grant for session events must not authorize a task-lifecycle event.
func TestSessionGrant_RefusesAnUndelegatedScope(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "g.json")
	sessionID := idForTest(t, sessionTestKey)
	if err := runSession([]string{
		"grant", "--key", ownerTestKey, "--session-key", sessionID,
		"--scopes", "session:event", "--valid-for", "1h", "--out", out,
	}); err != nil {
		t.Fatalf("session grant: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read grant: %v", err)
	}
	var g delegationsign.SignedGrant
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("decode grant: %v", err)
	}

	// A task-lifecycle event signed by the session key: the signer is right but the scope was
	// never granted, so authorization must refuse.
	e := a2a.Event{
		EventID:   "evt-scope-test",
		SessionID: "ses-grant-test",
		TaskID:    "tsk-1",
		Actor:     sessionID,
		Type:      a2a.EventTaskCompleted,
		Sequence:  1,
		IssuedAt:  time.Now().UTC(),
	}
	signed, err := eventsign.Sign(e, sessionTestKey, 8453)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	scope, err := eventsign.ScopeOfEvent(e.Type)
	if err != nil {
		t.Fatalf("scope of event: %v", err)
	}
	_, err = eventsign.AuthorizeEventWithGrant(eventsign.AuthorizeInput{
		Event: signed, Grant: g, Scope: scope,
		CurrentNonce: g.Grant.Nonce, Now: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("a session:event grant must not authorize a task-lifecycle event")
	}
	if !strings.Contains(err.Error(), "not granted") {
		t.Errorf("the refusal must name the scope problem, got: %v", err)
	}
}

// TestSessionGrant_RejectsAnUnknownScope keeps a typo from producing a grant that covers nothing.
func TestSessionGrant_RejectsAnUnknownScope(t *testing.T) {
	sessionID := idForTest(t, sessionTestKey)
	err := runSession([]string{
		"grant", "--key", ownerTestKey, "--session-key", sessionID,
		"--scopes", "session:event,receipt:anything",
	})
	if err == nil {
		t.Fatal("an unknown scope must be refused rather than silently dropped")
	}
	if !strings.Contains(err.Error(), "not grantable") {
		t.Errorf("the refusal must name the problem, got: %v", err)
	}
}

// TestSessionGrant_RejectsTheOwnerAsSessionKey keeps a no-op delegation from looking valid.
func TestSessionGrant_RejectsTheOwnerAsSessionKey(t *testing.T) {
	ownerID := idForTest(t, ownerTestKey)
	err := runSession([]string{
		"grant", "--key", ownerTestKey, "--session-key", ownerID,
	})
	if err == nil {
		t.Fatal("a session key equal to the owner is not a delegation and must be refused")
	}
}

// TestSessionGrant_RequiresASessionKey keeps the delegation target explicit.
func TestSessionGrant_RequiresASessionKey(t *testing.T) {
	if err := runSession([]string{"grant", "--key", ownerTestKey}); err == nil {
		t.Fatal("without --session-key the grant has no delegate and must be refused")
	}
}

// TestSessionGrant_SendsAGrantEnvelope checks the transport half: a grant reaches a relay
// under its own kind, so nothing downstream mistakes it for an event.
func TestSessionGrant_SendsAGrantEnvelope(t *testing.T) {
	relay := &recordingRelay{}
	srv := httptest.NewServer(relay.handler())
	defer srv.Close()

	sessionID := idForTest(t, sessionTestKey)
	if err := runSession([]string{
		"grant", "--key", ownerTestKey, "--session-key", sessionID,
		"--relay", srv.URL, "--valid-for", "1h",
	}); err != nil {
		t.Fatalf("session grant: %v", err)
	}

	relay.mu.Lock()
	defer relay.mu.Unlock()
	if len(relay.envelopes) != 1 {
		t.Fatalf("the relay received %d envelopes, want 1", len(relay.envelopes))
	}
	var kind string
	if err := json.Unmarshal(relay.envelopes[0]["kind"], &kind); err != nil {
		t.Fatalf("decode kind: %v", err)
	}
	if kind != "grant" {
		t.Fatalf("kind = %q, want %q: a grant is not an event", kind, "grant")
	}
}

// eventForTest builds a valid unsigned session event naming actor.
func eventForTest(t *testing.T, actor string) a2a.Event {
	t.Helper()
	e, err := a2a.SessionEvent("evt-grant-test", "ses-grant-test", actor, a2a.EventSessionOpen, 1, "", time.Now(), 0)
	if err != nil {
		t.Fatalf("SessionEvent: %v", err)
	}
	return e
}

// writeGrant signs a grant from ownerTestKey to the session key (sessionTestKey) and writes it.
func writeGrant(t *testing.T, scopes string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "grant.json")
	sessionID := idForTest(t, sessionTestKey)
	if err := runSession([]string{
		"grant", "--key", ownerTestKey, "--session-key", sessionID,
		"--scopes", scopes, "--valid-for", "1h", "--out", out,
	}); err != nil {
		t.Fatalf("session grant: %v", err)
	}
	return out
}

// TestSessionVerify_AuthorizesARealDelegatedEvent is the end-to-end the delegation layer was
// missing: a session key signs an event, and a reader that never saw the key accepts it because
// the owner's grant says it may.
func TestSessionVerify_AuthorizesARealDelegatedEvent(t *testing.T) {
	relay := newEventRelay()
	srv := httptest.NewServer(relay.handler())
	defer srv.Close()

	agent := idForTest(t, sessionTestKey)
	state := filepath.Join(t.TempDir(), "state.json")
	if err := runSession([]string{"open", "--key", sessionTestKey, "--relay", srv.URL, "--state", state}); err != nil {
		t.Fatalf("open: %v", err)
	}

	grantPath := writeGrant(t, "session:event")
	out, err := captureOutput(t, func() error {
		return runSession([]string{"verify", "--relay", srv.URL, "--agent", agent, "--grant", grantPath})
	})
	if err != nil {
		t.Fatalf("session verify: %v", err)
	}
	summary := out["summary"].(map[string]any)
	if summary["authorized"].(float64) != 1 {
		t.Fatalf("summary = %v, want 1 authorized", summary)
	}
	if summary["refused"].(float64) != 0 {
		t.Fatalf("summary = %v, want 0 refused", summary)
	}
}

// TestSessionVerify_RefusesAnUndelegatedScope is the authority half, end to end: the same event
// signed by the same key is refused when the grant does not cover its scope.
func TestSessionVerify_RefusesAnUndelegatedScope(t *testing.T) {
	relay := newEventRelay()
	srv := httptest.NewServer(relay.handler())
	defer srv.Close()

	agent := idForTest(t, sessionTestKey)
	state := filepath.Join(t.TempDir(), "state.json")
	if err := runSession([]string{"open", "--key", sessionTestKey, "--relay", srv.URL, "--state", state}); err != nil {
		t.Fatalf("open: %v", err)
	}

	// A grant that does NOT cover session events.
	grantPath := writeGrant(t, "task:progress")
	out, err := captureOutput(t, func() error {
		return runSession([]string{"verify", "--relay", srv.URL, "--agent", agent, "--grant", grantPath})
	})
	if err != nil {
		t.Fatalf("verify should not error on a refusal; it should report it: %v", err)
	}
	summary := out["summary"].(map[string]any)
	if summary["refused"].(float64) != 1 {
		t.Fatalf("summary = %v, want 1 refused", summary)
	}
	results := out["results"].([]any)
	reason, _ := results[0].(map[string]any)["reason"].(string)
	if !strings.Contains(reason, "not granted") {
		t.Errorf("the refusal must name the scope problem, got: %q", reason)
	}
}

// TestSessionVerify_RefusesARevokedGrant keeps freshness reachable through the reader: a fresher
// nonce than the grant carries must make every event refuse.
func TestSessionVerify_RefusesARevokedGrant(t *testing.T) {
	relay := newEventRelay()
	srv := httptest.NewServer(relay.handler())
	defer srv.Close()

	agent := idForTest(t, sessionTestKey)
	state := filepath.Join(t.TempDir(), "state.json")
	if err := runSession([]string{"open", "--key", sessionTestKey, "--relay", srv.URL, "--state", state}); err != nil {
		t.Fatalf("open: %v", err)
	}
	grantPath := writeGrant(t, "session:event")

	out, err := captureOutput(t, func() error {
		return runSession([]string{"verify", "--relay", srv.URL, "--agent", agent, "--grant", grantPath, "--grant-nonce", "2"})
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	results := out["results"].([]any)
	reason, _ := results[0].(map[string]any)["reason"].(string)
	if !strings.Contains(reason, "revoked") {
		t.Errorf("a higher current nonce must read as revoked, got: %q", reason)
	}
}

// TestSessionVerify_RefusesAnEventFromAnotherKey proves the reader checks the signature, not
// just the grant: an event from a key the grant did not name must be refused.
func TestSessionVerify_RefusesAnEventFromAnotherKey(t *testing.T) {
	relay := newEventRelay()
	srv := httptest.NewServer(relay.handler())
	defer srv.Close()

	// The grant names the session key, but the relay holds an event signed by a THIRD key.
	thirdID := idForTest(t, ownerTestKey)
	e := eventForTest(t, thirdID)
	signed, err := eventsign.Sign(e, ownerTestKey, 8453)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw, _ := json.Marshal(signed)
	relay.inject(thirdID, "event", raw)

	grantPath := writeGrant(t, "session:event")
	out, err := captureOutput(t, func() error {
		return runSession([]string{"verify", "--relay", srv.URL, "--agent", thirdID, "--grant", grantPath})
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	results := out["results"].([]any)
	reason, _ := results[0].(map[string]any)["reason"].(string)
	if !strings.Contains(reason, "not this grant's session key") {
		t.Errorf("an event from a key the grant did not name must be refused as a signer problem, got: %q", reason)
	}
}

// TestSessionVerify_SkipsNonEventKinds keeps a stream that also carries grants (or receipts)
// from being misread: the reader keeps only events and counts the rest.
func TestSessionVerify_SkipsNonEventKinds(t *testing.T) {
	relay := newEventRelay()
	srv := httptest.NewServer(relay.handler())
	defer srv.Close()

	agent := idForTest(t, sessionTestKey)
	relay.inject(agent, "grant", []byte(`{"not":"an event"}`))

	state := filepath.Join(t.TempDir(), "state.json")
	if err := runSession([]string{"open", "--key", sessionTestKey, "--relay", srv.URL, "--state", state}); err != nil {
		t.Fatalf("open: %v", err)
	}
	grantPath := writeGrant(t, "session:event")

	out, err := captureOutput(t, func() error {
		return runSession([]string{"verify", "--relay", srv.URL, "--agent", agent, "--grant", grantPath})
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	summary := out["summary"].(map[string]any)
	if summary["events"].(float64) != 1 {
		t.Errorf("events = %v, want 1", summary["events"])
	}
	if summary["nonEvents"].(float64) != 1 {
		t.Errorf("nonEvents = %v, want 1: the grant in the stream must be counted, not parsed as an event", summary["nonEvents"])
	}
}

// TestSessionVerify_AcceptsAnInlineGrant keeps the paste-a-grant path usable.
func TestSessionVerify_AcceptsAnInlineGrant(t *testing.T) {
	relay := newEventRelay()
	srv := httptest.NewServer(relay.handler())
	defer srv.Close()

	agent := idForTest(t, sessionTestKey)
	state := filepath.Join(t.TempDir(), "state.json")
	if err := runSession([]string{"open", "--key", sessionTestKey, "--relay", srv.URL, "--state", state}); err != nil {
		t.Fatalf("open: %v", err)
	}
	grantPath := writeGrant(t, "session:event")
	raw, err := os.ReadFile(grantPath)
	if err != nil {
		t.Fatalf("read grant: %v", err)
	}

	out, err := captureOutput(t, func() error {
		return runSession([]string{"verify", "--relay", srv.URL, "--agent", agent, "--grant", string(raw)})
	})
	if err != nil {
		t.Fatalf("verify with inline grant: %v", err)
	}
	if out["summary"].(map[string]any)["authorized"].(float64) != 1 {
		t.Fatalf("an inline grant must authorize the same as a file, got %v", out["summary"])
	}
}

// TestSessionVerify_RequiresTheGrant keeps the authority question from being skipped.
func TestSessionVerify_RequiresTheGrant(t *testing.T) {
	if err := runSession([]string{"verify", "--relay", "http://x", "--agent", "agent:eip155:8453:0x1"}); err == nil {
		t.Fatal("verify without --grant must be refused: there is no authority to check")
	}
}
