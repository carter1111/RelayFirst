package scoring

import (
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// Score: happy path ------------------------------------------------------

func TestScore_FirstSightingEarnsBasePoints(t *testing.T) {
	ledger := NewMemLedger()
	r := probeReceipt(agentA, testURL, testContentHash)

	v, err := Score(r, ledger, fullParams())
	if err != nil {
		t.Fatalf("Score: %v", err)
	}

	if v.Novelty != 1 {
		t.Errorf("Novelty = %v, want 1", v.Novelty)
	}
	if v.Verified != 1 {
		t.Errorf("Verified = %v, want 1", v.Verified)
	}
	if v.Diversity != 1 {
		t.Errorf("Diversity = %v, want 1 with no repeats", v.Diversity)
	}
	if v.Work != BasePoints {
		t.Errorf("Points = %v, want %v", v.Work, BasePoints)
	}
}

// Score is side-effect free: looking must not consume novelty.
func TestScore_DoesNotConsumeNovelty(t *testing.T) {
	ledger := NewMemLedger()
	r := probeReceipt(agentA, testURL, testContentHash)

	for i := 0; i < 3; i++ {
		v, err := Score(r, ledger, fullParams())
		if err != nil {
			t.Fatalf("Score: %v", err)
		}
		if v.Work != BasePoints {
			t.Fatalf("iteration %d: Score mutated the ledger (Points = %v)", i, v.Work)
		}
	}
	if ledger.Len() != 0 {
		t.Errorf("ledger grew during scoring: Len = %d, want 0", ledger.Len())
	}
}

// Score formula components ----------------------------------------------

// TestScore_VerifiedIsBinary: an unreproduced result is void, never part-credited.
func TestScore_VerifiedIsBinary(t *testing.T) {
	ledger := NewMemLedger()
	r := probeReceipt(agentA, testURL, testContentHash)

	p := fullParams()
	p.Verified = false

	v, err := Score(r, ledger, p)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if v.Work != 0 {
		t.Errorf("Points = %v, want 0 for an unverified result", v.Work)
	}
	if v.Verified != 0 {
		t.Errorf("Verified = %v, want 0", v.Verified)
	}
	if v.Reason == "" {
		t.Error("a zero score should explain itself")
	}
	// An unverified receipt must not occupy the artifact.
	if ledger.Len() != 0 {
		t.Errorf("ledger = %d, want 0", ledger.Len())
	}
}

// TestScore_DiversityAttenuates: diversity = 1/(1 + repeats*0.5).
func TestScore_DiversityAttenuates(t *testing.T) {
	cases := []struct {
		repeats int
		want    float64
	}{
		{0, 1.0},
		{1, 1.0 / 1.5},
		{2, 1.0 / 2.0},
		{4, 1.0 / 3.0},
	}

	for _, c := range cases {
		ledger := NewMemLedger()
		r := probeReceipt(agentA, testURL, testContentHash)
		p := fullParams()
		p.SameDomainRepeats = c.repeats

		v, err := Score(r, ledger, p)
		if err != nil {
			t.Fatalf("Score: %v", err)
		}
		if !almostEqual(v.Diversity, c.want, 1e-9) {
			t.Errorf("repeats=%d: Diversity = %v, want %v", c.repeats, v.Diversity, c.want)
		}
		wantPoints := BasePoints * c.want
		if !almostEqual(v.Work, wantPoints, 1e-9) {
			t.Errorf("repeats=%d: Points = %v, want %v", c.repeats, v.Work, wantPoints)
		}
	}
}

func TestScore_NegativeRepeatsTreatedAsZero(t *testing.T) {
	ledger := NewMemLedger()
	r := probeReceipt(agentA, testURL, testContentHash)
	p := fullParams()
	p.SameDomainRepeats = -5

	v, err := Score(r, ledger, p)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if v.Diversity != 1 {
		t.Errorf("Diversity = %v, want 1 (negative repeats clamp to 0)", v.Diversity)
	}
}

// Emit -------------------------------------------------------------------

func TestEmit_CreditsOnceThenZero(t *testing.T) {
	ledger := NewMemLedger()
	at := time.Unix(1791015800, 0)
	r := probeReceipt(agentA, testURL, testContentHash)

	first, err := RecordWork(r, ledger, NewMemWorkLedger(), fullParams(), at)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if first.Work != BasePoints {
		t.Errorf("first Points = %v, want %v", first.Work, BasePoints)
	}

	// The same artifact from a DIFFERENT agent is still a repeat.
	second, err := RecordWork(probeReceipt(agentB, testURL, testContentHash), ledger, NewMemWorkLedger(), fullParams(), at)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if second.Work != 0 {
		t.Errorf("second Points = %v, want 0 (artifact already seen)", second.Work)
	}
	if second.Reason == "" {
		t.Error("a zero score should explain itself")
	}
}

func TestEmit_RequiresLedger(t *testing.T) {
	r := probeReceipt(agentA, testURL, testContentHash)
	if _, err := RecordWork(r, nil, NewMemWorkLedger(), fullParams(), time.Unix(1791015800, 0)); err == nil {
		t.Error("Emit without a ledger must fail")
	}
}

// RED TEAM S3-8 ----------------------------------------------------------

// TestRedTeam_FiveAgentsSameURLAllZeroButFirst is the TASKS.md S3-8 red team.
//
// Five agents submit the identical observation. Exactly one may be credited;
// the other four must score zero. If this test ever passes with more than one
// earner, farming pays and the whole design collapses (invariant A6).
func TestRedTeam_FiveAgentsSameURLAllZeroButFirst(t *testing.T) {
	ledger := NewMemLedger()
	at := time.Unix(1791015800, 0)

	agents := []string{
		"agent:eip155:8453:0x0000000000000000000000000000000000000001",
		"agent:eip155:8453:0x0000000000000000000000000000000000000002",
		"agent:eip155:8453:0x0000000000000000000000000000000000000003",
		"agent:eip155:8453:0x0000000000000000000000000000000000000004",
		"agent:eip155:8453:0x0000000000000000000000000000000000000005",
	}

	earned := 0
	total := 0.0

	for i, agent := range agents {
		r := probeReceipt(agent, testURL, testContentHash)
		// Give each receipt a distinct id so they are genuinely separate
		// submissions rather than replays of one receipt.
		r.ReceiptID = "0x" + strings.Repeat(itoa(i), 64)

		v, err := RecordWork(r, ledger, NewMemWorkLedger(), fullParams(), at)
		if err != nil {
			t.Fatalf("agent %d: Emit: %v", i, err)
		}
		if v.Work > 0 {
			earned++
		}
		total += v.Work

		if i > 0 && v.Work != 0 {
			t.Errorf("agent %d earned %v; only the first sighting may earn (invariant A6)", i, v.Work)
		}
	}

	if earned != 1 {
		t.Errorf("earned count = %d, want exactly 1 out of 5 (S3-8)", earned)
	}
	if total != BasePoints {
		t.Errorf("total = %v, want %v (farming must not multiply the award)", total, BasePoints)
	}
	if ledger.Len() != 1 {
		t.Errorf("ledger Len = %d, want 1 distinct artifact", ledger.Len())
	}

	// Rejected submissions do not increment SeenCount: only the one earning
	// observation is recorded. See the note on Sighting.SeenCount.
	if s, ok := ledger.Lookup(mustKey(t, probeReceipt(agentA, testURL, testContentHash))); ok {
		if s.SeenCount != 1 {
			t.Errorf("SeenCount = %d, want 1 (only credited observations are recorded)", s.SeenCount)
		}
	}
}

// TestRedTeam_ManyAgentsFarmingIsLinearNotMultiplicative states the economic
// claim directly: N agents observing one artifact earn 1x, not Nx.
func TestRedTeam_ManyAgentsFarmingIsLinearNotMultiplicative(t *testing.T) {
	ledger := NewMemLedger()
	at := time.Unix(1791015800, 0)

	const n = 50
	total := 0.0
	for i := 0; i < n; i++ {
		r := probeReceipt("agent:eip155:8453:0x"+strings.Repeat(itoa(i%10), 40), testURL, testContentHash)
		r.ReceiptID = "0x" + strings.Repeat("f", 62) + itoa(i%10) + itoa(i/10)
		v, err := RecordWork(r, ledger, NewMemWorkLedger(), fullParams(), at)
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}
		total += v.Work
	}

	if total != BasePoints {
		t.Errorf("total for %d farming attempts = %v, want %v", n, total, BasePoints)
	}
}

