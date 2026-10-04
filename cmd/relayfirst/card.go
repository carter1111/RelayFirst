package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	a2asdk "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/relayfirst/relayfirst/internal/a2a"
	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/eip712"
	"github.com/relayfirst/relayfirst/internal/publish"
)

// The `card` command (P1 #3).
//
// # Why this exists
//
// `internal/a2a` could build, sign and verify cards, and `internal/publish` could fetch and
// verify them, but NOTHING in production ever called the building half — the card path existed
// only in tests. So the relay-set extension had no way to be published, and an agent had no way
// to declare where it publishes.
//
// That is the gap this closes: an agent can now emit its own signed card, including the relay
// set, and publish it to a node.
//
// # The trust story, restated because this is where it becomes real
//
// The card is signed by the agent's own key, so its contents — including where it publishes —
// are valid because of that signature and not because any directory says so. A node stores the
// card verbatim and cannot check the proof; a reader fetches it and verifies.

const cardUsage = `relayfirst card — build and publish a signed A2A agent card

  relayfirst card show [flags]
      Build the card and print it plus its proof. Nothing is published: this is for
      inspecting what you would advertise before you advertise it.

  relayfirst card publish --relay <node-url> [flags]
      Build, sign and POST the card to a node's /agents directory.

Flags:
  --key <hex>            Agent private key. Also RELAYFIRST_PRIVATE_KEY.
  --chain-id <n>         EVM chain id (default 8453).
  --name <name>          Human-readable agent name (default: relayfirst-agent).
  --description <text>   Human-readable description.
  --url <url>            The agent's own A2A endpoint.
  --ws-url <url>         Optional WebSocket binding to declare as a second interface.
  --skill <id:name>      Repeatable. A skill the agent advertises. At least one is required.
  --relay <url>          Repeatable. The node to publish to, and also a relay to declare.
  --inbox <url>          Declare a relay as an inbox (role=inbox). Priority is the order given.
  --backup <url>         Declare a relay as a backup (role=backup).

The relay set is the point of this command: a card that lists only your endpoint does not say
where your MESSAGES go. Declaring relays here is what lets another agent find your traffic
without a directory telling it, which is the whole reason discovery is trustless.

Keys are NEVER written to the config file; --key would leak into shell history and the process
table, so prefer RELAYFIRST_PRIVATE_KEY.
`

func runCard(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, cardUsage)
		return fmt.Errorf("card needs a subcommand (show or publish)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "show", "publish":
		return runCardBuild(sub, rest)
	case "-h", "--help", "help":
		fmt.Print(cardUsage)
		return nil
	default:
		fmt.Fprint(os.Stderr, cardUsage)
		return fmt.Errorf("unknown card subcommand %q", sub)
	}
}

