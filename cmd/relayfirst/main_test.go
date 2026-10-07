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

	"github.com/relayfirst/relayfirst/internal/merkle"
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
		// It must agree with the shared epoch function rather than hard-coding a
		// count. Epoch 0 is a legitimate answer pre-launch (the placeholder genesis
		// is recent), so "not zero" would be a brittle assertion.
		if want := scoring.EpochOf(time.Now()); got != want {
			t.Errorf("epochArg = %d, want the current epoch %d", got, want)
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

// captureStdout runs fn with os.Stdout on a pipe and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	_ = r.Close()
	return buf.String()
}

// TestBalanceRootFromCumulative_BindsPointsAndAgent: the balance root must be a pure
// function of the CUMULATIVE totals and the agents, matching the leaf a claim verifies
// against (MVP.md §6.2b).
func TestBalanceRootFromCumulative_BindsPointsAndAgent(t *testing.T) {
	a := "agent:eip155:8453:0x70997970c51812dc3a010c7d01b50e0d17dc79c8"
	b := "agent:eip155:8453:0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc"
	cumulative := map[string]int64{a: 140_000_000_000, b: 42_000_000_000}

	r1, err := balanceRootFromCumulative(7, cumulative)
	if err != nil {
		t.Fatalf("balanceRootFromCumulative: %v", err)
	}
	r2, err := balanceRootFromCumulative(7, cumulative)
	if err != nil {
		t.Fatalf("balanceRootFromCumulative (2): %v", err)
	}
	if r1 != r2 {
		t.Error("the balance root must be deterministic for the same totals")
	}

	moved := map[string]int64{a: 140_000_000_000, b: 43_000_000_000}
	r3, err := balanceRootFromCumulative(7, moved)
	if err != nil {
		t.Fatalf("balanceRootFromCumulative (moved): %v", err)
	}
	if r1 == r3 {
		t.Error("a different cumulative total must change the balance root")
	}

	r4, err := balanceRootFromCumulative(8, cumulative)
	if err != nil {
		t.Fatalf("balanceRootFromCumulative (epoch): %v", err)
	}
	if r1 == r4 {
		t.Error("a different epoch must change the balance root")
	}

	agent, err := merkle.AgentID(a)
	if err != nil {
		t.Fatalf("agent id: %v", err)
	}
	totals := map[merkle.Hash]int64{agent: 140_000_000_000}
	proof, err := merkle.BalanceProof(7, totals, agent)
	if err != nil {
		t.Fatalf("BalanceProof: %v", err)
	}
	if !merkle.Verify(proof) {
		t.Error("a proof built from the totals must verify against a balance root")
	}
}

// TestBalanceRootFromCumulative_OmitsZeroTotals: an agent with no points has no claim,
// so it must not appear as an unclaimable leaf.
func TestBalanceRootFromCumulative_OmitsZeroTotals(t *testing.T) {
	a := "agent:eip155:8453:0x70997970c51812dc3a010c7d01b50e0d17dc79c8"
	b := "agent:eip155:8453:0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc"

	with, err := balanceRootFromCumulative(7, map[string]int64{a: 140_000_000_000, b: 0})
	if err != nil {
		t.Fatalf("with zero: %v", err)
	}
	without, err := balanceRootFromCumulative(7, map[string]int64{a: 140_000_000_000})
	if err != nil {
		t.Fatalf("without zero: %v", err)
	}
	if with != without {
		t.Error("a zero-total agent must not change the balance root")
	}
}

