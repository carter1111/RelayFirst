package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/mining"
	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/scoring"
	"github.com/relayfirst/relayfirst/internal/store"
)

// These tests cover the anchor command's leaf-selection logic.
//
// It lives in package main because that is where the logic is, and testing it here
// is better than moving it somewhere more "testable" for its own sake. The ordering
// rule matters more than it looks: it is what lets an independent party recompute
// the same root, so a root whose ordering is not reproducible is not an anchor at
// all.

func id(n int) string { return fmt.Sprintf("0x%064x", n) }

// seededStore builds a store holding one signed receipt per (id, epoch) pair.
//
// The selection now happens in SQL, so these tests need real rows rather than in-memory
// values. That is a better test anyway: it exercises the query the CLI actually runs.
func seededStore(t *testing.T, pairs ...[2]int) *store.ReceiptStore {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "anchor.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	rs := store.NewReceiptStore(db)
	for i, p := range pairs {
		n, epoch := p[0], uint64(p[1])
		r := &receipt.Receipt{
			Schema:    receipt.Schema,
			ReceiptID: id(n),
			AgentID:   "agent:eip155:8453:0x0000000000000000000000000000000000000001",
			Epoch:     epoch,
			Task: receipt.Task{
				Type:          receipt.TaskProbe,
				Spec:          map[string]any{"url": "https://example.com"},
				SpecHash:      fmt.Sprintf("sha256:%064x", n),
				SelfGenerated: true,
			},
			Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015860},
			Result:       receipt.Result{Value: "200", Hash: fmt.Sprintf("sha256:%064x", n+1000)},
			Anchors:      []receipt.Anchor{{URL: "https://example.com", ContentHash: fmt.Sprintf("sha256:%064x", n+2000), Status: 200}},
			Verification: receipt.Verification{Status: receipt.VerificationPending},
		}
		if err := rs.Save(r, "", time.Unix(1791015800, 0)); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}
	return rs
}

// TestEpochReceipts_FiltersByEpoch: only the requested epoch's receipts may
// contribute to its root.
func TestEpochReceipts_FiltersByEpoch(t *testing.T) {
	rs := seededStore(t, [2]int{1, 10}, [2]int{2, 11}, [2]int{3, 10}, [2]int{4, 12})

	leaves, ids, err := epochReceipts(rs, 10)
	if err != nil {
		t.Fatalf("epochReceipts: %v", err)
	}
	if len(leaves) != 2 {
		t.Fatalf("got %d leaves, want 2", len(leaves))
	}
	if len(ids) != 2 {
		t.Fatalf("got %d ids, want 2", len(ids))
	}
	for _, got := range ids {
		if got == id(2) || got == id(4) {
			t.Errorf("epoch 10 picked up a receipt from another epoch: %s", got)
		}
	}
}

// TestEpochReceipts_SortsByID is the reproducibility guarantee.
//
// The root must not depend on insertion order, because insertion order is local to
// whoever wrote the receipts. Sorting by id means any party holding the same set
// arrives at the same tree.
func TestEpochReceipts_SortsByID(t *testing.T) {
	// Deliberately seeded in descending id order.
	rs := seededStore(t, [2]int{30, 5}, [2]int{10, 5}, [2]int{20, 5})

	_, ids, err := epochReceipts(rs, 5)
	if err != nil {
		t.Fatalf("epochReceipts: %v", err)
	}

	want := []string{id(10), id(20), id(30)}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids[%d] = %s, want %s (leaves must be sorted by id)", i, ids[i], want[i])
		}
	}
}

// TestEpochReceipts_IsOrderIndependent: the same set in any input order must yield
// the same leaves.
func TestEpochReceipts_IsOrderIndependent(t *testing.T) {
	orders := [][][2]int{
		{{1, 5}, {2, 5}, {3, 5}},
		{{3, 5}, {1, 5}, {2, 5}},
		{{2, 5}, {3, 5}, {1, 5}},
	}

	var first []string
	for i, pairs := range orders {
		rs := seededStore(t, pairs...)
		_, ids, err := epochReceipts(rs, 5)
		if err != nil {
			t.Fatalf("epochReceipts order %d: %v", i, err)
		}
		if i == 0 {
			first = ids
			continue
		}
		for j := range first {
			if ids[j] != first[j] {
				t.Errorf("order %d produced a different leaf sequence; the root would differ", i)
			}
		}
	}
}

