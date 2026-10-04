// Command relayfirst-verifier asserts verification conclusions (S10-0, ADR-0004).
//
// # Why this is a separate binary from relayfirst-node
//
// ADR-0004 ruled on a conflict: criterion ⑩ wants a node whose verification conclusions are
// attributable, but §7.1 wants a node that CANNOT verify — and that second property is a
// structural guarantee, enforced by the import graph, not a policy anyone remembers to
// follow.
//
// Wiring a key into the node would have broken the guarantee: the node binary would link
// secp256k1, the CI gate that proves it cannot forge would fail, and the honest fix would
// have been to relax the gate — trading a tested property for a convenient one.
//
// So the roles are split. This binary holds the key and signs conclusions. relayfirst-node
// holds no key and keeps its guarantee. A deployment can run both, and the two properties
// hold at the same time: a keyless relay that cannot forge, and a keyed verifier whose lies
// are evidence.
//
// # What it deliberately cannot do
//
// It signs ASSERTIONS, never receipts. An assertion says "I checked receipt X and here is my
// conclusion"; a receipt says "I did this work". Only the second is a claim about work
// performed, and only the second could be used to farm points. The two use different EIP-712
// structs and different domains, so a signature for one cannot be used as the other — and the
// import graph keeps internal/mining out, so this binary cannot even build a receipt.
//
// # What an assertion is worth, stated here because this binary produces them
//
// An assertion is an ATTRIBUTED OPINION, not a fact. It proves who claimed what; it does not
// prove the claim is true. A client that reads one and stops checking has replaced a
// verifiable system with a trusted one. Clients must still verify independently (S10-6).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/assertion"
	"github.com/relayfirst/relayfirst/internal/eip712"
	"github.com/relayfirst/relayfirst/internal/receipt"
)

