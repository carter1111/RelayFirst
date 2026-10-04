// Package mining generates and executes Proof of Agent Work tasks.
//
// The load-bearing constraint is in executor.go: only task types with an
// *independent ground truth* are accepted (MVP.md §5.0, invariant A2). That
// restriction is what makes verification binary — right or wrong, never
// "somewhat good" — and therefore what removes the need for a dispute layer
// for the whole MVP.
//
// Every executor returns a deterministic Result: a verifier who re-runs the
// same task must reach the same value and the same hash.
package mining

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// Deps are the collaborators an executor needs.
type Deps struct {
	// Fetcher captures anchors. Required for probe and extract; compute does
	// not touch the network.
	Fetcher *anchor.Fetcher

	// Client is reserved for executors that need raw HTTP beyond anchor
	// capture. Optional.
	Client *http.Client

	// Resolver handles natural-language field descriptions in extract tasks
	// (ADR-0002). It is optional: a deployment that only uses positional
	// dot-paths needs no resolver, and extract refuses semantic fields rather
	// than guessing when none is configured.
	Resolver Resolver
}

// Executor runs one task type and produces a deterministic result.
type Executor interface {
	// Type reports which task type this executor handles.
	Type() receipt.TaskType

	// Execute runs spec and returns the result plus the anchors that support
	// it. At least one anchor is mandatory.
	Execute(ctx context.Context, spec map[string]any) (receipt.Result, []receipt.Anchor, error)
}

// Executors returns the executor for each accepted task type.
//
// The map MUST cover exactly the accepted set — TestExecutorsCoverAcceptedTypes
// enforces this, so a task type cannot exist in the receipt package without an
// executor here.
func Executors(d Deps) map[receipt.TaskType]Executor {
	return map[receipt.TaskType]Executor{
		receipt.TaskProbe:   &ProbeExecutor{deps: d},
		receipt.TaskExtract: &ExtractExecutor{deps: d},
		receipt.TaskCompute: &ComputeExecutor{},
	}
}

// Run dispatches spec to the executor for taskType.
//
// An unaccepted type is rejected here as well as in receipt validation: defence
// in depth, because this is the boundary where a generated task would otherwise
// sneak into the mining loop.
func Run(ctx context.Context, d Deps, taskType receipt.TaskType, spec map[string]any) (receipt.Result, []receipt.Anchor, error) {
	if !taskType.Accepted() {
		return receipt.Result{}, nil, fmt.Errorf(
			"mining: task type %q is not accepted (allowed: probe/extract/compute)", taskType)
	}

	ex, ok := Executors(d)[taskType]
	if !ok {
		return receipt.Result{}, nil, fmt.Errorf("mining: no executor registered for %q", taskType)
	}

	res, anchors, err := ex.Execute(ctx, spec)
	if err != nil {
		return receipt.Result{}, nil, err
	}
	if len(anchors) == 0 {
		// A result with no evidence is unverifiable, so it is not a valid
		// piece of work (MVP.md §4.1).
		return receipt.Result{}, nil, fmt.Errorf("mining: %s produced no anchors", taskType)
	}
	return res, anchors, nil
}

// ------------------------------------------------------------------ helpers

// specString reads a required string field from a task spec.
func specString(spec map[string]any, key string) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("spec is nil")
	}
	raw, ok := spec[key]
	if !ok {
		return "", fmt.Errorf("spec is missing %q", key)
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("spec.%s must be a string, got %T", key, raw)
	}
	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("spec.%s is empty", key)
	}
	return s, nil
}

// specStringSlice reads an optional list-of-strings field.
func specStringSlice(spec map[string]any, key string) ([]string, error) {
	raw, ok := spec[key]
	if !ok {
		return nil, nil
	}
	switch v := raw.(type) {
	case []string:
		return v, nil
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("spec.%s[%d] must be a string, got %T", key, i, item)
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("spec.%s must be an array of strings, got %T", key, raw)
}

// specInt reads an optional integer field, accepting the JSON-number and
// string forms that survive a round trip through map[string]any.
func specInt(spec map[string]any, key string, fallback int) (int, error) {
	raw, ok := spec[key]
	if !ok || raw == nil {
		return fallback, nil
	}
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err != nil {
			return 0, fmt.Errorf("spec.%s must be an integer, got %q", key, v)
		}
		return n, nil
	}
	return 0, fmt.Errorf("spec.%s must be an integer, got %T", key, raw)
}
