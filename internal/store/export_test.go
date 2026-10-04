package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/store"
)

// TestExportReceipts_FilesVerifyOffline is the S6-6 acceptance property, asserted
// against the files on disk rather than in memory.
//
// "I can take my contribution with me" is the basis of trust here (MVP.md §9.2), and
// a user only discovers a broken export after they have already relied on it. So the
// check re-reads each written file and verifies the signature.
func TestExportReceipts_FilesVerifyOffline(t *testing.T) {
	receipts := []*receipt.Receipt{
		mkStoredReceipt(t, storeTestKey1, "https://a.example", expCh(0x600), "0x"+strings.Repeat("a1", 32), 1791015800),
		mkStoredReceipt(t, storeTestKey1, "https://b.example", expCh(0x601), "0x"+strings.Repeat("a2", 32), 1791015801),
		mkStoredReceipt(t, storeTestKey1, "https://c.example", expCh(0x602), "0x"+strings.Repeat("a3", 32), 1791015802),
	}

	dir := filepath.Join(t.TempDir(), "export")
	written, err := store.ExportReceipts(dir, receipts)
	if err != nil {
		t.Fatalf("ExportReceipts: %v", err)
	}
	if written != len(receipts) {
		t.Fatalf("wrote %d files, want %d", written, len(receipts))
	}

	for _, r := range receipts {
		path := filepath.Join(dir, r.ReceiptID+".json")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", path, err)
		}

		back, err := receipt.Unmarshal(raw)
		if err != nil {
			t.Fatalf("the exported file %s does not parse: %v", path, err)
		}
		if err := back.Validate(nil); err != nil {
			t.Errorf("the exported file %s does not verify: %v", path, err)
		}
	}
}

// TestExportReceipts_NamesFilesByReceiptID: naming by id makes a re-export
// idempotent instead of accumulating duplicates.
func TestExportReceipts_NamesFilesByReceiptID(t *testing.T) {
	r := mkStoredReceipt(t, storeTestKey1, "https://a.example", expCh(0x700), "0x"+strings.Repeat("b1", 32), 1791015800)

	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if _, err := store.ExportReceipts(dir, []*receipt.Receipt{r}); err != nil {
			t.Fatalf("ExportReceipts %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d file(s), want 1 — a re-export must not duplicate", len(entries))
	}
	if entries[0].Name() != r.ReceiptID+".json" {
		t.Errorf("file named %q, want %q", entries[0].Name(), r.ReceiptID+".json")
	}
}

func TestExportReceipts_EmptyListIsFine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty")

	written, err := store.ExportReceipts(dir, nil)
	if err != nil {
		t.Fatalf("ExportReceipts: %v", err)
	}
	if written != 0 {
		t.Errorf("wrote %d, want 0", written)
	}
	// The directory should still exist, so a caller's next step does not fail on a
	// missing path.
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("the export directory should exist even for an empty export: %v", err)
	}
}

func TestExportReceipts_RejectsEmptyDir(t *testing.T) {
	if _, err := store.ExportReceipts("", []*receipt.Receipt{}); err == nil {
		t.Error("an empty directory must be rejected rather than writing into the cwd")
	}
}

// TestExportReceipts_RejectsUnidentifiedReceipt: a receipt with no id would produce
// a file named ".json", which silently collides across receipts.
func TestExportReceipts_RejectsUnidentifiedReceipt(t *testing.T) {
	r := mkStoredReceipt(t, storeTestKey1, "https://a.example", expCh(0x800), "0x"+strings.Repeat("c1", 32), 1791015800)
	r.ReceiptID = ""

	if _, err := store.ExportReceipts(t.TempDir(), []*receipt.Receipt{r}); err == nil {
		t.Error("a receipt with no id must be rejected")
	}
}

// TestExportReceipts_SkipsNilEntries: a nil in the slice is not worth failing the
// whole export over, but it must not write a file.
func TestExportReceipts_SkipsNilEntries(t *testing.T) {
	r := mkStoredReceipt(t, storeTestKey1, "https://a.example", expCh(0x900), "0x"+strings.Repeat("d1", 32), 1791015800)

	dir := t.TempDir()
	written, err := store.ExportReceipts(dir, []*receipt.Receipt{nil, r, nil})
	if err != nil {
		t.Fatalf("ExportReceipts: %v", err)
	}
	if written != 1 {
		t.Errorf("wrote %d, want 1", written)
	}
}
