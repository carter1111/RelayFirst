package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests cover the MCP server (S13-4).
//
// The property that matters most is the one a test CANNOT check here: that this binary links no
// signing code. That is asserted by the CI import-graph gate, because it is a property of the build
// rather than of any behaviour. What IS tested here is the behavioural half: the server offers no
// signing tool, does not decode or re-encode a payload, and reports caveats rather than implying
// authority it does not have.

// run sends one JSON-RPC message through the server and returns the decoded response.
func run(t *testing.T, s *server, request string) (*rpcResponse, bool) {
	t.Helper()
	var out bytes.Buffer
	if err := s.serve(strings.NewReader(request+"\n"), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if strings.TrimSpace(out.String()) == "" {
		return nil, false
	}
	var resp rpcResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\nraw: %s", err, out.String())
	}
	return &resp, true
}

func testServer(nodeURL string) *server {
	return &server{baseURL: nodeURL, client: http.DefaultClient}
}

// TestInitialize_AdvertisesTheRelayBoundary keeps the constraint where a client displays it.
func TestInitialize_AdvertisesTheRelayBoundary(t *testing.T) {
	resp, ok := run(t, testServer("http://unused"), `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if !ok {
		t.Fatal("initialize must produce a response")
	}
	if resp.Error != nil {
		t.Fatalf("initialize failed: %v", resp.Error)
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	body := string(raw)

	if !strings.Contains(body, "holds no key") {
		t.Errorf("initialize must state that this server holds no key, got: %s", body)
	}
	if !strings.Contains(body, "cannot sign") {
		t.Errorf("initialize must state that this server cannot sign, got: %s", body)
	}
	if !strings.Contains(body, protocolVersion) {
		t.Errorf("initialize must report a protocol version, got: %s", body)
	}
}

// TestToolsList_OffersNoSigningTool is the behavioural half of the security property.
//
// The structural half is the import graph, which makes a signing tool impossible to implement. This
// checks the surface: nothing a model could call is named after signing, so an agent cannot even try.
func TestToolsList_OffersNoSigningTool(t *testing.T) {
	resp, _ := run(t, testServer("http://unused"), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := strings.ToLower(string(raw))

	for _, forbidden := range []string{"sign", "publish_card", "submit_receipt", "mint", "attest", "decrypt"} {
		if strings.Contains(body, `"name":"`+forbidden) {
			t.Errorf("tool list must not offer a %q tool: this server holds no key and must not "+
				"present a way to use one", forbidden)
		}
	}
	// And the tools that SHOULD exist are present, or the absence above would be vacuous.
	for _, want := range []string{"list_tasks", "claim_task", "list_agents", "query_observations", "relay_message"} {
		if !strings.Contains(body, want) {
			t.Errorf("tool %q must be offered, got: %s", want, body)
		}
	}
}

// TestListTasks_SaysItIsNotAnAssignment keeps the caveat attached to the result.
//
// A model reading a task list will act on it, and an agent that believed it had been ASSIGNED work
// would skip the requester's acceptance — so the caveat travels with the data rather than living in
// documentation the model never sees.
func TestListTasks_SaysItIsNotAnAssignment(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tasks" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"count":1,"offers":[{"taskId":"t1"}],"note":"a noticeboard"}`))
	}))
	defer node.Close()

	resp, _ := run(t, testServer(node.URL),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_tasks","arguments":{}}}`)
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "noticeboard") {
		t.Errorf("the node's caveat must pass through, got: %s", raw)
	}

	// And the tool DESCRIPTION carries it too, since that is what the model reads before calling.
	tools, _ := run(t, testServer(node.URL), `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	traw, _ := json.Marshal(tools.Result)
	if !strings.Contains(string(traw), "not an assignment") {
		t.Errorf("list_tasks must describe itself as not an assignment, got: %s", traw)
	}
}

// TestRelayMessage_ForwardsBase64Verbatim is the "only already-signed bytes" rule.
//
// The payload must reach the node byte-identical. Decoding and re-encoding would put the bytes
// through a transformation this server cannot verify, and would change what the signature covers.
func TestRelayMessage_ForwardsBase64Verbatim(t *testing.T) {
	var got map[string]any
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer node.Close()

	// A distinctive base64 string.
	const payload = "eyJzZW50aW5lbCI6ImRvLW5vdC1kZWNvZGUifQ=="
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"relay_message","arguments":` +
		`{"id":"0xabc","agentId":"agent:eip155:8453:0x1","kind":"receipt","payload":"` + payload + `"}}}`
	resp, _ := run(t, testServer(node.URL), req)
	if resp.Error != nil {
		t.Fatalf("tool error: %v", resp.Error)
	}

	if got["payload"] != payload {
		t.Errorf("the payload must be forwarded verbatim, got %v", got["payload"])
	}
	if got["kind"] != "receipt" {
		t.Errorf("kind must pass through, got %v", got["kind"])
	}
}

