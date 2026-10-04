package mining

import (
	"testing"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// TestGenerator_SemanticFieldsMakeExtractTasksCostly covers the ADR-0002 path:
// configuring semantic fields must turn extract tasks into inference-consuming
// ones, and must not leak into any other task type.
func TestGenerator_SemanticFieldsMakeExtractTasksCostly(t *testing.T) {
	g, err := NewGenerator([]string{"https://example.com"})
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	g.SemanticFields = []string{"the main product price", "the page title"}
	g.Provider = "openai"
	// Deterministic: index 1 of probe/extract/compute is extract.
	g.rand = func() (int64, error) { return 1, nil }

	_, spec, err := g.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}

	specs, err := ParseFieldSpecs(spec["fields"])
	if err != nil {
		t.Fatalf("ParseFieldSpecs: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("got %d field specs, want 2", len(specs))
	}
	if n := CountSemantic(specs); n != 2 {
		t.Errorf("CountSemantic = %d, want 2 — semantic fields must actually be semantic", n)
	}
	for i, s := range specs {
		if s.Path != "" {
			t.Errorf("spec %d has a positional path %q; it should be semantic", i, s.Path)
		}
	}

	// The provider must be recorded, or the receipt's work block would claim no
	// inference was consumed by the one task type that exists to consume it.
	if got := spec["provider"]; got != "openai" {
		t.Errorf("spec provider = %v, want %q so the receipt names the provider used", got, "openai")
	}
}

// TestGenerator_SemanticWithoutProviderRecordsLocal: an unset provider must not
// fall through to "none".
func TestGenerator_SemanticWithoutProviderRecordsLocal(t *testing.T) {
	g, err := NewGenerator([]string{"https://example.com"})
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	g.SemanticFields = []string{"the page title"}
	g.rand = func() (int64, error) { return 1, nil }

	_, spec, err := g.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got := spec["provider"]; got != "local" {
		t.Errorf("spec provider = %v, want \"local\"", got)
	}
}

// TestGenerator_WithoutSemanticFieldsStaysFree is the converse, and protects the
// "compute and plain extract cost nothing" property the economics rely on.
func TestGenerator_WithoutSemanticFieldsStaysFree(t *testing.T) {
	g, err := NewGenerator([]string{"https://example.com"})
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	g.rand = func() (int64, error) { return 1, nil } // extract

	_, spec, err := g.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}

	specs, err := ParseFieldSpecs(spec["fields"])
	if err != nil {
		t.Fatalf("ParseFieldSpecs: %v", err)
	}
	if n := CountSemantic(specs); n != 0 {
		t.Errorf("CountSemantic = %d, want 0 by default — an unconfigured miner must not incur inference cost", n)
	}
}

// TestGenerator_BlankSemanticFieldsFallBack: a flag passed with a blank value must
// not produce a field-less (zero-work) task.
func TestGenerator_BlankSemanticFieldsFallBack(t *testing.T) {
	g, err := NewGenerator([]string{"https://example.com"})
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	g.SemanticFields = []string{"", "   "}
	g.rand = func() (int64, error) { return 1, nil }

	_, spec, err := g.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}

	specs, err := ParseFieldSpecs(spec["fields"])
	if err != nil {
		t.Fatalf("ParseFieldSpecs: %v", err)
	}
	if len(specs) == 0 {
		t.Fatal("an extract task with no fields would be zero work; it must fall back")
	}
	if CountSemantic(specs) != 0 {
		t.Error("blank descriptions must not become semantic fields")
	}
}

// TestGenerator_SemanticFieldsApplyOnlyToExtract guards against the field list
// leaking into probe or compute tasks, which have no fields at all.
func TestGenerator_SemanticFieldsApplyOnlyToExtract(t *testing.T) {
	cases := []struct {
		idx      int64
		taskType receipt.TaskType
	}{
		{0, receipt.TaskProbe},
		{2, receipt.TaskCompute},
	}

	for _, c := range cases {
		g, err := NewGenerator([]string{"https://example.com"})
		if err != nil {
			t.Fatalf("NewGenerator: %v", err)
		}
		g.SemanticFields = []string{"the page title"}
		idx := c.idx
		g.rand = func() (int64, error) { return idx, nil }

		gotType, spec, err := g.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if gotType != c.taskType {
			t.Fatalf("got task type %q, want %q", gotType, c.taskType)
		}
		if _, ok := spec["fields"]; ok {
			t.Errorf("%s task should carry no fields", c.taskType)
		}
	}
}
