package mining

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// ExtractExecutor pulls a specified field out of a fetched document.
//
// Ground truth: re-fetching and re-extracting yields the same value. The spec
// must name the field(s) *before* fetching, so the task cannot be redefined
// after seeing the data — which is what keeps it from degenerating into the
// "fetch anything and hash it" pattern that MVP.md §5.0 rejects.
type ExtractExecutor struct{ deps Deps }

// Type reports the task type this executor handles.
func (e *ExtractExecutor) Type() receipt.TaskType { return receipt.TaskExtract }

// Output shapes. `json` returns a canonical JSON object of the requested
// fields; `text` returns a newline-joined, trimmed text form.
const (
	OutputJSON = "json"
	OutputText = "text"
)

// Execute fetches spec.url and extracts spec.fields.
//
// Supported spec keys:
//
//	url     (required) the document to fetch
//	fields  (required) fields to extract. Two forms (ADR-0002):
//	          ["data.price"]                      dot-paths, resolved mechanically
//	          [{"semantic": "the main price"}]    natural language, needs a Resolver
//	format  "json" (default) or "text"
//
// Dot-paths traverse nested JSON objects. A path that is absent yields a null
// entry rather than an error, so "the field is gone" is itself a deterministic
// observable result.
//
// Semantic fields exist to make the task cost something real: resolving "the main
// product price" on an unfamiliar messy page economically requires a model call,
// which is the inference budget the project's economics rest on. The cost is
// induced rather than enforced — see ADR-0002 — but it is the difference between a
// mineable asset and a free faucet.
func (e *ExtractExecutor) Execute(ctx context.Context, spec map[string]any) (receipt.Result, []receipt.Anchor, error) {
	url, err := specString(spec, "url")
	if err != nil {
		return receipt.Result{}, nil, fmt.Errorf("extract: %w", err)
	}

	specs, err := ParseFieldSpecs(spec["fields"])
	if err != nil {
		return receipt.Result{}, nil, fmt.Errorf("extract: %w", err)
	}
	if len(specs) == 0 {
		// Without named fields this collapses into "fetch and hash", which is
		// not accepted work (MVP.md §5.0).
		return receipt.Result{}, nil, fmt.Errorf("extract: spec.fields must name at least one field")
	}

	// Refuse semantic work early when no resolver is configured, rather than
	// fetching and then failing: a clear configuration error beats a wasted
	// network call.
	if CountSemantic(specs) > 0 && e.deps.Resolver == nil {
		return receipt.Result{}, nil, fmt.Errorf(
			"extract: %d field(s) use a natural-language description but no resolver is configured (ADR-0002)",
			CountSemantic(specs))
	}

	if e.deps.Fetcher == nil {
		return receipt.Result{}, nil, fmt.Errorf("extract: fetcher is required")
	}

	a, body, err := e.deps.Fetcher.Capture(ctx, url)
	if err != nil {
		return receipt.Result{}, nil, fmt.Errorf("extract: %w", err)
	}

	// Parse leniently: a non-JSON body is not an executor error, it is a
	// deterministic outcome ("this URL is not JSON"), reported as absent fields.
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		doc = nil
	}

	picked, err := ResolveFields(ctx, doc, specs, e.deps.Resolver, body)
	if err != nil {
		return receipt.Result{}, nil, fmt.Errorf("extract: %w", err)
	}

	value, err := renderPicked(picked, spec, parseFormat(spec))
	if err != nil {
		return receipt.Result{}, nil, fmt.Errorf("extract: %w", err)
	}

	return receipt.Result{
		Value: value,
		Hash:  anchor.HashString(value),
	}, []receipt.Anchor{a}, nil
}

// parseFormat reads spec.format, defaulting to JSON and reporting the error via
// renderPicked's caller when the value is unusable.
func parseFormat(spec map[string]any) string {
	raw, ok := spec["format"]
	if !ok {
		return OutputJSON
	}
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// renderPicked renders a resolved result object in the requested format.
func renderPicked(picked string, spec map[string]any, format string) (string, error) {
	switch format {
	case OutputJSON:
		return picked, nil
	case OutputText:
		// Text mode flattens the result object to one line per label, sorted, so
		// it is stable and readable in a terminal.
		var m map[string]any
		if err := json.Unmarshal([]byte(picked), &m); err != nil {
			return "", fmt.Errorf("render text: %w", err)
		}
		labels := make([]string, 0, len(m))
		for k := range m {
			labels = append(labels, k)
		}
		sort.Strings(labels)

		parts := make([]string, 0, len(labels))
		for _, label := range labels {
			parts = append(parts, renderScalar(m[label]))
		}
		return strings.Join(parts, "\n"), nil
	default:
		return "", fmt.Errorf("unsupported format %q (want %s or %s)", format, OutputJSON, OutputText)
	}
}

// renderExtraction builds the deterministic result string.
func renderExtraction(doc any, fields []string, format string) (string, error) {
	picked := make(map[string]any, len(fields))
	for _, path := range fields {
		picked[path] = lookupPath(doc, path)
	}

	if format == OutputText {
		parts := make([]string, 0, len(fields))
		for _, path := range fields {
			parts = append(parts, renderScalar(picked[path]))
		}
		return strings.Join(parts, "\n"), nil
	}

	// JSON output is canonicalised so the hash is stable regardless of key
	// order in the source document.
	out, err := json.Marshal(picked)
	if err != nil {
		return "", fmt.Errorf("marshal extraction: %w", err)
	}
	return string(out), nil
}

// lookupPath walks a dot-separated path through nested objects.
//
// Supports:
//
//	"price"            top-level key
//	"data.price"       nested object
//	"items.2.name"     array index (numeric segment)
//
// Returns nil when any segment is missing — absence is a valid observation.
func lookupPath(doc any, path string) any {
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return nil
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil
			}
			cur = node[idx]
		default:
			return nil
		}
	}
	return cur
}

// renderScalar renders a scalar for text output, matching the JSON spelling for
// numbers and booleans so both formats agree on values that appear in both.
func renderScalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case bool:
		return strconv.FormatBool(t)
	case float64:
		// Avoid exponent notation for integral values: 200 stays "200".
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}
