package store

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/scoring"
)

// newTestDB returns a temporary on-disk database.
//
// It deliberately uses a file rather than ":memory:" so that re-opening can be
// tested. Proving durability is the entire point of this package, and an
// in-memory database cannot demonstrate it.
func newTestDB(t *testing.T) (*DB, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "relayfirst.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

const (
	agentA     = "agent:eip155:8453:0x000000000000000000000000000000000000000a"
	agentB     = "agent:eip155:8453:0x000000000000000000000000000000000000000b"
	artifactK1 = "sha256:aaaa"
)

// interface conformance --------------------------------------------------

func TestLedgersSatisfyScoringInterfaces(t *testing.T) {
	db, _ := newTestDB(t)

	// Assigning to the interface types is the assertion; the compile-time
	// declarations in ledger.go also guard this, and this test documents intent.
	var _ scoring.Ledger = NewArtifactLedger(db)
	var _ scoring.PointsLedger = NewPointsLedger(db)
}

// artifact ledger --------------------------------------------------------

func TestArtifactLedger_FirstSightingIsGlobal(t *testing.T) {
	db, _ := newTestDB(t)
	l := NewArtifactLedger(db)
	at := time.Unix(1791015800, 0)

	seen, err := l.Observe(artifactK1, agentA, at)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !seen {
		t.Fatal("first observation should be novel")
	}

	// A DIFFERENT agent observing the same artifact must not be novel: the key
	// carries no agent component (invariant A6).
	seen, err = l.Observe(artifactK1, agentB, at)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if seen {
		t.Fatal("second observer must not be novel — the ledger is global")
	}

	s, ok := l.Lookup(artifactK1)
	if !ok {
		t.Fatal("Lookup should find the sighting")
	}
	if s.FirstAgentID != agentA {
		t.Errorf("FirstAgentID = %q, want %q", s.FirstAgentID, agentA)
	}
	if s.SeenCount != 2 {
		t.Errorf("SeenCount = %d, want 2", s.SeenCount)
	}
	if l.Len() != 1 {
		t.Errorf("Len = %d, want 1 distinct artifact", l.Len())
	}
}

// TestArtifactLedger_SurvivesReopen is the durability claim in code.
func TestArtifactLedger_SurvivesReopen(t *testing.T) {
	db, path := newTestDB(t)
	l := NewArtifactLedger(db)
	at := time.Unix(1791015800, 0)

	if _, err := l.Observe(artifactK1, agentA, at); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Re-open the same file: the sighting must still be there.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()

	l2 := NewArtifactLedger(db2)
	if l2.Len() != 1 {
		t.Fatalf("after reopen Len = %d, want 1 — the ledger must be durable", l2.Len())
	}

	// And novelty must still be consumed.
	seen, err := l2.Observe(artifactK1, agentB, at)
	if err != nil {
		t.Fatalf("Observe after reopen: %v", err)
	}
	if seen {
		t.Error("a durable ledger must not forget an artifact across a restart")
	}
}

func TestArtifactLedger_RejectsBadInput(t *testing.T) {
	db, _ := newTestDB(t)
	l := NewArtifactLedger(db)
	at := time.Unix(1791015800, 0)

	if _, err := l.Observe("", agentA, at); err == nil {
		t.Error("empty artifact key must be rejected")
	}
	if _, err := l.Observe(artifactK1, "", at); err == nil {
		t.Error("empty agent id must be rejected")
	}
}

func TestArtifactLedger_LookupMissing(t *testing.T) {
	db, _ := newTestDB(t)
	l := NewArtifactLedger(db)

	if _, ok := l.Lookup("sha256:nope"); ok {
		t.Error("Lookup for an unknown artifact must report not-found")
	}
}

func TestArtifactLedger_ConcurrentObserveYieldsOneWinner(t *testing.T) {
	db, _ := newTestDB(t)
	l := NewArtifactLedger(db)
	at := time.Unix(1791015800, 0)

	const workers = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners int
	)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seen, err := l.Observe("sha256:contended", agentA, at)
			if err != nil {
				t.Errorf("Observe: %v", err)
				return
			}
			if seen {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if winners != 1 {
		t.Errorf("%d workers claimed novelty, want exactly 1", winners)
	}
}

// points ledger ----------------------------------------------------------

func TestPointsLedger_CreditAndBalance(t *testing.T) {
	db, _ := newTestDB(t)
	l := NewPointsLedger(db)
	at := time.Unix(1791015800, 0)

	written, err := l.Credit("r1", agentA, 42, 10.0, at)
	if err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if !written {
		t.Fatal("first credit should write")
	}

	if got := l.Balance(agentA); got != 10.0 {
		t.Errorf("Balance = %v, want 10", got)
	}
	if got := l.EpochBalance(agentA, 42); got != 10.0 {
		t.Errorf("EpochBalance = %v, want 10", got)
	}
	if got := l.EpochBalance(agentA, 43); got != 0 {
		t.Errorf("EpochBalance for another epoch = %v, want 0", got)
	}
	if got := l.AgentCount(); got != 1 {
		t.Errorf("AgentCount = %d, want 1", got)
	}
}

func TestPointsLedger_IdempotentPerReceipt(t *testing.T) {
	db, _ := newTestDB(t)
	l := NewPointsLedger(db)
	at := time.Unix(1791015800, 0)

	for i := 0; i < 5; i++ {
		written, err := l.Credit("r1", agentA, 1, 10.0, at)
		if err != nil {
			t.Fatalf("Credit: %v", err)
		}
		if i == 0 && !written {
			t.Fatal("first credit should write")
		}
		if i > 0 && written {
			t.Fatalf("credit %d should be a no-op", i)
		}
	}

	if got := l.Balance(agentA); got != 10.0 {
		t.Errorf("Balance after 5 duplicates = %v, want 10", got)
	}
	if got := len(l.Entries()); got != 1 {
		t.Errorf("Entries = %d, want 1", got)
	}
}

func TestPointsLedger_SurvivesReopen(t *testing.T) {
	db, path := newTestDB(t)
	l := NewPointsLedger(db)
	at := time.Unix(1791015800, 0)

	if _, err := l.Credit("r1", agentA, 1, 12.5, at); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	db.Close()

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()

	l2 := NewPointsLedger(db2)
	if got := l2.Balance(agentA); got != 12.5 {
		t.Errorf("Balance after reopen = %v, want 12.5 — points must be durable", got)
	}
	// Idempotency must also survive: the receipt is still credited.
	written, err := l2.Credit("r1", agentA, 1, 12.5, at)
	if err != nil {
		t.Fatalf("Credit after reopen: %v", err)
	}
	if written {
		t.Error("a durable ledger must not re-credit an already-credited receipt")
	}
}

func TestPointsLedger_RejectsBadInput(t *testing.T) {
	db, _ := newTestDB(t)
	l := NewPointsLedger(db)
	at := time.Unix(1791015800, 0)

	if _, err := l.Credit("", agentA, 1, 10, at); err == nil {
		t.Error("empty receipt id must be rejected")
	}
	if _, err := l.Credit("r1", "", 1, 10, at); err == nil {
		t.Error("empty agent id must be rejected")
	}
	// A non-positive award writes nothing but is not an error.
	written, err := l.Credit("r1", agentA, 1, 0, at)
	if err != nil {
		t.Fatalf("Credit(0): %v", err)
	}
	if written {
		t.Error("a zero award must not write an entry")
	}
}

func TestPointsLedger_EntryAndEntries(t *testing.T) {
	db, _ := newTestDB(t)
	l := NewPointsLedger(db)
	at := time.Unix(1791015800, 0)

	if _, ok := l.Entry("missing"); ok {
		t.Error("Entry for an unknown receipt must report not-found")
	}

	for i, id := range []string{"r1", "r2", "r3"} {
		if _, err := l.Credit(id, agentA, 1, float64(i+1), at); err != nil {
			t.Fatalf("Credit: %v", err)
		}
	}

	e, ok := l.Entry("r2")
	if !ok {
		t.Fatal("Entry should be found")
	}
	if e.AgentID != agentA || e.Points != 2 {
		t.Errorf("unexpected entry %+v", e)
	}
	if got := len(l.Entries()); got != 3 {
		t.Errorf("Entries = %d, want 3", got)
	}
}

// TestPointsLedger_FixedPointRoundTrip: micro-point storage must not lose value.
func TestPointsLedger_FixedPointRoundTrip(t *testing.T) {
	db, _ := newTestDB(t)
	l := NewPointsLedger(db)
	at := time.Unix(1791015800, 0)

	// A value with sub-micro precision, to confirm the stored form is exact at
	// the declared resolution rather than drifting.
	const each = 0.000001
	const n = 1000

	for i := 0; i < n; i++ {
		id := "r" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if _, err := l.Credit(id, agentA, 1, each, at); err != nil {
			t.Fatalf("Credit(%s): %v", id, err)
		}
	}

	want := float64(n) * each
	if got := l.Balance(agentA); got != want {
		t.Errorf("Balance = %v, want %v (fixed-point round trip must be exact)", got, want)
	}
}

// cross-implementation parity -------------------------------------------

// cross-implementation parity -------------------------------------------

// cross-implementation parity -------------------------------------------

// TestLedgersAgreeWithInMemory pins that the durable implementation is
// behaviourally interchangeable with the in-memory one. If these ever diverge,
// which ledger a deployment happened to use would silently change outcomes.
func TestLedgersAgreeWithInMemory(t *testing.T) {
	db, _ := newTestDB(t)
	sqlLedger := NewArtifactLedger(db)
	memLedger := scoring.NewMemLedger()
	at := time.Unix(1791015800, 0)

	keys := []struct {
		key   string
		agent string
	}{
		{"sha256:k1", agentA},
		{"sha256:k1", agentB}, // repeat by another agent
		{"sha256:k2", agentA},
		{"sha256:k1", agentA}, // repeat by the same agent
		{"sha256:k3", agentB},
	}

	for i, c := range keys {
		sqlSeen, err := sqlLedger.Observe(c.key, c.agent, at)
		if err != nil {
			t.Fatalf("sql Observe: %v", err)
		}
		memSeen, err := memLedger.Observe(c.key, c.agent, at)
		if err != nil {
			t.Fatalf("mem Observe: %v", err)
		}
		if sqlSeen != memSeen {
			t.Errorf("step %d (%s, %s): sql seen=%v but mem seen=%v",
				i, c.key, c.agent, sqlSeen, memSeen)
		}
	}

	if sqlLedger.Len() != memLedger.Len() {
		t.Errorf("Len differs: sql=%d mem=%d", sqlLedger.Len(), memLedger.Len())
	}
}

// TestNoTransferCapabilityInSchema asserts the storage layer cannot move points
// either. Invariant A5 must hold at the schema, not only at the Go interface.
func TestNoTransferCapabilityInSchema(t *testing.T) {
	db, _ := newTestDB(t)

	rows, err := db.Handle().Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()

	forbidden := []string{"transfer", "balance", "wallet", "account", "withdraw"}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		for _, bad := range forbidden {
			if name == bad {
				t.Errorf("table %q looks like points could be moved between accounts (invariant A5)", name)
			}
		}
	}
}
