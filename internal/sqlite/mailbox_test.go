package sqlite_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/sqlite"
)

func openMessageStore(t *testing.T) (*sqlite.DB, *sqlite.MessageStore) {
	t.Helper()

	db, err := sqlite.Open(filepath.Join(t.TempDir(), "node.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, sqlite.NewMessageStore(db)
}

func msg(id, agent, kind string, payload []byte) sqlite.Message {
	return sqlite.Message{
		ID:         id,
		AgentID:    agent,
		Kind:       kind,
		Payload:    payload,
		ReceivedAt: time.Unix(1791015800, 0),
	}
}

func TestMessageStore_PutThenGet(t *testing.T) {
	_, ms := openMessageStore(t)

	m := msg("0xabc", "agent:eip155:8453:0x1", "receipt", []byte(`{"a":1}`))

	stored, err := ms.Put(m)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !stored {
		t.Error("the first Put should report stored=true")
	}

	got, ok := ms.Get("0xabc")
	if !ok {
		t.Fatal("Get did not find the message")
	}
	if got.AgentID != m.AgentID || got.Kind != m.Kind {
		t.Errorf("Get = %+v, want agent/kind to match %+v", got, m)
	}
	// The payload must round-trip byte-for-byte: a receipt's signature covers an
	// exact byte sequence, so any reserialization would invalidate it.
	if string(got.Payload) != string(m.Payload) {
		t.Errorf("payload = %q, want %q", got.Payload, m.Payload)
	}
}

// TestMessageStore_DedupByID is S5-3: a redelivery must store once.
func TestMessageStore_DedupByID(t *testing.T) {
	_, ms := openMessageStore(t)

	m := msg("0xdup", "agent:a", "receipt", []byte(`{"x":1}`))

	for i := 0; i < 5; i++ {
		stored, err := ms.Put(m)
		if err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
		wantStored := i == 0
		if stored != wantStored {
			t.Errorf("Put %d: stored = %v, want %v", i, stored, wantStored)
		}
	}

	if got := ms.Count(); got != 1 {
		t.Errorf("Count = %d, want 1 — a repeat delivery must not add a row", got)
	}
}

// TestMessageStore_ByAgentIsolates: a pull must return only the requesting
// agent's messages, or a node would leak one user's traffic to another.
func TestMessageStore_ByAgentIsolates(t *testing.T) {
	_, ms := openMessageStore(t)

	const alice = "agent:a"
	const bob = "agent:b"

	for i := 0; i < 3; i++ {
		if _, err := ms.Put(msg(fmt.Sprintf("0xa%d", i), alice, "receipt", []byte(`{"n":1}`))); err != nil {
			t.Fatalf("Put alice: %v", err)
		}
	}
	if _, err := ms.Put(msg("0xb0", bob, "receipt", []byte(`{"n":2}`))); err != nil {
		t.Fatalf("Put bob: %v", err)
	}

	got, err := ms.ByAgent(alice, 0)
	if err != nil {
		t.Fatalf("ByAgent: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("alice got %d message(s), want 3", len(got))
	}
	for _, m := range got {
		if m.AgentID != alice {
			t.Errorf("alice's pull returned a message for %q", m.AgentID)
		}
	}
}

func TestMessageStore_ByAgentLimit(t *testing.T) {
	_, ms := openMessageStore(t)

	for i := 0; i < 10; i++ {
		if _, err := ms.Put(msg(fmt.Sprintf("0x%02d", i), "agent:a", "receipt", []byte(`{}`))); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	got, err := ms.ByAgent("agent:a", 4)
	if err != nil {
		t.Fatalf("ByAgent: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("len = %d, want 4", len(got))
	}

	// Zero means "everything", which is what the HTTP layer relies on when no
	// limit is requested.
	all, err := ms.ByAgent("agent:a", 0)
	if err != nil {
		t.Fatalf("ByAgent(0): %v", err)
	}
	if len(all) != 10 {
		t.Errorf("len = %d, want 10 with no limit", len(all))
	}
}

func TestMessageStore_Validation(t *testing.T) {
	_, ms := openMessageStore(t)

	cases := map[string]sqlite.Message{
		"no id":      msg("", "agent:a", "receipt", []byte(`{}`)),
		"no agent":   msg("0x1", "", "receipt", []byte(`{}`)),
		"no kind":    msg("0x2", "agent:a", "", []byte(`{}`)),
		"no payload": msg("0x3", "agent:a", "receipt", nil),
	}

	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ms.Put(m); err == nil {
				t.Error("Put must reject this message")
			}
		})
	}
}

func TestMessageStore_AgentCount(t *testing.T) {
	_, ms := openMessageStore(t)

	if got := ms.AgentCount(); got != 0 {
		t.Errorf("AgentCount on an empty store = %d, want 0", got)
	}

	for i, agent := range []string{"a", "a", "b", "c"} {
		if _, err := ms.Put(msg(fmt.Sprintf("0x%d", i), "agent:"+agent, "receipt", []byte(`{}`))); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	if got := ms.AgentCount(); got != 3 {
		t.Errorf("AgentCount = %d, want 3 distinct agents", got)
	}
}

// TestMessageStore_SurvivesReopen: a node restart must not lose mail, because the
// sender has already been told the message was accepted.
func TestMessageStore_SurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.db")

	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ms := sqlite.NewMessageStore(db)
	if _, err := ms.Put(msg("0xkeep", "agent:a", "receipt", []byte(`{"keep":true}`))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	got, ok := sqlite.NewMessageStore(reopened).Get("0xkeep")
	if !ok {
		t.Fatal("the message did not survive a restart")
	}
	if string(got.Payload) != `{"keep":true}` {
		t.Errorf("payload = %q after reopen", got.Payload)
	}
}
