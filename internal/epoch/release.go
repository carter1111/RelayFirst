package epoch

import (
	"fmt"
	"time"
)

// assertReleaseGenesis panics when a mainnet build carries a provisional genesis.
//
// # Why this is untagged, and the init that calls it is tagged
//
// The refusal has to be a property of RELEASE builds, but it also has to be
// TESTABLE without one: a guard that only exists under `-tags mainnet` cannot be
// driven by the normal test run, so nobody would notice if it stopped working.
// So the function lives here, in every build, and only the init() that FIRES it
// is behind the tag (release_mainnet.go). Development is unaffected and the
// refusal is still exercised by the ordinary tests.
//
// It takes the value as an argument rather than reading GenesisValue so both
// branches — refuse and accept — are reachable from a test with explicit inputs.
func assertReleaseGenesis(value int64) {
	if value == placeholderGenesis {
		panic(fmt.Sprintf(
			"epoch: refusing to run a mainnet build with the provisional genesis %d (%s); "+
				"set GenesisValue to the public launch date (00:00 UTC) before building with -tags mainnet "+
				"(see docs/notes/blk-4-genesis-decision.md)",
			value, time.Unix(value, 0).UTC().Format("2006-01-02")))
	}
}
