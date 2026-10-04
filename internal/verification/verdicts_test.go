package verification_test

import (
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/verification"
)

// These tests cover the production verdict source, which is how S4-0 is actually
// closed: the score stops trusting the submitter and starts reading what a verifier
// recorded.

// verifiedReceipt builds a receipt carrying the given verification block.
func verifiedReceipt(status receipt.VerificationStatus, producer, verifier string, withEvidence bool) *receipt.Receipt {
	r := &receipt.Receipt{
		ReceiptID: "0x" + strings.Repeat("aa", 32),
		AgentID:   producer,
		Epoch:     7,
		Verification: receipt.Verification{
			Status: status,
		},
	}
	if verifier != "" {
		v := verifier
		r.Verification.VerifierID = &v
	}
	if withEvidence {
		h := "sha256:" + strings.Repeat("cd", 32)
		r.Verification.RecomputedHash = &h
		at := int64(1791015900)
		r.Verification.VerifiedAt = &at
	}
	return r
}

// TestRecordedVerdicts_OnlyExplicitVerifiedCounts is the conservative-default guard.
//
// Anything other than an affirmative verdict must read as not verified. Defaulting the
// other way would make the whole verification mechanism advisory.
func TestRecordedVerdicts_OnlyExplicitVerifiedCounts(t *testing.T) {
	producer := agentOf(t, keyProducer)
	verifier := agentOf(t, keyVerifier)

	source := verification.RecordedVerdicts{}

	cases := map[string]struct {
		status receipt.VerificationStatus
		want   bool
	}{
		"verified":   {receipt.VerificationVerified, true},
		"pending":    {receipt.VerificationPending, false},
		"rejected":   {receipt.VerificationRejected, false},
		"empty":      {"", false},
		"nonsense":   {"definitely-verified", false},
		"whitespace": {"  verified  ", false},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := verifiedReceipt(c.status, producer, verifier, true)
			if got := source.Verified(r); got != c.want {
				t.Errorf("Verified(status=%q) = %v, want %v", c.status, got, c.want)
			}
		})
	}
}

// TestRecordedVerdicts_RequiresEvidence: a `verified` status with no verifier and no
// recomputed hash is an unsupported claim. Accepting it would let a hand-edited
// receipt assert its own validity.
func TestRecordedVerdicts_RequiresEvidence(t *testing.T) {
	producer := agentOf(t, keyProducer)
	verifier := agentOf(t, keyVerifier)
	source := verification.RecordedVerdicts{}

	cases := map[string]struct {
		verifier     string
		withEvidence bool
		want         bool
	}{
		"verifier and evidence": {verifier, true, true},
		"no verifier":           {"", true, false},
		"no recomputed hash":    {verifier, false, false},
		"neither":               {"", false, false},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := verifiedReceipt(receipt.VerificationVerified, producer, c.verifier, c.withEvidence)
			if got := source.Verified(r); got != c.want {
				t.Errorf("Verified(%s) = %v, want %v", name, got, c.want)
			}
		})
	}
}

// TestRecordedVerdicts_RejectsSelfVerified is the one anti-collusion rule checkable
// from a receipt alone: a verdict written by the producer is not a verdict.
func TestRecordedVerdicts_RejectsSelfVerified(t *testing.T) {
	producer := agentOf(t, keyProducer)

	r := verifiedReceipt(receipt.VerificationVerified, producer, producer, true)

	if (verification.RecordedVerdicts{}).Verified(r) {
		t.Error("a receipt whose verifier is its own producer must not count as verified")
	}
}

func TestRecordedVerdicts_NilReceiptIsNotVerified(t *testing.T) {
	if (verification.RecordedVerdicts{}).Verified(nil) {
		t.Error("a nil receipt must not be considered verified")
	}
}

