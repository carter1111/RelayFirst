package sqlite

import (
	"path/filepath"
	"testing"
)

// These tests pin the DSN construction that applies the busy timeout to EVERY
// connection the pool opens.
//
// # Why a unit test as well as the behavioural ones
//
// The wider-pool tests (internal/sqlite and internal/store) prove the pragma reaches
// the connections under contention, which is the property that matters. This file
// covers the branches those tests never exercise: `:memory:` must be preserved
// verbatim, and a filename containing DSN syntax (`?`, `#`, `%`) must be escaped so
// it opens the file it names rather than being read as parameters.

func TestWithPragmas_FormsAndEscaping(t *testing.T) {
	const want = "file:" // prefix used in the expectations below
	const pragma = "_pragma=busy_timeout(5000)"

	cases := []struct {
		name string
		in   string
		out  string
	}{
		{"memory is left untouched", ":memory:", ":memory:"},
		{"plain path becomes a file URI", "/tmp/x.db", want + "/tmp/x.db?" + pragma},
		{"file URI without params gets ?", "file:foo.db", "file:foo.db?" + pragma},
		{"file URI with params gets &", "file:foo.db?cache=shared", "file:foo.db?cache=shared&" + pragma},
		{"question mark is escaped", "/tmp/a?b.db", want + "/tmp/a%3Fb.db?" + pragma},
		{"percent is escaped", "/tmp/a%b.db", want + "/tmp/a%25b.db?" + pragma},
		{"hash is escaped", "/tmp/a#b.db", want + "/tmp/a%23b.db?" + pragma},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := withPragmas(c.in); got != c.out {
				t.Errorf("withPragmas(%q) = %q, want %q", c.in, got, c.out)
			}
		})
	}
}

// TestOpenAppliesBusyTimeoutViaDSN proves the pragma actually reaches a connection,
// which is what the wider-pool measurements depend on.
//
// # Why this reads the pragma back rather than trusting the DSN string
//
// A DSN that is syntactically right but that the driver ignores would leave every
// connection at timeout 0 — exactly the obstacle this change removes. Reading the
// live value is the check that it took effect.
func TestOpenAppliesBusyTimeoutViaDSN(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "pragma.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	var got int
	if err := db.Handle().QueryRow("PRAGMA busy_timeout").Scan(&got); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if got != DefaultBusyTimeoutMillis {
		t.Errorf("busy_timeout = %d, want %d; the DSN pragma did not take effect, so a wider write "+
			"pool would fail with SQLITE_BUSY instead of waiting", got, DefaultBusyTimeoutMillis)
	}
}
