//go:build ignore

// gen_frozen_receipts writes the append-only signed-receipt corpus.
//
//	go run internal/devtools/gen_frozen_receipts.go
//
// It writes testdata/receipts/v<major>/<name>.json — one genuinely signed receipt
// per case, for the schema major it was signed under.
//
// # Why this exists (invariant A9 §① and §⑥)
//
// A9 says historical verification rules are never deleted: a receipt that was
// valid when it was signed must stay valid forever, because acceptance criterion
// ② requires a receipt to remain verifiable after every RelayFirst server is
// switched off. That guarantee is worthless if it is only asserted — it needs a
// corpus of real signatures that a test re-checks on every run.
//
// # Why the corpus is append-only
//
// The files are committed. Regenerating them would produce different bytes
// (signatures are deterministic for a fixed key, but any change to the fixture
// would silently rewrite history). The test that consumes this directory treats
// it as read-only: it fails if a file no longer verifies, which is the signal
// that a refactor broke backward compatibility.
//
// Adding a new schema major means adding a new v<major> directory, never editing
// an existing one.
//
// # Why the key is a public constant
//
// The key below is a well-known test vector, not a secret, and it never holds
// value. It is fixed so the corpus is reproducible and reviewable in a diff.
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

// frozenKey is a published test vector. Not a secret; holds no value.
const frozenKey = "0x4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"

func sha32(ch string) string { return "sha256:" + strings.Repeat(ch, 32) }

func agentID() (string, error) {
	return receipt.DeriveAgentID(frozenKey, 8453)
}

// case_ is one corpus entry: a name, whether to sign verbatim, and the receipt.
type case_ struct {
	name string

	// verbatim controls whether the payload is frozen before signing.
	//
	// Both paths must be represented. A verbatim receipt exercises S9-0b2; a
	// structural one exercises the rebuild path that every pre-existing receipt
	// uses. Without a structural entry the legacy path is pinned only by
	// same-build unit tests, which is the blind spot the corpus exists to cover
	// (finding L2).
	verbatim bool

	make func(agentID string) receipt.Receipt
}

