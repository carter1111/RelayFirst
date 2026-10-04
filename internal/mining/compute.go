package mining

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// ComputeExecutor runs a deterministic computation over its own spec.
//
// This is the only executor that touches no network. That matters: it is the
// natural self-test for the mining loop, and it proves the receipt pipeline
// works end to end without depending on any external service being up.
type ComputeExecutor struct{}

// Type reports the task type this executor handles.
func (e *ComputeExecutor) Type() receipt.TaskType { return receipt.TaskCompute }

// Supported operations.
const (
	OpHash     = "hash"     // sha256 over input; returns hex
	OpConcat   = "concat"   // join parts in order
	OpSortJSON = "sortjson" // re-emit a JSON object with sorted keys
)

// Execute runs spec.op over spec.input.
//
// Unlike probe and extract, compute has no external source of truth, so the
// anchor is synthetic: the spec is hashed and recorded as an "inline" anchor.
// That keeps every receipt shape-identical (≥1 anchor) while making explicit
// that the evidence here is the task definition itself.
func (e *ComputeExecutor) Execute(_ context.Context, spec map[string]any) (receipt.Result, []receipt.Anchor, error) {
	op, err := specString(spec, "op")
	if err != nil {
		return receipt.Result{}, nil, fmt.Errorf("compute: %w", err)
	}

	specHash, err := canonicalSpecHash(spec)
	if err != nil {
		return receipt.Result{}, nil, fmt.Errorf("compute: %w", err)
	}

	a := receipt.Anchor{
		URL:         "inline",
		ContentHash: specHash,
		FetchedAt:   specInt64(spec, "issuedAt"),
		Status:      200,
	}

	var value string
	switch strings.ToLower(strings.TrimSpace(op)) {
	case OpHash:
		input, err := specString(spec, "input")
		if err != nil {
			return receipt.Result{}, nil, fmt.Errorf("compute: %w", err)
		}
		sum := sha256.Sum256([]byte(input))
		value = hex.EncodeToString(sum[:])

	case OpConcat:
		parts, err := specStringSlice(spec, "parts")
		if err != nil {
			return receipt.Result{}, nil, fmt.Errorf("compute: %w", err)
		}
		if len(parts) == 0 {
			return receipt.Result{}, nil, fmt.Errorf("compute: spec.parts must not be empty")
		}
		value = strings.Join(parts, "")

	case OpSortJSON:
		raw, err := specString(spec, "input")
		if err != nil {
			return receipt.Result{}, nil, fmt.Errorf("compute: %w", err)
		}
		value, err = sortJSONKeys(raw)
		if err != nil {
			return receipt.Result{}, nil, fmt.Errorf("compute: %w", err)
		}

	default:
		return receipt.Result{}, nil, fmt.Errorf(
			"compute: unsupported op %q (want %s, %s or %s)", op, OpHash, OpConcat, OpSortJSON)
	}

	return receipt.Result{
		Value: value,
		Hash:  anchor.HashString(value),
	}, []receipt.Anchor{a}, nil
}

// sortJSONKeys re-emits a flat JSON object with its keys in sorted order.
//
// A deliberately small, total operation: it has an obvious ground truth and no
// ambiguity, which is exactly the property MVP.md §5.0 requires.
func sortJSONKeys(raw string) (string, error) {
	// The object is parsed as ordered key/value pairs without a JSON library's
	// map iteration order leaking in: we sort explicitly.
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return "", fmt.Errorf("input must be a JSON object")
	}

	body := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	if body == "" {
		return "{}", nil
	}

	pairs := splitTopLevel(body)
	sort.Strings(pairs)

	return "{" + strings.Join(pairs, ",") + "}", nil
}

// splitTopLevel splits a comma-separated object body, respecting nesting and
// string literals so that commas inside values do not split a pair.
func splitTopLevel(s string) []string {
	var out []string
	depth := 0
	inString := false
	escaped := false
	start := 0

	for i, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case inString:
			// Commas inside string values are not separators.
		case r == '{' || r == '[':
			depth++
		case r == '}' || r == ']':
			depth--
		case r == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	out = append(out, strings.TrimSpace(s[start:]))

	// Drop empty segments produced by trailing commas.
	kept := out[:0]
	for _, p := range out {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return kept
}

// canonicalSpecHash hashes a spec in a stable way, so the same logical task
// always yields the same anchor regardless of map iteration order.
func canonicalSpecHash(spec map[string]any) (string, error) {
	keys := make([]string, 0, len(spec))
	for k := range spec {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%v\n", k, spec[k])
	}
	return anchor.HashHex(h.Sum(nil)), nil
}

// specInt64 reads an optional integer field as int64.
func specInt64(spec map[string]any, key string) int64 {
	n, err := specInt(spec, key, 0)
	if err != nil {
		return 0
	}
	return int64(n)
}