// runCardBuild builds the card for either subcommand, since the only difference is whether the
// result is printed alone or also posted.
func runCardBuild(sub string, args []string) error {
	key := os.Getenv("RELAYFIRST_PRIVATE_KEY")
	chainID := uint64(8453)
	name := "relayfirst-agent"
	description := ""
	url := ""
	wsURL := ""
	var skills []a2asdk.AgentSkill
	var inboxes, backups []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			return fmt.Errorf("unexpected argument %q", a)
		}
		if i+1 >= len(args) {
			return fmt.Errorf("%s needs a value", a)
		}
		value := args[i+1]
		i++

		switch a {
		case "--key":
			key = value
		case "--chain-id":
			n, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return fmt.Errorf("--chain-id must be a number, got %q", value)
			}
			chainID = n
		case "--name":
			name = value
		case "--description":
			description = value
		case "--url":
			url = value
		case "--ws-url":
			wsURL = value
		case "--skill":
			// id:name, split on the FIRST colon so a name may contain one.
			id, skillName, found := strings.Cut(value, ":")
			if !found || strings.TrimSpace(id) == "" || strings.TrimSpace(skillName) == "" {
				return fmt.Errorf("--skill needs id:name, got %q", value)
			}
			skills = append(skills, a2asdk.AgentSkill{ID: id, Name: skillName})
		case "--inbox":
			inboxes = append(inboxes, value)
		case "--backup":
			backups = append(backups, value)
		case "--relay":
			// A publish target that is also declared as a relay, because a node you publish
			// through is by definition a relay you use. Declaring it here means the card is
			// complete without repeating the URL.
			inboxes = append(inboxes, value)
		default:
			return fmt.Errorf("unknown flag %q", a)
		}
	}

	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("no key: pass --key or set RELAYFIRST_PRIVATE_KEY. " +
			"Without one the card cannot be signed, and an unsigned card proves nothing")
	}
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("--url is required: it is where other agents reach you")
	}
	if len(skills) == 0 {
		return fmt.Errorf("at least one --skill is required: a card advertising no skills " +
			"advertises an agent that can do nothing")
	}

	agentID, err := agentIDFromKey(key, chainID)
	if err != nil {
		return err
	}

	spec := a2a.CardSpec{
		AgentID:      agentID,
		Name:         name,
		Description:  description,
		URL:          url,
		WebSocketURL: wsURL,
		Version:      version,
		Skills:       skills,
	}
	if len(inboxes) > 0 || len(backups) > 0 {
		set := a2a.RelaySetExtension{}
		// Priority is the position in the command line, which is the order the operator
		// expressed a preference in.
		for n, u := range inboxes {
			set.Endpoints = append(set.Endpoints, a2a.RelayEndpoint{
				URL: u, Role: a2a.RelayRoleInbox, Priority: n + 1,
			})
		}
		for n, u := range backups {
			set.Endpoints = append(set.Endpoints, a2a.RelayEndpoint{
				URL: u, Role: a2a.RelayRoleBackup, Priority: n + 1,
			})
		}
		spec.RelaySet = &set
	}

	card, err := a2a.Build(spec)
	if err != nil {
		return err
	}
	cardBytes, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal card: %w", err)
	}
	proof, err := publish.SignCard(key, chainID, agentID, cardBytes)
	if err != nil {
		return err
	}
	proofBytes, err := json.Marshal(proof)
	if err != nil {
		return fmt.Errorf("marshal proof: %w", err)
	}

	// Self-check before publishing. An unsigned or unverifiable card would be stored by a node
	// that cannot check it, and the mistake would surface at a reader — so it is caught here,
	// where the key and the bytes are both in hand.
	if err := publish.VerifyCardProof(proof, cardBytes); err != nil {
		return fmt.Errorf("the card we just built does not verify, refusing to publish it: %w", err)
	}

	if sub == "show" {
		return printJSON(map[string]any{
			"agentId":   agentID,
			"card":      json.RawMessage(cardBytes),
			"proof":     json.RawMessage(proofBytes),
			"note":      "signed by your key; publish with `relayfirst card publish --relay <url>`",
			"published": false,
		})
	}

	if len(inboxes) == 0 {
		return fmt.Errorf("publish needs a target: pass --relay <node-url>")
	}

	postBody, err := json.Marshal(map[string]any{
		"agentId": agentID,
		"card":    json.RawMessage(cardBytes),
		"proof":   json.RawMessage(proofBytes),
	})
	if err != nil {
		return fmt.Errorf("marshal publish body: %w", err)
	}

	var published []string
	var failures []string
	// Publishing to every declared inbox, not just the first: a relay set with several entries
	// means the agent wants redundancy, and publishing to one would make the others decorative.
	seen := map[string]bool{}
	for _, target := range inboxes {
		if seen[target] {
			continue
		}
		seen[target] = true
		if err := postCard(strings.TrimRight(target, "/")+"/agents", postBody); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", target, err))
			continue
		}
		published = append(published, target)
	}

	if len(published) == 0 {
		return fmt.Errorf("published to no node: %s", strings.Join(failures, "; "))
	}

	return printJSON(map[string]any{
		"agentId":   agentID,
		"published": published,
		"failures":  failures,
		"note": "the node stores the card verbatim and cannot verify it; readers verify the proof. " +
			"A partial publish is reported rather than hidden.",
	})
}

// postCard POSTs a signed card to a node's directory.
//
// It uses the standard library rather than reusing internal/publish, because publish's fan-out
// carries relay semantics (partial success, per-relay results) that a one-shot card publish does
// not need, and importing it here would tie a CLI command to a delivery abstraction that does not
// apply.
func postCard(endpoint string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("node returned %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

// agentIDFromKey derives the identity, refusing to accept one from the caller.
//
// It is shared with the verifier's reasoning: an identity chosen independently of the key would
// let someone publish a card attributed to an address they do not control.
func agentIDFromKey(key string, chainID uint64) (string, error) {
	priv, err := eip712.PrivateKeyFromHex(key)
	if err != nil {
		return "", fmt.Errorf("parse key: %w", err)
	}
	addr := eip712.Keccak256(priv.PubKey().SerializeUncompressed()[1:])[12:]
	id, err := agentid.Format(chainID, eip712.AddressToHex(addr))
	if err != nil {
		return "", err
	}
	return id, nil
}
