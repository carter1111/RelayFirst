// Package compliance enforces invariant A5 in user-facing text.
//
// # What A5 requires
//
// MVP.md §6.1: points are recorded in a ledger, non-transferable, unpriced, and carry no
// promised return. It adds that this "must be written into the UI and docs" — so A5 is not
// only a design constraint, it is a *claim* the product makes to users.
//
// That makes it enforceable in a way most invariants are not. The structural half is already
// covered: a reflection test pins the points ledger's method set, and adding a debit verb
// fails the build. This package covers the other half, which no structural test can reach —
// **the words**. A CLI that prints "your points are worth something" would violate A5 while
// every Go test still passed.
//
// # What this is, precisely
//
// A tripwire, not a proof. It matches known-risky vocabulary and requires human review of
// anything it cannot excuse. It cannot evaluate a novel phrasing, and it cannot tell a
// disclaimer from a claim when both use the same words. Both limits are tested rather than
// described as if they did not exist.
package compliance

import (
	"regexp"
	"strings"
)

// Concept groups claims by what A5 property they would violate.
type Concept string

const (
	// ConceptValue is an assertion that points have worth.
	ConceptValue Concept = "value"

	// ConceptReturn is a promise of profit, yield or interest.
	ConceptReturn Concept = "return"

	// ConceptPrice is an assertion that points have a price or a valuation.
	ConceptPrice Concept = "price"

	// ConceptRedemption is an assertion that points can be exchanged for something.
	ConceptRedemption Concept = "redemption"

	// ConceptTransfer is an assertion that points can move between holders.
	ConceptTransfer Concept = "transferability"

	// ConceptToken is a reference to a token, airdrop or TGE as a certainty.
	ConceptToken Concept = "token"
)

// Claim is one risky phrase.
type Claim struct {
	// Concept is the A5 property this would violate.
	Concept Concept

	// Pattern matches the risky phrasing.
	//
	// Patterns are case-insensitive because users read "Redeem", not "redeem" — a
	// case-sensitive table missed four of eighteen obvious violations when first written.
	//
	// They are word-anchored where the word is short, so a longer benign word containing it
	// does not trip the guard: `\bprice\b` does not match "unpriced", which is the approved
	// opposite.
	Pattern *regexp.Regexp

	// Why explains, for a reviewer, what the phrase would be asserting.
	Why string
}

