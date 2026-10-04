package node

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// Task relay (S10-3, MVP.md §7.3).
//
// # What the node does here, and what it refuses to
//
// It publishes task announcements and lets an executor claim one. It does not decide who
// may claim, does not verify the requester's identity, and does not enforce that a task's
// rules are followed. A task board is a noticeboard: it makes offers findable, and the two
// agents verify each other.
//
// # Why the node cannot enforce claim exclusivity, and why that is fine
//
// Two executors can claim the same task, and the node cannot prevent it — it has no
// authority, and giving it one would make it a market operator rather than a relay. What
// makes a double claim harmless is the protocol, not the board: the requester chose one
// offer, and the loser's work has no receipt the requester accepts. So the claim is
// recorded as an expression of interest, and the response says exactly that rather than
// implying the claim was granted.
//
// # Why the offer travels as JSON the node does not interpret
//
// The same reason as receipts and events: the node cannot validate a signature (MVP.md
// §7.1), so anything it "understood" about a task would be unverified opinion. It indexes
// the few fields a query needs and carries the rest verbatim.

// taskOfferRequest is the body of POST /tasks.
type taskOfferRequest struct {
	// TaskID is the offer's identity, chosen by the requester.
	TaskID string `json:"taskId"`

	// Requester is the agent offering the work.
	Requester string `json:"requester"`

	// Subject is what the task is about, for the query key. It is the URL a probe would
	// fetch, matching the observation index so a task and its results share one key.
	Subject string `json:"subject"`

	// Spec is the task's parameters, carried verbatim.
	//
	// # Why it is RawMessage
	//
	// The node has no opinion about a task's shape — that is the two agents' business, and
	// the schema is versioned by them. Decoding it into a struct here would make the node a
	// participant in a format it does not own, and a future task shape would break it.
	Spec json.RawMessage `json:"spec,omitempty"`

	// ExpiresAt is when the offer lapses, as the requester set it.
	//
	// The node records it but does not enforce it: expiry is a protocol transition
	// (ARCHITECTURE.md §4.5), decided from signed data, and a relay that enforced its own
	// clock would let two relays disagree about whether an offer was still open.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// taskOfferResponse acknowledges an offer.
type taskOfferResponse struct {
	OK      bool   `json:"ok"`
	TaskID  string `json:"taskId"`
	Note    string `json:"note"`
	Granted bool   `json:"granted"`
}

func (n *Node) handleOfferTask(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, n.cfg.MaxPayloadBytes)
	defer body.Close()

	raw, err := readAllLimited(body, n.cfg.MaxPayloadBytes)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("payload exceeds the %d byte limit", n.cfg.MaxPayloadBytes))
		return
	}

	var req taskOfferRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with taskId, requester and subject")
		return
	}

	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "taskId is required")
		return
	}
	requester := strings.TrimSpace(req.Requester)
	if requester == "" {
		writeError(w, http.StatusBadRequest, "requester is required")
		return
	}
	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		writeError(w, http.StatusBadRequest,
			"subject is required; it is what the task is about and how it will be found")
		return
	}

	offer := sqlite.TaskOffer{
		TaskID:    taskID,
		Requester: requester,
		Subject:   subject,
		Spec:      req.Spec,
		ExpiresAt: req.ExpiresAt,
		OfferedAt: n.cfg.Now(),
	}
	if err := n.cfg.Tasks.Put(offer); err != nil {
		n.cfg.Logger.Error("store task offer", "task", taskID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "could not store the offer")
		return
	}

	writeJSON(w, http.StatusOK, taskOfferResponse{
		OK:      true,
		TaskID:  taskID,
		Granted: false,
		Note: "posted to the board; this is a notice, not an assignment. " +
			"The node does not verify the requester, does not grant exclusivity, and does not " +
			"enforce expiry — the two agents verify each other.",
	})
}

// taskOfferView is one entry of a task listing.
type taskOfferView struct {
	TaskID    string          `json:"taskId"`
	Requester string          `json:"requester"`
	Subject   string          `json:"subject"`
	Spec      json.RawMessage `json:"spec,omitempty"`
	ExpiresAt *time.Time      `json:"expiresAt,omitempty"`
	OfferedAt time.Time       `json:"offeredAt"`
	Claims    int             `json:"claims"`
	Verified  bool            `json:"verified"`
	Note      string          `json:"note"`
}

// tasksResponse is the body of GET /tasks.
type tasksResponse struct {
	Subject string          `json:"subject,omitempty"`
	Count   int             `json:"count"`
	Offers  []taskOfferView `json:"offers"`
	Note    string          `json:"note"`
}

func (n *Node) handleListTasks(w http.ResponseWriter, r *http.Request) {
	subject := strings.TrimSpace(r.URL.Query().Get("subject"))
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	offers, err := n.cfg.Tasks.List(subject, limit)
	if err != nil {
		n.cfg.Logger.Error("list task offers", "subject", subject, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "could not list offers")
		return
	}

	out := tasksResponse{
		Subject: subject,
		Count:   len(offers),
		Offers:  make([]taskOfferView, 0, len(offers)),
		Note: "a noticeboard, not an authority: the node does not verify requesters, " +
			"does not grant exclusivity, and does not enforce expiry. Verify the requester yourself.",
	}
	for _, o := range offers {
		out.Offers = append(out.Offers, taskOfferView{
			TaskID:    o.TaskID,
			Requester: o.Requester,
			Subject:   o.Subject,
			Spec:      json.RawMessage(o.Spec),
			ExpiresAt: o.ExpiresAt,
			OfferedAt: o.OfferedAt,
			Claims:    o.Claims,
			Verified:  false,
			Note:      "posted verbatim; not verified by this node",
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// claimRequest is the body of POST /tasks/{id}/claim.
type claimRequest struct {
	// Claimant is the agent expressing interest.
	Claimant string `json:"claimant"`
}

// claimResponse acknowledges a claim.
//
// Granted is always false and is included on purpose: a node cannot grant exclusivity, and
// a caller that read an acknowledgement as a grant would believe it had won a race it may
// have lost. Saying so in the response is what stops that assumption.
type claimResponse struct {
	OK       bool   `json:"ok"`
	TaskID   string `json:"taskId"`
	Claimant string `json:"claimant"`
	Granted  bool   `json:"granted"`
	Note     string `json:"note"`
}

func (n *Node) handleClaimTask(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(r.PathValue("id"))
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "a task id is required")
		return
	}

	body := http.MaxBytesReader(w, r.Body, n.cfg.MaxPayloadBytes)
	defer body.Close()
	raw, err := readAllLimited(body, n.cfg.MaxPayloadBytes)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "payload is too large")
		return
	}
	var req claimRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with claimant")
		return
	}
	claimant := strings.TrimSpace(req.Claimant)
	if claimant == "" {
		writeError(w, http.StatusBadRequest, "claimant is required")
		return
	}

	_, ok, err := n.cfg.Tasks.ByID(taskID)
	if err != nil {
		n.cfg.Logger.Error("lookup task", "task", taskID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "could not read the offer")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such task on this board")
		return
	}

	if err := n.cfg.Tasks.AddClaim(taskID, claimant, n.cfg.Now()); err != nil {
		n.cfg.Logger.Error("record claim", "task", taskID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "could not record the claim")
		return
	}

	writeJSON(w, http.StatusOK, claimResponse{
		OK:       true,
		TaskID:   taskID,
		Claimant: claimant,
		Granted:  false,
		Note: "recorded as interest, NOT granted: this node cannot give exclusivity, " +
			"and another agent may claim the same task. The requester decides whose work it accepts.",
	})
}
