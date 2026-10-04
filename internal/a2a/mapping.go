package a2a

import (
	"fmt"
	"sort"
)

// SchemaVersion is the A2A protocol version RelayFirst targets.
//
// It is 1.0 because that is the revision whose Agent Card carries a list of
// interfaces, which is what lets a node expose HTTP and (later) WebSocket
// without a second card format. Pinned as a named constant so the choice is one
// edit, not a literal scattered through the wire layer.
const SchemaVersion = "1.0"

// SchemaRelation maps an A2A protocol version to the receipt schema majors a
// node may accept for NEW work at that protocol version.
//
// # Why this table exists as code
//
// Versioning has two independent axes: the A2A wire version (out-of-band, a
// header) and the receipt schema major (in-band, self-declared by each receipt).
// Nothing forces them to move together, and nothing would catch it if they
// drifted — a node could start rejecting writes it should accept, or worse,
// accept a schema major it has no profile for.
//
// The plan (docs/notes/upgrade-architecture-plan.md §2.6) requires the mapping to
// live in one place so the two axes cannot silently diverge. Keeping it as a
// table with a test is the enforceable version of "write it in the docs": prose
// does not fail a build.
//
// # Read vs write, the distinction that matters most
//
// This gates *writing* only. Verification is permanent (plan §2.7): every
// historical major stays verifiable forever, and a node that drops a major from
// this table is saying "do not sign new receipts at this version", never "I no
// longer recognise old ones". Conflating the two would break A9 §① — a node
// upgrade would quietly orphan receipts it had already accepted.
//
// The values follow §2.7's write policy: the newest major plus the previous one,
// so an upgrade does not instantly cut off producers that have not migrated.
var SchemaRelation = map[string][]int{
	"1.0": {1},
}

// WritableSchemaMajors returns the receipt majors a node may accept for new work
// at the given A2A protocol version, newest first.
//
// An unknown protocol version is an error rather than an empty list: an empty
// list is indistinguishable from "this version allows no writes", and a caller
// that forgot to add an entry would ship a node that silently rejects everything.
func WritableSchemaMajors(a2aVersion string) ([]int, error) {
	v, err := ParseVersion(a2aVersion)
	if err != nil {
		return nil, err
	}
	majors, ok := SchemaRelation[v.String()]
	if !ok {
		return nil, fmt.Errorf(
			"no schema mapping for A2A version %s: add it to SchemaRelation before "+
				"serving that version, or every write at it will be rejected", v)
	}
	out := append([]int(nil), majors...)
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out, nil
}
