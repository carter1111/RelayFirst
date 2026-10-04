package node_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/relayfirst/relayfirst/internal/node"
)

// These tests cover S10-3, the task board.
//
// The properties that matter are the ones the node REFUSES to provide, because the
// temptation is to have it arbitrate:
//
//   - a claim is recorded as INTEREST and never as a grant, since the node has no authority
//     and a caller that read an acknowledgement as a win would believe it won a race it may
//     have lost;
//   - expiry is stored but not enforced, because expiry is a protocol transition decided
//     from signed data (ARCHITECTURE.md §4.5), and a relay filtering by its own clock would
//     invent an authority it does not have;
//   - one agent claiming twice is a retry, not two claims, or the interest count would be
//     inflatable.

func offerTask(t *testing.T, srvURL, taskID, requester, subject string, extra map[string]any) *http.Response {
	t.Helper()
	body := map[string]any{
		"taskId":    taskID,
		"requester": requester,
		"subject":   subject,
	}
	for k, v := range extra {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal offer: %v", err)
	}
	resp, err := http.Post(srvURL+"/tasks", "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("POST /tasks: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func claimTask(t *testing.T, srvURL, taskID, claimant string) *http.Response {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"claimant": claimant})
	if err != nil {
		t.Fatalf("marshal claim: %v", err)
	}
	resp, err := http.Post(srvURL+"/tasks/"+taskID+"/claim", "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("POST claim: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// TestTasks_OfferAndList is the basic path.
func TestTasks_OfferAndList(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	const (
		taskID    = "tsk_board_1"
		requester = "agent:eip155:8453:0x00000000000000000000000000000000000000f1"
		subject   = "https://api.example.com/job"
	)
	resp := offerTask(t, srv.URL, taskID, requester, subject, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("offer returned %d", resp.StatusCode)
	}
	var ack struct {
		OK      bool   `json:"ok"`
		Granted bool   `json:"granted"`
		Note    string `json:"note"`
	}
	decode(t, resp, &ack)
	if !ack.OK {
		t.Error("the offer must be acknowledged")
	}
	if ack.Granted {
		t.Error("posting an offer is not a grant; the node has no authority to grant")
	}
	if !strings.Contains(ack.Note, "not an assignment") {
		t.Errorf("the acknowledgement must say it is a notice, not an assignment, got: %q", ack.Note)
	}

	// The listing must show it.
	var list struct {
		Count  int `json:"count"`
		Offers []struct {
			TaskID    string `json:"taskId"`
			Subject   string `json:"subject"`
			Requester string `json:"requester"`
			Verified  bool   `json:"verified"`
		} `json:"offers"`
		Note string `json:"note"`
	}
	decode(t, get(t, client, srv.URL+"/tasks"), &list)
	if list.Count != 1 {
		t.Fatalf("count = %d, want 1", list.Count)
	}
	if list.Offers[0].TaskID != taskID || list.Offers[0].Subject != subject {
		t.Errorf("offer did not round trip: %+v", list.Offers[0])
	}
	if list.Offers[0].Verified {
		t.Error("an offer must never be reported as verified; the node cannot verify a requester")
	}
	if !strings.Contains(list.Note, "does not verify requesters") {
		t.Errorf("the listing must say requesters are unverified, got: %q", list.Note)
	}
}

// TestTasks_ClaimIsInterestNotAGrant is the central property.
//
// The node cannot grant exclusivity. A response that let a caller believe it had won would
// make it stop looking for other work, and another agent may hold the same claim.
func TestTasks_ClaimIsInterestNotAGrant(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")

	const (
		taskID    = "tsk_claim_1"
		requester = "agent:eip155:8453:0x00000000000000000000000000000000000000f2"
		subject   = "https://api.example.com/claim"
	)
	offerTask(t, srv.URL, taskID, requester, subject, nil).Body.Close()

	resp := claimTask(t, srv.URL, taskID, "agent:eip155:8453:0x00000000000000000000000000000000000000f3")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim returned %d", resp.StatusCode)
	}
	var ack struct {
		OK       bool   `json:"ok"`
		Granted  bool   `json:"granted"`
		Claimant string `json:"claimant"`
		Note     string `json:"note"`
	}
	decode(t, resp, &ack)
	if ack.Granted {
		t.Fatal("a claim must not be reported as granted: the node cannot give exclusivity, " +
			"and a caller that believed it had won would stop looking for other work")
	}
	if !strings.Contains(ack.Note, "NOT granted") {
		t.Errorf("the response must say the claim was not granted, got: %q", ack.Note)
	}
	if !strings.Contains(ack.Note, "another agent may claim") {
		t.Errorf("the response must warn that a competitor may hold the same claim, got: %q", ack.Note)
	}
}

// TestTasks_TwoAgentsMayBothClaim is the consequence: the node does not arbitrate.
func TestTasks_TwoAgentsMayBothClaim(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	const (
		taskID    = "tsk_race_1"
		requester = "agent:eip155:8453:0x00000000000000000000000000000000000000f4"
		subject   = "https://api.example.com/race"
	)
	offerTask(t, srv.URL, taskID, requester, subject, nil).Body.Close()

	for _, a := range []string{
		"agent:eip155:8453:0x00000000000000000000000000000000000000f5",
		"agent:eip155:8453:0x00000000000000000000000000000000000000f6",
	} {
		r := claimTask(t, srv.URL, taskID, a)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("the second claim must not be refused by the node: got %d", r.StatusCode)
		}
		r.Body.Close()
	}

	var list struct {
		Offers []struct {
			Claims int `json:"claims"`
		} `json:"offers"`
	}
	decode(t, get(t, client, srv.URL+"/tasks"), &list)
	if len(list.Offers) != 1 {
		t.Fatalf("offers = %d, want 1", len(list.Offers))
	}
	if list.Offers[0].Claims != 2 {
		t.Errorf("claims = %d, want 2: the board records interest from both, "+
			"and the protocol decides who is accepted", list.Offers[0].Claims)
	}
}