func cases() []case_ {
	return []case_{
		{
			name:     "probe-basic",
			verbatim: true,
			make: func(a string) receipt.Receipt {
				return receipt.Receipt{
					Schema:  receipt.Schema,
					AgentID: a,
					Epoch:   42,
					Task: receipt.Task{
						Type:          receipt.TaskProbe,
						Spec:          map[string]any{"url": "https://api.example.com/health"},
						SpecHash:      sha32("3d"),
						SelfGenerated: true,
					},
					Work: receipt.Work{
						Provider:   "local",
						StartedAt:  1791015800,
						FinishedAt: 1791015862,
					},
					Result: receipt.Result{Value: "200", Hash: sha32("c1")},
					Anchors: []receipt.Anchor{{
						URL:         "https://api.example.com/health",
						ContentHash: sha32("7b"),
						FetchedAt:   1791015810,
						Status:      200,
						Bytes:       20480,
					}},
					Verification: receipt.Verification{Status: receipt.VerificationPending, Stake: 50},
				}
			},
		},
		{
			// The verbatim-payload form (S9-0b2). Its presence in the corpus
			// means the newer signing path is itself pinned by a permanent
			// fixture, not just by a unit test.
			name:     "extract-verbatim-payload",
			verbatim: true,
			make: func(a string) receipt.Receipt {
				return receipt.Receipt{
					Schema:  receipt.Schema,
					AgentID: a,
					Epoch:   43,
					Task: receipt.Task{
						Type:          receipt.TaskExtract,
						Spec:          map[string]any{"url": "https://api.example.com/ticker", "field": "price"},
						SpecHash:      sha32("5e"),
						SelfGenerated: false,
					},
					Work: receipt.Work{
						Provider:   "local",
						StartedAt:  1791102200,
						FinishedAt: 1791102260,
					},
					Result: receipt.Result{Value: "42.50", Hash: sha32("d2")},
					Anchors: []receipt.Anchor{{
						URL:         "https://api.example.com/ticker",
						ContentHash: sha32("1f"),
						FetchedAt:   1791102210,
						Status:      200,
						Bytes:       512,
					}},
					Verification: receipt.Verification{Status: receipt.VerificationPending, Stake: 50},
				}
			},
		},
		{
			// The structural path (finding L2). No payload is frozen, so the
			// verifier must rebuild the signed bytes from the structured fields —
			// which is what every receipt signed before the payload field existed
			// requires. Without this entry that path is pinned only by same-build
			// unit tests, and a regression in the rebuild would go unnoticed until
			// a user exported an old receipt.
			name:     "compute-structural",
			verbatim: false,
			make: func(a string) receipt.Receipt {
				return receipt.Receipt{
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
			},
		},
	}
}

func main() {
	a, err := agentID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "derive agent id: %v\n", err)
		os.Exit(1)
	}

	// The corpus lives under the schema major it was signed with. For now that
	// is v1; a future major adds a sibling directory rather than rewriting this.
	major, _, ok := receipt.ParseSchema(receipt.Schema)
	if !ok {
		fmt.Fprintf(os.Stderr, "cannot parse canonical schema %q\n", receipt.Schema)
		os.Exit(1)
	}
	dir := filepath.Join("testdata", "receipts", fmt.Sprintf("v%d", major))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}

	manifest := make(map[string]string, len(cases()))

	for _, c := range cases() {
		r := c.make(a)

		if c.verbatim {
			// Freeze the payload verbatim before signing. This is what a signer
			// does under the newer path, and it is what makes later additive
			// fields safe.
			payload, err := r.SignedPayload()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: payload: %v\n", c.name, err)
				os.Exit(1)
			}
			r.Payload = string(payload)
		}

		// Derive the id from the signed payload. Validate rejects a free-standing
		// id (S9-0h, finding B2), so the corpus must carry the derived one.
		id, err := r.DerivedReceiptID()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: derive receipt id: %v\n", c.name, err)
			os.Exit(1)
		}
		r.ReceiptID = id

		if err := r.Sign(frozenKey); err != nil {
			fmt.Fprintf(os.Stderr, "%s: sign: %v\n", c.name, err)
			os.Exit(1)
		}

		// Prove it verifies before writing it. A corpus entry that does not
		// verify would fail the gate immediately, so catching it here keeps the
		// failure at the generator rather than in CI.
		if err := r.Validate(nil); err != nil {
			fmt.Fprintf(os.Stderr, "%s: signed receipt does not verify: %v\n", c.name, err)
			os.Exit(1)
		}

		raw, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: marshal: %v\n", c.name, err)
			os.Exit(1)
		}
		raw = append(raw, '\n')

		path := filepath.Join(dir, c.name+".json")

		// Refuse to overwrite (finding M1).
		//
		// The corpus is append-only by design: these are historical artifacts, and
		// regenerating one would silently rewrite the very thing the compatibility
		// gate exists to protect. A developer who changed the canonical writer
		// could otherwise regenerate the corpus and make the gate pass while
		// history had been rewritten.
		//
		// Adding a schema major means adding a new v<major> directory, never
		// editing an existing file.
		if _, err := os.Stat(path); err == nil {
			fmt.Fprintf(os.Stderr,
				"%s already exists and the corpus is append-only.\n"+
					"If this is a deliberate new case, give it a new name.\n"+
					"If you are trying to make a failing compatibility test pass, that is the bug.\n",
				path)
			os.Exit(1)
		}

		if err := os.WriteFile(path, raw, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "%s: write: %v\n", c.name, err)
			os.Exit(1)
		}

		sum := sha256.Sum256(raw)
		manifest[c.name+".json"] = hex.EncodeToString(sum[:])
		fmt.Printf("wrote %s\n", path)
	}

	// The manifest (finding M1).
	//
	// The compatibility test asserts these hashes. Without them the gate only
	// checks that the current files verify — so a rewrite that also happened to
	// verify would pass, and the historical record would be gone without a trace.
	manifestPath := filepath.Join(dir, "MANIFEST.sha256")
	names := make([]string, 0, len(manifest))
	for name := range manifest {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("# SHA-256 of each frozen receipt. Append-only: never rewrite an existing entry.\n")
	b.WriteString("# Regenerate with: go run internal/devtools/gen_frozen_receipts.go (refuses to overwrite)\n")
	for _, name := range names {
		fmt.Fprintf(&b, "%s  %s\n", manifest[name], name)
	}

	if err := os.WriteFile(manifestPath, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write manifest: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", manifestPath)
}