// riskyClaims is the vocabulary this guard watches for.
//
// # Why these words and not others
//
// Each one is a word a user would read as "this has monetary character". The list is
// deliberately about *words a user sees*, not about every word that could conceivably imply
// value, because a guard that fires on ordinary prose trains its reader to ignore it — which
// is worse than not having it.
//
// Note what is absent: "points", "earn", "reward", "epoch", "budget". Those appear in
// legitimate text throughout this repository, and none of them on its own asserts value.
var riskyClaims = []Claim{
	// value
	{ConceptValue, mustCompile(`(?i)\bworth\b`), "asserts points have worth"},
	{ConceptValue, mustCompile(`(?i)\bvaluable\b`), "asserts points are valuable"},
	{ConceptValue, mustCompile(`(?i)\bmonetary\b`), "asserts monetary character"},
	{ConceptValue, mustCompile(`(?i)\bprofit\w*\b`), "promises profit"},
	{ConceptValue, mustCompile(`(?i)\bcash\b`), "frames points as cash"},
	{ConceptValue, mustCompile(`(?i)\busd[ct]?\b`), "denominates points in a currency"},

	// return
	{ConceptReturn, mustCompile(`(?i)\byield\b`), "promises yield"},
	{ConceptReturn, mustCompile(`(?i)\bapy\b`), "quotes an annual yield"},
	{ConceptReturn, mustCompile(`(?i)\bapr\b`), "quotes an annual rate"},
	{ConceptReturn, mustCompile(`(?i)\broi\b`), "quotes a return on investment"},
	{ConceptReturn, mustCompile(`(?i)\bdividend\w*\b`), "promises a dividend"},
	{ConceptReturn, mustCompile(`(?i)\binterest\b`), "promises interest"},
	{ConceptReturn, mustCompile(`(?i)\bpromised return\b`), "promises a return"},

	// price
	{ConceptPrice, mustCompile(`(?i)\bpriced\b`), "asserts points are priced"},
	{ConceptPrice, mustCompile(`(?i)\bprice\b`), "asserts points have a price"},
	{ConceptPrice, mustCompile(`(?i)\bvaluation\b`), "asserts a valuation"},

	// redemption
	{ConceptRedemption, mustCompile(`(?i)\bredeem\w*\b`), "asserts points are redeemable"},
	{ConceptRedemption, mustCompile(`(?i)\bredemption\b`), "asserts redemption"},
	{ConceptRedemption, mustCompile(`(?i)\bcash out\b`), "asserts points can be cashed out"},
	{ConceptRedemption, mustCompile(`(?i)\bwithdraw\w*\b`), "asserts points can be withdrawn"},
	{ConceptRedemption, mustCompile(`(?i)\bpayout\b`), "asserts a payout"},

	// transferability
	//
	// "sell" is anchored rather than stemmed on purpose: `\bsell\w*\b` also matches
	// "seller", which is a benign role noun. `\bsell\b` does not.
	{ConceptTransfer, mustCompile(`(?i)\btransferable\b`), "asserts points are transferable"},
	{ConceptTransfer, mustCompile(`(?i)\btradeable\b`), "asserts points are tradeable"},
	{ConceptTransfer, mustCompile(`(?i)\btradable\b`), "asserts points are tradable"},
	{ConceptTransfer, mustCompile(`(?i)\bsell\b`), "asserts points can be sold"},
	{ConceptTransfer, mustCompile(`(?i)\bsold\b`), "asserts points can be sold"},
	{ConceptTransfer, mustCompile(`(?i)\bexchange\b`), "frames points as exchangeable"},

	// token
	{ConceptToken, mustCompile(`(?i)\bairdrop\b`), "promises an airdrop"},
	{ConceptToken, mustCompile(`(?i)\bguarantee\w*\b`), "guarantees an outcome"},
}

// approvedPhrases are disclaimers that express the *opposite* of a risky claim.
//
// # Why exact phrasing rather than a negation heuristic
//
// The obvious approach is to look for a negation near a match and excuse it. That is easy to
// fool: "points are not redeemable, and you can exchange them for cash" contains a negation
// and a real violation in the same sentence, and a proximity check would excuse the violation.
//
// Registering the sanctioned wording instead makes the excusal precise. Every phrase here is
// a statement of what points are *not*, taken from the language MVP.md §6.1 requires. Adding
// to this list is therefore a deliberate act that a reviewer sees in the diff — which is the
// right place for that decision.
//
// A risk of this approach is worth stating: a genuine violation that happens to be worded
// exactly like a disclaimer would pass. That is unlikely, and the alternative (a heuristic
// that misfires both ways) is worse.
var approvedPhrases = []string{
	// The core A5 statement, in the forms this repository actually uses.
	"non-transferable",
	"nontransferable",
	"not transferable",
	"unpriced",
	"not priced",
	"no price",
	"price is not defined",
	"carry no promised return",
	"carries no promised return",
	"no promised return",
	"carry no promise",
	"non-transferable, unpriced",
	"no exchange, no rate",
	"no cash value",
	"not redeemable",
	"no redemption",
	"cannot be withdrawn",
	"not tradeable",
	"not tradable",
	"no airdrop",
	"no token",
	"never issue a token",
	"we may never issue a token",
	"no guaranteed",
	"not guaranteed",
	"no guarantee",
	"no yield",
	"no profit",
	"no monetary value",
}

