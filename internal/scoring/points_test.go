package scoring

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestNoTransferCapability enforces invariant A5.
//
// MVP.md §6.1 says points are non-transferable. The enforceable version of that
// claim is structural: the ledger interface must expose no way to move points
// between agents. If someone later adds Transfer/Debit/Spend, this test fails
// rather than the behaviour quietly shipping.
func TestNoTransferCapability(t *testing.T) {
	// Names that would indicate value can move between accounts.
	forbidden := []string{
		"transfer", "transferfrom", "send", "spend", "debit", "withdraw",
		"withdrawal", "move", "approve", "allowance", "burn", "mint",
	}

	var iface *PointsLedger
	typ := reflect.TypeOf(iface).Elem()

	for i := 0; i < typ.NumMethod(); i++ {
		name := strings.ToLower(typ.Method(i).Name)
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Errorf("PointsLedger.%s looks like a value-moving method; points must be non-transferable (invariant A5)",
					typ.Method(i).Name)
			}
		}
	}

	// The concrete type must not expose them either, even if not on the interface.
	concrete := reflect.TypeOf(&MemPointsLedger{})
	for i := 0; i < concrete.NumMethod(); i++ {
		name := strings.ToLower(concrete.Method(i).Name)
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Errorf("MemPointsLedger.%s looks like a value-moving method (invariant A5)",
					concrete.Method(i).Name)
			}
		}
	}
}

// TestPointsLedger_MethodSetIsClosedToMovement pins the exact method set, so an
// addition is a deliberate, reviewed act rather than a drive-by change.
func TestPointsLedger_MethodSetIsClosedToMovement(t *testing.T) {
	want := map[string]bool{
		"Credit":       true,
		"Balance":      true,
		"EpochBalance": true,
		"Entry":        true,
		"Entries":      true,
		"AgentCount":   true,
	}

	var iface *PointsLedger
	typ := reflect.TypeOf(iface).Elem()

	got := map[string]bool{}
	for i := 0; i < typ.NumMethod(); i++ {
		got[typ.Method(i).Name] = true
	}

	for name := range got {
		if !want[name] {
			t.Errorf("unexpected method %s on PointsLedger; points movement must be a reviewed decision", name)
		}
	}
	for name := range want {
		if !got[name] {
			t.Errorf("missing expected method %s on PointsLedger", name)
		}
	}
}

func TestPointsLedger_CreditAndBalance(t *testing.T) {
	l := NewMemPointsLedger()
	at := time.Unix(1791015800, 0)

	written, err := l.Credit("r1", "agent-a", 42, 10.0, at)
	if err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if !written {
		t.Fatal("first credit should write")
	}

	if got := l.Balance("agent-a"); got != 10.0 {
		t.Errorf("Balance = %v, want 10", got)
	}
	if got := l.EpochBalance("agent-a", 42); got != 10.0 {
		t.Errorf("EpochBalance = %v, want 10", got)
	}
	if got := l.EpochBalance("agent-a", 43); got != 0 {
		t.Errorf("EpochBalance for the wrong epoch = %v, want 0", got)
	}
	if got := l.AgentCount(); got != 1 {
		t.Errorf("AgentCount = %d, want 1", got)
	}
}

