package receipt_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
// still verifies — under the rules of the major it names, not under the current
// build's rules (finding M2).
//
// # Why the corpus is append-only
//
// These files are historical artifacts. Editing one to make a test pass would
// defeat the entire purpose — it would rewrite the thing being protected. Adding
// a new schema major means adding a new v<major> directory, never touching an
// existing one. The manifest asserted below is what makes that enforceable rather
// than merely intended (finding M1).

// corpusRoot is relative to this package (internal/receipt).
const corpusRoot = "../../testdata/receipts"

// manifestName is the per-major hash manifest.
const manifestName = "MANIFEST.sha256"

// TestFrozenCorpus_MatchesManifest is the M1 gate.
//
// The compatibility test only checks that the committed files verify. That is not
// enough on its own: a developer who changed the canonical writer could regenerate
// the corpus and commit the result, the gate would pass, and the historical record
// would have been silently rewritten — which is the one thing the corpus exists to
// prevent.
//
// Asserting a committed manifest closes that: rewriting a receipt now changes its
// hash and fails here, whatever the generator does.
func TestFrozenCorpus_MatchesManifest(t *testing.T) {
	majors, err := os.ReadDir(corpusRoot)
	if err != nil {
		t.Fatalf("read corpus root: %v", err)
	}

	checked := 0
	for _, majorDir := range majors {
		if !majorDir.IsDir() {
			continue
		}
		dir := filepath.Join(corpusRoot, majorDir.Name())

		manifest, err := readManifest(filepath.Join(dir, manifestName))
		if err != nil {
			t.Errorf("%s: %v\n"+
				"every major directory needs a manifest, or rewrites go undetected", majorDir.Name(), err)
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}

		seen := map[string]bool{}
		for _, e := range entries {
			if filepath.Ext(e.Name()) != ".json" {
				continue
			}
			seen[e.Name()] = true

			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			sum := sha256.Sum256(raw)
			got := hex.EncodeToString(sum[:])

			want, ok := manifest[e.Name()]
			if !ok {
				t.Errorf("%s/%s is not in the manifest; add it deliberately rather than by regeneration",
					majorDir.Name(), e.Name())
				continue
			}
			if got != want {
				t.Errorf("%s/%s has been rewritten (hash %s, manifest says %s).\n"+
					"The corpus is append-only: a changed receipt is a broken compatibility promise, not a stale file.",
					majorDir.Name(), e.Name(), got, want)
			}
			checked++
		}

		// A manifest entry with no file means an artifact was deleted, which is
		// the same class of problem in the other direction.
		for name := range manifest {
			if !seen[name] {
				t.Errorf("%s/%s is in the manifest but missing from the corpus; "+
					"deleting a historical receipt is not a way to fix a failing check", majorDir.Name(), name)
			}
		}
	}

	if checked == 0 {
		t.Fatal("verified zero manifest entries; the manifest is not being exercised")
	}
	t.Logf("manifest verified for %d receipt(s)", checked)
}

// TestFrozenCorpus_StillVerifies is the A9 §① gate.
//
// Each entry is validated under the rules of the major its directory names
// (finding M2). Validating everything under the current build's rules would pin
// the weaker property "a v1 receipt verifies under today's rules", which cannot
// detect a change that wrongly applied new rules to old artifacts.
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

		majorName := majorDir.Name()
		dir := filepath.Join(corpusRoot, majorName)

		// The directory name carries the ruleset. A directory this build cannot
		// map to a major is a defect: it would mean a corpus entry no verifier can
		// check.
		major, err := majorFromDirName(majorName)
		if err != nil {
			t.Errorf("%s: %v", majorName, err)
			continue
		}
		if !receipt.IsSupportedMajor(major) {
			t.Errorf("%s names a major this build does not support; "+
				"a frozen receipt must stay checkable by the build that ships it", majorName)
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		if len(entries) == 0 {
			t.Errorf("%s contains no receipts; an empty major directory protects nothing", majorName)
			continue
		}

		for _, e := range entries {
			if filepath.Ext(e.Name()) != ".json" {
				continue
			}
			path := filepath.Join(dir, e.Name())

			t.Run(majorName+"/"+e.Name(), func(t *testing.T) {
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

				// Validate under the major the directory names, so the check is
				// "v1 rules still accept a v1 receipt" rather than "today's rules
				// happen to accept it".
				if err := r.ValidateForMajor(major, nil); err != nil {
					t.Fatalf("a frozen receipt no longer verifies under v%d rules — "+
						"backward compatibility is broken: %v", major, err)
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

	dir := filepath.Join(corpusRoot, fmt.Sprintf("v%d", major))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("no frozen corpus for the current major (v%d): %v\n"+
			"generate it with: go run internal/devtools/gen_frozen_receipts.go", major, err)
	}
	if len(entries) == 0 {
		t.Fatalf("frozen corpus for the current major (v%d) is empty", major)
	}
}

// TestFrozenCorpus_ContainsBothPayloadForms pins both signing paths.
//
// The verbatim path (S9-0b2) and the structural rebuild path must each have a
// permanent fixture. Without the structural entry, the path that every
// pre-existing receipt uses is pinned only by same-build unit tests — the exact
// blind spot the corpus exists to cover (finding L2).
func TestFrozenCorpus_ContainsBothPayloadForms(t *testing.T) {
	dir := filepath.Join(corpusRoot, "v1")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	verbatim, structural := 0, 0
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
			verbatim++
		} else {
			structural++
		}
	}

	if verbatim == 0 {
		t.Error("no frozen receipt uses a verbatim payload; the S9-0b2 path is unprotected by the corpus")
	}
	if structural == 0 {
		t.Error("no frozen receipt uses the structural rebuild path; " +
			"that is the path every pre-existing receipt depends on, and it is currently unprotected by the corpus")
	}
}

// readManifest parses a MANIFEST.sha256 file into name -> hash.
//
// The format is the sha256sum convention: "<hex>  <name>", with # comments.
func readManifest(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("malformed manifest line: %q", line)
		}
		out[fields[1]] = fields[0]
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("manifest %s is empty", path)
	}
	return out, nil
}

// majorFromDirName parses a corpus directory name such as "v1" into a major.
func majorFromDirName(name string) (int, error) {
	if !strings.HasPrefix(name, "v") {
		return 0, fmt.Errorf("directory %q must be named v<major>", name)
	}
	var major int
	if _, err := fmt.Sscanf(name, "v%d", &major); err != nil {
		return 0, fmt.Errorf("directory %q is not v<major>: %w", name, err)
	}
	return major, nil
}
