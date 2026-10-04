package mining

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// stubResolver is a deterministic in-process Resolver.
//
// It records how many times it was called, which lets tests assert the cost
// claim: a positional-only spec must NOT invoke the resolver, while a semantic
// spec must.
type stubResolver struct {
	answers map[string]string
	calls   int
	err     error
}

func (r *stubResolver) Resolve(_ context.Context, description string, _ []byte) (string, bool, error) {
	r.calls++
	if r.err != nil {
		return "", false, r.err
	}
	v, ok := r.answers[description]
	if !ok {
		return "", false, nil
	}
	return v, true, nil
}

// ParseFieldSpecs -------------------------------------------------------

func TestParseFieldSpecs_BothForms(t *testing.T) {
	raw := []any{
		"data.price",
		map[string]any{"path": "data.symbol"},
		map[string]any{"semantic": "the main product price"},
	}

	specs, err := ParseFieldSpecs(raw)
	if err != nil {
		t.Fatalf("ParseFieldSpecs: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("got %d specs, want 3", len(specs))
	}

	if specs[0].Path != "data.price" || specs[0].Semantic != "" {
		t.Errorf("spec 0 = %+v", specs[0])
	}
	if specs[1].Path != "data.symbol" {
		t.Errorf("spec 1 = %+v", specs[1])
	}
	if specs[2].Semantic != "the main product price" || specs[2].Path != "" {
		t.Errorf("spec 2 = %+v", specs[2])
	}
}

func TestParseFieldSpecs_RejectsBadInput(t *testing.T) {
	cases := map[string]any{
		"both path and semantic": []any{map[string]any{"path": "a", "semantic": "b"}},
		"neither":                []any{map[string]any{}},
		"empty string":           []any{""},
		"wrong type in field":    []any{123},
		"wrong type for path":    []any{map[string]any{"path": 1}},
		"wrong type for sem":     []any{map[string]any{"semantic": true}},
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseFieldSpecs(raw); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestParseFieldSpecs_NilIsEmpty(t *testing.T) {
	specs, err := ParseFieldSpecs(nil)
	if err != nil {
		t.Fatalf("ParseFieldSpecs(nil): %v", err)
	}
	if len(specs) != 0 {
		t.Errorf("got %d specs, want 0", len(specs))
	}
}

func TestCountSemantic(t *testing.T) {
	specs := []FieldSpec{
		{Path: "a"},
		{Semantic: "b"},
		{Semantic: "c"},
	}
	if got := CountSemantic(specs); got != 2 {
		t.Errorf("CountSemantic = %d, want 2", got)
	}
	if got := CountSemantic(nil); got != 0 {
		t.Errorf("CountSemantic(nil) = %d, want 0", got)
	}
}

// NormalizeValue: the determinism guarantee ----------------------------

// TestNormalizeValue_MakesEquivalentFormsAgree is the property that keeps
// verification binary. Two honest runs seeing "$1,234.50" and "1234.5" describe
// the same answer and must hash identically.
func TestNormalizeValue_MakesEquivalentFormsAgree(t *testing.T) {
	groups := [][]string{
		{"1234.5", "1,234.5", "$1,234.50", " 1234.5 "},
		{"42", "42.0", "$42", "042"},
		{"-7", "-7.", "-7.0"},
		{"0.5", ".50", "$0.5", "0.50"},
		{"hello", "  hello  "},
	}

	for _, group := range groups {
		var first string
		for i, in := range group {
			got, err := NormalizeValue(in)
			if err != nil {
				t.Fatalf("NormalizeValue(%q): %v", in, err)
			}
			if i == 0 {
				first = got
				continue
			}
			if got != first {
				t.Errorf("NormalizeValue(%q) = %q but NormalizeValue(%q) = %q; equivalent forms must agree",
					in, got, group[0], first)
			}
		}
	}
}

// TestNormalizeValue_LeavesNonNumericAlone: the numeric rule is deliberately
// narrow, so text is not mangled into a number.
func TestNormalizeValue_LeavesNonNumericAlone(t *testing.T) {
	cases := map[string]string{
		"1-2 business days": "1-2 business days",
		"about 5 items":     "about 5 items",
		"v2.1.0":            "v2.1.0",
		"1.2.3":             "1.2.3",
		"12abc":             "12abc",
		"":                  "",
		"   ":               "",
	}

	for in, want := range cases {
		got, err := NormalizeValue(in)
		if err != nil {
			t.Fatalf("NormalizeValue(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("NormalizeValue(%q) = %q, want %q (must not mangle non-numeric text)", in, got, want)
		}
	}
}

func TestNormalizeValue_IsDeterministic(t *testing.T) {
	in := "$1,234.50"
	first, err := NormalizeValue(in)
	if err != nil {
		t.Fatalf("NormalizeValue: %v", err)
	}
	for i := 0; i < 50; i++ {
		again, err := NormalizeValue(in)
		if err != nil {
			t.Fatalf("NormalizeValue: %v", err)
		}
		if again != first {
			t.Fatalf("normalization drifted at iteration %d", i)
		}
	}
}

// ResolveFields ---------------------------------------------------------

func TestResolveFields_MixedSpecsOneDocument(t *testing.T) {
	doc := map[string]any{"data": map[string]any{"symbol": "ABC"}}
	body, _ := json.Marshal(doc)

	r := &stubResolver{answers: map[string]string{
		"the main product price": "$1,234.50",
	}}

	specs := []FieldSpec{
		{Path: "data.symbol"},
		{Semantic: "the main product price"},
	}

	got, err := ResolveFields(context.Background(), doc, specs, r, body)
	if err != nil {
		t.Fatalf("ResolveFields: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("result is not JSON: %v (%s)", err, got)
	}

	// Positional field resolved mechanically.
	if m["data.symbol"] != "ABC" {
		t.Errorf("data.symbol = %v, want ABC", m["data.symbol"])
	}
	// Semantic field resolved and normalized: "$1,234.50" collapses to "1234.5".
	if m["the main product price"] != "1234.5" {
		t.Errorf("semantic field = %v, want the normalized 1234.5", m["the main product price"])
	}
}

// TestResolveFields_MissingSemanticIsNull: absence is an observation, not a failure.
func TestResolveFields_MissingSemanticIsNull(t *testing.T) {
	doc := map[string]any{}
	r := &stubResolver{answers: map[string]string{}} // answers nothing

	specs := []FieldSpec{{Semantic: "something absent"}}
	got, err := ResolveFields(context.Background(), doc, specs, r, []byte("{}"))
	if err != nil {
		t.Fatalf("ResolveFields: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	v, present := m["something absent"]
	if !present {
		t.Fatal("the label must still appear in the result")
	}
	if v != nil {
		t.Errorf("absent field = %v, want null", v)
	}
}

func TestResolveFields_RequiresResolverForSemantic(t *testing.T) {
	specs := []FieldSpec{{Semantic: "needs a model"}}
	if _, err := ResolveFields(context.Background(), nil, specs, nil, nil); err == nil {
		t.Error("a semantic field with no resolver must fail rather than guess")
	}
}

func TestResolveFields_PropagatesResolverError(t *testing.T) {
	r := &stubResolver{err: errors.New("model unavailable")}
	specs := []FieldSpec{{Semantic: "anything"}}

	_, err := ResolveFields(context.Background(), nil, specs, r, nil)
	if err == nil {
		t.Fatal("expected the resolver error to propagate")
	}
	if !strings.Contains(err.Error(), "model unavailable") {
		t.Errorf("error should wrap the cause, got %v", err)
	}
}

// TestResolveFields_IsByteStable: the same inputs must produce the same string,
// because that string is hashed into the signed receipt.
func TestResolveFields_IsByteStable(t *testing.T) {
	doc := map[string]any{"data": map[string]any{"symbol": "ABC"}}
	body, _ := json.Marshal(doc)
	specs := []FieldSpec{
		{Path: "data.symbol"},
		{Semantic: "the price"},
	}

	var first string
	for i := 0; i < 25; i++ {
		r := &stubResolver{answers: map[string]string{"the price": "$9.99"}}
		got, err := ResolveFields(context.Background(), doc, specs, r, body)
		if err != nil {
			t.Fatalf("ResolveFields: %v", err)
		}
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("result drifted at iteration %d:\n  %s\n  %s", i, got, first)
		}
	}
}

// ExtractExecutor cost behaviour ---------------------------------------

// TestExtract_PositionalSpecDoesNotCallResolver is the cost claim in code.
//
// A purely positional spec is resolved mechanically, so it must NOT reach the
// resolver. If this ever regresses, every trivial task would burn tokens and the
// economics would invert.
func TestExtract_PositionalSpecDoesNotCallResolver(t *testing.T) {
	srv := jsonServer(t, 200, `{"data":{"symbol":"ABC"}}`)

	r := &stubResolver{answers: map[string]string{}}
	ex := &ExtractExecutor{deps: Deps{Fetcher: pinnedFetcher(t), Resolver: r}}

	res, _, err := ex.Execute(t.Context(), map[string]any{
		"url":    srv.URL,
		"fields": []any{"data.symbol"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 0 {
		t.Errorf("resolver called %d times for a positional spec, want 0 (mechanical work must stay free)", r.calls)
	}
	if !strings.Contains(res.Value, "ABC") {
		t.Errorf("value = %s", res.Value)
	}
}

// TestExtract_SemanticSpecCallsResolver: the converse — semantic work does cost.
func TestExtract_SemanticSpecCallsResolver(t *testing.T) {
	srv := jsonServer(t, 200, `<html><body>Price: $1,234.50</body></html>`)

	r := &stubResolver{answers: map[string]string{"the main product price": "$1,234.50"}}
	ex := &ExtractExecutor{deps: Deps{Fetcher: pinnedFetcher(t), Resolver: r}}

	res, _, err := ex.Execute(t.Context(), map[string]any{
		"url":    srv.URL,
		"fields": []any{map[string]any{"semantic": "the main product price"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.calls != 1 {
		t.Errorf("resolver called %d times, want 1", r.calls)
	}
	if !strings.Contains(res.Value, `"1234.5"`) {
		t.Errorf("value = %s, want the normalized price 1234.5", res.Value)
	}
}

// TestExtract_SemanticWithoutResolverFailsEarly: a configuration error must be
// reported before a wasted network call.
func TestExtract_SemanticWithoutResolverFailsEarly(t *testing.T) {
	srv := jsonServer(t, 200, `{}`)

	ex := &ExtractExecutor{deps: Deps{Fetcher: pinnedFetcher(t)}}
	_, _, err := ex.Execute(t.Context(), map[string]any{
		"url":    srv.URL,
		"fields": []any{map[string]any{"semantic": "anything"}},
	})
	if err == nil {
		t.Fatal("expected an error when a semantic field has no resolver")
	}
	if !strings.Contains(err.Error(), "no resolver is configured") {
		t.Errorf("error should name the cause, got %v", err)
	}
}

// TestExtract_StillRejectsEmptyFields guards §5.0 at the executor boundary.
func TestExtract_StillRejectsEmptyFields(t *testing.T) {
	srv := jsonServer(t, 200, `{"a":1}`)
	ex := &ExtractExecutor{deps: Deps{Fetcher: pinnedFetcher(t), Resolver: &stubResolver{}}}

	for _, spec := range []map[string]any{
		{"url": srv.URL},
		{"url": srv.URL, "fields": []any{}},
	} {
		if _, _, err := ex.Execute(t.Context(), spec); err == nil {
			t.Errorf("spec %#v must be rejected: without named fields this is 'fetch and hash'", spec)
		}
	}
}

// TestExtract_SemanticResultIsVerifiableShape: the semantic path must produce the
// same receipt shape as the positional path, so downstream scoring is unchanged.
func TestExtract_SemanticResultIsVerifiableShape(t *testing.T) {
	srv := jsonServer(t, 200, `Price: 42`)

	r := &stubResolver{answers: map[string]string{"the price": "42"}}
	ex := &ExtractExecutor{deps: Deps{Fetcher: pinnedFetcher(t), Resolver: r}}

	res, anchors, err := ex.Execute(t.Context(), map[string]any{
		"url":    srv.URL,
		"fields": []any{map[string]any{"semantic": "the price"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if res.Value == "" {
		t.Error("result value is empty")
	}
	if res.Hash == "" {
		t.Error("result hash is empty — the result would not be verifiable")
	}
	if len(anchors) == 0 {
		t.Error("semantic extraction must still carry an anchor")
	}

	// The receipt validator accepts the shape.
	rec := receipt.Receipt{
		Schema:    receipt.Schema,
		ReceiptID: "0x01",
		AgentID:   "agent:eip155:8453:0x0000000000000000000000000000000000000001",
		Epoch:     1,
		Task: receipt.Task{
			Type:          receipt.TaskExtract,
			Spec:          map[string]any{"url": srv.URL},
			SpecHash:      "sha256:" + strings.Repeat("3d", 32),
			SelfGenerated: true,
		},
		Work:    receipt.Work{Provider: "openai", StartedAt: 1, FinishedAt: 2},
		Result:  res,
		Anchors: anchors,
	}
	if err := rec.ValidateStructure(); err != nil {
		t.Errorf("semantic extract produced a receipt the validator rejects: %v", err)
	}
}