// TestPointsLedger_IdempotentPerReceipt: a replayed receipt must not double-pay.
func TestPointsLedger_IdempotentPerReceipt(t *testing.T) {
	l := NewMemPointsLedger()
	at := time.Unix(1791015800, 0)

	for i := 0; i < 5; i++ {
		written, err := l.Credit("r1", "agent-a", 42, 10.0, at)
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

	if got := l.Balance("agent-a"); got != 10.0 {
		t.Errorf("Balance after 5 duplicates = %v, want 10", got)
	}
	if got := len(l.Entries()); got != 1 {
		t.Errorf("Entries = %d, want 1", got)
	}
}

func TestPointsLedger_RejectsNonPositiveAward(t *testing.T) {
	l := NewMemPointsLedger()
	at := time.Unix(1791015800, 0)

	for _, pts := range []float64{0, -1, -0.0001} {
		written, err := l.Credit("r1", "agent-a", 1, pts, at)
		if err != nil {
			t.Fatalf("Credit(%v): %v", pts, err)
		}
		if written {
			t.Errorf("award %v should not write an entry", pts)
		}
	}
	if got := len(l.Entries()); got != 0 {
		t.Errorf("Entries = %d, want 0", got)
	}
}

func TestPointsLedger_RejectsBadInput(t *testing.T) {
	l := NewMemPointsLedger()
	at := time.Unix(1791015800, 0)

	cases := []struct {
		name      string
		receiptID string
		agentID   string
		points    float64
	}{
		{"empty receipt", "", "agent-a", 10},
		{"empty agent", "r1", "", 10},
		{"NaN", "r1", "agent-a", nan()},
		{"+Inf", "r1", "agent-a", inf(1)},
		{"-Inf", "r1", "agent-a", inf(-1)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := l.Credit(c.receiptID, c.agentID, 1, c.points, at); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// TestPointsLedger_NoValueGainedByMoving: with no movement API, the total is
// invariant under any sequence of credits. This states the A5 property directly.
func TestPointsLedger_NoValueGainedByMoving(t *testing.T) {
	l := NewMemPointsLedger()
	at := time.Unix(1791015800, 0)

	awards := map[string]float64{"r1": 10, "r2": 5.5, "r3": 0.25}
	for id, pts := range awards {
		if _, err := l.Credit(id, "agent-a", 1, pts, at); err != nil {
			t.Fatalf("Credit: %v", err)
		}
	}
	// Replay everything.
	for id, pts := range awards {
		if _, err := l.Credit(id, "agent-a", 1, pts, at); err != nil {
			t.Fatalf("Credit replay: %v", err)
		}
	}

	want := 10.0 + 5.5 + 0.25
	if got := l.TotalPoints(); got != want {
		t.Errorf("TotalPoints = %v, want %v", got, want)
	}
	if got := l.Balance("agent-a"); got != want {
		t.Errorf("Balance = %v, want %v", got, want)
	}
}

// TestPointsLedger_FixedPointAccumulation: many small awards must not drift.
func TestPointsLedger_FixedPointAccumulation(t *testing.T) {
	l := NewMemPointsLedger()
	at := time.Unix(1791015800, 0)

	const n = 10000
	const each = 0.000001 // one micro-point

	for i := 0; i < n; i++ {
		id := "r" + itoa(i)
		if _, err := l.Credit(id, "agent-a", 1, each, at); err != nil {
			t.Fatalf("Credit: %v", err)
		}
	}

	want := float64(n) * each
	if got := l.Balance("agent-a"); got != want {
		t.Errorf("Balance after %d micro-credits = %v, want %v (drift)", n, got, want)
	}
}

func TestPointsLedger_ConcurrentCreditIsSafe(t *testing.T) {
	l := NewMemPointsLedger()
	at := time.Unix(1791015800, 0)

	const workers = 8
	const perWorker = 250
	done := make(chan struct{}, workers)

	for w := 0; w < workers; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < perWorker; i++ {
				id := "r" + itoa(w) + "-" + itoa(i)
				if _, err := l.Credit(id, "agent-a", 1, 1.0, at); err != nil {
					t.Errorf("Credit: %v", err)
					return
				}
			}
		}(w)
	}
	for i := 0; i < workers; i++ {
		<-done
	}

	want := float64(workers * perWorker)
	if got := l.Balance("agent-a"); got != want {
		t.Errorf("Balance = %v, want %v", got, want)
	}
	if got := len(l.Entries()); got != workers*perWorker {
		t.Errorf("Entries = %d, want %d", got, workers*perWorker)
	}
}

func TestPointsLedger_EntryLookup(t *testing.T) {
	l := NewMemPointsLedger()
	at := time.Unix(1791015800, 0)

	if _, ok := l.Entry("missing"); ok {
		t.Error("Entry for an unknown receipt should not be found")
	}

	if _, err := l.Credit("r1", "agent-a", 7, 3.5, at); err != nil {
		t.Fatalf("Credit: %v", err)
	}

	e, ok := l.Entry("r1")
	if !ok {
		t.Fatal("Entry should be found")
	}
	if e.AgentID != "agent-a" || e.Epoch != 7 || e.Points != 3.5 {
		t.Errorf("unexpected entry %+v", e)
	}
	if !e.CreditedAt.Equal(at) {
		t.Errorf("CreditedAt = %v, want %v", e.CreditedAt, at)
	}
}
