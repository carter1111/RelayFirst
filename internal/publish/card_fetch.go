// Client-side agent-card discovery (S9-3).
//
// # Why fetching and verifying are one function
//
// A caller that fetches a card and forgets to verify it has a document that looks
// authoritative and is worth nothing. That is the single most likely misuse of a
// directory endpoint, so the API does not offer the two steps separately: you ask
// for a card and you either get a verified one or an error. There is no return
// value that a caller can mistake for "the card, probably fine".
//
// # What "verified" means here, and what it does not
//
// It means the proof is valid over the exact bytes received, and that the proof's
// identity matches the identity the card declares. It does NOT mean the agent is
// honest, that the endpoint answers, or that the node served the newest card. A
// directory is a place cards are found, not an authority on whether they are true.
package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	a2asdk "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/relayfirst/relayfirst/internal/agentid"
)

// DefaultCardFetchTimeout bounds one directory request.
//
// A directory is a convenience, not a dependency, so a slow or dead node must
// cost a bounded delay. The same reasoning as the relay fan-out timeout.
const DefaultCardFetchTimeout = 10 * time.Second

// CardFetcher retrieves and verifies agent cards from a node's directory.
type CardFetcher struct {
	// BaseURL is the node's root, e.g. "http://localhost:8080".
	BaseURL string

	// Client is the HTTP client. Nil uses a client with DefaultCardFetchTimeout.
	Client *http.Client
}

// NewCardFetcher returns a fetcher for a node.
func NewCardFetcher(baseURL string) *CardFetcher {
	return &CardFetcher{BaseURL: strings.TrimRight(baseURL, "/")}
}

func (f *CardFetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return &http.Client{Timeout: DefaultCardFetchTimeout}
}

// cardEnvelope is the node's response shape.
//
// Card and Proof are RawMessage so the exact bytes the node served are what gets
// verified. Decoding into a struct and re-marshalling would produce different
// bytes and invalidate a good proof — the same trap the node avoids on its side.
type cardEnvelope struct {
	AgentID  string          `json:"agentId"`
	Card     json.RawMessage `json:"card"`
	Proof    json.RawMessage `json:"proof"`
	Verified bool            `json:"verified"`
	Note     string          `json:"note"`
}

// Fetch retrieves one agent's card and verifies it.
//
// The returned card is the parsed structure, for convenient reading; the proof was
// checked against the raw bytes, which are not returned because a caller that had
// them might be tempted to re-verify with a different serializer.
func (f *CardFetcher) Fetch(ctx context.Context, agentID string) (*a2asdk.AgentCard, error) {
	if _, err := agentid.Parse(agentID); err != nil {
		return nil, fmt.Errorf("publish: %w", err)
	}

	endpoint := f.BaseURL + "/agents/" + url.PathEscape(agentID)
	env, err := f.get(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	// A node that claims it verified is lying about its own capability: it has no
	// way to check a signature (MVP.md §7.1). Treat that as a reason to distrust
	// the response rather than a helpful signal.
	if env.Verified {
		return nil, fmt.Errorf(
			"publish: node claims it verified the card at %s, which it cannot do; "+
				"treat this directory as untrusted", endpoint)
	}

	if env.AgentID != agentID {
		return nil, fmt.Errorf(
			"publish: node returned a card for %q when asked for %q", env.AgentID, agentID)
	}

	var proof CardProof
	if err := json.Unmarshal(env.Proof, &proof); err != nil {
		return nil, fmt.Errorf("publish: node returned a malformed proof: %w", err)
	}

	var card a2asdk.AgentCard
	if err := json.Unmarshal(env.Card, &card); err != nil {
		return nil, fmt.Errorf("publish: node returned a malformed card: %w", err)
	}

	// The verification is over the bytes the node served, and checks that the
	// proof and the card agree on the identity.
	if err := VerifyCard(&card, env.Card, proof); err != nil {
		return nil, fmt.Errorf("publish: card from %s failed verification: %w", endpoint, err)
	}
	return &card, nil
}

// List retrieves and verifies every card in the directory.
//
// # Why one bad card does not fail the listing
//
// A directory is shared: any agent can publish anything. If a single unverifiable
// card aborted the listing, one bad actor could hide every other agent from a
// reader. So bad cards are skipped and reported alongside the good ones, and the
// caller decides. That is also why the return is a struct rather than a slice —
// the rejected entries are information a caller needs, not noise to discard.
func (f *CardFetcher) List(ctx context.Context, limit int) (*CardListing, error) {
	endpoint := f.BaseURL + "/agents"
	if limit > 0 {
		endpoint += fmt.Sprintf("?limit=%d", limit)
	}

	raw, err := f.getRaw(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var listing struct {
		Count int            `json:"count"`
		Cards []cardEnvelope `json:"cards"`
		Note  string         `json:"note"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		return nil, fmt.Errorf("publish: node returned a malformed directory listing: %w", err)
	}

	out := &CardListing{Note: listing.Note}
	for _, env := range listing.Cards {
		if env.Verified {
			out.Rejected = append(out.Rejected, RejectedCard{
				AgentID: env.AgentID,
				Reason:  "node claimed to verify a card, which it cannot do",
			})
			continue
		}
		var proof CardProof
		if err := json.Unmarshal(env.Proof, &proof); err != nil {
			out.Rejected = append(out.Rejected, RejectedCard{
				AgentID: env.AgentID,
				Reason:  "malformed proof: " + err.Error(),
			})
			continue
		}
		var card a2asdk.AgentCard
		if err := json.Unmarshal(env.Card, &card); err != nil {
			out.Rejected = append(out.Rejected, RejectedCard{
				AgentID: env.AgentID,
				Reason:  "malformed card: " + err.Error(),
			})
			continue
		}
		if err := VerifyCard(&card, env.Card, proof); err != nil {
			out.Rejected = append(out.Rejected, RejectedCard{
				AgentID: env.AgentID,
				Reason:  err.Error(),
			})
			continue
		}
		out.Cards = append(out.Cards, VerifiedCard{Card: &card, AgentID: env.AgentID})
	}
	return out, nil
}

// CardListing is the result of listing a directory.
type CardListing struct {
	// Cards are the entries whose proofs verified.
	Cards []VerifiedCard

	// Rejected are the entries that did not, with the reason. They are returned
	// rather than dropped so a caller can log or display them.
	Rejected []RejectedCard

	// Note is the node's own description of the directory.
	Note string
}

// VerifiedCard is a card whose proof checked out.
type VerifiedCard struct {
	Card    *a2asdk.AgentCard
	AgentID string
}

// RejectedCard is an entry that failed verification.
type RejectedCard struct {
	AgentID string
	Reason  string
}

func (f *CardFetcher) get(ctx context.Context, endpoint string) (cardEnvelope, error) {
	raw, err := f.getRaw(ctx, endpoint)
	if err != nil {
		return cardEnvelope{}, err
	}
	var env cardEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return cardEnvelope{}, fmt.Errorf("publish: node returned malformed JSON: %w", err)
	}
	return env, nil
}

// getRaw performs the request and returns the body, capped so a hostile node
// cannot make the client read an unbounded response.
func (f *CardFetcher) getRaw(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("publish: build request: %w", err)
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("publish: fetch %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxCardBytes))
	if err != nil {
		return nil, fmt.Errorf("publish: read %s: %w", endpoint, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("publish: %s returned %d: %s",
			endpoint, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// MaxCardBytes caps a card response.
//
// A card is a small JSON document — a few KB with a generous skill list. The cap
// is far above that and far below anything that would trouble a client, so a node
// cannot use an oversized response as a denial-of-service.
const MaxCardBytes = 1 << 20
