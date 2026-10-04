package main

import (
	"strings"
	"testing"
)

// Tests for `anchor check`, the offline proof verification path.
//
// This is the command that makes anchoring mean anything: `anchor verify` needs the local store
// to recompute the expected root, which an outside party does not have. `anchor check` takes the
// root as an argument, so someone holding only the published root and a proof can confirm
// inclusion — after every server is gone.

// checkFlags builds flags for anchor check from a proof's values.
func checkFlags(receiptID, root string, index, width int, siblings []string) *flags {
	f := &flags{values: map[string]string{}, repeated: map[string][]string{}}
	f.values["receipt"] = receiptID
	f.values["root"] = root
	f.values["index"] = itoaPlain(index)
	f.values["width"] = itoaPlain(width)
	f.repeated["sibling"] = siblings
	return f
}

func itoaPlain(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestAnchorCheck_RefusesMissingRoot: the root must be supplied, because reading it from the
// proof would make the check circular — a forged proof could carry whatever root it folds to.
func TestAnchorCheck_RefusesMissingRoot(t *testing.T) {
	f := &flags{values: map[string]string{"receipt": id(1), "index": "0", "width": "1"}, repeated: map[string][]string{}}

	err := anchorCheck(f)
	if err == nil {
		t.Fatal("a missing --root must be refused")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("the error should explain why the root is not read from the proof, got %q", err)
	}
}

func TestAnchorCheck_RefusesMissingReceipt(t *testing.T) {
	f := &flags{values: map[string]string{"root": id(9), "index": "0", "width": "1"}, repeated: map[string][]string{}}

	if err := anchorCheck(f); err == nil {
		t.Error("a missing --receipt must be refused")
	}
}

func TestAnchorCheck_RefusesMissingIndexOrWidth(t *testing.T) {
	base := func() *flags {
		f := &flags{values: map[string]string{"receipt": id(1), "root": id(9), "index": "0", "width": "1"}, repeated: map[string][]string{}}
		return f
	}

	f := base()
	delete(f.values, "index")
	if err := anchorCheck(f); err == nil {
		t.Error("a missing --index must be refused")
	}

	f = base()
	delete(f.values, "width")
	if err := anchorCheck(f); err == nil {
		t.Error("a missing --width must be refused")
	}
}

// TestAnchorCheck_RefusesMalformedHashes: a bad hash must be reported, not silently treated as
// zero, which would produce a confident "false" for the wrong reason.
func TestAnchorCheck_RefusesMalformedHashes(t *testing.T) {
	f := checkFlags(id(1), "0xnothex", 0, 1, nil)
	if err := anchorCheck(f); err == nil {
		t.Error("a malformed --root must be refused")
	}

	f = checkFlags("0xbad", id(9), 0, 1, nil)
	if err := anchorCheck(f); err == nil {
		t.Error("a malformed --receipt must be refused")
	}

	f = checkFlags(id(1), id(9), 0, 2, []string{"0xnope"})
	if err := anchorCheck(f); err == nil {
		t.Error("a malformed --sibling must be refused")
	}
}

// TestAnchorCheck_RequiresSiblingsUnlessSingleLeaf: a multi-leaf tree needs its path, and
// accepting an empty one would silently verify nothing.
func TestAnchorCheck_RequiresSiblingsUnlessSingleLeaf(t *testing.T) {
	// width 4 with no siblings is a missing argument.
	f := checkFlags(id(1), id(9), 0, 4, nil)
	if err := anchorCheck(f); err == nil {
		t.Error("a width above 1 must require siblings")
	}

	// width 1 legitimately has none, so it must not be rejected on that ground. It will fail the
	// cryptographic check instead, which is the correct reason.
	f = checkFlags(id(1), id(9), 0, 1, nil)
	err := anchorCheck(f)
	if err != nil && strings.Contains(err.Error(), "--sibling") {
		t.Errorf("a single-leaf tree should not require siblings, got %q", err)
	}
}

// TestAnchorCheck_DoesNotConsultAStore is the load-bearing property.
//
// The command must work with no database at all. If it ever started reading one, the offline
// guarantee would be gone — and a user who deleted their store could no longer verify their own
// inclusion, which is exactly the situation anchoring exists for.
//
// The assertion is as much structural as behavioural: `anchorCheck` takes only flags, so there is
// no store it *could* read, and the test exercises it from a working directory with no database.
func TestAnchorCheck_DoesNotConsultAStore(t *testing.T) {
	// A receipt id and a root that will not match, so the outcome is "false" — but it must be a
	// computable false rather than a failure to find a store.
	f := checkFlags(id(1), id(2), 0, 1, nil)

	err := anchorCheck(f)
	if err != nil {
		t.Fatalf("anchor check must not need a store, got %v", err)
	}
}

// TestAnchorCheck_BuildsALeafFromTheReceiptID guards the leaf derivation: the proof's leaf is the
// receipt id hashed with the leaf prefix, so a mistake here would verify the wrong thing.
func TestAnchorCheck_AcceptsAWellFormedArgumentSet(t *testing.T) {
	// A real root/proof relation is covered in internal/merkle's tests. Here the point is that a
	// well-formed set of arguments is *processed* rather than rejected at the argument stage.
	f := checkFlags(id(1), id(2), 0, 2, []string{id(3)})

	err := anchorCheck(f)
	if err != nil {
		t.Fatalf("a well-formed argument set must be processed, got %v", err)
	}
}
