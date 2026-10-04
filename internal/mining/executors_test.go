package mining

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// pinnedFetcher returns a Fetcher with a fixed clock, so anchor timestamps are
// reproducible.
func pinnedFetcher(t *testing.T) *anchor.Fetcher {
	t.Helper()
	f := anchor.NewFetcher()
	f.Now = func() time.Time { return time.Unix(1791015800, 0) }
	return f
}

// jsonServer serves body with the given status.
func jsonServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ---------------------------------------------------------------- probe

func TestProbe_Success(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{"ok":true}`)

	ex := &ProbeExecutor{deps: Deps{Fetcher: pinnedFetcher(t)}}
	res, anchors, err := ex.Execute(t.Context(), map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if res.Value != "200" {
		t.Errorf("result value = %q, want \"200\"", res.Value)
	}
	if res.Hash != anchor.HashString("200") {
		t.Errorf("result hash does not match value")
	}
	if len(anchors) != 1 {
		t.Fatalf("expected 1 anchor, got %d", len(anchors))
	}
	if anchors[0].Status != http.StatusOK {
		t.Errorf("anchor status = %d, want 200", anchors[0].Status)
	}
}

// TestProbe_Non2xxIsSuccess pins a deliberate design choice: "the URL returns
// 500" is a valid deterministic result, not an executor failure. Only transport
// errors are errors.
func TestProbe_Non2xxIsSuccess(t *testing.T) {
	for _, code := range []int{400, 404, 500, 503} {
		srv := jsonServer(t, code, "nope")

		ex := &ProbeExecutor{deps: Deps{Fetcher: pinnedFetcher(t)}}
		res, _, err := ex.Execute(t.Context(), map[string]any{"url": srv.URL})
		if err != nil {
			t.Fatalf("status %d: Execute returned error: %v", code, err)
		}
		want := http.StatusText(code) // unused; kept for readability
		_ = want
		if res.Value == "" {
			t.Errorf("status %d: empty result value", code)
		}
	}
}

func TestProbe_TransportFailureIsError(t *testing.T) {
	// Start then immediately close a server to get a refused connection.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	ex := &ProbeExecutor{deps: Deps{Fetcher: pinnedFetcher(t)}}
	if _, _, err := ex.Execute(t.Context(), map[string]any{"url": url}); err == nil {
		t.Error("expected an error for an unreachable URL")
	}
}

func TestProbe_RequiresURL(t *testing.T) {
	ex := &ProbeExecutor{deps: Deps{Fetcher: pinnedFetcher(t)}}
	for _, spec := range []map[string]any{nil, {}, {"url": ""}, {"url": "   "}, {"url": 123}} {
		if _, _, err := ex.Execute(t.Context(), spec); err == nil {
			t.Errorf("expected an error for spec %#v", spec)
		}
	}
}

// --------------------------------------------------------------- extract

func TestExtract_JSONField(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{"data":{"price":42,"symbol":"ABC"}}`)

	ex := &ExtractExecutor{deps: Deps{Fetcher: pinnedFetcher(t)}}
	res, anchors, err := ex.Execute(t.Context(), map[string]any{
		"url":    srv.URL,
		"fields": []any{"data.price", "data.symbol"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	want := `{"data.price":42,"data.symbol":"ABC"}`
	if res.Value != want {
		t.Errorf("value = %s, want %s", res.Value, want)
	}
	if len(anchors) != 1 {
		t.Fatalf("expected 1 anchor, got %d", len(anchors))
	}
}

// TestExtract_MissingFieldIsAnObservation: an absent field is a deterministic
// result (null), not an error. Re-running yields the same answer.
func TestExtract_MissingFieldIsAnObservation(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{"present":1}`)

	ex := &ExtractExecutor{deps: Deps{Fetcher: pinnedFetcher(t)}}
	res, _, err := ex.Execute(t.Context(), map[string]any{
		"url":    srv.URL,
		"fields": []any{"absent"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Value != `{"absent":null}` {
		t.Errorf("value = %s, want {\"absent\":null}", res.Value)
	}
}

func TestExtract_TextFormat(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{"a":"hello ","b":"world"}`)

	ex := &ExtractExecutor{deps: Deps{Fetcher: pinnedFetcher(t)}}
	res, _, err := ex.Execute(t.Context(), map[string]any{
		"url":    srv.URL,
		"fields": []any{"a", "b"},
		"format": "text",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Values are trimmed so trailing whitespace in the source does not change
	// the result string.
	if res.Value != "hello\nworld" {
		t.Errorf("value = %q, want %q", res.Value, "hello\nworld")
	}
}

// TestExtract_RequiresFields is the A2 guard at the executor level: without
// named fields this would degenerate into "fetch and hash".
func TestExtract_RequiresFields(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{"a":1}`)

	ex := &ExtractExecutor{deps: Deps{Fetcher: pinnedFetcher(t)}}
	for _, spec := range []map[string]any{
		{"url": srv.URL},
		{"url": srv.URL, "fields": []any{}},
	} {
		if _, _, err := ex.Execute(t.Context(), spec); err == nil {
			t.Errorf("expected an error for spec %#v", spec)
		}
	}
}

func TestExtract_RejectsBadFormat(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{"a":1}`)

	ex := &ExtractExecutor{deps: Deps{Fetcher: pinnedFetcher(t)}}
	_, _, err := ex.Execute(t.Context(), map[string]any{
		"url":    srv.URL,
		"fields": []any{"a"},
		"format": "yaml",
	})
	if err == nil {
		t.Error("expected an error for an unsupported format")
	}
}

func TestLookupPath(t *testing.T) {
	doc := map[string]any{
		"price": 1,
		"data":  map[string]any{"symbol": "ABC"},
		"items": []any{
			map[string]any{"n": "first"},
			map[string]any{"n": "second"},
		},
	}

	cases := []struct {
		path string
		want any
	}{
		{"price", 1},
		{"data.symbol", "ABC"},
		{"items.0.n", "first"},
		{"items.1.n", "second"},
		{"data.missing", nil},
		{"items.9.n", nil},
		{"price.deeper", nil}, // scalar cannot be traversed
	}

	for _, c := range cases {
		if got := lookupPath(doc, c.path); got != c.want {
			t.Errorf("lookupPath(%q) = %#v, want %#v", c.path, got, c.want)
		}
	}
}

// --------------------------------------------------------------- compute

func TestCompute_Hash(t *testing.T) {
	ex := &ComputeExecutor{}
	res, anchors, err := ex.Execute(t.Context(), map[string]any{
		"op":    OpHash,
		"input": "hello",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// sha256("hello")
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if res.Value != want {
		t.Errorf("value = %s, want %s", res.Value, want)
	}
	if len(anchors) != 1 {
		t.Fatalf("expected 1 anchor, got %d", len(anchors))
	}
	// compute has no external source, so its anchor is explicitly synthetic.
	if anchors[0].URL != "inline" {
		t.Errorf("compute anchor URL = %q, want \"inline\"", anchors[0].URL)
	}
}

func TestCompute_Concat(t *testing.T) {
	ex := &ComputeExecutor{}
	res, _, err := ex.Execute(t.Context(), map[string]any{
		"op":    OpConcat,
		"parts": []any{"a", "b", "c"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Value != "abc" {
		t.Errorf("value = %q, want \"abc\"", res.Value)
	}
}

func TestCompute_SortJSON(t *testing.T) {
	ex := &ComputeExecutor{}
	res, _, err := ex.Execute(t.Context(), map[string]any{
		"op":    OpSortJSON,
		"input": `{"z":1,"a":2,"m":3}`,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Value != `{"a":2,"m":3,"z":1}` {
		t.Errorf("value = %s, want {\"a\":2,\"m\":3,\"z\":1}", res.Value)
	}
}

func TestCompute_RejectsUnknownOp(t *testing.T) {
	ex := &ComputeExecutor{}
	_, _, err := ex.Execute(t.Context(), map[string]any{
		"op":    "summarize", // a generation-type op, deliberately rejected
		"input": "x",
	})
	if err == nil {
		t.Error("expected an error for an unsupported op")
	}
}

// TestCompute_IsDeterministicResult: the same spec must yield the same result
// and the same anchor hash, regardless of map iteration order.
func TestCompute_IsDeterministicResult(t *testing.T) {
	ex := &ComputeExecutor{}

	spec := func() map[string]any {
		return map[string]any{
			"op":    OpHash,
			"input": "stable",
			"extra": "value",
			"n":     7,
		}
	}

	firstRes, firstAnchors, err := ex.Execute(t.Context(), spec())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for i := 0; i < 100; i++ {
		res, anchors, err := ex.Execute(t.Context(), spec())
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Value != firstRes.Value || res.Hash != firstRes.Hash {
			t.Fatalf("iteration %d: result drifted", i)
		}
		if anchors[0].ContentHash != firstAnchors[0].ContentHash {
			t.Fatalf("iteration %d: anchor hash drifted — spec hashing is not order-stable", i)
		}
	}
}

// ------------------------------------------------------------------- Run

func TestRun_DispatchesToCorrectExecutor(t *testing.T) {
	srv := jsonServer(t, http.StatusOK, `{"a":1}`)
	d := Deps{Fetcher: pinnedFetcher(t)}

	res, anchors, err := Run(context.Background(), d, receipt.TaskProbe, map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatalf("Run probe: %v", err)
	}
	if res.Value != "200" {
		t.Errorf("probe via Run: value = %q", res.Value)
	}
	if len(anchors) != 1 {
		t.Errorf("probe via Run: anchors = %d", len(anchors))
	}

	res, _, err = Run(context.Background(), d, receipt.TaskCompute, map[string]any{
		"op": OpConcat, "parts": []any{"x", "y"},
	})
	if err != nil {
		t.Fatalf("Run compute: %v", err)
	}
	if res.Value != "xy" {
		t.Errorf("compute via Run: value = %q", res.Value)
	}
}

// TestSplitTopLevel guards the JSON key sorter's parser against commas inside
// nested values and string literals.
func TestSplitTopLevel(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{`"a":1,"b":2`, 2},
		{`"a":{"x":1,"y":2},"b":3`, 2},
		{`"a":"x,y","b":2`, 2},
		{`"a":[1,2,3],"b":4`, 2},
		{`"a":1`, 1},
		{``, 0},
	}

	for _, c := range cases {
		got := splitTopLevel(c.in)
		if len(got) != c.want {
			t.Errorf("splitTopLevel(%q) returned %d parts, want %d (%#v)", c.in, len(got), c.want, got)
		}
	}
}
