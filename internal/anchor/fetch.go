// Package anchor handles the externally re-fetchable evidence that makes a
// receipt verifiable.
//
// A receipt is worthless without an anchor: without one it is just an agent
// claiming it did work. An anchor records *what* was fetched and *when*, so
// that anyone can re-fetch and compare (MVP.md §4.1, §5.4).
//
// Two distinct operations live here:
//
//   - Capture: fetch a URL and record the anchor.
//   - Verify:  re-fetch and confirm the content still matches.
//
// Verify is deliberately time-bounded by the caller (MVP.md §5.4): content
// changes, so an anchor is only expected to match within the epoch window.
package anchor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/receipt"
)

// DefaultMaxBytes caps how much of a response body is read. Anchors must stay
// small: the receipt carries only hashes and metadata, and the relay must not
// be asked to move large payloads (MVP.md §4.1).
const DefaultMaxBytes = 4 << 20 // 4 MiB

// DefaultTimeout bounds a single fetch.
const DefaultTimeout = 15 * time.Second

// Fetcher captures and verifies anchors over HTTP.
type Fetcher struct {
	// Client is the HTTP client used for fetches. When nil, a client with
	// DefaultTimeout is used.
	Client *http.Client

	// MaxBytes caps the response body read. Zero means DefaultMaxBytes.
	MaxBytes int64

	// Now is injected so tests can pin timestamps. When nil, time.Now is used.
	Now func() time.Time
}

// NewFetcher returns a Fetcher with sane defaults.
func NewFetcher() *Fetcher {
	return &Fetcher{
		Client:   &http.Client{Timeout: DefaultTimeout},
		MaxBytes: DefaultMaxBytes,
		Now:      time.Now,
	}
}

func (f *Fetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return &http.Client{Timeout: DefaultTimeout}
}

func (f *Fetcher) maxBytes() int64 {
	if f.MaxBytes > 0 {
		return f.MaxBytes
	}
	return DefaultMaxBytes
}

func (f *Fetcher) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// ErrTooLarge is returned when a response body exceeds the configured cap.
var ErrTooLarge = errors.New("anchor: response body exceeds limit")

// Capture fetches url, hashes the body, and returns the anchor plus the raw
// body. The body is returned because executors need it to compute a
// deterministic result; only the hash goes into the receipt.
//
// A non-2xx response is NOT an error: an unreachable URL is a legitimate probe
// result (`result.value` carries the status code). Only transport failures and
// oversized bodies are errors.
func (f *Fetcher) Capture(ctx context.Context, url string) (receipt.Anchor, []byte, error) {
	if strings.TrimSpace(url) == "" {
		return receipt.Anchor{}, nil, errors.New("anchor: url is empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return receipt.Anchor{}, nil, fmt.Errorf("anchor: build request: %w", err)
	}
	// Identify the client honestly; some sites reject empty User-Agent.
	req.Header.Set("User-Agent", "relayfirst/0.2 (+proof-of-agent-work)")

	resp, err := f.client().Do(req)
	if err != nil {
		return receipt.Anchor{}, nil, fmt.Errorf("anchor: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes()+1))
	if err != nil {
		return receipt.Anchor{}, nil, fmt.Errorf("anchor: read body: %w", err)
	}
	if int64(len(body)) > f.maxBytes() {
		return receipt.Anchor{}, nil, fmt.Errorf("%w: %s", ErrTooLarge, url)
	}

	sum := sha256.Sum256(body)
	return receipt.Anchor{
		URL:         url,
		ContentHash: HashHex(sum[:]),
		FetchedAt:   f.now().Unix(),
		Status:      resp.StatusCode,
		Bytes:       uint64(len(body)),
	}, body, nil
}

// Verify re-fetches the anchor's URL and reports whether the content still
// matches its recorded hash.
//
// A false result with a nil error means "content changed" — which is expected
// once the epoch window has passed (MVP.md §5.4) — not that anything is broken.
func (f *Fetcher) Verify(ctx context.Context, a receipt.Anchor) (bool, error) {
	fresh, _, err := f.Capture(ctx, a.URL)
	if err != nil {
		return false, err
	}
	return fresh.ContentHash == a.ContentHash, nil
}

// HashHex renders a digest as "sha256:<hex>", the form used throughout the
// receipt (`contentHash`, `result.hash`, `specHash`).
//
// The "sha256:" prefix is part of the wire format: receipt.checkHash expects it
// and the TypeScript side must produce the same string.
func HashHex(sum []byte) string {
	return "sha256:" + hex.EncodeToString(sum)
}

// HashBytes hashes b and renders it in the receipt's hash format.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return HashHex(sum[:])
}

// HashString hashes s and renders it in the receipt's hash format.
func HashString(s string) string { return HashBytes([]byte(s)) }
