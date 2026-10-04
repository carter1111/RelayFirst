package receipt_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// The frozen corpus (S9-0f, invariant A9 §① and §⑥).
//
// # What this protects
//
// A9 requires that historical verification rules are never deleted, because
// acceptance criterion ② promises a receipt stays verifiable after every
// RelayFirst server is switched off. That promise is easy to state and easy to
// break silently: a refactor changes how the signed payload is rebuilt, every
// unit test still passes because they all sign and verify within the same build,
// and the only thing that breaks is the ability to verify a receipt signed by an
// *older* version. Nobody notices until a user exports an old receipt.
//
// This test is the tripwire. Each file under testdata/receipts/v<major>/ is a
// genuinely signed receipt, committed to the repository, and every run asserts it
// still verifies.
//
// # Why the corpus is append-only
//
// These files are historical artifacts. Editing one to make a test pass would
// defeat the entire purpose — it would rewrite the thing being protected. Adding
// a new schema major means adding a new v<major> directory, never touching an
// existing one.

// corpusRoot is relative to this package (internal/receipt).
const corpusRoot = "../../testdata/receipts"

// TestFrozenCorpus_StillVerifies is the A9 §① gate.
func TestFrozenCorpus_StillVerifies(t *testing.T) {
	majors, err := os.ReadDir(corpusRoot)
	if err != nil {
		t.Fatalf("read corpus root: %v", err)
	}
	if len(majors) == 0 {
		t.Fatal("the frozen corpus is empty; a missing corpus is not a passing gate")
	}

	total := 0
	for _, majorDir := range majors {
		if !majorDir.IsDir() {
			continue
		}

		major := majorDir.Name()
		dir := filepath.Join(corpusRoot, major)

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		if len(entries) == 0 {
			t.Errorf("%s contains no receipts; an empty major directory protects nothing", major)
			continue
		}

		for _, e := range entries {
			if filepath.Ext(e.Name()) != ".json" {
				continue
			}
			path := filepath.Join(dir, e.Name())

			t.Run(major+"/"+e.Name(), func(t *testing.T) {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read: %v", err)
				}

				r, err := receipt.Unmarshal(raw)
				if err != nil {
					// A decode failure here means a historical receipt can no
					// longer even be read — a backward-compatibility break.
					t.Fatalf("a frozen receipt no longer decodes: %v", err)
				}

				if err := r.ValidateStructure(); err != nil {
					t.Fatalf("a frozen receipt no longer validates structurally: %v", err)
				}

				// The signature check is the real assertion: it recomputes the
				// signed bytes. If the payload path changed, this is where it
				// surfaces.
				if err := r.Validate(nil); err != nil {
					t.Fatalf("a frozen receipt no longer verifies — backward compatibility is broken: %v", err)
				}
			})
			total++
		}
	}

	if total == 0 {
		t.Fatal("scanned zero frozen receipts; the corpus is not being exercised")
	}
	t.Logf("verified %d frozen receipt(s) across %d major version(s)", total, len(majors))
}

// TestFrozenCorpus_CoversCurrentMajor keeps the corpus honest as versions are
// added.
//
// Without this, a future schema major could be introduced with no frozen receipt
// for it, and the compatibility guarantee would silently stop applying to the
// newest version — exactly the version most likely to change.
func TestFrozenCorpus_CoversCurrentMajor(t *testing.T) {
	major, _, ok := receipt.ParseSchema(receipt.Schema)
	if !ok {
		t.Fatalf("cannot parse the canonical schema %q", receipt.Schema)
	}

	dir := filepath.Join(corpusRoot, "v"+itoa(major))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("no frozen corpus for the current major (v%d): %v\n"+
			"generate it with: go run internal/devtools/gen_frozen_receipts.go", major, err)
	}
	if len(entries) == 0 {
		t.Fatalf("frozen corpus for the current major (v%d) is empty", major)
	}
}

// TestFrozenCorpus_ContainsAVerbatimPayloadReceipt guards the S9-0b2 path
// specifically.
//
// The structural fallback path is exercised by every receipt that predates the
// payload field; without an explicit case the verbatim path could regress while
// the corpus stayed green.
func TestFrozenCorpus_ContainsAVerbatimPayloadReceipt(t *testing.T) {
	dir := filepath.Join(corpusRoot, "v1")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	found := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		r, err := receipt.Unmarshal(raw)
		if err != nil {
			continue
		}
		if r.Payload != "" {
			found++
		}
	}

	if found == 0 {
		t.Error("no frozen receipt uses the verbatim payload; the S9-0b2 path is unprotected by the corpus")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
