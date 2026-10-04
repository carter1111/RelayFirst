//go:build ignore

// gen_add_structural writes the structural corpus entry added for finding L2.
//
//	go run internal/devtools/gen_add_structural.go
//
// # Why this is separate from the main generator
//
// The main generator refuses to overwrite an existing corpus file, because the
// corpus is append-only (finding M1). That refusal makes it unusable for adding
// one entry to a corpus that already has files — which is exactly the operation
// this script performs.
//
// So this is a one-off: it writes only the new entry and then rebuilds the
// manifest from every file on disk. It is idempotent for the entry it owns and
// touches nothing else. Once the entry exists, the main generator's manifest
// step keeps it current.
//
// It exists as a file rather than a shell snippet because the receipt has to be
// genuinely signed, and the signing helpers are in this package.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

const structuralKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"

func sha32(ch string) string { return "sha256:" + strings.Repeat(ch, 32) }

func main() {
	a, err := receipt.DeriveAgentID(structuralKey, 8453)
	if err != nil {
		fmt.Fprintf(os.Stderr, "derive agent id: %v\n", err)
		os.Exit(1)
	}

	r := receipt.Receipt{
		Schema:  receipt.Schema,
		AgentID: a,
		Epoch:   44,
		Task: receipt.Task{
			Type:          receipt.TaskCompute,
			Spec:          map[string]any{"op": "sortjson", "input": `{"b":1,"a":2}`},
			SpecHash:      sha32("7a"),
			SelfGenerated: true,
		},
		Work: receipt.Work{
			Provider:   "none",
			StartedAt:  1791188600,
			FinishedAt: 1791188601,
		},
		Result: receipt.Result{Value: `{"a":2,"b":1}`, Hash: sha32("e4")},
		Anchors: []receipt.Anchor{{
			URL:         "inline",
			ContentHash: sha32("6c"),
			FetchedAt:   1791188601,
			Bytes:       13,
		}},
		Verification: receipt.Verification{Status: receipt.VerificationPending, Stake: 50},
	}

	// Deliberately no Payload: this entry must exercise the structural rebuild
	// path that every receipt signed before the payload field uses.
	id, err := r.DerivedReceiptID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "derive receipt id: %v\n", err)
		os.Exit(1)
	}
	r.ReceiptID = id

	if err := r.Sign(structuralKey); err != nil {
		fmt.Fprintf(os.Stderr, "sign: %v\n", err)
		os.Exit(1)
	}
	if err := r.Validate(nil); err != nil {
		fmt.Fprintf(os.Stderr, "signed receipt does not verify: %v\n", err)
		os.Exit(1)
	}

	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}
	raw = append(raw, '\n')

	dir := filepath.Join("testdata", "receipts", "v1")
	path := filepath.Join(dir, "compute-structural.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", path)

	// Rebuild the manifest from every file present, so it covers the new entry
	// and any pre-existing ones.
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read dir: %v\n", err)
		os.Exit(1)
	}

	manifest := map[string]string{}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", e.Name(), err)
			os.Exit(1)
		}
		sum := sha256.Sum256(b)
		manifest[e.Name()] = hex.EncodeToString(sum[:])
	}

	names := make([]string, 0, len(manifest))
	for n := range manifest {
		names = append(names, n)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("# SHA-256 of each frozen receipt. Append-only: never rewrite an existing entry.\n")
	b.WriteString("# Regenerate with: go run internal/devtools/gen_frozen_receipts.go (refuses to overwrite)\n")
	for _, n := range names {
		fmt.Fprintf(&b, "%s  %s\n", manifest[n], n)
	}

	mp := filepath.Join(dir, "MANIFEST.sha256")
	if err := os.WriteFile(mp, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write manifest: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d entries)\n", mp, len(names))
}
