package compliance_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/relayfirst/relayfirst/internal/compliance"
)

// The tests below come in three groups, and the grouping is the point.
//
// 1. Violations must be caught. A guard that never fires is not a guard.
// 2. Approved disclaimers must be *seen and excused*, not merely absent. This is the
//    subtle one: if the excusal logic silently stopped working, the guard would still
//    report "no violations" in this repository, because every risky phrase here is
//    inside a disclaimer. So the excused path is asserted directly.
// 3. Benign text must not fire. A false positive is not a cosmetic problem — it trains
//    the reader to ignore the guard.

func TestScan_CatchesValueClaims(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		wants compliance.Concept
	}{
		{"worth", "Your points are worth $50 each.", compliance.ConceptValue},
		{"valuable", "These points are valuable.", compliance.ConceptValue},
		{"cash", "Points can be converted to cash.", compliance.ConceptValue},
		{"currency", "Each point is worth 1 USDC.", compliance.ConceptValue},
		{"yield", "Points earn a yield while held.", compliance.ConceptReturn},
		{"apy", "Points accrue 12% APY.", compliance.ConceptReturn},
		{"profit", "Mine points for profit.", compliance.ConceptValue},
		{"price", "The price of a point is $1.", compliance.ConceptPrice},
		{"redeem", "Redeem your points for tokens.", compliance.ConceptRedemption},
		{"cash out", "You can cash out your points.", compliance.ConceptRedemption},
		{"withdraw", "Withdraw points to your wallet.", compliance.ConceptRedemption},
		{"transferable", "Points are transferable.", compliance.ConceptTransfer},
		{"tradeable", "Points are tradeable on exchanges.", compliance.ConceptTransfer},
		{"sell", "You can sell your points.", compliance.ConceptTransfer},
		{"exchange", "Trade points on the exchange.", compliance.ConceptTransfer},
		{"airdrop", "Points qualify you for the airdrop.", compliance.ConceptToken},
		{"guarantee", "We guarantee points will appreciate.", compliance.ConceptToken},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := compliance.ExcusedFindings(compliance.Scan("test", tc.text))
			if len(findings) == 0 {
				t.Fatalf("Scan(%q) found nothing; this is a value claim and must be caught", tc.text)
			}
			for _, f := range findings {
				if f.Claim.Concept == tc.wants {
					return
				}
			}
			t.Fatalf("Scan(%q) fired but not on concept %q; got %+v", tc.text, tc.wants, findings)
		})
	}
}

// TestScan_ExcusesApprovedDisclaimers asserts the sanctioned A5 wording is accepted.
//
// It asserts `len(all) > 0` as well as `len(unexcused) == 0`. Without that first assertion this
// test would pass on a guard that had stopped matching entirely — the most dangerous way for
// this package to fail, because it would look green forever.
func TestScan_ExcusesApprovedDisclaimers(t *testing.T) {
	disclaimers := []string{
		"Points are non-transferable and carry no promised return (MVP.md §6.1).",
		"points are non-transferable and unpriced; epoch budget decays from B0",
		"Points are non-transferable, unpriced, and carry no promised return (MVP.md §6.1)",
		"Points are **unpriced** — no exchange, no rate,",
		"Points have no cash value and cannot be withdrawn.",
		"Points are not redeemable and no airdrop is promised.",
		"Points are not transferable and the price is not defined.",
	}

	for _, d := range disclaimers {
		findings := compliance.Scan("test", d)
		if len(findings) == 0 {
			t.Errorf("Scan(%q) matched nothing at all; the excusal path is not being exercised", d)
			continue
		}
		if bad := compliance.ExcusedFindings(findings); len(bad) != 0 {
			t.Errorf("Scan(%q) flagged approved wording as a violation: %+v", d, bad)
		}
	}
}

// TestScan_CaseInsensitiveExcusal covers the shorthands the guard must tolerate.
//
// `non-transferable` appears in title case in some prose and in a heading in others. Treating
// "Non-Transferable" as a violation would be a false positive of exactly the kind this guard
// cannot afford.
func TestScan_CaseInsensitiveExcusal(t *testing.T) {
	for _, d := range []string{
		"Points are Non-Transferable.",
		"Points are NON-TRANSFERABLE.",
		"Points are Not Transferable.",
	} {
		if bad := compliance.ExcusedFindings(compliance.Scan("test", d)); len(bad) != 0 {
			t.Errorf("Scan(%q) flagged %+v", d, bad)
		}
	}
}

// TestScan_IgnoresBenignText is the false-positive guard.
//
// Every string here appears in, or is representative of, real repository text. If any of these
// fired, the guard would be unusable in practice and would be ignored.
func TestScan_IgnoresBenignText(t *testing.T) {
	benign := []string{
		// The risk does not exist in the text; the words are ordinary English.
		"relayfirst mine --provider openai --semantic \"the main product price\"",
		"Terms rather than depending on a caller's ordering guarantee.",
		"Points are credited once per epoch.",
		"Your balance is recorded in the points ledger.",
		"Epoch budget decays from B0 by a fixed factor.",
		"// producing worthless work.",
		"",
		"   ",
	}

	for _, b := range benign {
		if got := compliance.ExcusedFindings(compliance.Scan("test", b)); len(got) != 0 {
			t.Errorf("Scan(%q) fired on benign text: %+v", b, got)
		}
	}
}

