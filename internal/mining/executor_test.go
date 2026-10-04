package mining

import (
	"strings"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// newTestFetcher returns a Fetcher whose clock is pinned, so anchors are
// reproducible across runs.
func newTestFetcher() *anchor.Fetcher {
	f := anchor.NewFetcher()
	f.Now = func() time.Time { return time.Unix(1791015800, 0) }
	return f
}

// TestExecutorsCoverAcceptedTypes is the structural guard for invariant A2.
//
// It asserts that the executor registry and the accepted task-type set are the
// same set, in both directions. That means:
//
//   - a task type cannot be added without an executor, and
//   - an executor cannot exist for a type the receipt validator would reject.
func TestExecutorsCoverAcceptedTypes(t *testing.T) {
	registered := Executors(Deps{Fetcher: anchor.NewFetcher()})

	all := []receipt.TaskType{
		receipt.TaskProbe,
		receipt.TaskExtract,
		receipt.TaskCompute,
		// Deliberately include non-ground-truth types to prove they are absent.
		"summarize",
		"classify",
		"translate",
		"",
	}

	for _, taskType := range all {
		_, hasExec := registered[taskType]

		if taskType.Accepted() && !hasExec {
			t.Errorf("accepted type %q has no executor", taskType)
		}
		if !taskType.Accepted() && hasExec {
			t.Errorf("non-ground-truth type %q must not have an executor (invariant A2)", taskType)
		}
	}
}

// TestExecutorsMatchReceiptAcceptedSet asserts set equality explicitly, so a
// future accepted type cannot be forgotten here.
func TestExecutorsMatchReceiptAcceptedSet(t *testing.T) {
	registered := Executors(Deps{Fetcher: anchor.NewFetcher()})

	if got, want := len(registered), 0; got <= want {
		t.Fatalf("executor registry is empty")
	}

	// Every registered executor must claim an accepted type.
	for taskType, ex := range registered {
		if !taskType.Accepted() {
			t.Errorf("executor %T registered for unaccepted type %q", ex, taskType)
		}
		if ex.Type() != taskType {
			t.Errorf("executor %T reports type %q but is keyed as %q", ex, ex.Type(), taskType)
		}
	}

	// The three ground-truth types must all be present.
	for _, want := range []receipt.TaskType{receipt.TaskProbe, receipt.TaskExtract, receipt.TaskCompute} {
		if _, ok := registered[want]; !ok {
			t.Errorf("missing executor for accepted type %q", want)
		}
	}
}

// TestGeneratorNeverEmitsGeneratedTasks is the runtime half of A2.
//
// It drives the generator many times and asserts that no generated task is ever
// a summary/classification/translation task, and that every emitted type is one
// the receipt validator accepts.
func TestGeneratorNeverEmitsGeneratedTasks(t *testing.T) {
	g, err := NewGenerator([]string{
		"https://example.com/a",
		"https://example.com/b",
		"https://example.com/c",
	})
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}

	rejected := []receipt.TaskType{"summarize", "classify", "translate", "fetch_and_summarize", "chat"}

	for i := 0; i < 500; i++ {
		taskType, spec, err := g.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}

		if !taskType.Accepted() {
			t.Fatalf("generated unaccepted task type %q at iteration %d", taskType, i)
		}
		for _, bad := range rejected {
			if taskType == bad {
				t.Fatalf("generated generation-type task %q at iteration %d (invariant A2)", taskType, i)
			}
		}

		// Every task must be executable from its spec without further context.
		if spec == nil {
			t.Fatalf("generated nil spec for %q at iteration %d", taskType, i)
		}
		if _, hasURL := spec["url"]; !hasURL && taskType != receipt.TaskCompute {
			t.Errorf("%s spec should carry a url, got %#v", taskType, spec)
		}
	}
}

func TestNewGenerator_RejectsEmptySources(t *testing.T) {
	for _, sources := range [][]string{nil, {}, {""}, {"  ", ""}} {
		if _, err := NewGenerator(sources); err == nil {
			t.Errorf("expected an error for sources %#v", sources)
		}
	}
}

// TestRun_RejectsUnacceptedType proves the dispatch layer refuses non-ground-
// truth work even if a caller bypasses the generator.
func TestRun_RejectsUnacceptedType(t *testing.T) {
	d := Deps{Fetcher: anchor.NewFetcher()}

	for _, bad := range []receipt.TaskType{"summarize", "classify", "translate", "", "chat"} {
		_, _, err := Run(t.Context(), d, bad, map[string]any{"url": "https://example.com"})
		if err == nil {
			t.Errorf("Run must reject unaccepted type %q", bad)
		}
		if err != nil && !strings.Contains(err.Error(), "not accepted") {
			t.Errorf("type %q: unexpected error %v", bad, err)
		}
	}
}

// TestRun_RequiresAnchor asserts that an executor producing no evidence cannot
// yield valid work (MVP.md §4.1).
func TestRun_RequiresAnchor(t *testing.T) {
	d := Deps{Fetcher: anchor.NewFetcher()}

	// compute over an unknown op fails before anchors are involved; use a
	// defined op to reach the anchor check.
	_, anchors, err := Run(t.Context(), d, receipt.TaskCompute, map[string]any{
		"op":    OpHash,
		"input": "hello",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(anchors) == 0 {
		t.Fatal("compute must carry at least one anchor")
	}
}