// pointsTokens are the subjects this guard rules on.
//
// A risky word only becomes a *claim* when the surrounding line is talking about points.
// Without this scoping the vocabulary is unusable: `price` appears in a legitimate mining
// example ("extract the main product price"), `guarantee` appears in a comment about message
// ordering, and `exchange` appears in ordinary prose. Flagging all of those would train a
// reader to ignore the guard, which is worse than having no guard at all.
//
// The cost of scoping is stated plainly: a value claim that never says "points" is missed.
// That is accepted, because a claim a user cannot tell is about points is not the claim A5
// forbids.
//
// "point" (singular) is included deliberately. "Each point is worth 1 USDC" and "the price of a
// point" are exactly the sentences this guard exists to catch, and requiring the plural would
// miss both — a real defect the test suite found rather than a hypothetical one.
var pointsTokens = []string{"point", "积分", "balance", "ledger"}

// inScope reports whether a line is talking about the points ledger.
func inScope(line string) bool {
	lower := strings.ToLower(line)
	for _, tok := range pointsTokens {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}

// Finding is one suspected violation.
type Finding struct {
	// Source identifies where the text came from, e.g. a file path or a CLI command.
	Source string

	// Line is the 1-based line number, or 0 when unknown.
	Line int

	// Text is the matched phrasing, for a reviewer to see in context.
	Text string

	// Claim is the risky phrase that matched.
	Claim Claim

	// Excused is true when the match lies inside an approved phrase.
	Excused bool

	// Excuse names the approved phrase that covered the match.
	Excuse string
}

// ExcusedFindings returns only the findings that need human review.
func ExcusedFindings(findings []Finding) []Finding {
	var out []Finding
	for _, f := range findings {
		if !f.Excused {
			out = append(out, f)
		}
	}
	return out
}

// Scan examines text and returns every risky match that is *about* points, marked excused or not.
//
// It returns excused matches as well as unexcused ones so a test can assert that the
// disclaimers were *seen and accepted* rather than merely absent. A guard that reported
// nothing would be indistinguishable from one that was not running.
//
// Findings on lines that never mention points are dropped rather than reported: see
// pointsTokens for why, and for the cost of that choice.
func Scan(source string, text string) []Finding {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	var findings []Finding
	for i, line := range strings.Split(text, "\n") {
		if !inScope(line) {
			continue
		}
		for _, claim := range riskyClaims {
			for _, loc := range claim.Pattern.FindAllStringIndex(line, -1) {
				start, end := loc[0], loc[1]

				excuse, excused := coveringPhrase(line, start, end)

				findings = append(findings, Finding{
					Source:  source,
					Line:    i + 1,
					Text:    line[start:end],
					Claim:   claim,
					Excused: excused,
					Excuse:  excuse,
				})
			}
		}
	}
	return findings
}

// coveringPhrase reports whether [start,end) lies inside an approved phrase.
//
// The comparison is case-insensitive because disclaimers appear in different cases across
// the CLI and the docs, and treating "Non-Transferable" as a violation would be a false
// positive of exactly the kind this guard must avoid.
func coveringPhrase(text string, start, end int) (string, bool) {
	lower := strings.ToLower(text)

	for _, phrase := range approvedPhrases {
		p := strings.ToLower(phrase)
		// Search outward so every occurrence of the phrase is considered, not just the first.
		from := 0
		for {
			i := strings.Index(lower[from:], p)
			if i < 0 {
				break
			}
			i += from
			if start >= i && end <= i+len(p) {
				return phrase, true
			}
			from = i + 1
		}
	}
	return "", false
}

func mustCompile(pattern string) *regexp.Regexp {
	re, err := regexp.Compile(pattern)
	if err != nil {
		// A malformed pattern is a programming error, and failing loudly at package
		// initialisation is better than a guard that silently checks nothing.
		panic("compliance: bad pattern " + pattern + ": " + err.Error())
	}
	return re
}
