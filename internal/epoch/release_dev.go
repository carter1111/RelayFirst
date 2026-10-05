//go:build !mainnet

package epoch

// This is the non-mainnet counterpart of release_mainnet.go.
//
// release.go holds the refusal itself (untagged, so the ordinary test run covers
// it). release_mainnet.go fires it via init() when built with -tags mainnet. This
// file is what remains in a normal build: a provisional genesis is EXPECTED and
// nothing refuses.
//
// The split is deliberate so "is this a release?" is answered by the build tag
// rather than by a runtime flag someone can forget to pass.
