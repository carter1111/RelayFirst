package receipt

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Schema versioning (S9-0a, invariant A9 — see MVP.md §17.4).
//
// # Why this is not just a string comparison
//
// The previous check was `r.Schema != Schema`, which has two problems that only
// appear once other people run this code:
//
//  1. It rejects `relayfirst.receipt.v1.1` — an *additive* change that an old
//     verifier is perfectly capable of validating, because the fields it knows
//     about are all still present and unchanged.
//  2. It reports that rejection as "invalid", which is wrong and actively
//     misleading: "invalid" means "I checked and this is broken", while the
//     honest answer is "this is a version I do not know how to check".
//     Conflating the two makes a verifier that is merely out of date look like
//     it caught a forgery. That is the failure mode this file exists to prevent.
//
// The distinction is carried by two separate error types, not by an error
// string, so a caller cannot accidentally collapse them.
//
// # The grammar
//
//	relayfirst.receipt.v<major>[.<minor>]
//
// A missing minor means zero, so `relayfirst.receipt.v1` and
// `relayfirst.receipt.v1.0` are the same version. The existing published string
// is `v1` and must be accepted forever; that is what Schema below still holds.

// schemaPrefix is the fixed namespace before the version. Schema (declared in
// receipt.go) is the canonical v1 identifier and is deliberately left unchanged:
// every already-signed receipt carries that exact string, and A9 forbids
// breaking their verification. New versions are added to supportedMajors below,
// not by rewriting Schema.
const schemaPrefix = "relayfirst.receipt.v"

// supportedMajors lists the major versions this build can validate.
//
// A9 §① requires that entries are never removed: dropping one would silently
// invalidate receipts that were valid when they were signed. Add to this list;
// do not subtract from it.
var supportedMajors = []int{1}

// ValidateForMajor verifies the structure and signature using the rules of a
// specific schema major (S9-0, finding M2).
//
// # Why a per-major entry point is needed
//
// The corpus test used to validate every entry with the current build's rules.
// That pins the wrong property: "a v1 receipt verifies under today's rules"
// rather than "under v1 rules". Those differ the moment v2 changes anything, and
// the difference is exactly what the corpus exists to detect — so validating only
// under current rules makes the gate unable to express cross-version
// compatibility at all.
//
// Passing the major explicitly lets a test say which ruleset it expects, so a
// change that silently applied v2 rules to v1 artifacts would fail.
//
// # What it does not do
//
// It does not override the receipt's own schema. A caller cannot use this to
// validate a v1 receipt under v2 rules and call it compatible: the major must
// match what the receipt declares, or the check fails. Otherwise this would
// reintroduce the relabel confusion that S9-0h closed.
func (r Receipt) ValidateForMajor(major int, expectedAgent []byte) error {
	declared, _, ok := ParseSchema(r.Schema)
	if !ok {
		return invalid("schema %q is malformed", r.Schema)
	}
	if declared != major {
		return invalid(
			"receipt declares schema major v%d but was validated as v%d; "+
				"validating under a different major's rules would not establish compatibility",
			declared, major)
	}
	return r.Validate(expectedAgent)
}

// SupportedMajors returns the schema majors this build can validate.
//
// It is exported so a test or an operator can assert coverage without reaching
// into the package, and so the corpus test can check that every directory it
// finds corresponds to a major this build actually supports.
func SupportedMajors() []int {
	out := make([]int, len(supportedMajors))
	copy(out, supportedMajors)
	return out
}

// IsSupportedMajor reports whether this build can validate the given major.
func IsSupportedMajor(major int) bool {
	for _, m := range supportedMajors {
		if m == major {
			return true
		}
	}
	return false
}

// IsUnsupported reports whether err means "this build cannot check that version"
// rather than "the receipt is broken" (H2, S9-0j).
//
// # Why callers must branch on this
//
// The whole point of the UnsupportedError/ValidationError split is that an
// out-of-date verifier should say "upgrade me", not "this is forged". That
// distinction is worthless if no production caller inspects it: an operator who
// sees a generic failure will conclude forgery, and the error type exists only to
// be tested.
//
// Callers that report a verdict to a human — the CLI, the self-check verdict
// source, the miner's pre-report check — must use this and say which side is
// behind, because the fix is completely different.
func IsUnsupported(err error) bool {
	var un *UnsupportedError
	return errors.As(err, &un)
}

// UnsupportedError means "this is a version I do not know how to check", as
// opposed to ValidationError which means "I checked and it is broken".
//
// Keeping these distinct is a correctness requirement, not a style choice: a
// verifier that is simply older than the receipt must say so, or an operator
// will read "unsupported" as "someone forged this".
type UnsupportedError struct {
	Reason string
}

func (e *UnsupportedError) Error() string { return "receipt: unsupported: " + e.Reason }

func unsupported(format string, args ...any) error {
	return &UnsupportedError{Reason: fmt.Sprintf(format, args...)}
}

// ParseSchema splits a schema identifier into its major and minor components.
//
// It returns ok=false only for a malformed identifier (wrong prefix, missing or
// non-numeric version). Whether the parsed major is *supported* is a separate
// question, answered by CheckSchema — a well-formed schema for a version this
// build does not know is not malformed, it is unknown.
func ParseSchema(s string) (major, minor int, ok bool) {
	rest, found := strings.CutPrefix(s, schemaPrefix)
	if !found {
		return 0, 0, false
	}

	majorStr, minorStr, hasMinor := strings.Cut(rest, ".")
	if majorStr == "" {
		return 0, 0, false
	}

	m, err := strconv.Atoi(majorStr)
	if err != nil || m < 0 {
		return 0, 0, false
	}

	if !hasMinor {
		// `v1` and `v1.0` denote the same version.
		return m, 0, true
	}
	if minorStr == "" {
		return 0, 0, false
	}

	n, err := strconv.Atoi(minorStr)
	if err != nil || n < 0 {
		return 0, 0, false
	}
	return m, n, true
}

// CheckSchema reports whether s is a schema this build can validate.
//
// Three outcomes, and the caller must not merge the first two:
//
//	nil                  → supported; proceed to validate the body
//	*UnsupportedError    → well-formed but unknown major; say "too old", not "forged"
//	*ValidationError     → malformed; this is a real defect in the receipt
func CheckSchema(s string) error {
	major, _, ok := ParseSchema(s)
	if !ok {
		return invalid("schema %q is malformed (want %q followed by a version)", s, schemaPrefix)
	}

	for _, m := range supportedMajors {
		if m == major {
			// A higher minor is an additive change: unknown fields are ignored
			// rather than rejected, so an older verifier can still check it.
			return nil
		}
	}

	// Name the direction explicitly. "Unsupported" alone leaves the operator
	// guessing which side is behind, and the fix is completely different.
	if major > maxSupportedMajor() {
		return unsupported(
			"schema %q is newer than this build supports (supported majors: %s); upgrade the verifier",
			s, formatMajors())
	}
	return unsupported(
		"schema %q predates the supported majors (%s); this build no longer validates it",
		s, formatMajors())
}

func maxSupportedMajor() int {
	max := 0
	for _, m := range supportedMajors {
		if m > max {
			max = m
		}
	}
	return max
}

func formatMajors() string {
	parts := make([]string, 0, len(supportedMajors))
	for _, m := range supportedMajors {
		parts = append(parts, "v"+strconv.Itoa(m))
	}
	return strings.Join(parts, ", ")
}
