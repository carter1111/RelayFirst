// Package scoring turns receipts into points.
//
// The load-bearing idea (MVP.md §5.2) is that the dedup ledger is keyed by the
// *artifact* a task observed, not by the agent that observed it. External new
// content is finite and cannot be fabricated, so the second agent to submit the
// same artifact contributed no new information and earns nothing. That single
// rule is what makes farming unprofitable.
//
// Two invariants are enforced structurally rather than by convention:
//
//	A5  points are non-transferable. This package exposes no way to move points
//	    between agents; see points.go.
//	A6  the dedup ledger is global. Keys are artifact keys only; the agent id is
//	    stored as attribution, never as part of the key.
package scoring

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// ArtifactKey identifies the external content a task observed.
//
// Per MVP.md §5.2:
//
//	artifactKey = sha256(task.type + task.spec.url + contentHash)
//
// The url and contentHash come from the receipt's first anchor. The task type
// is included so that the same url observed by a probe and by an extract is
// treated as two distinct observations: they are different questions with
// different answers, and neither should shadow the other.
func ArtifactKey(r *receipt.Receipt) (string, error) {
	if r == nil {
		return "", fmt.Errorf("scoring: nil receipt")
	}
	if !r.Task.Type.Accepted() {
		return "", fmt.Errorf("scoring: task type %q is not accepted (allowed: probe/extract/compute)", r.Task.Type)
	}

	url, err := SpecURL(r)
	if err != nil {
		return "", err
	}

	if len(r.Anchors) == 0 {
		return "", fmt.Errorf("scoring: receipt has no anchors")
	}
	// Anchor[0] is the primary evidence; additional anchors are supporting
	// material and do not change the artifact identity.
	contentHash := strings.TrimSpace(r.Anchors[0].ContentHash)
	if contentHash == "" {
		return "", fmt.Errorf("scoring: anchor[0].contentHash is empty")
	}

	h := sha256.New()
	h.Write([]byte(string(r.Task.Type)))
	h.Write([]byte{0x00}) // length-independent separator between fields
	h.Write([]byte(url))
	h.Write([]byte{0x00})
	h.Write([]byte(contentHash))

	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// SpecURL extracts the url a task operated on.
//
// Compute tasks touch no network, so they have no url; their spec instead
// includes the operation itself so that distinct computations do not collide.
// The receipt stores the spec verbatim, so this stays deterministic.
func SpecURL(r *receipt.Receipt) (string, error) {
	if r.Task.Spec == nil {
		return "", fmt.Errorf("scoring: task.spec is nil")
	}

	if raw, ok := r.Task.Spec["url"]; ok {
		s, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("scoring: spec.url must be a string, got %T", raw)
		}
		if strings.TrimSpace(s) == "" {
			return "", fmt.Errorf("scoring: spec.url is empty")
		}
		return s, nil
	}

	// Compute tasks: derive a stable identity from the operation. Two compute
	// receipts for the same op+input are genuinely the same artifact, which is
	// exactly the behaviour the ledger needs.
	if op, ok := r.Task.Spec["op"]; ok {
		opStr, ok := op.(string)
		if !ok {
			return "", fmt.Errorf("scoring: spec.op must be a string, got %T", op)
		}
		var b strings.Builder
		b.WriteString("compute:")
		b.WriteString(opStr)
		for _, field := range []string{"input", "parts"} {
			if v, ok := r.Task.Spec[field]; ok {
				fmt.Fprintf(&b, "|%s=%v", field, v)
			}
		}
		return b.String(), nil
	}

	// Alternatively the spec hash pins the task uniquely when no url exists.
	if h := strings.TrimSpace(r.Task.SpecHash); h != "" {
		return "spechash:" + h, nil
	}

	return "", fmt.Errorf("scoring: spec has neither url nor op nor specHash, so the artifact cannot be identified")
}

// Domain returns the host of the primary anchor's url, used by the diversity
// factor (MVP.md §5.3). An unparseable or absent url yields "" — callers treat
// that as "no domain signal" rather than as an error.
func Domain(r *receipt.Receipt) string {
	if r == nil || len(r.Anchors) == 0 {
		return ""
	}
	return DomainOfURL(r.Anchors[0].URL)
}

// DomainOfURL extracts the lowercase host from a URL, tolerating the synthetic
// "inline" anchor used by compute tasks (which yields "").
func DomainOfURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || s == "inline" {
		return ""
	}

	// Strip scheme.
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// Strip userinfo.
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	// Strip path / query / fragment.
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	// Strip port.
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}