// TestTasks_DuplicateClaimCountsOnce is the anti-inflation rule.
//
// The claim count is what a requester reads to judge whether the board is alive. An agent
// that could inflate it by re-posting would make the board lie.
func TestTasks_DuplicateClaimCountsOnce(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	const (
		taskID    = "tsk_dup_1"
		requester = "agent:eip155:8453:0x00000000000000000000000000000000000000f7"
		subject   = "https://api.example.com/dupclaim"
		claimant  = "agent:eip155:8453:0x00000000000000000000000000000000000000f8"
	)
	offerTask(t, srv.URL, taskID, requester, subject, nil).Body.Close()

	for i := 0; i < 3; i++ {
		claimTask(t, srv.URL, taskID, claimant).Body.Close()
	}

	var list struct {
		Offers []struct {
			Claims int `json:"claims"`
		} `json:"offers"`
	}
	decode(t, get(t, client, srv.URL+"/tasks"), &list)
	if list.Offers[0].Claims != 1 {
		t.Errorf("claims = %d, want 1: one agent claiming three times is one interest, "+
			"or the count a requester reads could be inflated", list.Offers[0].Claims)
	}
}

// TestTasks_ExpiredOfferIsStillListed is the §4.5 rule.
//
// Expiry is a protocol transition decided from signed data, not a relay's judgement. A node
// filtering by its own clock would hide work it merely believed was late, and two relays
// with skewed clocks would disagree about what was open.
func TestTasks_ExpiredOfferIsStillListed(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	const (
		taskID    = "tsk_expired_1"
		requester = "agent:eip155:8453:0x00000000000000000000000000000000000000f9"
		subject   = "https://api.example.com/expired"
	)
	// An expiry in the past.
	offerTask(t, srv.URL, taskID, requester, subject, map[string]any{
		"expiresAt": "2020-01-01T00:00:00Z",
	}).Body.Close()

	var list struct {
		Count  int `json:"count"`
		Offers []struct {
			TaskID    string `json:"taskId"`
			ExpiresAt string `json:"expiresAt"`
		} `json:"offers"`
	}
	decode(t, get(t, client, srv.URL+"/tasks"), &list)
	if list.Count != 1 {
		t.Fatalf("a lapsed offer must still be listed: expiry is the protocol's decision, "+
			"not this node's (got count %d)", list.Count)
	}
	if list.Offers[0].ExpiresAt == "" {
		t.Error("the expiry must be returned so the client can apply its own clock")
	}
}

