package term_test

import (
	"os"
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/term"
)

func TestPaint_RespectsTheFlag(t *testing.T) {
	if got := term.Paint("x", term.Cyan, false); got != "x" {
		t.Errorf("colour off must emit nothing, got %q", got)
	}
	got := term.Paint("x", term.Cyan, true)
	if !strings.HasPrefix(got, term.Cyan) || !strings.HasSuffix(got, term.Reset) {
		t.Errorf("colour on must wrap with the escape and a reset, got %q", got)
	}
}

func TestIsTTY_FalseForAPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	if term.IsTTY(w) {
		t.Error("a pipe must not be treated as a terminal")
	}
	if term.IsTTY(nil) {
		t.Error("nil must not be treated as a terminal")
	}
}

func TestWidth_CountsRunesNotBytes(t *testing.T) {
	// "é" is two bytes, one rune. Width exists so a layout does not mis-measure it.
	if got := term.Width("abé"); got != 3 {
		t.Errorf("Width(abé) = %d, want 3", got)
	}
	if got := term.Width("x\r\n"); got != 1 {
		t.Errorf("trailing CR/LF must not count, got %d", got)
	}
}
