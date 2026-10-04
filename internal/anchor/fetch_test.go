package anchor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

func pinnedFetcher() *Fetcher {
	f := NewFetcher()
	f.Now = func() time.Time { return time.Unix(1791015800, 0) }
	return f
}

func TestCapture_RecordsHashAndMetadata(t *testing.T) {
	const body = `{"ok":true}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	f := pinnedFetcher()
	a, got, err := f.Capture(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if string(got) != body {
		t.Errorf("body = %q, want %q", got, body)
	}
	if a.ContentHash != HashString(body) {
		t.Errorf("contentHash = %s, want %s", a.ContentHash, HashString(body))
	}
	if a.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", a.Status)
	}
	if a.Bytes != uint64(len(body)) {
		t.Errorf("bytes = %d, want %d", a.Bytes, len(body))
	}
	if a.FetchedAt != 1791015800 {
		t.Errorf("fetchedAt = %d, want the pinned clock", a.FetchedAt)
	}
	if !strings.HasPrefix(a.ContentHash, "sha256:") {
		t.Errorf("contentHash must carry the sha256: prefix, got %s", a.ContentHash)
	}
}

// TestCapture_HashIsStable: the same content must always hash the same, which
// is what makes re-fetch verification meaningful.
func TestCapture_HashIsStable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("constant"))
	}))
	defer srv.Close()

	f := pinnedFetcher()

	var first string
	for i := 0; i < 5; i++ {
		a, _, err := f.Capture(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("Capture: %v", err)
		}
		if i == 0 {
			first = a.ContentHash
			continue
		}
		if a.ContentHash != first {
			t.Fatalf("hash drifted at iteration %d: %s vs %s", i, a.ContentHash, first)
		}
	}
}

func TestVerify_DetectsChangedContent(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	f := pinnedFetcher()

	body = "v1"
	a, _, err := f.Capture(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	// Unchanged: verification succeeds.
	ok, err := f.Verify(context.Background(), a)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Error("unchanged content must verify")
	}

	// Content updates: verification reports a mismatch (not an error). This is
	// expected behaviour once the epoch window passes — MVP.md §5.4.
	body = "v2"
	ok, err = f.Verify(context.Background(), a)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Error("changed content must NOT verify")
	}
}

func TestCapture_EmptyURLRejected(t *testing.T) {
	f := pinnedFetcher()
	for _, u := range []string{"", "   "} {
		if _, _, err := f.Capture(context.Background(), u); err == nil {
			t.Errorf("expected an error for url %q", u)
		}
	}
}

func TestCapture_TransportFailureIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	if _, _, err := pinnedFetcher().Capture(context.Background(), url); err == nil {
		t.Error("expected an error for an unreachable URL")
	}
}

// TestCapture_EnforcesByteLimit: anchors must stay small, and the limit must
// fail loudly rather than silently truncating (which would corrupt the hash).
func TestCapture_EnforcesByteLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
	}))
	defer srv.Close()

	f := pinnedFetcher()
	f.MaxBytes = 1024

	_, _, err := f.Capture(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected ErrTooLarge")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCapture_Non2xxIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	a, body, err := pinnedFetcher().Capture(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("non-2xx must not be an error: %v", err)
	}
	if a.Status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", a.Status)
	}
	// The error body is still captured: it is part of the observable result.
	if string(body) != "boom" {
		t.Errorf("body = %q, want %q", body, "boom")
	}
}

// TestHashHex_FormatMatchesReceiptValidator pins the hash wire format. The
// receipt validator expects "sha256:" + 32 bytes; if this changes, receipts
// produced here stop validating.
func TestHashHex_FormatMatchesReceiptValidator(t *testing.T) {
	h := HashString("anything")

	if !strings.HasPrefix(h, "sha256:") {
		t.Fatalf("hash must start with sha256:, got %s", h)
	}
	// 7 ("sha256:") + 64 hex chars for a 32-byte digest.
	if len(h) != 71 {
		t.Errorf("hash length = %d, want 71", len(h))
	}

	// The validator accepts it.
	a := receipt.Anchor{
		URL:         "https://example.com",
		ContentHash: h,
		FetchedAt:   1791015800,
		Status:      200,
	}
	r := receipt.Receipt{
		Schema:  receipt.Schema,
		AgentID: "agent:eip155:8453:0x0000000000000000000000000000000000000001",
		Epoch:   1,
		Task: receipt.Task{
			Type:          receipt.TaskProbe,
			Spec:          map[string]any{"url": "https://example.com"},
			SpecHash:      h,
			SelfGenerated: true,
		},
		Result:  receipt.Result{Value: "200", Hash: h},
		Anchors: []receipt.Anchor{a},
	}
	derived, err := r.DerivedReceiptID()
	if err != nil {
		t.Fatalf("DerivedReceiptID: %v", err)
	}
	r.ReceiptID = derived

	if err := r.ValidateStructure(); err != nil {
		t.Errorf("anchor produced here must satisfy the receipt validator: %v", err)
	}
}
