package assertion_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/assertion"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// This is the S10-0 acceptance test, and it is criterion ⑩'s real shape:
//
//	"two nodes reach opposite conclusions about the same receipt, and the client can say
//	 which node ADDRESS lied, without trusting either."
//
// The distinction from S10-5's tests is what is being exercised. Those tested the mechanism
// with one verifier; this tests DISAGREEMENT between two identified verifiers, which is the
// property that makes attributability worth having. If a verdict could not be attributed, two
// conflicting claims would be unresolvable — you would have a contradiction with no way to
// say who produced it.

// dishonestVerifier builds a runner that returns a fixed answer regardless of the receipt.
func verifierWithAnswer(t *testing.T, key string, answer bool, err error) *assertion.VerifierRunner {
	t.Helper()
	return &assertion.VerifierRunner{
		VerifierID:    agentIDFor(t, key),
		PrivateKeyHex: key,
		ChainID:       chainID,
		Recheck: func(context.Context, *receipt.Receipt) (bool, error) {
			return answer, err
		},
		Now: func() time.Time { return time.Unix(1791015900, 0) },
	}
}

// TestCriterion10_TwoNodesDisagreeAndClientNamesTheLiar is the acceptance criterion.
func TestCriterion10_TwoNodesDisagreeAndClientNamesTheLiar(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)

	// Node A re-checks and says valid.
	honest := verifierWithAnswer(t, verifierKey, true, nil)
	assertA, err := honest.Assert(context.Background(), r, raw)
	if err != nil {
		t.Fatalf("node A assert: %v", err)
	}

	// Node B says invalid, with a reason. Whether B is lying or A is, the client cannot know
	// from the claims alone — which is exactly why it must check for itself.
	liar := &assertion.VerifierRunner{
		VerifierID:    agentIDFor(t, otherKey),
		PrivateKeyHex: otherKey,
		ChainID:       chainID,
		Recheck: func(context.Context, *receipt.Receipt) (bool, error) {
			return false, nil
		},
		Now: func() time.Time { return time.Unix(1791015900, 0) },
	}
	assertB, err := liar.Assert(context.Background(), r, raw)
	if err != nil {
		t.Fatalf("node B assert: %v", err)
	}

	// Both assertions must be genuine and attributable, or there would be nothing to argue
	// about — only two anonymous claims.
	if err := assertA.Verify(raw); err != nil {
		t.Fatalf("node A's assertion must be genuine: %v", err)
	}
	if err := assertB.Verify(raw); err != nil {
		t.Fatalf("node B's assertion must be genuine: %v", err)
	}
	if assertA.VerifierID == assertB.VerifierID {
		t.Fatal("the two nodes must have distinct identities, or 'who lied' is unanswerable")
	}

	// The client checks for itself and reaches its own conclusion.
	checkA := assertion.Check(raw, nil, &assertA)
	checkB := assertion.Check(raw, nil, &assertB)

	if !checkA.ReceiptValid || !checkB.ReceiptValid {
		t.Fatal("the client's own check must stand in both cases: the receipt is valid")
	}

	// A's verdict agrees with the client; B's does not. So B is the one whose claim
	// conflicts with a check the client performed itself — and B's identity is a value the
	// client can name, which is the whole point of attributability.
	if !checkA.Agrees {
		t.Errorf("node A's verdict matches the client's own check and must be recorded as agreeing")
	}
	if checkB.Agrees {
		t.Errorf("node B's verdict conflicts with the client's own check and must not be recorded as agreeing")
	}
	if checkB.Note == "" {
		t.Error("the disagreement must be reported, not silently resolved")
	}

	// The identity that lied is a specific, checkable value: B's address.
	if assertB.VerifierID != agentIDFor(t, otherKey) {
		t.Errorf("the disagreeing verdict must be attributable to B's identity, got %q", assertB.VerifierID)
	}
}

// TestCriterion10_ClientIsNotSwungByEitherClaim is the other half: the client's conclusion
// must not depend on how many nodes agree, or a majority of liars would win.
func TestCriterion10_ClientIsNotSwungByEitherClaim(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)

	// Three nodes, all claiming invalid, against a receipt the client can see is valid.
	keys := []string{verifierKey, otherKey, "0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6"}
	for _, k := range keys {
		runner := &assertion.VerifierRunner{
			VerifierID:    agentIDFor(t, k),
			PrivateKeyHex: k,
			ChainID:       chainID,
			Recheck:       func(context.Context, *receipt.Receipt) (bool, error) { return false, nil },
			Now:           func() time.Time { return time.Unix(1791015900, 0) },
		}
		a, err := runner.Assert(context.Background(), r, raw)
		if err != nil {
			t.Fatalf("assert: %v", err)
		}

		out := assertion.Check(raw, nil, &a)
		if !out.ReceiptValid {
			t.Fatal("the client must still accept the valid receipt; agreement by numbers " +
				"must not override a check the client performed itself")
		}
		if out.Agrees {
			t.Error("a disagreeing verdict must not be recorded as agreement no matter how many nodes say it")
		}
	}
}

// TestCriterion10_UnsupportedIsDistinguishableFromADisagreement keeps a version mismatch from
// looking like a lie.
//
// A node that cannot check a newer schema is not accusing anyone, and a client that lumped the
// two together would report a liar where there was only an out-of-date node.
func TestCriterion10_UnsupportedIsDistinguishableFromADisagreement(t *testing.T) {
	r, raw := signedReceipt(t, producerKey)

	outdated := &assertion.VerifierRunner{
		VerifierID:    agentIDFor(t, verifierKey),
		PrivateKeyHex: verifierKey,
		ChainID:       chainID,
		Recheck: func(context.Context, *receipt.Receipt) (bool, error) {
			return false, errors.New("this build cannot check schema v99")
		},
		Now: func() time.Time { return time.Unix(1791015900, 0) },
	}
	a, err := outdated.Assert(context.Background(), r, raw)
	if err != nil {
		t.Fatalf("assert: %v", err)
	}

	out := assertion.Check(raw, nil, &a)
	if !out.ReceiptValid {
		t.Fatal("the client's own check stands")
	}
	if out.AssertionVerdict != assertion.VerdictUnsupported {
		t.Errorf("verdict = %s, want %s", out.AssertionVerdict, assertion.VerdictUnsupported)
	}
	if !out.AssertionGenuine {
		t.Error("the assertion is genuine; the node simply could not decide")
	}
	// The two must be told apart in the note, since that is what an operator reads.
	if out.Note == "" || !containsAny(out.Note, "could not check", "not an accusation") {
		t.Errorf("an unsupported verdict must not read as an accusation, got: %q", out.Note)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
