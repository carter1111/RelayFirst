package verification_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/verification"
)

// These tests cover the Activity injection, which is what makes the ratio cap an
// actual constraint rather than a rule that only exists in a policy's unit test.
//
// This was a real gap: Verify used to pass (0, 0) to AllowVerify unconditionally, so a
// ratio-configured policy would have refused every verification in production — or, if
// the policy were permissive, enforced nothing at all. Neither is visible from testing
// the policy alone.

// stubActivity reports fixed epoch counts.
type stubActivity struct {
	produced int
	verified int
	err      error
}

func (s stubActivity) Produced(uint64, string) (int, error) { return s.produced, s.err }
func (s stubActivity) Verified(uint64, string) (int, error) { return s.verified, s.err }

// permissivePolicy allows verification but records what counts it was given.
type permissivePolicy struct {
	who    string
	gotP   int
	gotV   int
	called bool
}

func (p *permissivePolicy) Assign(*receipt.Receipt, []string) (string, error) { return p.who, nil }

func (p *permissivePolicy) AllowVerify(_ uint64, _ string, produced, verified int) error {
	p.called = true
	p.gotP = produced
	p.gotV = verified
	return nil
}

// TestActivityCountsReachThePolicy is the assertion the gap needed: whatever the
// Activity source reports must arrive at the policy, or the ratio is measured against
// zeros and enforces nothing.
func TestActivityCountsReachThePolicy(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("91", 32), "https://example.com/health", "200")
	policy := &permissivePolicy{who: agentOf(t, keyVerifier)}

	stakes := verification.NewMemStakeLedger()
	v, err := verification.New(verification.Config{
		Policy:      policy,
		Stakes:      stakes,
		Receipts:    newFakeUpdater(),
		Recomputer:  &fakeRecomputer{result: r.Result},
		Activity:    stubActivity{produced: 40, verified: 12},
		StakePoints: 50,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := v.Verify(context.Background(), r); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if !policy.called {
		t.Fatal("AllowVerify was never called")
	}
	if policy.gotP != 40 || policy.gotV != 12 {
		t.Errorf("policy saw produced=%d verified=%d, want 40/12 — the counts did not reach it",
			policy.gotP, policy.gotV)
	}
}

// TestRatioCapIsEnforcedThroughVerify drives the cap through the real entry point
// rather than calling the policy directly, so a wiring mistake cannot hide.
func TestRatioCapIsEnforcedThroughVerify(t *testing.T) {
	verifier := agentOf(t, keyVerifier)

	cases := []struct {
		name     string
		produced int
		verified int
		wantErr  bool
	}{
		{"within the cap", 10, 2, false},
		{"at the cap", 10, 5, false},
		{"past the cap", 10, 6, true},
		{"pure verifier", 0, 0, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("92", 32), "https://example.com/health", "200")

			v, err := verification.New(verification.Config{
				Policy:      verification.NewEligiblePolicy(verification.EligiblePolicyConfig{Candidates: []string{verifier}}),
				Stakes:      verification.NewMemStakeLedger(),
				Receipts:    newFakeUpdater(),
				Recomputer:  &fakeRecomputer{result: r.Result},
				Activity:    stubActivity{produced: c.produced, verified: c.verified},
				StakePoints: 50,
				Candidates:  []string{verifier},
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			_, err = v.Verify(context.Background(), r)
			if c.wantErr && err == nil {
				t.Errorf("produced=%d verified=%d must be refused through Verify", c.produced, c.verified)
			}
			if !c.wantErr && err != nil {
				t.Errorf("produced=%d verified=%d should be allowed, got %v", c.produced, c.verified, err)
			}
		})
	}
}

// TestNoActivityFailsClosed: with no Activity source the counts are zero, and a policy
// that refuses a pure verifier must therefore refuse. The alternative — silently
// permitting — would disable the rule whenever the wiring was forgotten, which is the
// worst possible failure mode for a safety check.
func TestNoActivityFailsClosed(t *testing.T) {
	verifier := agentOf(t, keyVerifier)
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("93", 32), "https://example.com/health", "200")

	v, err := verification.New(verification.Config{
		Policy:      verification.NewEligiblePolicy(verification.EligiblePolicyConfig{Candidates: []string{verifier}}),
		Stakes:      verification.NewMemStakeLedger(),
		Receipts:    newFakeUpdater(),
		Recomputer:  &fakeRecomputer{result: r.Result},
		StakePoints: 50,
		Candidates:  []string{verifier},
		// No Activity: counts default to 0/0.
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := v.Verify(context.Background(), r); err == nil {
		t.Error("with no activity source the ratio cannot be evaluated, so verification must be refused rather than permitted")
	}
}

// TestAllowAllStillPermitsWithoutActivity records the deliberate exception: the testing
// policy is the one that may proceed without counts, so tests and dry runs do not need
// an activity source.
func TestAllowAllStillPermitsWithoutActivity(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("94", 32), "https://example.com/health", "200")

	v, err := verification.New(verification.Config{
		Policy:      verification.AllowAll{Verifier: agentOf(t, keyVerifier)},
		Stakes:      verification.NewMemStakeLedger(),
		Receipts:    newFakeUpdater(),
		Recomputer:  &fakeRecomputer{result: r.Result},
		StakePoints: 50,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := v.Verify(context.Background(), r); err != nil {
		t.Errorf("AllowAll should not need an activity source, got %v", err)
	}
}

// TestActivityErrorIsReported: a count that cannot be read must stop verification
// rather than defaulting to zero, because zero silently means "pure verifier" and would
// refuse a legitimate verifier for the wrong reason.
func TestActivityErrorIsReported(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("95", 32), "https://example.com/health", "200")

	v, err := verification.New(verification.Config{
		Policy:      verification.AllowAll{Verifier: agentOf(t, keyVerifier)},
		Stakes:      verification.NewMemStakeLedger(),
		Receipts:    newFakeUpdater(),
		Recomputer:  &fakeRecomputer{result: r.Result},
		Activity:    stubActivity{err: errors.New("epoch stats unavailable")},
		StakePoints: 50,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = v.Verify(context.Background(), r)
	if err == nil {
		t.Fatal("an unreadable activity count must stop verification")
	}
	if !strings.Contains(err.Error(), "epoch stats unavailable") {
		t.Errorf("error should carry the cause, got %v", err)
	}
}

// TestVerifierRefusesAnEmptyAssignment: a policy returning "" would otherwise reach the
// comparison as an empty verifier id, which is not an agent.
func TestVerifierRefusesAnEmptyAssignment(t *testing.T) {
	r := mkReceipt(t, keyProducer, "0x"+strings.Repeat("96", 32), "https://example.com/health", "200")

	v, err := verification.New(verification.Config{
		Policy:      emptyAssigner{},
		Stakes:      verification.NewMemStakeLedger(),
		Receipts:    newFakeUpdater(),
		Recomputer:  &fakeRecomputer{result: r.Result},
		StakePoints: 50,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := v.Verify(context.Background(), r); err == nil {
		t.Error("an empty assignment must be refused")
	}
}

type emptyAssigner struct{}

func (emptyAssigner) Assign(*receipt.Receipt, []string) (string, error) { return "", nil }
func (emptyAssigner) AllowVerify(uint64, string, int, int) error        { return nil }

// TestVerifierRefusesNilReceipt: a nil receipt is a programming error, not a verdict.
func TestVerifierRefusesNilReceipt(t *testing.T) {
	v, err := verification.New(verification.Config{
		Policy:      verification.AllowAll{Verifier: agentOf(t, keyVerifier)},
		Stakes:      verification.NewMemStakeLedger(),
		Receipts:    newFakeUpdater(),
		Recomputer:  &fakeRecomputer{result: receipt.Result{Value: "200", Hash: hash32("x")}},
		StakePoints: 50,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := v.Verify(context.Background(), nil); err == nil {
		t.Error("a nil receipt must be refused")
	}
}

// TestVerifierRequiresEveryCollaborator keeps construction fail-fast, so a
// half-configured verifier cannot exist.
func TestVerifierRequiresEveryCollaborator(t *testing.T) {
	full := verification.Config{
		Policy:      verification.AllowAll{Verifier: agentOf(t, keyVerifier)},
		Stakes:      verification.NewMemStakeLedger(),
		Receipts:    newFakeUpdater(),
		Recomputer:  &fakeRecomputer{result: receipt.Result{Value: "200", Hash: hash32("y")}},
		StakePoints: 50,
		Now:         func() time.Time { return time.Unix(1791015900, 0) },
	}

	if _, err := verification.New(full); err != nil {
		t.Fatalf("a fully configured verifier should construct, got %v", err)
	}

	cases := map[string]func(c *verification.Config){
		"no policy":     func(c *verification.Config) { c.Policy = nil },
		"no stakes":     func(c *verification.Config) { c.Stakes = nil },
		"no receipts":   func(c *verification.Config) { c.Receipts = nil },
		"no recomputer": func(c *verification.Config) { c.Recomputer = nil },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := full
			mutate(&c)
			if _, err := verification.New(c); err == nil {
				t.Errorf("New must refuse a config with %s", name)
			}
		})
	}
}
