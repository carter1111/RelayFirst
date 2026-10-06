package mining

import (
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/scoring"
)

// These tests cover S4-0: the scoring sink must take its "verified" answer from an
// injected verdict source rather than from the submitter's own validation.
//
// The point of the injection is not abstraction for its own sake. Before S4 the sink
// asked the submitter whether the submitter's work was good, which meant the score
// trusted exactly the party it was meant to be checking. These tests pin the new
// behaviour: an unverified receipt earns nothing, and the choice of source is visible
// rather than implicit.

// stubVerdicts answers from a fixed value.
type stubVerdicts struct {
	verified bool
	seen     int
}

func (s *stubVerdicts) Verified(*receipt.Receipt) bool {
	s.seen++
	return s.verified
}

func verdictTestReceipt(t *testing.T, url string) *receipt.Receipt {
	t.Helper()
	return mkProbe(t, testKey(1), url, contentHash(0x7b), "0x"+strings.Repeat("ab", 32), testEpoch)
}

// TestScoringSink_UsesInjectedVerdictSource is the core S4-0 assertion: the source is
// consulted, and its answer decides the score.
func TestScoringSink_UsesInjectedVerdictSource(t *testing.T) {
	t.Run("verifier says yes, the receipt earns", func(t *testing.T) {
		inner := newFakeStore()
		work := scoring.NewMemWorkLedger()
		source := &stubVerdicts{verified: true}

		sink := &ScoringSink{
			Inner:     inner,
			Receipts:  inner,
			Artifacts: scoring.NewMemLedger(),
			Work:      work,
			Verdicts:  source,
			Clock:     func() time.Time { return time.Unix(1791015800, 0) },
		}

		r := verdictTestReceipt(t, "https://example.com/verified")
		if err := sink.Save(r, "sha256:v1", at()); err != nil {
			t.Fatalf("Save: %v", err)
		}

		if source.seen == 0 {
			t.Fatal("the verdict source was never consulted, so the injection is decorative")
		}
		// The verdict decides whether WORK is recorded (D1: points come from settlement).
		if got := work.Count(); got != 1 {
			t.Errorf("work records = %d, want 1 when the verifier agreed", got)
		}
	})

	t.Run("verifier says no, the receipt earns nothing", func(t *testing.T) {
		inner := newFakeStore()
		work := scoring.NewMemWorkLedger()
		ledger := scoring.NewMemLedger()
		source := &stubVerdicts{verified: false}

		sink := &ScoringSink{
			Inner:     inner,
			Receipts:  inner,
			Artifacts: ledger,
			Work:      work,
			Verdicts:  source,
			Clock:     func() time.Time { return time.Unix(1791015800, 0) },
		}

		r := verdictTestReceipt(t, "https://example.com/rejected")
		if err := sink.Save(r, "sha256:v2", at()); err != nil {
			t.Fatalf("Save: %v", err)
		}

		if got := work.Count(); got != 0 {
			t.Errorf("work records = %d, want 0 when the verifier disagreed", got)
		}
		// A rejected receipt must not consume the artifact either, or it would deny
		// novelty to whoever later does the work honestly.
		if ledger.Len() != 0 {
			t.Errorf("artifacts = %d, want 0 for an unverified receipt", ledger.Len())
		}
	})
}

// TestScoringSink_DefaultSourceIsTheSelfCheck records the compatibility behaviour
// explicitly, so the fallback is a documented decision rather than an accident.
func TestScoringSink_DefaultSourceIsTheSelfCheck(t *testing.T) {
	inner := newFakeStore()
	work := scoring.NewMemWorkLedger()

	// No Verdicts configured.
	sink := &ScoringSink{
		Inner:     inner,
		Receipts:  inner,
		Artifacts: scoring.NewMemLedger(),
		Work:      work,
		Clock:     func() time.Time { return time.Unix(1791015800, 0) },
	}

	r := verdictTestReceipt(t, "https://example.com/default")
	if err := sink.Save(r, "sha256:d", at()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if got := work.Count(); got != 1 {
		t.Errorf("work records = %d; the documented default reproduces the pre-S4 behaviour "+
			"(self-check), so a well-formed receipt should still record work", got)
	}
}

// TestSelfCheckVerdictsRejectsMalformed: the fallback is weaker than verification, but
// it must still refuse a receipt that is not well formed, or it would be weaker than
// useless.
func TestSelfCheckVerdictsRejectsMalformed(t *testing.T) {
	source := SelfCheckVerdicts{}

	if source.Verified(nil) {
		t.Error("a nil receipt must not be considered verified")
	}

	r := verdictTestReceipt(t, "https://example.com/ok")
	if !source.Verified(r) {
		t.Fatal("precondition: a valid receipt should pass the self-check")
	}

	// Tamper after signing.
	r.Result.Value = "500"
	if source.Verified(r) {
		t.Error("a tampered receipt must fail the self-check")
	}
}

// TestSelfCheckVerdictsDescribesItsLimitation: the source must say what it is, because
// an operator reading a log needs to know whether "verified" meant anything.
func TestSelfCheckVerdictsDescribesItsLimitation(t *testing.T) {
	got := SelfCheckVerdicts{}.Describe()

	for _, want := range []string{"self-check", "NOT"} {
		if !containsFold(got, want) {
			t.Errorf("Describe() = %q, should mention %q so the limitation is visible", got, want)
		}
	}
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToUpper(haystack), strings.ToUpper(needle))
}
