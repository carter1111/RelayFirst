package mining

import (
	"context"
	"fmt"
	"strconv"

	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

// ProbeExecutor checks whether a URL is reachable and reports the status code.
//
// Ground truth: an independent prober asking the same URL at nearly the same
// time gets the same status code. That is why the result is the status as a
// string, not a free-form judgement (MVP.md §5.0).
//
// A 4xx/5xx is a *successful probe*, not an error: "the URL returns 500" is a
// perfectly good deterministic result. Only transport failure is an error.
type ProbeExecutor struct{ deps Deps }

// Type reports the task type this executor handles.
func (e *ProbeExecutor) Type() receipt.TaskType { return receipt.TaskProbe }

// Execute fetches spec.url and reports its HTTP status code.
func (e *ProbeExecutor) Execute(ctx context.Context, spec map[string]any) (receipt.Result, []receipt.Anchor, error) {
	url, err := specString(spec, "url")
	if err != nil {
		return receipt.Result{}, nil, fmt.Errorf("probe: %w", err)
	}
	if e.deps.Fetcher == nil {
		return receipt.Result{}, nil, fmt.Errorf("probe: fetcher is required")
	}

	a, _, err := e.deps.Fetcher.Capture(ctx, url)
	if err != nil {
		return receipt.Result{}, nil, fmt.Errorf("probe: %w", err)
	}

	value := strconv.Itoa(a.Status)

	return receipt.Result{
		Value: value,
		Hash:  anchor.HashString(value),
	}, []receipt.Anchor{a}, nil
}
