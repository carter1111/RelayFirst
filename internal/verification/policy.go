package verification

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// This file holds the assignment policies.
//
// # The state of BLK-3
//
// The production assignment policy is an OPEN DECISION. MVP.md §5.5 says only "the
// protocol randomly assigns a verifier, not self-selected", and the choice of random
// source has real trade-offs: a deterministic seed is recomputable by anyone but lets
// an attacker grind agent ids to be chosen; a registry-based or node-based policy is
// simpler but concentrates trust somewhere.
//
// Rather than freeze that choice, the mechanism takes a Policy, and this file
// provides one reference implementation that is honest about its limits. Every real
// candidate remains implementable without touching the verification mechanism.
//
// What a policy CANNOT do is weaken the anti-collusion rules: Verifier.Verify
// re-checks the assignment and refuses a self-verification regardless of what a
// policy returns.

// RatioPolicy enforces MVP.md §5.6 rule 2: an agent may not verify far more than it
// produces.
//
// # Why a ratio rather than a fixed cap
//
// A fixed cap would penalise a prolific producer, who legitimately has more work in
// circulation to verify. A ratio scales the allowance with the agent's own
// production, so the rule means "you must be doing the work you are judging" rather
// than "you may only judge a few things". A pure verifier — one with no production at
// all — is refused outright, because it has no reputation to lose.
type RatioPolicy struct {
	// MaxVerifiedPerProduced is the allowance, as a multiple of what the agent
	// produced in the epoch. 0.5 means "half as many verifications as productions".
	MaxVerifiedPerProduced float64
}

// NewRatioPolicy returns a RatioPolicy.
//
// A non-positive ratio is not silently corrected: a ratio of zero would refuse all
// verification, and a negative one is meaningless. Both are almost certainly a
// configuration mistake, so AllowVerify refuses them explicitly rather than
// producing a verifier that quietly cannot work.
func NewRatioPolicy(maxVerifiedPerProduced float64) *RatioPolicy {
	return &RatioPolicy{MaxVerifiedPerProduced: maxVerifiedPerProduced}
}

// AllowVerify reports whether agent may verify given its epoch activity.
//
// # The boundary case, stated rather than hidden
//
// produced and verified are what the agent has already done this epoch, so the rule
// is `verified <= produced * ratio`. An agent sitting exactly at its allowance is
// therefore permitted this verification, which puts it one over afterwards.
//
// That is a deliberate choice, not an oversight. MVP.md §5.6 calls this a "ratio cap"
// — a proportion that keeps verification a subsidiary activity — and it is enforced
// here as a soft deterrence bound rather than a security boundary. A hard boundary
// would need a shared counter and an atomic check-then-increment, which is a
// different design; the honest thing is to document where the edge sits.
//
// # Why the counts come from the caller
//
// This package does not own an epoch's production statistics — the mining and store
// layers do. Passing them in keeps the policy a pure function of facts it did not
// have to gather, which makes it testable and lets a different policy use a different
// measure without touching the mechanism.
func (p *RatioPolicy) AllowVerify(epoch uint64, agent string, produced, verified int) error {
	if p == nil || p.MaxVerifiedPerProduced <= 0 {
		return errors.New("verification: the ratio policy needs a positive ratio; as configured no verification could ever be allowed")
	}
	if agent == "" {
		return errors.New("verification: no agent to check")
	}
	if produced < 0 || verified < 0 {
		return errors.New("verification: activity counts cannot be negative")
	}

	// A pure verifier has no production to be measured against, so it has nothing at
	// risk and cannot be allowed to judge. This is the rule that makes "just verify
	// everything" unprofitable rather than free.
	if produced == 0 {
		return fmt.Errorf("verification: agent %s produced nothing in epoch %d, so it may not verify; a pure verifier has no stake to lose", agent, epoch)
	}

	allowance := float64(produced) * p.MaxVerifiedPerProduced
	if float64(verified) > allowance {
		return fmt.Errorf("verification: agent %s has verified %d against %d produced in epoch %d, which exceeds the %g cap",
			agent, verified, produced, epoch, p.MaxVerifiedPerProduced)
	}
	return nil
}

// EligiblePolicyConfig configures the reference assigner.
type EligiblePolicyConfig struct {
	// Candidates are the agents that may be assigned.
	Candidates []string

	// Ratio is the anti-collusion allowance. Zero means DefaultRatio.
	Ratio float64

	// Seed is mixed into the selection. It is a parameter rather than a fixed
	// constant so a deployment can change it per epoch without a code change, and so
	// tests can pin it.
	Seed string
}

// DefaultRatio is the default verification allowance.
//
// A half is chosen so that verification is a subsidiary activity: it cannot become
// more than half of what an agent does, which keeps production the dominant activity
// as MVP.md §5.7 requires ("verification rewards must be less than production
// rewards, so pure verification is not worthwhile").
const DefaultRatio = 0.5

