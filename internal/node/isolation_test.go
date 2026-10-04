package node_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNodeBinaryCannotVerifySignatures enforces the architectural claim in
// MVP.md §7.1 as a test rather than as a comment.
//
// # Why this is a test and not a code review note
//
// A "dumb" node is not a style preference. Verification happens on the client
// (§5.4), and that is what stops a hostile node from being able to forge work. If
// the node could link eip712, then one future edit could start accepting a receipt
// because its signature verified — which would quietly move a trust decision into
// the least trustworthy component in the system.
//
// This was not hypothetical. An earlier draft of S5 kept the receipt-wrapping
// helpers in internal/protocol while internal/node imported protocol, so
// `go list -deps` showed the node binary linking secp256k1 and eip712. The handler
// happened not to verify anything, and nothing structural prevented it from
// starting to. Splitting the format from the receipt adapters fixed it; this test
// keeps it fixed.
//
// # Why the binary, not the package
//
// Transitive dependencies are the whole problem, so checking the package's own
// import list would miss it. The compiled binary's dependency closure is what
// actually ships in the container image.
func TestNodeBinaryCannotVerifySignatures(t *testing.T) {
	if testing.Short() {
		t.Skip("go list requires the toolchain; skipped under -short")
	}

	repoRoot := "../.."

	// Forbidden: anything that can verify a signature, mine, or call a model. The
	// node is a public server image, so none of it belongs there.
	forbidden := []string{
		"internal/eip712",
		"internal/receipt",
		"secp256k1",
		"internal/mining",
		"internal/llm",
	}

	cmd := exec.Command("go", "list", "-deps", "./cmd/relayfirst-node")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}

	deps := string(out)
	var found []string
	for _, f := range forbidden {
		if strings.Contains(deps, f) {
			found = append(found, f)
		}
	}

	if len(found) > 0 {
		t.Errorf("the relayfirst-node binary links packages it must not:\n  %s\n\n"+
			"The node is store-and-forward only (MVP.md §7.1); it must not be able to verify\n"+
			"a signature, mine, or call a model. A transitive dependency is the usual cause —\n"+
			"check for a shared package that imports internal/receipt.",
			strings.Join(found, "\n  "))
	}
}

// TestNodePackageDoesNotImportReceipt is the narrower, faster guard: even the node
// package's direct and transitive deps must stay clear of signing code.
//
// It is kept separate from the binary check so a failure points at the layer that
// introduced the dependency rather than only at the final image.
func TestNodePackageDoesNotImportReceipt(t *testing.T) {
	if testing.Short() {
		t.Skip("go list requires the toolchain; skipped under -short")
	}

	cmd := exec.Command("go", "list", "-deps", "./internal/node")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}

	for _, forbidden := range []string{"internal/eip712", "internal/receipt", "secp256k1"} {
		if strings.Contains(string(out), forbidden) {
			t.Errorf("internal/node transitively depends on %q; the node must not be able to verify signatures", forbidden)
		}
	}
}