// TestRecordedVerdicts_PrefersFreshLookup: a caller may hold a receipt object that
// predates the verifier's write — the miner's in-memory copy, for instance. When a
// lookup is supplied, the authoritative state must win, or a just-verified receipt
// would be scored as unverified depending on which object the caller happened to pass.
func TestRecordedVerdicts_PrefersFreshLookup(t *testing.T) {
	producer := agentOf(t, keyProducer)
	verifier := agentOf(t, keyVerifier)

	// The caller's copy is still pending.
	stale := verifiedReceipt(receipt.VerificationPending, producer, "", false)

	// The store's copy has been verified.
	fresh := verifiedReceipt(receipt.VerificationVerified, producer, verifier, true)

	source := verification.RecordedVerdicts{
		Lookup: func(string) (*receipt.Receipt, bool) { return fresh, true },
	}

	if !source.Verified(stale) {
		t.Error("a fresh lookup must be preferred over a stale in-memory receipt")
	}

	// And when the lookup has nothing, the passed receipt is used as-is.
	empty := verification.RecordedVerdicts{
		Lookup: func(string) (*receipt.Receipt, bool) { return nil, false },
	}
	if empty.Verified(stale) {
		t.Error("with no lookup result, a pending receipt must remain unverified")
	}
}

// TestMemVerdicts_RoundTrips: the in-process store is what lets a verifier and a
// scorer share a verdict without either holding the other's data.
func TestMemVerdicts_RoundTrips(t *testing.T) {
	store := verification.NewMemVerdicts()
	receiptID := "0x" + strings.Repeat("bb", 32)
	verifier := agentOf(t, keyVerifier)

	h := "sha256:" + strings.Repeat("ef", 32)
	store.Record(receiptID, receipt.Verification{
		Status:         receipt.VerificationVerified,
		VerifierID:     &verifier,
		RecomputedHash: &h,
	})

	got, ok := store.Lookup(receiptID)
	if !ok {
		t.Fatal("the recorded verdict was not found")
	}
	if got.Verification.Status != receipt.VerificationVerified {
		t.Errorf("status = %q", got.Verification.Status)
	}

	if _, ok := store.Lookup("0x" + strings.Repeat("cc", 32)); ok {
		t.Error("an unrecorded receipt must not be found")
	}
}

// TestMemVerdicts_HoldsNoPoints is the A5 guard for this store: it records verdicts,
// not amounts, so it cannot become a second place points live.
func TestMemVerdicts_HoldsNoPoints(t *testing.T) {
	store := verification.NewMemVerdicts()
	receiptID := "0x" + strings.Repeat("dd", 32)

	verifier := agentOf(t, keyVerifier)
	h := "sha256:" + strings.Repeat("11", 32)
	store.Record(receiptID, receipt.Verification{
		Status:         receipt.VerificationVerified,
		VerifierID:     &verifier,
		RecomputedHash: &h,
	})

	// The store exposes Lookup and Record. Nothing should return a balance.
	methods := verification.MemVerdictsMethods()
	if len(methods) == 0 {
		t.Fatal("no methods reported; the guard would be vacuous")
	}
	for _, name := range methods {
		lower := strings.ToLower(name)
		for _, bad := range []string{"balance", "transfer", "debit", "credit", "spend", "withdraw"} {
			if strings.Contains(lower, bad) {
				t.Errorf("MemVerdicts.%s looks like a value-moving method; a verdict store must record verdicts, not amounts", name)
			}
		}
	}
}

// TestSelfCheckAdapter_IsExplicitlyNamed: the weak source must be nameable, so an
// operator reading the wiring sees that verification is not happening rather than
// assuming a nil default was harmless.
func TestSelfCheckAdapter_IsExplicitlyNamed(t *testing.T) {
	got := verification.SelfCheckAdapter{}.Describe()
	if !strings.Contains(strings.ToUpper(got), "NOT") {
		t.Errorf("Describe() = %q, should state plainly that it is not adversarial verification", got)
	}
}

// TestRecordedVerdicts_DescribesItself: the production source should say what it reads,
// so a CLI can report which source is active.
func TestRecordedVerdicts_DescribesItself(t *testing.T) {
	got := verification.RecordedVerdicts{}.Describe()
	if !strings.Contains(strings.ToLower(got), "verifier") {
		t.Errorf("Describe() = %q, should mention the verifier", got)
	}
}
