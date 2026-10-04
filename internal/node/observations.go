package node

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// The observation index and cross-verification view (S10-1, S10-2).
//
// # The distinction this file turns on: indexing is not verifying
//
// MVP.md §7.1 says a node does not verify signatures, and that is enforced by the import
// graph — this package cannot reach eip712 or receipt, so it *cannot* verify even if
// someone tried. §7.3 explains why that is compatible with an index: indexing and querying
// do not break security, because whoever retrieves the data can re-verify it.
//
// So this reads a receipt's JSON to extract a few fields, and makes no claim about the
// receipt being true. A search engine indexes pages without vouching for them; the index
// answers "where can I find X", and the consumer checks what it finds. A forged receipt is
// indexed like any other and is discarded by the consumer's signature check — which is
// exactly what would happen if the node had never indexed it, except the honest ones are
// now findable.
//
// # Why the fields are read by JSON path rather than by a shared struct
//
// The receipt's Go type lives in internal/receipt, which links secp256k1. Using it here
// would link signing code into the node and break the property above. So the fields are
// addressed by name against the raw JSON — which is also the honest description of what an
// indexer does: it knows a few paths, not the semantics.

// receiptIndex is the subset of a receipt the index reads.
//
// # Why the tags name the exact wire paths
//
// These are the same keys the receipt's canonical form uses, so an index built here and an
// index built by any other reader agree on what a field is called. The struct is
// deliberately not the receipt type: it is a projection, and keeping it separate is what
// makes "the node does not understand receipts, it recognises a few keys" true rather than
// aspirational.
type receiptIndex struct {
	ReceiptID string `json:"receiptId"`
	AgentID   string `json:"agentId"`
	Epoch     uint64 `json:"epoch"`
	Task      struct {
		Type string `json:"type"`
		Spec struct {
			URL string `json:"url"`
		} `json:"spec"`
		// Spec is also read as a raw map because a compute task's spec may carry its own
		// shape. The typed field above covers the common probe/extract case.
		RawSpec map[string]any `json:"-"`
	} `json:"task"`
	Result struct {
		Hash string `json:"hash"`
	} `json:"result"`
	Anchors []struct {
		URL         string `json:"url"`
		ContentHash string `json:"contentHash"`
	} `json:"anchors"`
}

// indexReceipt extracts an observation from a receipt's bytes.
//
// # What it does when a field is missing
//
// It returns an error, and the caller decides. That is deliberate: an observation with no
// subject cannot be found by the query this table exists for, so indexing it would add a
// row that only inflates counts. A receipt this index cannot understand is not corrupt —
// it is simply not indexable, and saying so is better than a half-row.
func indexReceipt(raw []byte, at time.Time) (sqlite.Observation, error) {
	var ri receiptIndex
	if err := json.Unmarshal(raw, &ri); err != nil {
		return sqlite.Observation{}, fmt.Errorf("index: receipt is not JSON: %w", err)
	}
	if strings.TrimSpace(ri.ReceiptID) == "" {
		return sqlite.Observation{}, fmt.Errorf("index: receipt has no receiptId")
	}

	subject := strings.TrimSpace(ri.Task.Spec.URL)
	if subject == "" {
		// Fall back to the first anchor's URL, which is where a receipt records the
		// evidence it actually fetched. A receipt whose spec carries no url (a compute
		// task) still has an anchor, and that anchor is the thing worth finding.
		for _, a := range ri.Anchors {
			if u := strings.TrimSpace(a.URL); u != "" {
				subject = u
				break
			}
		}
	}
	if subject == "" {
		return sqlite.Observation{}, fmt.Errorf("index: receipt %s names no subject url", ri.ReceiptID)
	}

	var contentHash string
	if len(ri.Anchors) > 0 {
		contentHash = strings.TrimSpace(ri.Anchors[0].ContentHash)
	}

	return sqlite.Observation{
		ReceiptID:   ri.ReceiptID,
		Subject:     subject,
		ContentHash: contentHash,
		ResultHash:  strings.TrimSpace(ri.Result.Hash),
		AgentID:     strings.TrimSpace(ri.AgentID),
		TaskType:    strings.TrimSpace(ri.Task.Type),
		Epoch:       ri.Epoch,
		IndexedAt:   at,
	}, nil
}

// observationView is one entry of a query response.
//
// Verified is always false and is included on purpose: the field is what stops a consumer
// from assuming the index filtered anything. See the package comment on internal/node.
type observationView struct {
	ReceiptID   string `json:"receiptId"`
	Subject     string `json:"subject"`
	ContentHash string `json:"contentHash"`
	ResultHash  string `json:"resultHash"`
	AgentID     string `json:"agentId"`
	TaskType    string `json:"taskType"`
	Epoch       uint64 `json:"epoch"`
	Verified    bool   `json:"verified"`
	Note        string `json:"note"`
}

// observationsResponse is the body of GET /observations.
type observationsResponse struct {
	Subject string            `json:"subject"`
	Count   int               `json:"count"`
	Entries []observationView `json:"entries"`
	Note    string            `json:"note"`
}