// TestSettle_EmitsCumulativeBalanceRoot is the end-to-end for IMP-1: after work is
// recorded and settled, `relayfirst settle` must emit the balance root a claim is
// verified against, over CUMULATIVE totals.
func TestSettle_EmitsCumulativeBalanceRoot(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "s.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ledgers := store.NewScoringLedgers(db)
	agents := []string{
		"agent:eip155:8453:0x70997970c51812dc3a010c7d01b50e0d17dc79c8",
		"agent:eip155:8453:0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc",
	}
	for i, a := range agents {
		if _, err := ledgers.Work.Record(scoring.WorkRecord{
			ReceiptID:   fmt.Sprintf("0x%064x", i+1),
			AgentID:     a,
			Epoch:       7,
			Work:        float64(10 * (i + 1)),
			ArtifactKey: fmt.Sprintf("sha256:%02x", i+1),
			RecordedAt:  time.Now(),
		}); err != nil {
			t.Fatalf("Record work: %v", err)
		}
	}
	_ = db.Close()

	out := captureStdout(t, func() {
		if err := runSettle([]string{"--db", dbPath, "--epoch", "7"}); err != nil {
			t.Fatalf("settle: %v", err)
		}
	})

	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("settle output must be JSON, got:\n%s\nerr: %v", out, err)
	}
	root, ok := decoded["balanceRoot"].(string)
	if !ok || root == "" {
		t.Fatalf("settle must emit a balanceRoot, got:\n%s", out)
	}

	// The emitted root must equal the one recomputed from the settled points (which,
	// after one epoch, are the cumulative totals). A claimant can reproduce it.
	settled := map[string]float64{}
	if rows, ok := decoded["points"].([]any); ok {
		for _, r := range rows {
			m := r.(map[string]any)
			settled[m["agentId"].(string)] = m["points"].(float64)
		}
	}
	cumulative := map[string]int64{}
	for a, p := range settled {
		cumulative[a] = int64(p*scoring.MicroPerPoint + 0.5)
	}
	want, err := balanceRootFromCumulative(7, cumulative)
	if err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if root != want.Hex() {
		t.Errorf("emitted balanceRoot %s != recomputed %s", root, want.Hex())
	}
}

// epoch's unsettled estimate, labelled so it is not mistaken for settled points.
func TestStatusDashboard_ShowsPendingAsUnsettled(t *testing.T) {
	out := map[string]any{
		"epoch":             uint64(7),
		"epochEndsAt":       "2026-10-15T00:00:00Z",
		"receipts":          2,
		"distinctArtifacts": 2,
		"totalPoints":       12.5,
		"note":              "x",
	}
	agents := []agentOut{{
		AgentID:        "agent:eip155:8453:0xabc",
		PointsLifetime: 12.5,
		PointsEpoch:    0,
		Receipts:       2,
		Credited:       2,
	}}
	pending := map[string]float64{"agent:eip155:8453:0xabc": 3.5}

	got := captureStdout(t, func() { printStatusDashboard(out, agents, nil, pending) })

	if !strings.Contains(got, "pending") {
		t.Errorf("the dashboard must show the pending estimate, got:\n%s", got)
	}
	if !strings.Contains(got, "unsettled") {
		t.Errorf("the pending line must be labelled unsettled, got:\n%s", got)
	}
	if !strings.Contains(got, "3.5") {
		t.Errorf("the pending estimate value must appear, got:\n%s", got)
	}
	// The settled line must still be present and distinct.
	if !strings.Contains(got, "settled") {
		t.Errorf("the settled line must remain, got:\n%s", got)
	}
}

// TestStatus_NonTTYHasNoPendingEstimate pins that the pending estimate did NOT leak
// into the frozen JSON: it is a live projection, not a settled balance, and the piped
// contract must not gain a field.
func TestStatus_NonTTYHasNoPendingEstimate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "s.db")
	out := captureStdout(t, func() {
		if err := runStatus([]string{"--db", dbPath}); err != nil {
			t.Fatalf("status: %v", err)
		}
	})
	if strings.Contains(out, "pending") {
		t.Errorf("the piped status JSON must not carry a pending estimate, got:\n%s", out)
	}
}