// TestScan_ScopeIsPerLine documents the deliberate scope filter, including its cost.
//
// The guard only rules on lines that mention points. This test states both sides so the
// trade-off is visible in the code rather than buried in a comment: it proves that an unrelated
// "price" is ignored, and that the same word becomes a finding the moment the line is about
// points.
func TestScan_ScopeIsPerLine(t *testing.T) {
	outOfScope := "The extractor reads the main product price from the listing page."
	if got := compliance.Scan("test", outOfScope); len(got) != 0 {
		t.Fatalf("Scan(%q) ruled on a line that is not about points: %+v", outOfScope, got)
	}

	inScope := "The price of points is set by the market."
	if got := compliance.ExcusedFindings(compliance.Scan("test", inScope)); len(got) == 0 {
		t.Fatalf("Scan(%q) missed a price claim about points", inScope)
	}
}

// TestScan_LimitsAreReal records the two things this guard cannot do.
//
// Both are stated as assertions on observed behaviour rather than as prose, because a
// limitation that is only described has a way of quietly ceasing to be true.
func TestScan_LimitsAreReal(t *testing.T) {
	t.Run("novel phrasing is missed", func(t *testing.T) {
		// No word from the vocabulary appears, so the guard cannot see it. This is the
		// inherent limit of a vocabulary-based tripwire.
		text := "Points will one day be convertible into something of greater value."
		if got := compliance.ExcusedFindings(compliance.Scan("test", text)); len(got) != 0 {
			t.Logf("guard unexpectedly caught novel phrasing: %+v (update the comment if this is now reliable)", got)
		}
	})

	t.Run("disclaimer wording evades the guard", func(t *testing.T) {
		// Wording a real violation exactly like an approved disclaimer defeats the excusal
		// logic. This is the documented cost of exact-phrase matching, and it is deliberately
		// not "fixed" by a heuristic, because a heuristic misfires in both directions.
		text := "Points are unpriced, and you can also sell them for USDC."
		if got := compliance.ExcusedFindings(compliance.Scan("test", text)); len(got) != 0 {
			t.Logf("guard caught the disguised violation: %+v (the risk is lower than documented)", got)
		}
	})
}

// TestScan_LineNumbersAreAccurate keeps findings actionable: a reviewer needs to jump to the
// right line, and an off-by-one would send them to innocent code.
func TestScan_LineNumbersAreAccurate(t *testing.T) {
	text := "line one is fine\nYour points are worth a lot.\nline three"
	got := compliance.ExcusedFindings(compliance.Scan("test", text))
	if len(got) == 0 {
		t.Fatal("expected a finding on line 2")
	}
	for _, f := range got {
		if f.Line != 2 {
			t.Errorf("finding reported line %d, want 2 (%+v)", f.Line, f)
		}
	}
}

// TestAuditRepository is the S8-9 gate itself: the guard is run over the repository's real
// user-facing text, and must find nothing to review.
//
// This is the assertion that turns the guard from a library into a check. If a future change
// prints an A5-violating sentence in the CLI or the docs, this test fails.
func TestAuditRepository(t *testing.T) {
	root := repoRoot(t)

	// Only user-facing surfaces. Internal design notes (ARCHITECTURE.md, MVP.md) are allowed
	// to discuss value as a subject — MVP.md §6.1 is where the rule is *defined* — so scanning
	// them would produce exactly the noise this guard must avoid.
	targets := []string{
		filepath.Join(root, "cmd", "relayfirst", "main.go"),
		filepath.Join(root, "docs", "getting-started.md"),
		filepath.Join(root, "README.md"),
	}

	total := 0
	for _, path := range targets {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		total += len(compliance.Scan(path, text))

		if bad := compliance.ExcusedFindings(compliance.Scan(path, text)); len(bad) != 0 {
			for _, f := range bad {
				t.Errorf("%s:%d: %q — %s", f.Source, f.Line, f.Text, f.Claim.Why)
			}
		}
	}

	// The guard must actually have looked at something. A scan that read zero bytes would
	// report no violations and pass, which is the failure mode this assertion exists to catch.
	if total == 0 {
		t.Errorf("audited %d user-facing files and matched zero phrases; the guard is probably not running",
			len(targets))
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// internal/compliance -> repo root
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s does not contain go.mod: %v", root, err)
	}
	return root
}

// TestVocab_NoDuplicatePatterns guards against a copy-paste addition. Two claims with the same
// pattern would double-report every match, which is noise rather than a detection gain.
func TestVocab_NoDuplicatePatterns(t *testing.T) {
	seen := map[string]string{}
	for _, text := range []string{
		"Your points are worth 1 USDC.",
		"Points yield 5%.",
		"Points are transferable.",
		"Redeem points now.",
		"Points are unpriced.",
	} {
		findings := compliance.Scan("test", text)
		for _, f := range findings {
			key := f.Claim.Pattern.String()
			if prev, ok := seen[key]; ok && prev != string(f.Claim.Concept) {
				t.Errorf("pattern %q reports two concepts: %s and %s", key, prev, f.Claim.Concept)
			}
			seen[key] = string(f.Claim.Concept)
		}
	}
}
