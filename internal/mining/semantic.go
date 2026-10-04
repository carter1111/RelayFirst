package mining

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// Resolver turns a natural-language field description into a value drawn from a
// fetched document.
//
// # Why this interface exists (ADR-0002)
//
// A dot-path lookup ("data.price") is mechanical: a script resolves it for free.
// A natural-language description ("the main product price") is not, on a messy
// and unfamiliar page — resolving it economically requires a model call. That is
// what makes the task consume real inference budget, which is the scarce resource
// the MVP's economics rest on.
//
// # The honesty caveat, stated where implementers will see it
//
// This cost is *economically induced*, not protocol-enforced. Someone willing to
// hand-write per-site parsing rules can still bypass the model. Semantic fields
// raise the cost of farming; they do not prove it. The primary anti-farming load
// remains the global artifact dedup ledger (invariant A6). Do not describe this
// as unbypassable.
//
// # Determinism requirement
//
// Whatever a Resolver returns is normalized and hashed into the receipt, so it
// MUST be a deterministic string for a given (document, description) pair. If a
// resolver returns free-form prose, verification stops being binary and a dispute
// layer becomes necessary — which MVP.md §5.0 forbids. Implementations that
// cannot guarantee a normalized value must return an error instead of guessing.
type Resolver interface {
	// Resolve returns the normalized value for description, read from body.
	//
	// A missing field is reported as ("", false, nil): absence is a legitimate
	// deterministic observation, not a failure. An error means the resolver could
	// not produce a *trustworthy* value at all.
	Resolve(ctx context.Context, description string, body []byte) (value string, found bool, err error)
}

// FieldSpec is one requested field.
//
// Exactly one of Path or Semantic must be set:
//
//   - Path: a positional dot-path ("data.price"), resolved mechanically.
//   - Semantic: a natural-language description ("the main product price"),
//     requiring a Resolver.
type FieldSpec struct {
	// Path is a dot-path into the document, resolved mechanically.
	Path string

	// Semantic is a natural-language description, resolved by a Resolver.
	Semantic string
}

// Validate reports whether the spec is well-formed.
func (f FieldSpec) Validate() error {
	hasPath := strings.TrimSpace(f.Path) != ""
	hasSemantic := strings.TrimSpace(f.Semantic) != ""

	switch {
	case hasPath && hasSemantic:
		return fmt.Errorf("field spec sets both path and semantic; pick one")
	case !hasPath && !hasSemantic:
		return fmt.Errorf("field spec sets neither path nor semantic")
	}
	return nil
}

// Label returns a stable key for this field in the result object.
//
// Semantic fields key on their description, so the same question over the same
// document yields the same result shape across runs.
func (f FieldSpec) Label() string {
	if p := strings.TrimSpace(f.Path); p != "" {
		return p
	}
	return strings.TrimSpace(f.Semantic)
}

// ParseFieldSpecs reads a task spec's "fields" entry.
//
// Two forms are accepted:
//
//	["data.price", "data.symbol"]                      // dot-paths
//	[{"semantic": "the main product price"}]           // natural language
//	[{"path": "data.price"}]                           // explicit path
//
// A bare string is treated as a dot-path. This keeps the existing S2 form working
// while allowing the semantic form that ADR-0002 introduces.
func ParseFieldSpecs(raw any) ([]FieldSpec, error) {
	if raw == nil {
		return nil, nil
	}

	items, ok := raw.([]any)
	if !ok {
		if ss, ok := raw.([]string); ok {
			out := make([]FieldSpec, 0, len(ss))
			for _, s := range ss {
				spec := FieldSpec{Path: s}
				if err := spec.Validate(); err != nil {
					return nil, err
				}
				out = append(out, spec)
			}
			return out, nil
		}
		return nil, fmt.Errorf("spec.fields must be an array, got %T", raw)
	}

	out := make([]FieldSpec, 0, len(items))
	for i, item := range items {
		switch v := item.(type) {
		case string:
			spec := FieldSpec{Path: v}
			if err := spec.Validate(); err != nil {
				return nil, fmt.Errorf("fields[%d]: %w", i, err)
			}
			out = append(out, spec)

		case map[string]any:
			spec := FieldSpec{}
			if p, ok := v["path"]; ok {
				s, ok := p.(string)
				if !ok {
					return nil, fmt.Errorf("fields[%d].path must be a string, got %T", i, p)
				}
				spec.Path = s
			}
			if s, ok := v["semantic"]; ok {
				str, ok := s.(string)
				if !ok {
					return nil, fmt.Errorf("fields[%d].semantic must be a string, got %T", i, s)
				}
				spec.Semantic = str
			}
			if err := spec.Validate(); err != nil {
				return nil, fmt.Errorf("fields[%d]: %w", i, err)
			}
			out = append(out, spec)

		default:
			return nil, fmt.Errorf("fields[%d] must be a string or an object, got %T", i, item)
		}
	}
	return out, nil
}

