package mining

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// Generator produces self-generated mining tasks (MVP.md §2, §5.0).
//
// This type is the enforcement point for invariant A2. It can only emit the
// three ground-truth task types, because the type set it draws from is built
// from receipt.TaskType.Accepted(). There is no code path that produces a
// summary, classification or translation task — see TestGeneratorNeverEmits-
// GeneratedTasks, which asserts this against the whole accepted set.
type Generator struct {
	// Sources are the URLs the generator may probe or extract from. Required.
	Sources []string

	// SemanticFields, when non-empty, replaces the fixed positional field list in
	// extract tasks with these natural-language descriptions.
	//
	// # Why this is a generator field and not a spec field
	//
	// The field list must be fixed *before* the response is fetched, so that a
	// task cannot be redefined after seeing what came back (see extract.go). A
	// generator-level default is fixed at construction and therefore satisfies
	// that; accepting field descriptions per task from a remote party would not.
	//
	// Setting this makes extract tasks consume real inference budget, which is the
	// cost ADR-0002 is about. Resolving them requires a Resolver in mining.Deps;
	// without one, an extract task fails cleanly rather than silently degrading to
	// a mechanical lookup that would have cost nothing.
	SemanticFields []string

	// Provider names the inference provider used to resolve SemanticFields, and is
	// written into the extract spec so the receipt's work block names the provider
	// that was actually used. Empty means "local".
	//
	// Without this a semantic task would record provider "none", which reads as
	// "no inference was consumed" — the opposite of the truth for the one task
	// type whose whole purpose is to consume inference.
	//
	// Limitation: token counts are still not populated (they stay 0). This records
	// *which* provider was used, not how much it cost. See REQ-INFER-4.
	Provider string

	// rand is the entropy source; nil means crypto/rand.
	rand func() (int64, error)
}

// NewGenerator returns a Generator over sources.
//
// Sources must be non-empty: a generator with nothing to work on would silently
// produce zero work, which is worse than failing loudly.
func NewGenerator(sources []string) (*Generator, error) {
	clean := make([]string, 0, len(sources))
	for _, s := range sources {
		if s = strings.TrimSpace(s); s != "" {
			clean = append(clean, s)
		}
	}
	if len(clean) == 0 {
		return nil, fmt.Errorf("mining: generator requires at least one source URL")
	}
	return &Generator{Sources: clean, rand: cryptoInt64}, nil
}

// Next produces one task. Callers must not assume which type they receive.
//
// A2 is enforced structurally: acceptedTypes() returns only the three
// ground-truth types, and nextTaskType indexes into it. Widening the generator
// therefore requires widening the accepted set, which would fail
// TestGeneratorNeverEmitsGeneratedTasks and the receipt validator at once.
func (g *Generator) Next() (receipt.TaskType, map[string]any, error) {
	types := g.acceptedTypes()
	if len(types) == 0 {
		return "", nil, fmt.Errorf("mining: no accepted task types")
	}

	idx, err := g.intn(int64(len(types)))
	if err != nil {
		return "", nil, err
	}
	taskType := types[idx]

	src, err := g.pickSource()
	if err != nil {
		return "", nil, err
	}

	spec, err := g.specFor(taskType, src)
	if err != nil {
		return "", nil, err
	}
	return taskType, spec, nil
}

// acceptedTypes returns exactly the ground-truth task types.
//
// Derived from receipt.TaskType.Accepted() rather than hard-coded, so the
// generator and the validator cannot drift apart.
func (g *Generator) acceptedTypes() []receipt.TaskType {
	all := []receipt.TaskType{receipt.TaskProbe, receipt.TaskExtract, receipt.TaskCompute}
	out := make([]receipt.TaskType, 0, len(all))
	for _, t := range all {
		if t.Accepted() {
			out = append(out, t)
		}
	}
	return out
}

// specFor builds the spec for one task type over src.
func (g *Generator) specFor(taskType receipt.TaskType, src string) (map[string]any, error) {
	switch taskType {
	case receipt.TaskProbe:
		return map[string]any{"url": src}, nil

	case receipt.TaskExtract:
		// The field list is fixed *before* fetching, so the task cannot be
		// redefined after seeing the response (see extract.go).
		fields := []any{"status", "id"}
		semantic := false
		if len(g.SemanticFields) > 0 {
			// Natural-language fields make this task cost inference budget
			// (ADR-0002). The list is fixed at construction for the same reason
			// the positional one is.
			semantic = true
			fields = make([]any, 0, len(g.SemanticFields))
			for _, desc := range g.SemanticFields {
				d := strings.TrimSpace(desc)
				if d == "" {
					continue
				}
				fields = append(fields, map[string]any{"semantic": d})
			}
			if len(fields) == 0 {
				// Every description was blank; fall back rather than emit a task
				// with no fields, which would be a zero-work receipt.
				fields = []any{"status", "id"}
				semantic = false
			}
		}

		spec := map[string]any{
			"url":    src,
			"fields": fields,
			"format": OutputJSON,
		}
		if semantic {
			// Record the provider in the spec so the receipt's work block names
			// what was actually used, rather than reporting "none" — which would
			// claim no inference was consumed by the one task type that exists to
			// consume it.
			provider := strings.TrimSpace(g.Provider)
			if provider == "" {
				provider = "local"
			}
			spec["provider"] = provider
		}
		return spec, nil

	case receipt.TaskCompute:
		// Compute needs no network, so derive its input deterministically from
		// the source rather than fetching anything.
		return map[string]any{
			"op":    OpHash,
			"input": src,
		}, nil
	}

	// Unreachable while acceptedTypes() is the only source of task types, but
	// kept explicit so a future edit cannot silently pass an unaccepted type.
	return nil, fmt.Errorf("mining: generator refused to build an unaccepted task type %q", taskType)
}

func (g *Generator) pickSource() (string, error) {
	idx, err := g.intn(int64(len(g.Sources)))
	if err != nil {
		return "", err
	}
	return g.Sources[idx], nil
}

// intn returns a uniform random int64 in [0, n).
func (g *Generator) intn(n int64) (int64, error) {
	if n <= 0 {
		return 0, fmt.Errorf("mining: invalid range %d", n)
	}
	if g.rand != nil {
		v, err := g.rand()
		if err != nil {
			return 0, err
		}
		if v < 0 {
			v = -v
		}
		return v % n, nil
	}
	return cryptoIntn(n)
}

// cryptoInt64 returns a non-negative random int64.
func cryptoInt64() (int64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("mining: entropy: %w", err)
	}
	return int64(binary.BigEndian.Uint64(b[:]) >> 1), nil
}

// cryptoIntn returns a uniform random int64 in [0, n).
func cryptoIntn(n int64) (int64, error) {
	if n <= 0 {
		return 0, fmt.Errorf("mining: invalid range %d", n)
	}
	// Note: the variable is named `v`, not `big`, so it does not shadow the
	// math/big package imported for big.NewInt.
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		return 0, fmt.Errorf("mining: entropy: %w", err)
	}
	return v.Int64(), nil
}
