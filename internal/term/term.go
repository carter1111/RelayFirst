// Package term holds the terminal-output helpers shared by the node, the
// dashboard and the miner CLI: whether a stream is a terminal, and how to colour
// and lay out text when it is.
//
// # Why this is its own package
//
// Three binaries need the same answer to "is this a terminal", and the rule that
// depends on it is identical everywhere: decorate for a human, stay byte-for-byte
// plain for a pipe. Two or three copies of that decision would drift, and the drift
// would show up as escapes in someone's `docker logs` — the exact failure the rule
// exists to prevent.
//
// # What it deliberately does not do
//
// It holds no cryptography and imports nothing from RelayFirst, so a node may import
// it without gaining any ability to sign or verify (MVP.md §7.1), and the import-graph
// gate keeps passing. It is also not a TUI framework: it is the small surface — a TTY
// test, a colour wrap — that everything shares, leaving any real rendering to the
// package that needs it.
package term

import (
	"os"
	"strings"
)

// ANSI escapes. Emitted only when colour is explicitly on, so a piped run never
// carries them.
const (
	Reset  = "\033[0m"
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	Cyan   = "\033[36m"
	Green  = "\033[32m"
	Red    = "\033[31m"
	Yellow = "\033[33m"
)

// IsTTY reports whether f is an interactive terminal.
//
// It uses os.FileMode and nothing else: a pipe or a redirected file is not a
// character device. That single check is what lets one binary serve both a human and
// a log scraper without a flag.
func IsTTY(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// Paint wraps s in colour and a reset when on, and returns s unchanged when not.
//
// Every coloured write goes through here, so there is one place that decides whether
// escapes appear and no path can leak them into a piped log.
func Paint(s, colour string, on bool) string {
	if !on {
		return s
	}
	return colour + s + Reset
}

// Width returns a printable width estimate for a line.
//
// It counts runes rather than bytes so a multi-byte character is not measured as
// several columns. It does not handle East-Asian double-width characters; each counts
// as one here, which is honest for the ASCII-heavy output this is used for and is
// documented rather than guessed at.
func Width(s string) int {
	return len([]rune(strings.TrimRight(s, "\r\n")))
}