// CountSemantic reports how many of specs require a Resolver.
//
// The extract executor uses this to decide whether an LLM call is needed: a
// purely positional spec must not be routed through a model, both to keep it free
// and because the mechanical answer is strictly more reliable.
func CountSemantic(specs []FieldSpec) int {
	n := 0
	for _, s := range specs {
		if strings.TrimSpace(s.Semantic) != "" {
			n++
		}
	}
	return n
}

// ResolveFields produces the deterministic result object for specs.
//
// It returns a canonical JSON object mapping each field's label to its normalized
// value (null when absent). The value set is stable across runs and across
// implementations, which is what keeps verification binary (ADR-0002).
func ResolveFields(ctx context.Context, doc any, specs []FieldSpec, r Resolver, body []byte) (string, error) {
	picked := make(map[string]any, len(specs))

	for _, spec := range specs {
		if err := spec.Validate(); err != nil {
			return "", err
		}

		if path := strings.TrimSpace(spec.Path); path != "" {
			// Mechanical: no model, no cost, no ambiguity.
			picked[path] = lookupPath(doc, path)
			continue
		}

		desc := strings.TrimSpace(spec.Semantic)
		if r == nil {
			return "", fmt.Errorf("field %q needs a semantic resolver, but none is configured", desc)
		}

		value, found, err := r.Resolve(ctx, desc, body)
		if err != nil {
			return "", fmt.Errorf("resolve %q: %w", desc, err)
		}
		if !found {
			// Absence is an observation, not an error, so it is recorded as null
			// rather than failing the task.
			picked[desc] = nil
			continue
		}

		// The resolver's output becomes part of the signed payload, so it must be
		// normalized before it is hashed. An unnormalized value would make two
		// honest runs disagree and turn verification into a judgement call.
		norm, err := NormalizeValue(value)
		if err != nil {
			return "", fmt.Errorf("resolve %q: %w", desc, err)
		}
		picked[desc] = norm
	}

	out, err := canonicalJSONObject(picked)
	if err != nil {
		return "", fmt.Errorf("encode extraction: %w", err)
	}
	return out, nil
}

// NormalizeValue canonicalizes a resolver's output so that two honest runs agree.
//
// Rules (fixed, and part of the wire format):
//
//   - surrounding whitespace is trimmed
//   - a numeric string becomes its decimal form, WITHOUT thousands separators
//     and without a currency symbol
//   - everything else is kept verbatim as trimmed text
//
// The numeric rule is deliberately narrow. It is not trying to understand money;
// it is only removing the formatting variation that would otherwise make
// "$1,234.50" and "1234.5" hash differently for the same real answer.
func NormalizeValue(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}

	if n, ok := normalizeNumeric(s); ok {
		return n, nil
	}
	return s, nil
}