// TestClaim_ProducesAVerifyingProof is the IMP-4 core: claim must produce a proof that
// verifies against the balance root of the requested epoch, over the cumulative total.
func TestClaim_ProducesAVerifyingProof(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "s.db")
	seedSettledWork(t, dbPath, 7)

	out := captureStdout(t, func() {
		if err := runClaim([]string{"--db", dbPath, "--epoch", "7",
			"--agent", "agent:eip155:8453:0x70997970c51812dc3a010c7d01b50e0d17dc79c8"}); err != nil {
			t.Fatalf("claim: %v", err)
		}
	})

	var got struct {
		AgentHash  string   `json:"agentHash"`
		Epoch      uint64   `json:"epoch"`
		TotalMicro int64    `json:"totalMicro"`
		Root       string   `json:"root"`
		Leaf       string   `json:"leaf"`
		Index      int      `json:"index"`
		Proof      []string `json:"proof"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("claim output must be JSON, got:\n%s\nerr: %v", out, err)
	}
	if strings.Contains(out, "privateKey") || strings.Contains(out, "mnemonic") {
		t.Errorf("a claim proof must never carry key material, got:\n%s", out)
	}

	agentHash, err := merkle.IDFromHex(got.AgentHash)
	if err != nil {
		t.Fatalf("agentHash: %v", err)
	}
	leaf, _ := merkle.IDFromHex(got.Leaf)
	root, _ := merkle.IDFromHex(got.Root)
	siblings := make([]merkle.Hash, 0, len(got.Proof))
	for _, s := range got.Proof {
		h, err := merkle.IDFromHex(s)
		if err != nil {
			t.Fatalf("proof sibling %q: %v", s, err)
		}
		siblings = append(siblings, h)
	}
	_ = agentHash

	proof := merkle.Proof{Leaf: leaf, Index: got.Index, Siblings: siblings, Root: root, Width: nextPow2For(len(siblings) + 1)}
	if !merkle.Verify(proof) {
		t.Error("the claim proof must verify against the root it carries")
	}

	// And the leaf must be the balance leaf for that cumulative total.
	wantLeaf, err := merkle.BalanceLeaf(agentHash, got.TotalMicro, got.Epoch)
	if err != nil {
		t.Fatalf("BalanceLeaf: %v", err)
	}
	if wantLeaf != leaf {
		t.Errorf("claim leaf %s is not BalanceLeaf(agent, total, epoch) %s", leaf, wantLeaf)
	}
}

// TestClaim_NoKey_RefusesToGuessAmongSeveralAgents: with more than one settled agent
// and no --agent/--key, claim must refuse rather than pick one.
func TestClaim_NoKey_RefusesToGuessAmongSeveralAgents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "s.db")
	seedSettledWork(t, dbPath, 7) // two agents

	err := runClaim([]string{"--db", dbPath, "--epoch", "7"})
	if err == nil {
		t.Fatal("claim must refuse to guess which of several agents to claim for")
	}
	if !strings.Contains(err.Error(), "--agent") {
		t.Errorf("the error should tell the user to pass --agent or --key, got: %v", err)
	}
}

// TestClaim_UnsettledEpochIsAnError: claiming an epoch with no settled points says so,
// rather than emitting a proof over nothing.
func TestClaim_UnsettledEpochIsAnError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "s.db")
	seedSettledWork(t, dbPath, 7)

	if err := runClaim([]string{"--db", dbPath, "--epoch", "999"}); err == nil {
		t.Fatal("claiming an epoch with no settled points must be an error")
	}
}

// seedSettledWork records work for two agents and settles the epoch, so claim has
// points to prove.
func seedSettledWork(t *testing.T, dbPath string, epoch uint64) {
	t.Helper()
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ledgers := store.NewScoringLedgers(db)
	agents := []string{
		"agent:eip155:8453:0x70997970c51812dc3a010c7d01b50e0d17dc79c8",
		"agent:eip155:8453:0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc",
	}
	for i, a := range agents {
		if _, err := ledgers.Work.Record(scoring.WorkRecord{
			ReceiptID:   fmt.Sprintf("0x%064x", i+1),
			AgentID:     a,
			Epoch:       epoch,
			Work:        float64(10 * (i + 1)),
			ArtifactKey: fmt.Sprintf("sha256:%02x", i+1),
			RecordedAt:  time.Now(),
		}); err != nil {
			t.Fatalf("Record work: %v", err)
		}
	}
	if _, err := (&mining.ScoringSink{
		Work:   ledgers.Work,
		Points: ledgers.Points,
		Clock:  func() time.Time { return time.Unix(1791090000, 0) },
	}).Finalize(epoch, time.Now()); err != nil {
		t.Fatalf("settle: %v", err)
	}
	_ = db.Close()
}

// TestClaim_ProvesCumulativeAcrossEpochs guards the semantic that RelayPoints.sol
// depends on: a claim proves the total earned BY an epoch, not one epoch's award. If a
// claim for epoch 7 showed only epoch 7's points, an on-chain claim would SET the total
// to that and erase epoch 5 -- the exact bug a per-epoch total would cause.
func TestClaim_ProvesCumulativeAcrossEpochs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "s.db")
	const agent = "agent:eip155:8453:0x70997970c51812dc3a010c7d01b50e0d17dc79c8"

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ledgers := store.NewScoringLedgers(db)
	for _, e := range []uint64{5, 7} {
		if _, err := ledgers.Work.Record(scoring.WorkRecord{
			ReceiptID:   fmt.Sprintf("0x%064x", e),
			AgentID:     agent,
			Epoch:       e,
			Work:        10,
			ArtifactKey: fmt.Sprintf("sha256:%02x", e),
			RecordedAt:  time.Now(),
		}); err != nil {
			t.Fatalf("Record work: %v", err)
		}
	}
	sink := &mining.ScoringSink{Work: ledgers.Work, Points: ledgers.Points, Clock: func() time.Time { return time.Unix(1791090000, 0) }}
	for _, e := range []uint64{5, 7} {
		if _, err := sink.Finalize(e, time.Now()); err != nil {
			t.Fatalf("settle %d: %v", e, err)
		}
	}
	want5 := ledgers.Points.EpochBalance(agent, 5)
	want7 := ledgers.Points.EpochBalance(agent, 7)
	_ = db.Close()

	out := captureStdout(t, func() {
		if err := runClaim([]string{"--db", dbPath, "--epoch", "7", "--agent", agent}); err != nil {
			t.Fatalf("claim: %v", err)
		}
	})
	var got struct {
		Total float64 `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("claim JSON: %v", err)
	}
	// The total must be epoch 5 + epoch 7, not epoch 7 alone.
	if diff := got.Total - (want5 + want7); diff > 1e-6 || diff < -1e-6 {
		t.Errorf("claim total = %v, want cumulative %v (epoch5 %v + epoch7 %v)", got.Total, want5+want7, want5, want7)
	}
	if want5 <= 0 || got.Total <= want7 {
		t.Errorf("epoch 5's points (%v) must be included in the total (%v)", want5, got.Total)
	}
}