// RED TEAM S3-9 ----------------------------------------------------------

// TestRedTeam_FabricatedContentHashCannotReuseNovelty is the TASKS.md S3-9 red
// team at the scoring layer.
//
// An agent that fabricates a contentHash gets a *different* artifact key, so it
// cannot free-ride on someone else's novelty — but it also cannot be credited
// here, because reproducing the result is what earns credit and a fabricated
// anchor does not reproduce. Scoring therefore refuses it when verification
// fails, which is exactly the path an honest verifier drives it down.
//
// The complementary case (re-fetching rejects the lie) lives in the anchor
// package; this test pins the scoring half of the boundary.
func TestRedTeam_FabricatedContentHashCannotReuseNovelty(t *testing.T) {
	ledger := NewMemLedger()

	// Honest agent observes the real content first.
	honest := probeReceipt(agentA, testURL, testContentHash)
	v, err := RecordWork(honest, ledger, NewMemWorkLedger(), fullParams(), time.Unix(1791015800, 0))
	if err != nil {
		t.Fatalf("Emit honest: %v", err)
	}
	if v.Work != BasePoints {
		t.Fatalf("honest Points = %v, want %v", v.Work, BasePoints)
	}

	// Attacker invents a contentHash for the same url. That yields a distinct
	// artifact key, so novelty is not the control that stops it — verification is.
	fabricated := probeReceipt(agentB, testURL, "sha256:"+strings.Repeat("de", 32))
	p := fullParams()
	p.Verified = false // a re-fetch cannot reproduce invented content

	v, err = Score(fabricated, ledger, p)
	if err != nil {
		t.Fatalf("Score fabricated: %v", err)
	}
	if v.Work != 0 {
		t.Errorf("fabricated content scored %v, want 0", v.Work)
	}

	// And the fabricated key must differ from the honest one, so it cannot be
	// used to claim the honest agent's novelty either.
	hk, err := ArtifactKey(honest)
	if err != nil {
		t.Fatalf("ArtifactKey honest: %v", err)
	}
	fk, err := ArtifactKey(fabricated)
	if err != nil {
		t.Fatalf("ArtifactKey fabricated: %v", err)
	}
	if hk == fk {
		t.Error("a fabricated contentHash must not collide with the honest artifact")
	}
}

