package mining

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// These tests cover the anchor consistency check, which is the part of verification that
// answers "does the evidence still describe the source?" rather than "does the task still
// produce this?".
//
// It exists because a probe receipt records a status code *and* a content hash, and the
// content hash is inside the signed payload. A verifier that only re-ran the task would
// accept a signed claim about content it never checked.

// stubFetcher returns a fixed anchor, or an error.
type stubFetcher struct {
	anchor receipt.Anchor
	err    error
	calls  int
}

func (s *stubFetcher) Capture(_ context.Context, url string) (receipt.Anchor, []byte, error) {
	s.calls++
	if s.err != nil {
		return receipt.Anchor{}, nil, s.err
	}
	a := s.anchor
	a.URL = url
	return a, []byte("body"), nil
}

func anchorReceipt(id, url, cHash string) *receipt.Receipt {
	r := &receipt.Receipt{
		AgentID: "agent:eip155:8453:0x0000000000000000000000000000000000000001",
		Epoch:   1,
		Anchors: []receipt.Anchor{{URL: url, ContentHash: cHash, Status: 200, Bytes: 4}},
	}
	// Derive the id from the payload (S9-0h, finding B2). `id` still distinguishes
	// fixtures via the anchor URL above.
	r.ReceiptID = derivedID(r)
	return r
}

// derivedID derives a receipt id, panicking on an impossible error.
func derivedID(r *receipt.Receipt) string {
	id, err := r.DerivedReceiptID()
	if err != nil {
		panic(err)
	}
	return id
}

// TestAnchorConsistency_AcceptsMatchingEvidence is the honest case.
func TestAnchorConsistency_AcceptsMatchingEvidence(t *testing.T) {
	r := anchorReceipt("0x1", "https://example.com/a", "sha256:"+strings.Repeat("aa", 32))

	f := &stubFetcher{anchor: receipt.Anchor{ContentHash: "sha256:" + strings.Repeat("aa", 32)}}
	c := NewAnchorConsistency(f)

	if err := c.Check(context.Background(), r); err != nil {
		t.Fatalf("matching evidence must pass: %v", err)
	}
	if f.calls != 1 {
		t.Errorf("fetcher called %d times, want 1", f.calls)
	}
}

// TestAnchorConsistency_RejectsDriftedContent is the reason the check exists: the source now
// returns different bytes than the receipt recorded.
func TestAnchorConsistency_RejectsDriftedContent(t *testing.T) {
	r := anchorReceipt("0x2", "https://example.com/b", "sha256:"+strings.Repeat("bb", 32))

	// The source now returns something else.
	f := &stubFetcher{anchor: receipt.Anchor{ContentHash: "sha256:" + strings.Repeat("cc", 32)}}
	c := NewAnchorConsistency(f)

	err := c.Check(context.Background(), r)
	if err == nil {
		t.Fatal("drifted content must be rejected")
	}
	// The message must name both hashes, so an operator can tell drift from a bug.
	if !strings.Contains(err.Error(), strings.Repeat("bb", 6)) {
		t.Errorf("the error should show the recorded hash, got %q", err)
	}
	if !strings.Contains(err.Error(), strings.Repeat("cc", 6)) {
		t.Errorf("the error should show the current hash, got %q", err)
	}
}

// TestAnchorConsistency_UnreachableSourceIsNotAPass: treating an unreachable source as
// agreement would make the check avoidable by taking the source offline.
func TestAnchorConsistency_UnreachableSourceIsNotAPass(t *testing.T) {
	r := anchorReceipt("0x3", "https://example.com/c", "sha256:"+strings.Repeat("dd", 32))

	f := &stubFetcher{err: errors.New("connection refused")}
	c := NewAnchorConsistency(f)

	if err := c.Check(context.Background(), r); err == nil {
		t.Error("an unreachable source must not be treated as agreement")
	}
}

// TestAnchorConsistency_SkipsSyntheticAnchors: compute tasks have no external evidence, so
// there is nothing to re-fetch.
func TestAnchorConsistency_SkipsSyntheticAnchors(t *testing.T) {
	r := anchorReceipt("0x4", "inline", "sha256:"+strings.Repeat("ee", 32))

	f := &stubFetcher{anchor: receipt.Anchor{ContentHash: "sha256:" + strings.Repeat("ff", 32)}}
	c := NewAnchorConsistency(f)

	// A receipt whose only anchor is synthetic has nothing checkable, so the check must say
	// so rather than silently reporting success — "no error" must not be read as "the
	// evidence holds".
	err := c.Check(context.Background(), r)
	if err == nil {
		t.Fatal("a receipt with no fetchable anchor must not report success")
	}
	if f.calls != 0 {
		t.Errorf("the fetcher should not have been called for a synthetic anchor, called %d times", f.calls)
	}
	if !strings.Contains(err.Error(), "nothing could be checked") {
		t.Errorf("the error should explain that nothing was checked, got %q", err)
	}
}

// TestAnchorConsistency_ChecksEveryFetchableAnchor: a receipt can carry several anchors, and
// checking only the first would let a forged second one through.
func TestAnchorConsistency_ChecksEveryFetchableAnchor(t *testing.T) {
	good := "sha256:" + strings.Repeat("11", 32)

	r := &receipt.Receipt{
		ReceiptID: "0x5",
		Anchors: []receipt.Anchor{
			{URL: "https://example.com/first", ContentHash: good, Status: 200},
			{URL: "https://example.com/second", ContentHash: "sha256:" + strings.Repeat("22", 32), Status: 200},
		},
	}

	// The stub always returns the good hash, so the second anchor's recorded hash cannot
	// match it.
	f := &stubFetcher{anchor: receipt.Anchor{ContentHash: good}}
	c := NewAnchorConsistency(f)

	if err := c.Check(context.Background(), r); err == nil {
		t.Error("a later anchor that does not match must be caught")
	}
	if f.calls != 2 {
		t.Errorf("fetcher called %d times, want 2 — every anchor must be checked", f.calls)
	}
}

func TestAnchorConsistency_RejectsNilReceiptAndNoFetcher(t *testing.T) {
	if err := NewAnchorConsistency(&stubFetcher{}).Check(context.Background(), nil); err == nil {
		t.Error("a nil receipt must be refused")
	}
	if err := (&AnchorConsistency{}).Check(context.Background(), anchorReceipt("0x6", "https://e.com", "sha256:"+strings.Repeat("33", 32))); err == nil {
		t.Error("a checker with no fetcher must be refused rather than silently passing")
	}
}

func TestShortHash(t *testing.T) {
	long := "sha256:" + strings.Repeat("ab", 32)
	got := shortHash(long)
	if len(got) >= len(long) {
		t.Errorf("shortHash should shorten, got %q", got)
	}
	if !strings.HasPrefix(got, "sha256:") {
		t.Errorf("shortHash should keep the algorithm prefix, got %q", got)
	}
	if shortHash("short") != "short" {
		t.Error("a short hash should pass through")
	}
}