// TestRelayMessage_RequiresEveryField keeps a partial envelope from reaching the node.
func TestRelayMessage_RequiresEveryField(t *testing.T) {
	s := testServer("http://unused")
	cases := []string{
		`{"id":"0xabc","agentId":"a","kind":"receipt"}`,
		`{"id":"","agentId":"a","kind":"receipt","payload":"x"}`,
		`{"id":"0xabc","agentId":"","kind":"receipt","payload":"x"}`,
		`{"id":"0xabc","agentId":"a","kind":"","payload":"x"}`,
		`{"id":"0xabc","agentId":"a","kind":"receipt","payload":""}`,
	}
	for _, args := range cases {
		got, err := s.callTool("relay_message", json.RawMessage(args))
		if err == nil {
			t.Errorf("incomplete envelope %s must be refused, got %q", args, got)
		}
	}
}

// TestUnknownToolIsReported keeps a typo from looking like an empty result.
func TestUnknownToolIsReported(t *testing.T) {
	s := testServer("http://unused")
	if _, err := s.callTool("sign_receipt", json.RawMessage(`{}`)); err == nil {
		t.Fatal("an unknown tool must be reported, and 'sign_receipt' must certainly not exist")
	}
}

// TestToolFailureIsAResultNotAProtocolError is what lets a model read and react to a failure.
func TestToolFailureIsAResultNotAProtocolError(t *testing.T) {
	resp, _ := run(t, testServer("http://127.0.0.1:1"),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_tasks","arguments":{}}}`)
	if resp.Error != nil {
		t.Fatalf("a tool failure must not be a JSON-RPC error, got: %v", resp.Error)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), `"isError":true`) {
		t.Errorf("the failure must be marked isError so the model sees it, got: %s", raw)
	}
}

// TestNotificationProducesNoResponse keeps an MCP client from receiving a reply it never asked for.
func TestNotificationProducesNoResponse(t *testing.T) {
	_, ok := run(t, testServer("http://unused"),
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if ok {
		t.Fatal("a notification must not produce a response")
	}
	// Even an unknown notification is silent, since there is no id to reply to.
	_, ok = run(t, testServer("http://unused"), `{"jsonrpc":"2.0","method":"something/unknown"}`)
	if ok {
		t.Fatal("an unknown notification must not produce a response")
	}
}

// TestParseErrorIsReported keeps a malformed line from killing the server.
func TestParseErrorIsReported(t *testing.T) {
	resp, ok := run(t, testServer("http://unused"), `this is not json`)
	if !ok {
		t.Fatal("a malformed message must produce a parse error response")
	}
	if resp.Error == nil || resp.Error.Code != codeParseError {
		t.Errorf("want a parse error, got: %+v", resp)
	}
}

// TestMethodNotFoundIsReported covers an unknown method WITH an id.
func TestMethodNotFoundIsReported(t *testing.T) {
	resp, _ := run(t, testServer("http://unused"), `{"jsonrpc":"2.0","id":7,"method":"tools/unknown"}`)
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Errorf("want method-not-found, got: %+v", resp)
	}
}

// TestPingIsAnswered keeps the liveness check working.
func TestPingIsAnswered(t *testing.T) {
	resp, ok := run(t, testServer("http://unused"), `{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	if !ok || resp.Error != nil {
		t.Fatalf("ping must be answered: %+v", resp)
	}
}

// TestUnreachableRelayIsAHumanReadableFailure keeps a dead node from looking like a protocol bug.
func TestUnreachableRelayIsAHumanReadableFailure(t *testing.T) {
	s := testServer("http://127.0.0.1:1")
	_, err := s.callTool("list_tasks", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("an unreachable relay must fail")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("the failure must say the relay is unreachable, got: %v", err)
	}
}

// TestResponseSizeIsBounded keeps a hostile or broken node from filling the agent's context.
func TestResponseSizeIsBounded(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Far more than the cap.
		big := strings.Repeat("x", maxResponseBytes+1024)
		_, _ = w.Write([]byte(`{"data":"` + big + `"}`))
	}))
	defer node.Close()

	got, err := testServer(node.URL).callTool("list_agents", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("callTool: %v", err)
	}
	if len(got) > maxResponseBytes {
		t.Errorf("a node must not be able to make one tool call exceed the cap: got %d bytes", len(got))
	}
}