func TestEpochReceipts_EmptyEpoch(t *testing.T) {
	rs := seededStore(t, [2]int{1, 10})

	leaves, ids, err := epochReceipts(rs, 99)
	if err != nil {
		t.Fatalf("epochReceipts: %v", err)
	}
	if len(leaves) != 0 || len(ids) != 0 {
		t.Errorf("an epoch with no receipts must yield no leaves, got %d", len(leaves))
	}
}

// TestEpochReceipts_RejectsUnusableID: a malformed id cannot become a Merkle leaf, and
// silently skipping it would produce a root over fewer receipts than the epoch holds — which
// would be undetectable from the root alone.
//
// The malformed row is inserted with raw SQL because Save requires a well-formed receipt, and
// the point is precisely that a bad id must not slip through the leaf builder.
func TestEpochReceipts_RejectsUnusableID(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "bad.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	rs := store.NewReceiptStore(db)

	good := &receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: id(1),
		AgentID:   "agent:eip155:8453:0x0000000000000000000000000000000000000001",
		Epoch:     5,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": "https://example.com"},
			SpecHash:      fmt.Sprintf("sha256:%064x", 1),
			SelfGenerated: true,
		},
		Work:         receipt.Work{Provider: "local", StartedAt: 1791015800, FinishedAt: 1791015860},
		Result:       receipt.Result{Value: "200", Hash: fmt.Sprintf("sha256:%064x", 2)},
		Anchors:      []receipt.Anchor{{URL: "https://example.com", ContentHash: fmt.Sprintf("sha256:%064x", 3), Status: 200}},
		Verification: receipt.Verification{Status: receipt.VerificationPending},
	}
	if err := rs.Save(good, "", time.Unix(1791015800, 0)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A row whose id is not 32 bytes of hex, inserted directly.
	if _, err := db.Handle().Exec(
		`INSERT INTO receipts (receipt_id, agent_id, epoch, task_type, body, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"not-a-valid-receipt-id", "agent:x", 5, "probe", `{}`, 1791015800); err != nil {
		t.Fatalf("raw insert: %v", err)
	}

	_, _, err = epochReceipts(rs, 5)
	if err == nil {
		t.Fatal("an unusable receipt id must be reported, not skipped — skipping would change the tree silently")
	}
	if !strings.Contains(err.Error(), "not usable as a leaf") {
		t.Errorf("the error should explain the id cannot be a leaf, got %v", err)
	}
}

func TestNextPow2For(t *testing.T) {
	cases := map[int]int{-1: 1, 0: 1, 1: 1, 2: 2, 3: 4, 4: 4, 5: 8, 16: 16, 17: 32}
	for in, want := range cases {
		if got := nextPow2For(in); got != want {
			t.Errorf("nextPow2For(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestEpochArg(t *testing.T) {
	t.Run("explicit", func(t *testing.T) {
		f := &flags{values: map[string]string{"epoch": "42"}}
		got, err := f.epochArg()
		if err != nil {
			t.Fatalf("epochArg: %v", err)
		}
		if got != 42 {
			t.Errorf("epochArg = %d, want 42", got)
		}
	})

	t.Run("defaults to now", func(t *testing.T) {
		f := &flags{values: map[string]string{}}
		got, err := f.epochArg()
		if err != nil {
			t.Fatalf("epochArg: %v", err)
		}
		if got == 0 {
			t.Error("the default epoch should be the current one, not 0")
		}
	})

	t.Run("rejects nonsense", func(t *testing.T) {
		f := &flags{values: map[string]string{"epoch": "later"}}
		if _, err := f.epochArg(); err == nil {
			t.Error("a non-numeric epoch must be rejected")
		}
	})
}

func TestIntArg(t *testing.T) {
	f := &flags{values: map[string]string{"index": "3"}}
	if got, err := f.intArg("index"); err != nil || got != 3 {
		t.Errorf("intArg(index) = %d, %v; want 3, nil", got, err)
	}

	// Missing must be an error rather than a silent zero: a proof index of 0 is a
	// real position, so defaulting to it would verify the wrong thing.
	f = &flags{values: map[string]string{}}
	if _, err := f.intArg("index"); err == nil {
		t.Error("a missing integer flag must be an error, not zero")
	}

	f = &flags{values: map[string]string{"width": "four"}}
	if _, err := f.intArg("width"); err == nil {
		t.Error("a non-numeric flag must be rejected")
	}
}

// TestRepeatedFlagsCoverEveryRepeatableFlag guards the parser's explicit list.
//
// A repeatable flag missing from that list is silently overwritten — which is
// exactly how `--semantic` was once dropped without any error, producing tasks that
// appeared to work but cost nothing.
func TestRepeatedFlagsCoverEveryRepeatableFlag(t *testing.T) {
	// Every flag the CLI documents as repeatable.
	for _, name := range []string{"source", "semantic", "relay", "sibling"} {
		if !repeatableFlags[name] {
			t.Errorf("%q is documented as repeatable but is missing from repeatableFlags; "+
				"passing it twice would silently keep only the last value", name)
		}
	}
}

// TestFirstScreen_IsInteractiveOnly pins the rule the node's banner follows too: the
// branded first screen appears on a terminal, and a piped invocation keeps the exact
// usage text a script or a `| grep` expects.
func TestFirstScreen_IsInteractiveOnly(t *testing.T) {
	// Capture what the no-argument path prints with os.Stdout on a pipe.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	runErr := run(nil)
	_ = w.Close()
	os.Stdout = old
	if runErr != nil {
		t.Fatalf("run(nil): %v", runErr)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()
	out := buf.String()

	// A pipe is not a terminal, so this must be the usage text, with no logo art.
	if !strings.Contains(out, "Usage:") {
		t.Errorf("a piped no-arg run must print usage, got:\n%s", out)
	}
	if strings.Contains(out, "██████") {
		t.Errorf("a piped run must not print the logo, got:\n%s", out)
	}
	if strings.Contains(out, "\033") {
		t.Errorf("a piped run must contain no ANSI escapes, got:\n%s", out)
	}
}

// TestStatus_NonTTYStaysJSON is the freeze that matters most for status: scripts and the
// docs read this JSON, so a piped run must keep it and carry no decoration.
func TestStatus_NonTTYStaysJSON(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "s.db")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	runErr := runStatus([]string{"--db", dbPath})
	_ = w.Close()
	os.Stdout = old
	if runErr != nil {
		t.Fatalf("status: %v", runErr)
	}

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()
	out := buf.String()

	// Valid JSON, the fields the docs name, and no ANSI.
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("a piped status must be JSON, got:\n%s\nerr: %v", out, err)
	}
	for _, k := range []string{"epoch", "receipts", "distinctArtifacts", "totalPoints", "note"} {
		if _, ok := decoded[k]; !ok {
			t.Errorf("status JSON must keep the %q field", k)
		}
	}
	if strings.Contains(out, "\033") {
		t.Errorf("a piped status must contain no ANSI escapes, got:\n%s", out)
	}
}

// TestLiveProgress_PipedIsOneLinePerIteration is the freeze for the mine loop: piped,
// it must stay one line per iteration with no carriage return, so a log of a run is
// exactly what it always was.
func TestLiveProgress_PipedIsOneLinePerIteration(t *testing.T) {
	p := &liveProgress{
		AgentID:     "agent:eip155:8453:0xabc",
		Points:      scoring.NewMemPointsLedger(),
		EpochOf:     func(time.Time) uint64 { return 7 },
		Interactive: false,
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	p.Report(mining.RunResult{Task: "probe", Receipt: &receipt.Receipt{ReceiptID: "0xdeadbeef"}})
	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()
	out := buf.String()

	if strings.Contains(out, "\r") {
		t.Errorf("a piped progress line must not use a carriage return, got %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("a piped progress line must end with a newline, got %q", out)
	}
	if strings.Contains(out, "\033") {
		t.Errorf("a piped progress line must contain no ANSI, got %q", out)
	}
	if !strings.Contains(out, "tasks: 1") {
		t.Errorf("the piped line must keep its fields, got %q", out)
	}
}
