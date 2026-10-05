//go:build mainnet

package epoch

// init refuses to run a mainnet binary while the genesis is still provisional.
//
// # Why this runs at startup rather than at build time
//
// Go build tags cannot fail a build from inside a file, and a compile-time
// assertion involving a runtime-readable message is not expressible. Running in
// init means the binary crashes immediately with a readable reason instead of
// quietly minting receipts under the wrong epoch.
//
// # What it prevents
//
// The genesis is inside a receipt's signed payload, so a mainnet binary shipped
// with the development placeholder would sign receipts under a genesis nobody
// agreed to. Those receipts cannot be re-signed after the real genesis is set.
// This turns a silent mistake into a loud startup failure.
//
// The release procedure is therefore: set GenesisValue to the launch date, THEN
// build with -tags mainnet. A plain `go build` (no tag) is unaffected, so
// development keeps working against the placeholder.
//
// The refusal itself lives in release.go (untagged), so the ordinary test run
// exercises it; this file only decides WHEN it fires.
func init() { assertReleaseGenesis(GenesisValue) }
