package eip712

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// hexOf renders a digest as 0x-prefixed lowercase hex, matching viem's output.
func hexOf(b []byte) string { return "0x" + hex.EncodeToString(b) }

// newReader avoids importing strings just for one call site in the tests.
func newReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// normalise converts the JSON-decoded message tree into the shape encodeValue
// expects.
//
// Two things happen here that matter for cross-language parity:
//
//   - json.Number is rendered as a decimal string, so integers of any size
//     survive without float64 precision loss (a uint256 has 256 bits; float64
//     has 53, and the round-trip would silently corrupt large values).
//   - nested objects and arrays are walked recursively, because the same
//     conversion is needed at every depth.
func normalise(v any) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("message must be an object, got %T", v)
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		conv, err := normaliseValue(val)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", k, err)
		}
		out[k] = conv
	}
	return out, nil
}

func normaliseValue(v any) (any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case json.Number:
		return t.String(), nil
	case map[string]any:
		return normalise(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			conv, err := normaliseValue(item)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = conv
		}
		return out, nil
	default:
		return v, nil
	}
}
