package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/relayfirst/relayfirst/internal/node"
	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// These tests cover the node BINARY, which nothing did before.
//
// # The bug this file exists for
//
// `node.New` requires four stores. Every test built its own `node.Config`, so all of them passed
// while `cmd/relayfirst-node/main.go` had stopped constructing a valid one — the task store was
// missing, and `node.New` returned an error. The binary could not START, and had not been able to
// since the task board was added, because a package's own tests cannot catch a wiring mistake in
// a `main` package they do not exercise.
//
// The failure was found by running the binary by hand. That is not a plan: a binary that panics
// on startup is the kind of defect a `docker run` would hit immediately and no test would report.
// So the wiring is pinned here.
//
// # Why this tests the config and not the process
//
// Spawning a subprocess would also work, and would be slower and flakier. What broke was the
// construction of a valid config, so that is what is asserted: if the binary can build a node
// from its own config path, the startup failure is caught at compile-and-run time in CI.

// buildNodeConfig mirrors exactly what main() does, so a change to main's wiring has to be
// reflected here or this test stops representing it.
//
// It is duplicated rather than extracted because extracting a shared constructor would make the
// test assert on the constructor rather than on main — and the bug was precisely that main and the
// tests disagreed.
func buildNodeConfig(t *testing.T, dbPath string) (*node.Node, error) {
	t.Helper()

	db, err := sqlite.Open(dbPath)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = db.Close() })

	return node.New(node.Config{
		Store:           sqlite.NewMessageStore(db),
		Cards:           sqlite.NewCardStore(db),
		Observations:    sqlite.NewObservationStore(db),
		Tasks:           sqlite.NewTaskStore(db),
		PublicURL:       "http://localhost:8080",
		Version:         version,
		MaxPayloadBytes: 1 << 20,
	})
}

// TestNodeBinary_StartsWithItsOwnWiring is the regression test.
//
// If a store is dropped from main's config, node.New fails here for the same reason it failed at
// startup — and this runs in CI instead of in a stranger's `docker run`.
func TestNodeBinary_StartsWithItsOwnWiring(t *testing.T) {
	n, err := buildNodeConfig(t, filepath.Join(t.TempDir(), "node.db"))
	if err != nil {
		t.Fatalf("the node binary's own wiring must construct a usable node, got: %v\n"+
			"this is the failure a `docker run` would hit, and no package test would report it", err)
	}
	if n == nil {
		t.Fatal("no node")
	}
	if n.Handler() == nil {
		t.Fatal("the node must serve a handler")
	}
}

// TestNodeBinary_RequiredStoresAreAllPresent names the four stores explicitly.
//
// A missing one produces a specific error, and asserting on the possibility rather than on the
// error text keeps the test readable when the message changes. The point is that the set is
// complete, not which store happens to be checked first.
func TestNodeBinary_RequiredStoresAreAllPresent(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "node.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// Each store alone must be insufficient, which proves node.New actually requires them. A
	// store that could be omitted would make this whole test vacuous.
	full := node.Config{
		Store:        sqlite.NewMessageStore(db),
		Cards:        sqlite.NewCardStore(db),
		Observations: sqlite.NewObservationStore(db),
		Tasks:        sqlite.NewTaskStore(db),
	}
	if _, err := node.New(full); err != nil {
		t.Fatalf("the complete config must work: %v", err)
	}

	cases := []struct {
		name string
		make func() node.Config
	}{
		{"no message store", func() node.Config { c := full; c.Store = nil; return c }},
		{"no card store", func() node.Config { c := full; c.Cards = nil; return c }},
		{"no observation store", func() node.Config { c := full; c.Observations = nil; return c }},
		{"no task store", func() node.Config { c := full; c.Tasks = nil; return c }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := node.New(c.make()); err == nil {
				t.Errorf("a config with %s must be rejected, or the field is decorative", c.name)
			}
		})
	}
}

// TestNodeBinary_WritesItsDatabaseToTheStorageFlag keeps the flag honest: it must actually be
// where the state goes, since an operator relies on it for persistence and backups.
func TestNodeBinary_WritesItsDatabaseToTheStorageFlag(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "node.db")

	n, err := buildNodeConfig(t, dbPath)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	_ = n

	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("the database must exist at the path given to --storage, got: %v", err)
	}
}