// TestTasks_FilterBySubject is the query the board exists for.
func TestTasks_FilterBySubject(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	const requester = "agent:eip155:8453:0x00000000000000000000000000000000000000fa"
	offerTask(t, srv.URL, "tsk_a", requester, "https://api.example.com/a", nil).Body.Close()
	offerTask(t, srv.URL, "tsk_b", requester, "https://api.example.com/b", nil).Body.Close()

	var list struct {
		Count int `json:"count"`
	}
	decode(t, get(t, client, srv.URL+"/tasks?subject=https://api.example.com/a"), &list)
	if list.Count != 1 {
		t.Errorf("filtered count = %d, want 1", list.Count)
	}
}

// TestTasks_SpecIsCarriedVerbatim is the forward-compatibility rule: the node has no opinion
// about a task's shape, so a spec with fields it does not know must survive.
func TestTasks_SpecIsCarriedVerbatim(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	const (
		taskID    = "tsk_spec_1"
		requester = "agent:eip155:8453:0x00000000000000000000000000000000000000fb"
		subject   = "https://api.example.com/spec"
	)
	offerTask(t, srv.URL, taskID, requester, subject, map[string]any{
		"spec": map[string]any{
			"url":         subject,
			"futureField": map[string]any{"added": true},
		},
	}).Body.Close()

	var list struct {
		Offers []struct {
			Spec json.RawMessage `json:"spec"`
		} `json:"offers"`
	}
	decode(t, get(t, client, srv.URL+"/tasks"), &list)
	if len(list.Offers) != 1 {
		t.Fatalf("offers = %d, want 1", len(list.Offers))
	}
	if !strings.Contains(string(list.Offers[0].Spec), "futureField") {
		t.Errorf("an unknown spec field must survive: the node does not own the task format, "+
			"so it must not drop what it does not recognise. got: %s", list.Offers[0].Spec)
	}
}

// TestTasks_RejectsIncompleteOffer covers the index-integrity checks.
func TestTasks_RejectsIncompleteOffer(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	cases := []struct {
		name string
		body map[string]any
	}{
		{"no taskId", map[string]any{"requester": "a", "subject": "s"}},
		{"no requester", map[string]any{"taskId": "t", "subject": "s"}},
		{"no subject", map[string]any{"taskId": "t", "requester": "a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, _ := json.Marshal(c.body)
			resp, err := http.Post(srv.URL+"/tasks", "application/json", strings.NewReader(string(raw)))
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("an offer with %s must be rejected, got %d", c.name, resp.StatusCode)
			}
		})
	}
}

// TestTasks_ClaimUnknownTaskIs404 covers the lookup miss.
func TestTasks_ClaimUnknownTaskIs404(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	resp := claimTask(t, srv.URL, "tsk_does_not_exist", "agent:eip155:8453:0x00000000000000000000000000000000000000fc")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("claiming an unknown task must be 404, got %d", resp.StatusCode)
	}
}

// TestTasks_RepostUpdatesRatherThanDuplicates keeps a retry from making a task appear twice.
func TestTasks_RepostUpdatesRatherThanDuplicates(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	client := srv.Client()

	const (
		taskID    = "tsk_repost_1"
		requester = "agent:eip155:8453:0x00000000000000000000000000000000000000fd"
		subject   = "https://api.example.com/repost"
	)
	offerTask(t, srv.URL, taskID, requester, subject, nil).Body.Close()
	offerTask(t, srv.URL, taskID, requester, subject, nil).Body.Close()

	var list struct {
		Count int `json:"count"`
	}
	decode(t, get(t, client, srv.URL+"/tasks"), &list)
	if list.Count != 1 {
		t.Errorf("count = %d, want 1: re-posting means 'this is the current offer', not two",
			list.Count)
	}
}

// TestTasks_WellKnownAdvertisesTheBoard keeps discovery honest.
func TestTasks_WellKnownAdvertisesTheBoard(t *testing.T) {
	srv, _ := newTestNode(t, "http://test.local")
	var wk struct {
		Endpoints []string `json:"endpoints"`
		Tasks     int      `json:"tasks"`
	}
	decode(t, get(t, srv.Client(), srv.URL+"/.well-known/relayfirst"), &wk)

	joined := strings.Join(wk.Endpoints, " ")
	for _, want := range []string{"POST /tasks", "GET /tasks"} {
		if !strings.Contains(joined, want) {
			t.Errorf("well-known must advertise %q, got: %v", want, wk.Endpoints)
		}
	}
	_ = node.KindEvent
}
