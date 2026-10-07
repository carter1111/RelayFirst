package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/store"
)

func seededClaimDB(t *testing.T) (*store.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "s.db")
	seedSettledWork(t, dbPath, 7)
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, "agent:eip155:8453:0x70997970c51812dc3a010c7d01b50e0d17dc79c8"
}

// TestClaimWeb_RequiresTheTicket: without the one-time ticket the view is forbidden, so a
// local process that did not mint it cannot read the claim.
func TestClaimWeb_RequiresTheTicket(t *testing.T) {
	db, _ := seededClaimDB(t)
	w, err := newClaimWeb(db, time.Minute)
	if err != nil {
		t.Fatalf("newClaimWeb: %v", err)
	}
	h := w.handler()

	for _, url := range []string{"/", "/?t=wrong", "/?t="} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s = %d, want 403 without the ticket", url, rec.Code)
		}
	}
}

// TestClaimWeb_ServesReadOnlyHTMLWithTheTicket: with the ticket, the page shows the
// claimable total as read-only HTML, and carries no script.
func TestClaimWeb_ServesReadOnlyHTMLWithTheTicket(t *testing.T) {
	db, agent := seededClaimDB(t)
	w, err := newClaimWeb(db, time.Minute)
	if err != nil {
		t.Fatalf("newClaimWeb: %v", err)
	}

	rec := httptest.NewRecorder()
	h := w.handler()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?t="+w.tokenValue()+"&epoch=7&agent="+agent, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("authorized GET = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "claim proof") {
		t.Errorf("the page must render the claim view, got:\n%s", body)
	}
	// No script: the page must not be able to fetch, sign or exfiltrate.
	if strings.Contains(body, "<script") {
		t.Error("the claim page must contain no script")
	}
	// The claimable total is shown (a positive number appears).
	if !strings.Contains(body, "claimable through epoch") {
		t.Errorf("the page must show the claimable total, got:\n%s", body)
	}
}

// TestClaimWeb_ExpiredTicketIsForbidden: a ticket is a capability, and it must not outlive
// the conversation that created it.
func TestClaimWeb_ExpiredTicketIsForbidden(t *testing.T) {
	db, agent := seededClaimDB(t)
	w, err := newClaimWeb(db, time.Minute)
	if err != nil {
		t.Fatalf("newClaimWeb: %v", err)
	}
	w.expires = time.Now().Add(-time.Second) // force expiry

	rec := httptest.NewRecorder()
	w.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?t="+w.tokenValue()+"&agent="+agent, nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("an expired ticket must be forbidden, got %d", rec.Code)
	}
}

// TestClaimWeb_TicketIsRandom: two views must not share a ticket, or one link would open
// another session's view.
func TestClaimWeb_TicketIsRandom(t *testing.T) {
	db, _ := seededClaimDB(t)
	a, _ := newClaimWeb(db, time.Minute)
	b, _ := newClaimWeb(db, time.Minute)
	if a.tokenValue() == b.tokenValue() {
		t.Error("two claim views must not share a ticket")
	}
	if len(a.tokenValue()) != 32 { // 16 bytes hex
		t.Errorf("ticket = %q, want 32 hex chars", a.tokenValue())
	}
}

func TestSubtleConstantTimeEqual(t *testing.T) {
	if !subtleConstantTimeEqual("abc", "abc") {
		t.Error("equal strings must compare equal")
	}
	if subtleConstantTimeEqual("abc", "abd") || subtleConstantTimeEqual("abc", "ab") {
		t.Error("different strings must compare unequal")
	}
}