func (n *Node) handleObservations(w http.ResponseWriter, r *http.Request) {
	subject := strings.TrimSpace(r.URL.Query().Get("subject"))
	if subject == "" {
		writeError(w, http.StatusBadRequest,
			"subject is required; it is the URL an observation was made about")
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	obs, err := n.cfg.Observations.BySubject(subject, limit)
	if err != nil {
		n.cfg.Logger.Error("query observations", "subject", subject, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "could not read observations")
		return
	}

	out := observationsResponse{
		Subject: subject,
		Count:   len(obs),
		Entries: make([]observationView, 0, len(obs)),
		Note: "an index, not an endorsement: this node does not verify signatures " +
			"(MVP.md §7.1), so these are claims as published. Fetch the receipt and verify it yourself.",
	}
	for _, o := range obs {
		out.Entries = append(out.Entries, observationView{
			ReceiptID:   o.ReceiptID,
			Subject:     o.Subject,
			ContentHash: o.ContentHash,
			ResultHash:  o.ResultHash,
			AgentID:     o.AgentID,
			TaskType:    o.TaskType,
			Epoch:       o.Epoch,
			Verified:    false,
			Note:        "verbatim as indexed; not verified by this node",
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// evidenceGroup is one content-hash group in the cross-verification view.
type evidenceGroup struct {
	ContentHash    string   `json:"contentHash"`
	DistinctAgents int      `json:"distinctAgents"`
	Observations   int      `json:"observations"`
	ReceiptIDs     []string `json:"receiptIds"`
}

// evidenceResponse is the body of GET /observations/{id}/evidence.
type evidenceResponse struct {
	Subject string          `json:"subject"`
	Groups  []evidenceGroup `json:"groups"`
	Note    string          `json:"note"`
}

// handleEvidence serves the cross-verification view (S10-2).
//
// # What it reports, and the word that does the work
//
// It groups a subject's observations by claimed content hash and counts DISTINCT agents
// per group. The word doing the work is "distinct": two receipts from one agent are one
// claim repeated, so counting rows would let a single agent manufacture agreement. The
// count is over agents for exactly that reason.
//
// # What it does not conclude
//
// That agreement is correct. Two colluding agents agree too, and one agent observing a
// changed page produces two groups. So this is a signal to look at, and the response says
// so rather than letting a consumer read a number as a verdict.
//
// The path parameter is a receipt id, because that is what a consumer has after a query:
// they saw a receipt in the index and want to know who else saw the same thing. The
// subject is looked up from that receipt rather than passed, so the two cannot disagree.
func (n *Node) handleEvidence(w http.ResponseWriter, r *http.Request) {
	receiptID := strings.TrimSpace(r.PathValue("id"))
	if receiptID == "" {
		writeError(w, http.StatusBadRequest, "a receipt id is required")
		return
	}

	obs, ok, err := n.cfg.Observations.ByReceipt(receiptID)
	if err != nil {
		n.cfg.Logger.Error("lookup observation", "receipt", receiptID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "could not read the observation")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "that receipt is not indexed here")
		return
	}

	groups, err := n.cfg.Observations.EvidenceFor(obs.Subject, 0)
	if err != nil {
		n.cfg.Logger.Error("cross-verification", "subject", obs.Subject, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "could not build the evidence view")
		return
	}

	out := evidenceResponse{
		Subject: obs.Subject,
		Groups:  make([]evidenceGroup, 0, len(groups)),
		Note: "independent agreement is evidence, not proof: agents can collude, and one agent " +
			"observing a changed page produces two groups. Verify each receipt yourself.",
	}
	for _, g := range groups {
		out.Groups = append(out.Groups, evidenceGroup{
			ContentHash:    g.ContentHash,
			DistinctAgents: g.DistinctAgents,
			Observations:   g.Observations,
			ReceiptIDs:     g.ReceiptIDs,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// indexReceiptEnvelope indexes a receipt envelope if it carries a receipt payload.
//
// # Why this never fails the publish
//
// A receipt the index cannot understand is still a message worth storing and forwarding —
// that is the node's primary job, and dropping it because an optional index did not like it
// would turn an indexing limitation into a delivery failure. So failures are logged and the
// publish continues.
//
// This is the same shape as the card directory: the node's obligations are delivery, and
// everything else is additive.
func (n *Node) indexReceiptEnvelope(env Envelope) {
	if env.Kind != KindReceipt {
		return
	}
	obs, err := indexReceipt(env.Payload, n.cfg.Now())
	if err != nil {
		// Debug rather than warn: an unindexable receipt is not an error condition, and
		// logging it loudly on every publish would be noise an operator cannot act on.
		n.cfg.Logger.Debug("receipt not indexed", "id", env.ID, "reason", err.Error())
		return
	}
	if err := n.cfg.Observations.Put(obs); err != nil {
		n.cfg.Logger.Error("index receipt", "id", env.ID, "error", err.Error())
	}
}

// readAllLimited reads a body with a cap, returning an error when the cap is exceeded.
//
// It exists because the POST handlers need the same bounded read, and http.MaxBytesReader's
// error is indistinguishable from a read failure without a shared wrapper.
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) >= limit {
		return nil, fmt.Errorf("body exceeds the %d byte limit", limit)
	}
	return raw, nil
}

// parseLimit reads a limit query parameter, rejecting anything that is not a
// non-negative integer.
func parseLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("limit must be a non-negative integer")
	}
	return v, nil
}
