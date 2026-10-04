// Command relayfirst-mcp exposes a relay node's task board and card directory over MCP.
//
// # THE SECURITY PROPERTY THIS BINARY EXISTS TO HAVE
//
// MVP.md §9.2: "the MCP server must never hold the master key." The reason is that an MCP server
// runs inside the agent's process, and an agent can be steered by prompt injection. If the MCP
// server could sign, an injected instruction could make the agent sign an arbitrary receipt on the
// user's behalf.
//
// # Why this is enforced rather than promised
//
// The guarantee is not a policy in this file. It is the import graph: this binary links
// internal/node and internal/protocol and NOTHING that can sign. Run:
//
//	go list -deps ./cmd/relayfirst-mcp | grep -E 'internal/(eip712|receipt|publish|assertion|delegationsign|e2ee|mining)'
//
// and it is empty. CI asserts exactly that, so the property cannot be lost by a later edit that
// merely looks like a convenience.
//
// # What it can therefore do, and what it cannot
//
// It can discover tasks, claim them, relay already-signed bytes, and read the directories. It CANNOT
// sign a receipt, produce a card proof, or verify a signature — because it cannot reach any of that
// code.
//
// The consequence worth stating plainly: it also cannot VERIFY what it relays. That is not a gap. A
// relay is not an authority (ARCHITECTURE.md §5.7), and the producer's CLI verified before signing.
// A client that wants to check a receipt does it with its own verifier, which is the whole point of
// criterion ②.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	serverName    = "relayfirst-mcp"
	serverVersion = "0.1.0"

	// protocolVersion is the MCP revision this speaks.
	protocolVersion = "2025-06-18"
)

// server holds the node this MCP instance talks to.
//
// # Why the node URL is the only configuration
//
// A relay is a convenience endpoint, and nothing here needs an identity. Configuration is a URL and
// at most a timeout, so there is no place to put a key even by mistake — the same reason the minimal
// node takes no key.
type server struct {
	baseURL string
	client  *http.Client
}

func main() {
	base := os.Getenv("RELAYFIRST_RELAY")
	if base == "" {
		// Defaulting to a local node matches the doc's "one docker run" story: the first thing a
		// user runs is a node on their machine.
		base = "http://localhost:8080"
	}
	s := &server{
		baseURL: strings.TrimRight(base, "/"),
		client:  &http.Client{Timeout: 20 * time.Second},
	}
	if err := s.serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", serverName, err)
		os.Exit(1)
	}
}

// serve runs the stdio JSON-RPC loop.
//
// MCP over stdio is newline-delimited JSON-RPC 2.0: one message per line, no framing headers. A
// malformed line is answered with a parse error rather than killing the server, because an agent
// sending a bad message is a normal condition and a server that exited would look like a crash.
func (s *server) serve(in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	enc := json.NewEncoder(out)

	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			resp, hasResponse := s.handle(line)
			if hasResponse {
				if encErr := enc.Encode(resp); encErr != nil {
					return fmt.Errorf("write response: %w", encErr)
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read request: %w", err)
		}
	}
}

// rpcRequest is a JSON-RPC 2.0 request.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is a JSON-RPC 2.0 response.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// JSON-RPC error codes. They are the standard ones so a client library can interpret them.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// handle dispatches one message, returning whether a response is due.
//
// # Why a notification produces no response
//
// JSON-RPC distinguishes a request (has an id, expects a reply) from a notification (no id). Answering
// a notification is a protocol error, and an MCP client that received an unexpected reply would treat
// it as a response to something it never sent.
func (s *server) handle(line []byte) (*rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return &rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: codeParseError, Message: "message is not valid JSON"},
		}, true
	}
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		return s.reply(req, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    serverName,
				"version": serverVersion,
			},
			// The instruction is returned here because this is where an MCP client shows it, and
			// the boundary is the one thing a user of this server must understand.
			"instructions": "This server is a RELAY: it discovers and claims tasks and forwards " +
				"already-signed bytes. It holds no key and cannot sign or verify anything. Signing " +
				"happens in the relayfirst CLI, outside the agent's process.",
		})
	case "notifications/initialized", "initialized":
		// The client telling us it is ready. No response.
		return nil, false
	case "ping":
		return s.reply(req, map[string]any{})
	case "tools/list":
		return s.reply(req, map[string]any{"tools": toolDefinitions()})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return s.error(req, codeInvalidParams, "params must name a tool"), !isNotification
		}
		result, err := s.callTool(params.Name, params.Arguments)
		if err != nil {
			// A tool failure is a RESULT with isError, not a JSON-RPC error. The distinction matters:
			// a JSON-RPC error means the request was malformed, while a tool error is information the
			// model is meant to read and act on.
			r, _ := s.reply(req, map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			})
			return r, !isNotification
		}
		r, _ := s.reply(req, map[string]any{
			"content": []map[string]any{{"type": "text", "text": result}},
		})
		return r, !isNotification
	default:
		if isNotification {
			return nil, false
		}
		return s.error(req, codeMethodNotFound, fmt.Sprintf("unknown method %q", req.Method)), true
	}
}

func (s *server) reply(req rpcRequest, result any) (*rpcResponse, bool) {
	return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, true
}

func (s *server) error(req rpcRequest, code int, msg string) *rpcResponse {
	return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: msg}}
}

