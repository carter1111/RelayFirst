package scoring

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestEmissionModels_QuantifiedDiscrepancy measures the divergence between the two emission
// models this repository describes, so the conflict is a number rather than an assertion.
//
// # The two models
//
// MVP.md §5.3 defines a per-receipt rate:
//
//	points(receipt) = BASE × verified × novelty × diversity × budgetFactor
//	BASE = 10
//
// MVP.md §6.2 defines a proportional share of a fixed epoch budget:
//
//	points_i = B(n) × (work_i / Σwork)
//	B(n) = B0 × decay^n, B0 = 1,000,000
//
// These are not the same rule, and the difference is not a rounding matter: §6.2's total supply
// per epoch is an *input* (the budget is spent), while §5.3's is an *output* that depends on how
// many receipts exist (the budget is not a constraint at all).
//
// # What this test does and does not do
//
// It does NOT assert which model is correct. Emission is an economics decision that belongs to a
// human, and picking one silently would be a range change made by an agent. It computes both on
// identical inputs and pins the ratio, so the discrepancy is visible and cannot drift unnoticed.
//
// If someone later aligns the implementation with §6.2 (or the docs with §5.3), this test fails
// and forces that to be a deliberate act rather than an accident.
func TestEmissionModels_QuantifiedDiscrepancy(t *testing.T) {
	const epoch = 0 // epoch 0 has the full B0, which makes the comparison easiest to read
	const agents = 10
	const receiptsPerAgent = 10

	// Model A: what the implementation does. Every receipt earns the formula's value.
	perReceipt := BasePoints // with verified=1, novelty=1, diversity=1, budgetFactor=1
	modelATotal := float64(agents * receiptsPerAgent * perReceipt)

	// Model B: what MVP.md §6.2 describes. The epoch budget is shared proportionally.
	// Every agent did identical work, so each gets an equal share.
	work := map[string]float64{}
	for a := 0; a < agents; a++ {
		work[fmt.Sprintf("agent-%d", a)] = float64(receiptsPerAgent)
	}
	allocation, err := Allocate(epoch, work)
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	capped := CapAllocation(epoch, allocation)
	modelBTotal := TotalAllocated(capped)

	// Model B with the per-agent cap applied caps a single agent at B(n) × 5%. With ten equal
	// agents each would receive 10% of the pool, so the cap bites and the epoch emits *less* than
	// its budget. That is documented behaviour in emission.go, not a bug.
	t.Logf("model A (implementation, §5.3): %.2f points for %d receipts", modelATotal, agents*receiptsPerAgent)
	t.Logf("model B (§6.2, proportional + 5%% cap): %.2f points, budget %.0f, cap %.0f",
		modelBTotal, EpochBudget(epoch), PerAgentCap(epoch))

	ratio := modelATotal / modelBTotal
	t.Logf("ratio A/B: %.4f (a %.1fx difference)", ratio, ratio)

	// The two models must differ substantially; if they ever agree, either the docs or the
	// implementation was changed and this test's premise is gone.
	if ratio < 2 && ratio > 0.5 {
		t.Errorf("the two emission models now produce similar totals (%.2f vs %.2f). "+
			"If they were deliberately aligned, update MVP.md §5.3/§6.2 and this test together; "+
			"do not let the divergence disappear silently.", modelATotal, modelBTotal)
	}

	// The structural difference, stated as an assertion rather than prose: model A's total scales
	// with the number of receipts, model B's does not.
	doubled := float64(agents * receiptsPerAgent * 2 * perReceipt)
	if doubled <= modelATotal {
		t.Error("model A should scale with receipt count")
	}
	if modelBTotal > EpochBudget(epoch) {
		t.Errorf("model B emitted %.2f, more than its budget %.0f", modelBTotal, EpochBudget(epoch))
	}
}

// TestProportionalModelIsUnreachableFromScoring records the fact that the §6.2 model is currently
// unreachable from the scoring path, which is the strongest statement of the conflict.
//
// `Allocate` and `CapAllocation` are exported, tested and documented — and called by nothing
// outside this package's own tests. Meanwhile receipts are credited through `Score`/`Emit`. So
// whichever model is intended, only one of them is in force, and a reader of §6.2 would
// reasonably conclude the other one is.
//
// The finding is structural and the test states it as such: `Params` has no Σwork field, so the
// scoring function *cannot* express a proportional share. That absence is the evidence — it is
// not something a future caller could accidentally restore.
func TestProportionalModelIsUnreachableFromScoring(t *testing.T) {
	// Demonstrate what the proportional path does: it weights by work, which requires a
	// denominator.
	allocation, err := Allocate(0, map[string]float64{"a": 1, "b": 3})
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if allocation["b"] <= allocation["a"] {
		t.Errorf("Allocate should weight by work: b=%.2f, a=%.2f", allocation["b"], allocation["a"])
	}

	// The per-receipt path has no denominator, so it cannot weight by work. Verifying that a
	// score is unchanged when an unrelated agent does more work makes the point concretely.
	r := probeReceipt("agent:a", "https://example.com/x", testContentHash)
	ledger := NewMemLedger()

	alone, err := Score(r, ledger, fullParams())
	if err != nil {
		t.Fatalf("Score: %v", err)
	}

	// A second agent does a great deal of work. Under §6.2 this would dilute agent:a's share;
	// under §5.3 it changes nothing, because the formula has no notion of other agents.
	other := probeReceipt("agent:b", "https://example.com/y", testContentHash)
	other.ReceiptID = "0x" + "cd" // distinct id
	for i := 0; i < 100; i++ {
		otherCopy := *other
		otherCopy.ReceiptID = "0x" + strings.Repeat("cd", 32) + string(rune('a'+i%26))
		if _, err := Emit(&otherCopy, ledger, fullParams(), time.Unix(1791015800, 0)); err != nil {
			t.Fatalf("Emit: %v", err)
		}
	}

	withCompetition, err := Score(r, ledger, fullParams())
	if err != nil {
		t.Fatalf("Score: %v", err)
	}

	if alone.Points != withCompetition.Points {
		t.Errorf("the per-receipt score changed when another agent did more work (%v vs %v); "+
			"under §6.2 it would have been diluted, so this records which model is in force",
			alone.Points, withCompetition.Points)
	}

	t.Logf("per-receipt model: %.2f points regardless of other agents' work; "+
		"§6.2 would have shared a fixed budget of %.0f", alone.Points, EpochBudget(0))
}