// TestRedTeam_RejectedSubmissionCannotBurnArtifact pins the security property
// that only credited observations reach the ledger.
//
// The attack: submit a receipt that will be rejected but that names a
// respectable artifact. If rejection still recorded the key, the attacker could
// deny novelty to the agent who later does the real work — a cheap griefing
// vector. Rejected submissions therefore leave no trace.
func TestRedTeam_RejectedSubmissionCannotBurnArtifact(t *testing.T) {
	ledger := NewMemLedger()
	at := time.Unix(1791015800, 0)

	// Attacker submits the honest artifact but fails verification.
	attacker := probeReceipt(agentB, testURL, testContentHash)
	rejected := fullParams()
	rejected.Verified = false

	v, err := RecordWork(attacker, ledger, NewMemWorkLedger(), rejected, at)
	if err != nil {
		t.Fatalf("Emit rejected: %v", err)
	}
	if v.Work != 0 {
		t.Fatalf("rejected submission scored %v, want 0", v.Work)
	}
	if ledger.Len() != 0 {
		t.Fatalf("ledger Len = %d after a rejected submission, want 0 — rejection must not claim the artifact",
			ledger.Len())
	}

	// The honest agent can still earn novelty afterwards.
	honest := probeReceipt(agentA, testURL, testContentHash)
	v, err = RecordWork(honest, ledger, NewMemWorkLedger(), fullParams(), at)
	if err != nil {
		t.Fatalf("Emit honest: %v", err)
	}
	if v.Work != BasePoints {
		t.Errorf("honest agent earned %v after a rejected attempt, want %v", v.Work, BasePoints)
	}
}

// TestRedTeam_UnacceptedTaskTypeCannotScore guards A2 at the scoring boundary.
func TestRedTeam_UnacceptedTaskTypeCannotScore(t *testing.T) {
	ledger := NewMemLedger()

	for _, bad := range []receipt.TaskType{"summarize", "classify", "translate", ""} {
		r := probeReceipt(agentA, testURL, testContentHash)
		r.Task.Type = bad

		if _, err := Score(r, ledger, fullParams()); err == nil {
			t.Errorf("task type %q must not be scoreable (invariant A2)", bad)
		}
		if _, err := RecordWork(r, ledger, NewMemWorkLedger(), fullParams(), time.Unix(1791015800, 0)); err == nil {
			t.Errorf("task type %q must not be emittable", bad)
		}
	}
}

func mustKey(t *testing.T, r *receipt.Receipt) string {
	t.Helper()
	k, err := ArtifactKey(r)
	if err != nil {
		t.Fatalf("ArtifactKey: %v", err)
	}
	return k
}