// normalizeNumeric attempts the narrow numeric normalization described above.
//
// It reports ok=false for anything that is not unambiguously a single number, so
// text like "1-2 business days" is left untouched rather than mangled.
func normalizeNumeric(s string) (string, bool) {
	// Strip common currency markers and grouping separators, but only when the
	// remainder is entirely numeric — otherwise leave the string alone.
	trimmed := s

	// Strip a single leading currency marker if present. A rune-based check is
	// required because '€' and '¥' do not fit in a byte.
	if r, size := utf8.DecodeRuneInString(trimmed); size > 0 {
		switch r {
		case '$', '€', '£', '¥', '￥':
			trimmed = strings.TrimSpace(trimmed[size:])
		}
	}

	trimmed = strings.ReplaceAll(trimmed, ",", "")
	trimmed = strings.ReplaceAll(trimmed, " ", "")

	if trimmed == "" {
		return "", false
	}

	// Accept an optional leading sign, digits, and at most one decimal point.
	seenDigit := false
	seenDot := false
	for i, r := range trimmed {
		switch {
		case r >= '0' && r <= '9':
			seenDigit = true
		case (r == '-' || r == '+') && i == 0:
			// sign allowed only at the start
		case r == '.':
			if seenDot {
				return "", false
			}
			seenDot = true
		default:
			return "", false
		}
	}
	if !seenDigit {
		return "", false
	}

	// Canonicalize the spelling so that numerically equal inputs collapse to one
	// string. This is what makes "$1,234.50" and "1234.5" agree: merely stripping
	// decoration is not enough, because "1234.50" and "1234.5" are the same real
	// answer and must not hash differently.
	neg := false
	switch {
	case strings.HasPrefix(trimmed, "-"):
		neg = true
		trimmed = trimmed[1:]
	case strings.HasPrefix(trimmed, "+"):
		trimmed = trimmed[1:]
	}

	intPart := trimmed
	fracPart := ""
	if i := strings.IndexByte(trimmed, '.'); i >= 0 {
		intPart = trimmed[:i]
		fracPart = trimmed[i+1:]
	}

	// Drop leading zeros in the integer part, keeping at least one digit.
	intPart = strings.TrimLeft(intPart, "0")
	if intPart == "" {
		intPart = "0"
	}
	// Drop trailing zeros in the fraction; a fully-zero fraction disappears.
	fracPart = strings.TrimRight(fracPart, "0")

	out := intPart
	if fracPart != "" {
		out += "." + fracPart
	}

	// "-0" and "0" are the same value; normalize the sign away from zero.
	if neg && out != "0" {
		out = "-" + out
	}
	return out, true
}

// canonicalJSONObject renders a map as canonical JSON with sorted keys.
//
// It exists so that the extraction result is byte-stable: two honest runs over
// the same document must produce the same string, because that string is hashed
// into the receipt and its stability is what keeps verification binary.
//
// encoding/json already sorts map keys, and the values here are JSON scalars or
// nested documents, so its output is suitable — unlike the receipt's own encoder,
// which needs control over HTML escaping and multi-byte handling.
func canonicalJSONObject(m map[string]any) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// jsonUnmarshal parses a document, tolerating non-JSON bodies.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// resolveAndAnchor is the shared body of the extract executor's two modes.
//
// It is factored out so the positional and semantic paths cannot drift apart in
// how they capture evidence or hash their result.
//
// It is factored out so the positional and semantic paths cannot drift apart in
// how they capture evidence or hash their result.
func resolveAndAnchor(
	ctx context.Context,
	deps Deps,
	url string,
	specs []FieldSpec,
) (receipt.Result, []receipt.Anchor, error) {
	if deps.Fetcher == nil {
		return receipt.Result{}, nil, fmt.Errorf("extract: fetcher is required")
	}

	a, body, err := deps.Fetcher.Capture(ctx, url)
	if err != nil {
		return receipt.Result{}, nil, fmt.Errorf("extract: %w", err)
	}

	// Parse leniently: a non-JSON body is not an executor error, it is a
	// deterministic outcome ("this url is not JSON"), reported as absent fields.
	var doc any
	if err := jsonUnmarshal(body, &doc); err != nil {
		doc = nil
	}

	value, err := ResolveFields(ctx, doc, specs, deps.Resolver, body)
	if err != nil {
		return receipt.Result{}, nil, fmt.Errorf("extract: %w", err)
	}

	return receipt.Result{
		Value: value,
		Hash:  anchor.HashString(value),
	}, []receipt.Anchor{a}, nil
}