// EligiblePolicy is a reference Policy: it picks a candidate deterministically from
// the receipt, and enforces the ratio cap.
//
// # Which BLK-3 candidate this resembles, and its weakness
//
// This is the "deterministic seed" option: the choice is recomputable by anyone, so
// two honest verifiers agree on who was supposed to check a receipt, and no server
// decides. That property matters because a receipt's verifier is meant to be
// re-derivable from public data.
//
// Its known weakness is grindability: an attacker who can choose its own agent id can
// keep hashing until it lands on itself as verifier. MVP.md §5.6 already records that
// sybil self-verification is an accepted, linearly-costly trade rather than a solved
// problem, and this policy does not change that — it should not be described as
// removing it.
//
// It is provided as a *reference*, not as the chosen production policy. BLK-3 remains
// open, and a different Policy can be substituted without touching anything else.
type EligiblePolicy struct {
	cfg EligiblePolicyConfig
}

// NewEligiblePolicy returns the reference policy.
func NewEligiblePolicy(cfg EligiblePolicyConfig) *EligiblePolicy {
	if cfg.Ratio == 0 {
		cfg.Ratio = DefaultRatio
	}

	// The candidate set is sorted and de-duplicated so the choice depends on the set
	// rather than on the order a caller happened to list it in. Without this, two
	// verifiers with the same view but a different ordering would disagree on who was
	// assigned.
	seen := map[string]bool{}
	clean := make([]string, 0, len(cfg.Candidates))
	for _, c := range cfg.Candidates {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		clean = append(clean, c)
	}
	sort.Strings(clean)
	cfg.Candidates = clean

	return &EligiblePolicy{cfg: cfg}
}

// Assign picks a verifier for r.
//
// candidates, when non-empty, replaces the configured set, so a caller with a fresher
// view can use it without rebuilding the policy.
func (p *EligiblePolicy) Assign(r *receipt.Receipt, candidates []string) (string, error) {
	if r == nil {
		return "", errors.New("verification: nil receipt")
	}

	pool := candidates
	if len(pool) == 0 {
		pool = p.cfg.Candidates
	}

	// The producer is excluded here as well as in Verify. Doing it in both places is
	// deliberate: a policy that leaks an impossible assignment would otherwise cause a
	// confusing failure later, and a mechanism that relies only on the policy would
	// trust the component most likely to be replaced.
	eligible := make([]string, 0, len(pool))
	for _, c := range pool {
		if c != "" && c != r.AgentID {
			eligible = append(eligible, c)
		}
	}
	if len(eligible) == 0 {
		return "", fmt.Errorf("verification: no eligible verifier for %s (the only candidates were the producer)", r.ReceiptID)
	}
	sort.Strings(eligible)

	// Deterministic selection: a position derived from the seed and the receipt.
	//
	// The modulus is applied to a hash-like fold rather than to a counter, so
	// consecutive receipts do not walk the candidate list in order — which would make
	// the assignment predictable in a way an attacker could farm.
	return eligible[p.slot(r)], nil
}

// slot returns the index into the sorted eligible set.
func (p *EligiblePolicy) slot(r *receipt.Receipt) int {
	// A small, dependency-free fold. This is NOT cryptography and must not be used
	// for anything that needs collision resistance (invariant A1); it only spreads
	// receipts across candidates, and Verify does not depend on it for safety.
	var acc uint64 = 1469598103934665603 // FNV-1a offset basis
	for _, b := range []byte(p.cfg.Seed + "|" + strings.TrimPrefix(r.ReceiptID, "0x")) {
		acc ^= uint64(b)
		acc *= 1099511628211
	}
	return int(acc % uint64(p.poolSize(r)))
}

// poolSize returns the number of eligible candidates for r.
func (p *EligiblePolicy) poolSize(r *receipt.Receipt) int {
	n := 0
	for _, c := range p.cfg.Candidates {
		if c != "" && c != r.AgentID {
			n++
		}
	}
	if n == 0 {
		// Assign rejects this case before slot is reached; returning 1 keeps the
		// modulus well-defined rather than panicking.
		return 1
	}
	return n
}

// AllowVerify enforces the ratio cap.
func (p *EligiblePolicy) AllowVerify(epoch uint64, agent string, produced, verified int) error {
	ratio := p.cfg.Ratio
	if ratio == 0 {
		ratio = DefaultRatio
	}
	return NewRatioPolicy(ratio).AllowVerify(epoch, agent, produced, verified)
}

// AllowAll is a Policy that assigns a fixed verifier and permits unlimited
// verification.
//
// # It exists for tests and dry runs only
//
// AllowAll disables both anti-collusion rules, so it must not be used in a deployment
// that intends verification to mean anything. It is exported because tests in other
// packages need a way to drive the mechanism without a candidate registry, and a
// labelled testing seam is better than each caller writing its own permissive policy.
type AllowAll struct {
	// Verifier is the agent always assigned. If it equals the producer, Verify
	// refuses — which is the correct behaviour and a useful way to test that guard.
	Verifier string
}

// Assign returns the fixed verifier.
func (a AllowAll) Assign(*receipt.Receipt, []string) (string, error) {
	if strings.TrimSpace(a.Verifier) == "" {
		return "", errors.New("verification: AllowAll needs a verifier")
	}
	return a.Verifier, nil
}

// AllowVerify permits everything, including a pure verifier.
func (a AllowAll) AllowVerify(uint64, string, int, int) error { return nil }