const usage = `relayfirst-verifier — assert verification conclusions (S10-0)

  relayfirst-verifier identity [flags]
      Print this verifier's identity. It is derived from the key, never chosen.

  relayfirst-verifier assert [flags] <receipt.json>
      Re-check a receipt and emit a SIGNED assertion about it.

  relayfirst-verifier check [flags] <receipt.json> <assertion.json>
      Verify a receipt independently AND evaluate an assertion about it. This is the
      client-side check (S10-6): the assertion is an input to evaluate, not an answer
      to believe.

Identity flags:
  --key <hex>        Verifier private key. Also RELAYFIRST_VERIFIER_KEY.
  --chain-id <n>     EVM chain id for the identity (default 8453).

Assert flags:
  --key <hex>        As above.
  --chain-id <n>     As above.
  --url <url>        Re-fetch each anchor and compare its content hash. Off by default:
                     it verifies the CLAIM rather than the receipt, needs the network, and
                     content legitimately changes, so an honest receipt can fail it later.

Check flags:
  --expect-agent <agentId>   Require the receipt to be signed by this agent.

WHAT AN ASSERTION IS: an attributed opinion, not a fact. It proves WHO claimed what. A
client that accepts one without checking has replaced a verifiable system with a trusted
one. Keys are never written to config files: --key would leak into shell history and the
process table, so prefer the environment variable (the same rule as relayfirst).

This binary signs ASSERTIONS, never receipts: it cannot claim work was done. See ADR-0004.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("a command is required")
	}
	switch args[0] {
	case "identity":
		return runIdentity(args[1:])
	case "assert":
		return runAssert(args[1:])
	case "check":
		return runCheck(args[1:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// flags is a deliberately small parser.
//
// # Why not a CLI framework
//
// The same reason relayfirst does not use one: this binary has three commands and a handful
// of flags, and a dependency that large would be more code to audit than the parsing it
// replaces. That deviation is recorded as ADR-3 rather than left implicit.
type flags struct {
	key         string
	chainID     uint64
	url         string
	expectAgent string
	rest        []string
}

func parseFlags(args []string) (*flags, error) {
	f := &flags{chainID: 8453}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		switch {
		case a == "--key":
			v, err := next()
			if err != nil {
				return nil, err
			}
			f.key = v
		case a == "--chain-id":
			v, err := next()
			if err != nil {
				return nil, err
			}
			if _, err := fmt.Sscanf(v, "%d", &f.chainID); err != nil {
				return nil, fmt.Errorf("--chain-id must be a number, got %q", v)
			}
		case a == "--url":
			v, err := next()
			if err != nil {
				return nil, err
			}
			f.url = v
		case a == "--expect-agent":
			v, err := next()
			if err != nil {
				return nil, err
			}
			f.expectAgent = v
		case strings.HasPrefix(a, "-"):
			return nil, fmt.Errorf("unknown flag %q", a)
		default:
			f.rest = append(f.rest, a)
		}
	}
	if f.key == "" {
		f.key = os.Getenv("RELAYFIRST_VERIFIER_KEY")
	}
	if f.key == "" {
		return nil, fmt.Errorf("no key: pass --key or set RELAYFIRST_VERIFIER_KEY. " +
			"Without a key this binary cannot sign, and an unsigned verdict is not attributable")
	}
	return f, nil
}

func runIdentity(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}
	id, err := verifierID(f.key, f.chainID)
	if err != nil {
		return err
	}
	out := map[string]any{
		"verifierId": id,
		"chainId":    f.chainID,
		"note": "this is the identity every assertion from this key is attributed to; " +
			"it is derived from the key and cannot be chosen",
	}
	return emit(out)
}

func runAssert(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}
	if len(f.rest) != 1 {
		return fmt.Errorf("assert needs exactly one receipt file")
	}
	raw, err := os.ReadFile(f.rest[0])
	if err != nil {
		return fmt.Errorf("read receipt: %w", err)
	}

	// Parse and validate before asserting anything. An assertion about a receipt that does
	// not even verify would be a signed claim about nothing, and it would be attributed.
	r, err := receipt.Unmarshal(raw)
	if err != nil {
		return fmt.Errorf("the receipt does not parse: %w", err)
	}
	if err := r.Validate(nil); err != nil {
		// The receipt is broken. That is still an answer worth signing — but only after
		// saying so, so a caller can see it was deliberate rather than a bug.
		fmt.Fprintf(os.Stderr, "note: the receipt does not verify on its own: %v\n", err)
	}

	id, err := verifierID(f.key, f.chainID)
	if err != nil {
		return err
	}

	runner := &assertion.VerifierRunner{
		VerifierID:    id,
		PrivateKeyHex: f.key,
		ChainID:       f.chainID,
		Recheck: func(ctx context.Context, rec *receipt.Receipt) (bool, error) {
			return recheck(ctx, rec, f.url)
		},
		Now: time.Now,
	}

	a, err := runner.Assert(context.Background(), r, raw)
	if err != nil {
		return err
	}
	encoded, err := a.MarshalCanonical()
	if err != nil {
		return err
	}
	if _, err := os.Stdout.Write(encoded); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

func runCheck(args []string) error {
	// check needs no key: it is the client-side path, and a client verifies rather than signs.
	f := &flags{chainID: 8453}
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]

		// Flags that take a value and are accepted-but-ignored here, so a caller can reuse
		// one command line across subcommands. check needs no key: it is the client-side
		// path, and a client verifies rather than signs.
		switch a {
		case "--key", "--chain-id", "--url", "--expect-agent":
			if i+1 >= len(args) {
				return fmt.Errorf("%s needs a value", a)
			}
			value := args[i+1]
			i++
			if a == "--expect-agent" {
				f.expectAgent = value
			}
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("unknown flag %q", a)
			}
			positional = append(positional, a)
		}
	}
	if len(positional) != 2 {
		return fmt.Errorf("check needs a receipt file and an assertion file")
	}

	receiptRaw, err := os.ReadFile(positional[0])
	if err != nil {
		return fmt.Errorf("read receipt: %w", err)
	}
	assertionRaw, err := os.ReadFile(positional[1])
	if err != nil {
		return fmt.Errorf("read assertion: %w", err)
	}
	a, err := assertion.UnmarshalAssertion(assertionRaw)
	if err != nil {
		return err
	}

	var expect []byte
	if f.expectAgent != "" {
		parsed, err := agentid.Parse(f.expectAgent)
		if err != nil {
			return err
		}
		addr, err := eip712.HexToAddress(parsed.Address)
		if err != nil {
			return err
		}
		expect = addr
	}

	out := assertion.Check(receiptRaw, expect, &a)
	return emit(map[string]any{
		"receiptValid":     out.ReceiptValid,
		"assertionPresent": out.AssertionPresent,
		"assertionGenuine": out.AssertionGenuine,
		"assertionVerdict": string(out.AssertionVerdict),
		"agrees":           out.Agrees,
		"note":             out.Note,
	})
}

// verifierID derives the identity from the key, refusing to accept one from the caller.
//
// # Why the identity is never an input
//
// An identity that could be chosen independently of the key would let a caller produce
// assertions attributed to someone else — the exact failure the EIP-712 binding exists to
// prevent. Deriving it removes the possibility rather than checking for it.
func verifierID(key string, chainID uint64) (string, error) {
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

// recheck performs the verification an assertion attests to.
//
// # What it checks, and the honest limit of it
//
// It confirms the receipt's own signature and structure, and optionally re-fetches the anchor
// to see whether the content still matches. That is NOT the full recompute a miner
// performs — reproduction needs the original task's executor and network conditions, which
// this binary does not have.
//
// So the verdict means "the receipt is well-formed, correctly signed, and its anchor still
// says what it claimed" — which is weaker than "the work reproduces". The reason is attached
// rather than implied, because a verifier that overstated its own check would be producing
// exactly the kind of unattributable confidence this whole stage exists to remove.
func recheck(_ context.Context, r *receipt.Receipt, refetchURL string) (bool, error) {
	if err := r.Validate(nil); err != nil {
		return false, fmt.Errorf("the receipt does not verify: %w", err)
	}
	if refetchURL == "" {
		// Offline mode: the signature and structure are what can be checked without a
		// network, and saying that plainly is better than pretending a deeper check ran.
		return true, nil
	}
	return checkAnchor(r, refetchURL)
}

func emit(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
