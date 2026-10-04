package sqlite_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// These tests cover the durable commitment and verdict stores (S4), and the A5 guard for
// the commitment table.
//
// The guard matters more than the round-trips: the schema is the one place a future change
// could quietly introduce a balance, and a balance is exactly what invariant A5 forbids.

func openVerificationDB(t *testing.T) *sqlite.DB {
	t.Helper()

	db, err := sqlite.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func at() time.Time { return time.Unix(1791015900, 0) }

// ---------------------------------------------------------------- commitments

func TestCommitmentLedger_CommitIsIdempotentPerID(t *testing.T) {
	l := sqlite.NewCommitmentLedger(openVerificationDB(t))

	for i := 0; i < 3; i++ {
		wrote, err := l.Commit("c-1", "agent:a", 7, 50, at())
		if err != nil {
			t.Fatalf("Commit %d: %v", i, err)
		}
		if wantWrote := i == 0; wrote != wantWrote {
			t.Errorf("Commit %d wrote=%v, want %v", i, wrote, wantWrote)
		}
	}

	committed, _, _ := l.Standing("agent:a")
	if committed != 50 {
		t.Errorf("committed = %d after 3 commits, want 50 — a commitment must not accumulate", committed)
	}
}

func TestCommitmentLedger_ReleaseAndSlash(t *testing.T) {
	l := sqlite.NewCommitmentLedger(openVerificationDB(t))

	if _, err := l.Commit("c-rel", "agent:a", 7, 50, at()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := l.Commit("c-slash", "agent:a", 7, 30, at()); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if _, err := l.Release("c-rel", at()); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := l.Slash("c-slash", at()); err != nil {
		t.Fatalf("Slash: %v", err)
	}

	committed, released, slashed := l.Standing("agent:a")
	if committed != 80 {
		t.Errorf("committed = %d, want 80", committed)
	}
	if released != 50 {
		t.Errorf("released = %d, want 50", released)
	}
	if slashed != 30 {
		t.Errorf("slashed = %d, want 30", slashed)
	}
}

// TestCommitmentLedger_SettlesOnce: a second verdict must not be able to change the
// consequence the first one recorded.
func TestCommitmentLedger_SettlesOnce(t *testing.T) {
	l := sqlite.NewCommitmentLedger(openVerificationDB(t))

	if _, err := l.Commit("c-1", "agent:a", 7, 50, at()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, err := l.Release("c-1", at()); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := l.Slash("c-1", at()); err == nil {
		t.Error("a released commitment must not then be slashable")
	}

	c, ok := l.Get("c-1")
	if !ok {
		t.Fatal("the commitment disappeared")
	}
	if c.State != sqlite.CommitmentReleased {
		t.Errorf("state = %q, want released — the second settlement must not have taken effect", c.State)
	}
}

func TestCommitmentLedger_UnknownSettlementIsReported(t *testing.T) {
	l := sqlite.NewCommitmentLedger(openVerificationDB(t))

	if _, err := l.Release("nope", at()); err == nil {
		t.Error("releasing an unknown commitment must be reported")
	}
	if _, err := l.Slash("nope", at()); err == nil {
		t.Error("slashing an unknown commitment must be reported")
	}
}

func TestCommitmentLedger_RejectsBadInput(t *testing.T) {
	l := sqlite.NewCommitmentLedger(openVerificationDB(t))

	if _, err := l.Commit("", "agent:a", 7, 50, at()); err == nil {
		t.Error("an empty id must be rejected")
	}
	if _, err := l.Commit("c", "", 7, 50, at()); err == nil {
		t.Error("an empty agent must be rejected")
	}
	if _, err := l.Commit("c", "agent:a", 7, 0, at()); err == nil {
		t.Error("a zero amount is not a commitment and must be rejected")
	}
}

// TestCommitmentLedger_SurvivesReopen: a restart must not forget what is at risk.
func TestCommitmentLedger_SurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v.db")

	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	l := sqlite.NewCommitmentLedger(db)
	if _, err := l.Commit("c-1", "agent:a", 7, 50, at()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	committed, _, _ := sqlite.NewCommitmentLedger(reopened).Standing("agent:a")
	if committed != 50 {
		t.Errorf("committed = %d after reopen, want 50", committed)
	}
}

// ---------------------------------------------------------------- verdicts

func TestVerdictStore_RoundTrip(t *testing.T) {
	s := sqlite.NewVerdictStore(openVerificationDB(t))

	v := sqlite.Verdict{
		ReceiptID:      "0x1",
		AgentID:        "agent:producer",
		VerifierID:     "agent:verifier",
		Status:         "verified",
		RecomputedHash: "sha256:" + "ab",
		VerifiedAt:     at(),
		RecordedAt:     at(),
	}
	if err := s.Record(v); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, ok := s.Get("0x1")
	if !ok {
		t.Fatal("the verdict was not found")
	}
	if got.VerifierID != v.VerifierID || got.Status != v.Status || got.RecomputedHash != v.RecomputedHash {
		t.Errorf("round trip lost a field: %+v", got)
	}
}

// TestVerdictStore_LatestVerdictWins: re-verification legitimately produces a fresh verdict,
// unlike a commitment which settles once. A re-check after an outage must be able to correct
// an earlier inconclusive result.
func TestVerdictStore_LatestVerdictWins(t *testing.T) {
	s := sqlite.NewVerdictStore(openVerificationDB(t))

	if err := s.Record(sqlite.Verdict{ReceiptID: "0x1", VerifierID: "agent:v", Status: "pending", RecordedAt: at()}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Record(sqlite.Verdict{ReceiptID: "0x1", VerifierID: "agent:v", Status: "verified", RecordedAt: at()}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, _ := s.Get("0x1")
	if got.Status != "verified" {
		t.Errorf("status = %q, want the latest (verified)", got.Status)
	}
	if s.Count() != 1 {
		t.Errorf("Count = %d, want 1 — a receipt has one current verdict", s.Count())
	}
}

func TestVerdictStore_RejectsBadInput(t *testing.T) {
	s := sqlite.NewVerdictStore(openVerificationDB(t))

	if err := s.Record(sqlite.Verdict{VerifierID: "a", Status: "verified"}); err == nil {
		t.Error("a missing receipt id must be rejected")
	}
	if err := s.Record(sqlite.Verdict{ReceiptID: "0x1", Status: "verified"}); err == nil {
		t.Error("a missing verifier id must be rejected")
	}
	if err := s.Record(sqlite.Verdict{ReceiptID: "0x1", VerifierID: "a"}); err == nil {
		t.Error("a missing status must be rejected")
	}
}

func TestVerdictStore_MissingIsNotFound(t *testing.T) {
	s := sqlite.NewVerdictStore(openVerificationDB(t))
	if _, ok := s.Get("0xnope"); ok {
		t.Error("an unrecorded receipt must not be found")
	}
}

// ---------------------------------------------------------------- the A5 guard

// TestCommitmentSchemaHasNoBalance is the structural guard for invariant A5 on the new table.
//
// The behavioural tests above can only exercise the columns that exist. If someone later adds
// a balance column — or a trigger that writes to point_entries — those tests would keep
// passing while the commitment table became a place points live. This asserts the schema.
func TestCommitmentSchemaHasNoBalance(t *testing.T) {
	db := openVerificationDB(t)

	rows, err := db.Handle().Query(`PRAGMA table_info(verification_commitments)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()

	forbidden := []string{"balance", "credit", "debit", "spend", "transfer", "withdraw", "point"}

	columns := 0
	for rows.Next() {
		var (
			cid      int
			name     string
			ctype    string
			notNull  int
			dflt     any
			primaryK int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &primaryK); err != nil {
			t.Fatalf("scan: %v", err)
		}
		columns++

		lower := name
		for _, bad := range forbidden {
			if containsFold(lower, bad) {
				t.Errorf("verification_commitments has a column %q that looks like a point balance; "+
					"a commitment is a recorded magnitude, not a spendable one (invariant A5)", name)
			}
		}
	}

	if columns == 0 {
		t.Fatal("no columns reported; the guard would be vacuous")
	}
}

// TestNoTriggerMovesPointsFromCommitments: a trigger could move points without adding a
// method or a column, which would slip past both other guards.
func TestNoTriggerMovesPointsFromCommitments(t *testing.T) {
	db := openVerificationDB(t)

	rows, err := db.Handle().Query(`SELECT name, sql FROM sqlite_master WHERE type = 'trigger'`)
	if err != nil {
		t.Fatalf("sqlite_master: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var body *string
		if err := rows.Scan(&name, &body); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if body == nil {
			continue
		}
		if containsFold(*body, "point_entries") {
			t.Errorf("trigger %q touches point_entries; nothing may move points as a side effect (invariant A5)", name)
		}
	}
}

func containsFold(haystack, needle string) bool {
	h := []rune(haystack)
	n := []rune(needle)
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			a, b := h[i+j], n[j]
			if a >= 'a' && a <= 'z' {
				a -= 32
			}
			if b >= 'a' && b <= 'z' {
				b -= 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
