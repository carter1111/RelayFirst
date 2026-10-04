package node

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// Agent Card directory endpoints (S9-3, MVP.md §7.3).
//
// # What the node does here, and what it refuses to do
//
// It stores published cards and serves them back. It does NOT verify the proof,
// and that is the design rather than a gap (MVP.md §7.1): a node that could check
// a signature would be a node that could forge one, and "validate on the client"
// is what makes a hostile node unable to fabricate work.
//
// So the response is explicit about it. A client that reads a card from here has
// learned what was published, not what is true, and the directory says so in the
// same breath as it answers. The alternative — a bare list of cards — invites a
// reader to assume the node filtered them, which is exactly the assumption the
// node must not create.
//
// # Why the node still checks the shape
//
// It validates that agentId parses and that card/proof are non-empty, because a
// directory keyed by a malformed id is useless to everyone. That is a syntax check
// on the index, not a judgement about the content: the same distinction the
// messages endpoint already makes.

// publishCardRequest is the body of POST /agents.
//
// Card and Proof are raw JSON, not decoded structures. Decoding and re-encoding
// would change the bytes, and the proof covers the exact bytes the publisher
// signed — so a round trip through this endpoint would break every proof. They are
// carried as json.RawMessage for that reason, not for convenience.
type publishCardRequest struct {
	AgentID string          `json:"agentId"`
	Card    json.RawMessage `json:"card"`
	Proof   json.RawMessage `json:"proof"`
}

// publishCardResponse acknowledges a stored card.
//
// Verified is always false and is included on purpose. A client should be able to
// see, from the response, that acceptance is not validation — an omitted field
// would let a caller assume otherwise.
type publishCardResponse struct {
	OK       bool   `json:"ok"`
	AgentID  string `json:"agentId"`
	Verified bool   `json:"verified"`
	Note     string `json:"note"`
}

func (n *Node) handlePublishCard(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, n.cfg.MaxPayloadBytes)
	defer body.Close()

	raw, err := io.ReadAll(body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("payload exceeds the %d byte limit", n.cfg.MaxPayloadBytes))
		return
	}

	var req publishCardRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with agentId, card and proof")
		return
	}

	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agentId is required")
		return
	}
	// Syntax only. A malformed id would make the directory key meaningless, and
	// the id is the one field the node indexes on.
	if _, err := agentid.Parse(agentID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Card) == 0 {
		writeError(w, http.StatusBadRequest, "card is required")
		return
	}
	if len(req.Proof) == 0 {
		writeError(w, http.StatusBadRequest, "proof is required")
		return
	}
	// Each part must be a JSON object. This is a cheap guard against storing
	// arbitrary blobs under a card key, and it is still not a validity claim.
	if !looksLikeJSONObject(req.Card) {
		writeError(w, http.StatusBadRequest, "card must be a JSON object")
		return
	}
	if !looksLikeJSONObject(req.Proof) {
		writeError(w, http.StatusBadRequest, "proof must be a JSON object")
		return
	}

	err = n.cfg.Cards.Put(sqlite.AgentCard{
		AgentID:   agentID,
		Card:      req.Card,
		Proof:     req.Proof,
		UpdatedAt: n.cfg.Now(),
	})
	if err != nil {
		n.cfg.Logger.Error("store agent card", "agent", agentID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "could not store the card")
		return
	}

	writeJSON(w, http.StatusOK, publishCardResponse{
		OK:       true,
		AgentID:  agentID,
		Verified: false,
		Note: "stored, not verified: this node cannot check signatures. " +
			"Fetch the card and proof and verify them yourself (MVP.md §5.4).",
	})
}

// getCardResponse returns one card, verbatim.
type getCardResponse struct {
	AgentID  string          `json:"agentId"`
	Card     json.RawMessage `json:"card"`
	Proof    json.RawMessage `json:"proof"`
	Verified bool            `json:"verified"`
	Note     string          `json:"note"`
}

func (n *Node) handleGetCard(w http.ResponseWriter, r *http.Request) {
	agentID := strings.TrimSpace(r.PathValue("agentId"))
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agentId is required")
		return
	}

	card, ok, err := n.cfg.Cards.Get(agentID)
	if err != nil {
		n.cfg.Logger.Error("get agent card", "agent", agentID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "could not read the card")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no card published for that agent")
		return
	}

	writeJSON(w, http.StatusOK, getCardResponse{
		AgentID:  card.AgentID,
		Card:     json.RawMessage(card.Card),
		Proof:    json.RawMessage(card.Proof),
		Verified: false,
		Note: "returned verbatim as published; this node has not verified the proof. " +
			"Verify it on the client (MVP.md §5.4).",
	})
}

// listCardsResponse is the directory listing.
type listCardsResponse struct {
	Count int               `json:"count"`
	Cards []getCardResponse `json:"cards"`
	Note  string            `json:"note"`
}

func (n *Node) handleListCards(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		limit = v
	}

	cards, err := n.cfg.Cards.List(limit)
	if err != nil {
		n.cfg.Logger.Error("list agent cards", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "could not list cards")
		return
	}

	out := listCardsResponse{
		Count: len(cards),
		Cards: make([]getCardResponse, 0, len(cards)),
		Note: "a directory, not an authority: these are cards as published. " +
			"This node does not verify proofs or endorse the agents listed.",
	}
	for _, c := range cards {
		out.Cards = append(out.Cards, getCardResponse{
			AgentID:  c.AgentID,
			Card:     json.RawMessage(c.Card),
			Proof:    json.RawMessage(c.Proof),
			Verified: false,
			Note:     "verbatim as published; verify on the client",
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// looksLikeJSONObject reports whether raw is a JSON object.
//
// It is deliberately not json.Valid on a struct: the point is to reject a bare
// string or array, while leaving the object's contents entirely unexamined. The
// node has no opinion about what a card says.
func looksLikeJSONObject(raw []byte) bool {
	trimmed := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return false
	}
	var probe map[string]any
	return json.Unmarshal(raw, &probe) == nil
}