// toolDefinitions is the MCP tool list.
//
// # What is deliberately absent
//
// There is no "sign", "submit_receipt" or "publish_card" tool. Adding one would require the signing
// code this binary cannot link, so the absence is not a decision that could drift — but naming it
// here means a reader sees the intent rather than having to infer it from a list.
func toolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name": "list_tasks",
			"description": "List open tasks from the relay's board. A listing, not an assignment: " +
				"the node does not verify requesters or grant exclusivity. Filter by subject to find " +
				"work about a specific URL.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"subject": map[string]any{"type": "string", "description": "Only tasks about this URL."},
					"limit":   map[string]any{"type": "integer", "description": "Cap the number returned."},
				},
			},
		},
		{
			"name": "claim_task",
			"description": "Record interest in a task. This is NOT a grant: the node cannot grant " +
				"exclusivity, and another agent may claim the same task. The requester decides whose " +
				"work it accepts.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"taskId", "claimant"},
				"properties": map[string]any{
					"taskId":   map[string]any{"type": "string"},
					"claimant": map[string]any{"type": "string", "description": "The claiming agent's id."},
				},
			},
		},
		{
			"name": "list_agents",
			"description": "List published agent cards. A directory, not an endorsement: the node " +
				"stores cards verbatim and does not verify their proofs.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			"name": "query_observations",
			"description": "Query the relay's receipt index for a URL. An index, not a verification: " +
				"the results are claims as published, and a consumer must verify each receipt itself.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"subject"},
				"properties": map[string]any{
					"subject": map[string]any{"type": "string", "description": "The URL to look up."},
				},
			},
		},
		{
			"name": "relay_message",
			"description": "Forward an ALREADY-SIGNED envelope to the relay. This server cannot sign " +
				"anything, so the bytes must come from the relayfirst CLI. The relay stores them " +
				"verbatim and does not check the signature.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"id", "agentId", "kind", "payload"},
				"properties": map[string]any{
					"id":      map[string]any{"type": "string"},
					"agentId": map[string]any{"type": "string"},
					"kind":    map[string]any{"type": "string", "description": "e.g. receipt or event."},
					"payload": map[string]any{
						"type":        "string",
						"description": "The signed bytes, base64-encoded. The CLI produces these.",
					},
				},
			},
		},
	}
}

// callTool runs one tool and returns text for the model.
//
// # Why the results are text rather than structured content
//
// The point of these tools is that a model reads them and decides what to do. Returning a schema the
// model has to parse would add a failure mode without adding information, and every response already
// carries the caveats the model needs — that a listing is not an assignment, that an index is not a
// verification — because those caveats are the ones an eager model would otherwise skip.
func (s *server) callTool(name string, args json.RawMessage) (string, error) {
	switch name {
	case "list_tasks":
		var p struct {
			Subject string `json:"subject"`
			Limit   int    `json:"limit"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("arguments must be an object: %v", err)
			}
		}
		q := url.Values{}
		if p.Subject != "" {
			q.Set("subject", p.Subject)
		}
		if p.Limit > 0 {
			q.Set("limit", fmt.Sprint(p.Limit))
		}
		return s.get("/tasks", q)

	case "claim_task":
		var p struct {
			TaskID   string `json:"taskId"`
			Claimant string `json:"claimant"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("arguments must be an object: %v", err)
		}
		if strings.TrimSpace(p.TaskID) == "" || strings.TrimSpace(p.Claimant) == "" {
			return "", fmt.Errorf("taskId and claimant are required")
		}
		return s.post("/tasks/"+url.PathEscape(p.TaskID)+"/claim",
			map[string]any{"claimant": p.Claimant})

	case "list_agents":
		return s.get("/agents", nil)

	case "query_observations":
		var p struct {
			Subject string `json:"subject"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("arguments must be an object: %v", err)
		}
		if strings.TrimSpace(p.Subject) == "" {
			return "", fmt.Errorf("subject is required")
		}
		q := url.Values{}
		q.Set("subject", p.Subject)
		return s.get("/observations", q)

	case "relay_message":
		var p struct {
			ID      string `json:"id"`
			AgentID string `json:"agentId"`
			Kind    string `json:"kind"`
			Payload string `json:"payload"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("arguments must be an object: %v", err)
		}
		for field, v := range map[string]string{"id": p.ID, "agentId": p.AgentID, "kind": p.Kind, "payload": p.Payload} {
			if strings.TrimSpace(v) == "" {
				return "", fmt.Errorf("%s is required", field)
			}
		}
		// The payload travels as the base64 the CLI produced. This server does not decode, parse or
		// re-encode it: decoding would put the bytes through a transformation it cannot verify, and a
		// re-encode would change what the signature covers.
		return s.post("/messages", map[string]any{
			"id": p.ID, "agentId": p.AgentID, "kind": p.Kind, "payload": p.Payload,
		})

	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

// get performs a GET and returns the body, capped so a node cannot make this read unbounded data.
func (s *server) get(path string, q url.Values) (string, error) {
	endpoint := s.baseURL + path
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	return s.do(http.MethodGet, endpoint, nil)
}

// post performs a POST with a JSON body.
func (s *server) post(path string, body any) (string, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}
	return s.do(http.MethodPost, s.baseURL+path, bytes.NewReader(raw))
}

// maxResponseBytes bounds a response.
//
// A relay is a public endpoint, so its answer is not trusted to be small. The cap keeps a hostile or
// merely broken node from filling the agent's context window with one tool call — which would be a
// denial of service against the agent, not against this process.
const maxResponseBytes = 512 << 10

func (s *server) do(method, endpoint string, body io.Reader) (string, error) {
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("relay %s is unreachable: %w", s.baseURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("read relay response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("relay returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return string(raw), nil
}