// TestAnchorManifestAndAudit_AuditIsNonVacuous is the omission "discoverable" path
// (incentive.md §10.10, option B): a manifest lets anyone recompute the root from the
// published set, and flag a receipt they believe they earned that is missing.
func TestAnchorManifestAndAudit_AuditIsNonVacuous(t *testing.T) {
	// One store with epoch 5 holding ids 1 and 2, and a second store with id 3 too.
	rs := seededStore(t, [2]int{1, 5}, [2]int{2, 5})

	// A manifest for epoch 5 (ids 1,2), and a root computed from exactly those.
	manOut := captureStdout(t, func() {
		if err := anchorManifest(&flags{values: map[string]string{"epoch": "5"}, repeated: map[string][]string{}}, rs); err != nil {
			t.Fatalf("manifest: %v", err)
		}
	})
	rootDir := t.TempDir()
	manPath := filepath.Join(rootDir, "manifest.json")
	if err := os.WriteFile(manPath, []byte(manOut), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	var man map[string]any
	if err := json.Unmarshal([]byte(manOut), &man); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	root := man["root"].(string)

	// 1. An honest audit: the manifest recomputes to the root, nothing missing.
	err := anchorAudit(&flags{
		values:   map[string]string{"manifest": manPath, "root": root},
		repeated: map[string][]string{"require": {id(1)}},
	})
	if err != nil {
		t.Errorf("an honest manifest with the required receipt present must pass, got: %v", err)
	}

	// 2. A receipt the caller earned but the manifest omits must be flagged, non-zero.
	forgedDir := t.TempDir()
	tamperedPath := filepath.Join(forgedDir, "manifest.json")
	tampered := map[string]any{"epoch": 5, "root": root, "receipts": []string{id(1)}} // id(2) dropped
	raw, _ := json.Marshal(tampered)
	_ = os.WriteFile(tamperedPath, raw, 0o644)

	err = anchorAudit(&flags{
		values:   map[string]string{"manifest": tamperedPath, "root": root},
		repeated: map[string][]string{"require": {id(2)}},
	})
	if err == nil {
		t.Error("a manifest that omits a required receipt must fail the audit")
	}

	// 3. A manifest whose set does not recompute to the root must be caught.
	stale := map[string]any{"epoch": 5, "root": root, "receipts": []string{id(1), id(2), id(3)}}
	raw2, _ := json.Marshal(stale)
	stalePath := filepath.Join(forgedDir, "stale.json")
	_ = os.WriteFile(stalePath, raw2, 0o644)

	err = anchorAudit(&flags{
		values:   map[string]string{"manifest": stalePath, "root": root},
		repeated: map[string][]string{},
	})
	if err == nil {
		t.Error("a set that does not recompute to the root must fail the audit")
	}
}

// TestAnchorAudit_RequiresBothInputs: audit must not run without a root and a manifest.
func TestAnchorAudit_RequiresBothInputs(t *testing.T) {
	if err := anchorAudit(&flags{values: map[string]string{}, repeated: map[string][]string{}}); err == nil {
		t.Error("audit without --root must error")
	}
	if err := anchorAudit(&flags{values: map[string]string{"root": id(9)}, repeated: map[string][]string{}}); err == nil {
		t.Error("audit without --manifest must error")
	}
}

// TestLayer0_EndToEnd_NodePoolPaidAndAliveAloneEarnsNoBonus is the Layer 0 acceptance:
// settle splits the budget, pays the node pool, and does NOT pay a Layer 1 bonus for a
// node that is merely alive. A farm's machines are alive; "alive" must not buy the bonus.
func TestLayer0_EndToEnd_NodePoolPaidAndAliveAloneEarnsNoBonus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "s.db")

	const (
		worker = "agent:eip155:8453:0x00000000000000000000000000000000000000a1" // works
		idle   = "agent:eip155:8453:0x00000000000000000000000000000000000000a2" // bound+alive, no work
		node1  = "agent:eip155:8453:0x00000000000000000000000000000000000000b1"
		node2  = "agent:eip155:8453:0x00000000000000000000000000000000000000b2"
	)

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ledgers := store.NewScoringLedgers(db)
	now := time.Unix(1791090000, 0)

	// Only the worker produced a receipt this epoch.
	if _, err := ledgers.Work.Record(scoring.WorkRecord{
		ReceiptID: "0x0000000000000000000000000000000000000000000000000000000000000001",
		AgentID:   worker, Epoch: 7, Work: 10,
		ArtifactKey: "sha256:aa", RecordedAt: now,
	}); err != nil {
		t.Fatalf("record work: %v", err)
	}

	// Both agents are bound; both nodes are alive with tenure past the floor.
	bl := store.NewBindingLedger(db)
	if err := bl.Bind(worker, node1, 7, now); err != nil {
		t.Fatalf("bind worker: %v", err)
	}
	if err := bl.Bind(idle, node2, 7, now); err != nil {
		t.Fatalf("bind idle: %v", err)
	}
	tl := store.NewTenureLedger(db)
	for e := uint64(1); e <= 5; e++ {
		if _, err := tl.Record(node1, e, true, now); err != nil {
			t.Fatalf("tenure node1: %v", err)
		}
		if _, err := tl.Record(node2, e, true, now); err != nil {
			t.Fatalf("tenure node2: %v", err)
		}
	}
	_ = db.Close()

	out := captureStdout(t, func() {
		if err := runSettle([]string{"--db", dbPath, "--epoch", "7"}); err != nil {
			t.Fatalf("settle: %v", err)
		}
	})

	var decoded struct {
		NodePoolPct float64 `json:"nodePoolPct"`
		Points      []struct {
			AgentID string  `json:"agentId"`
			Points  float64 `json:"points"`
		} `json:"points"`
		NodePoints []struct {
			NodeID string  `json:"nodeId"`
			Points float64 `json:"points"`
		} `json:"nodePoints"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("settle JSON: %v\n%s", err, out)
	}

	// Phase 1: the node pool is half the budget.
	if decoded.NodePoolPct != 0.50 {
		t.Errorf("nodePoolPct = %v, want 0.50 at epoch 7", decoded.NodePoolPct)
	}

	// The node pool was actually paid: both alive nodes appear.
	nodePaid := map[string]float64{}
	for _, n := range decoded.NodePoints {
		nodePaid[n.NodeID] = n.Points
	}
	if nodePaid[node1] <= 0 || nodePaid[node2] <= 0 {
		t.Fatalf("both alive nodes must receive Layer 0 points, got %v", nodePaid)
	}

	// Layer 1: the worker appears; the idle agent does NOT. A bound, alive node does not
	// by itself earn a work bonus -- that is the M1 property, at the settlement level.
	workPaid := map[string]float64{}
	for _, p := range decoded.Points {
		workPaid[p.AgentID] = p.Points
	}
	if workPaid[worker] <= 0 {
		t.Errorf("the worker must receive Layer 1 points, got %v", workPaid)
	}
	if _, present := workPaid[idle]; present {
		t.Errorf("an agent with no receipts must not receive a work bonus for being bound to "+
			"an alive node; it got %v", workPaid[idle])
	}

	// The node pool was paid for liveness (Layer 0) while the idle AGENT got no Layer 1:
	// alive != work, which is exactly the M1 point.
	if nodePaid[node2] > 0 && len(workPaid) == 0 {
		t.Error("sanity: worker should have some Layer 1 points")
	}
}

// TestBind_IsOneBindingPerIdentity: rebinding must be refused.
func TestBind_IsOneBindingPerIdentity(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "s.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	bl := store.NewBindingLedger(db)
	const agent = "agent:eip155:8453:0x00000000000000000000000000000000000000a1"
	if err := bl.Bind(agent, "agent:eip155:8453:0x00000000000000000000000000000000000000b1", 7, time.Now()); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if err := bl.Bind(agent, "agent:eip155:8453:0x00000000000000000000000000000000000000b2", 8, time.Now()); err == nil {
		t.Error("a second binding for the same identity must be refused")
	}
}
