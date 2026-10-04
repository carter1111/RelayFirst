package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// ExportReceipts writes each receipt's canonical bytes into dir as
// <receiptId>.json, returning how many were written.
//
// # Why this lives here rather than in the CLI
//
// "I can take my contribution with me" is the basis of trust in a system whose
// whole claim is that it does not need to be trusted (MVP.md §9.2), so the export
// path deserves a test. A function inside package main cannot be tested from
// another package, and an untested export is the kind of code that silently stops
// producing verifiable files — which a user would only discover after they needed
// them.
//
// # Why the canonical bytes
//
// The signature covers a canonical serialization of the receipt. Writing any other
// encoding would produce a file that looks right and fails verification, so this
// writes exactly the bytes the verifier expects.
//
// # Idempotency
//
// Files are named by receipt id, so re-running an export overwrites the same files
// rather than accumulating duplicates. A receipt is immutable once signed, so an
// overwrite is always the same content.
func ExportReceipts(dir string, receipts []*receipt.Receipt) (int, error) {
	if dir == "" {
		return 0, fmt.Errorf("store: export directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("store: create export directory: %w", err)
	}

	written := 0
	for _, r := range receipts {
		if r == nil {
			continue
		}
		if r.ReceiptID == "" {
			return written, fmt.Errorf("store: refusing to export a receipt with no id")
		}

		body, err := r.MarshalCanonical()
		if err != nil {
			return written, fmt.Errorf("store: marshal receipt %s: %w", r.ReceiptID, err)
		}

		path := filepath.Join(dir, r.ReceiptID+".json")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return written, fmt.Errorf("store: write %s: %w", path, err)
		}
		written++
	}
	return written, nil
}
