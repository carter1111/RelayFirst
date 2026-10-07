// Command relayfirst is the RelayFirst CLI.
//
// Subcommands:
//
//	verify   verify a receipt offline, with no network access
//	id       print the agent id derived from a private key
//	mine     run the mining loop continuously, persisting receipts
//	stats    report what the local store holds
//	version  print the version
//
// The `verify` path is the one that matters most: it is the operational proof of
// acceptance criterion ② (MVP.md §11) — a receipt must remain verifiable after
// every RelayFirst server is switched off.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/relayfirst/relayfirst/internal/agentid"
	"github.com/relayfirst/relayfirst/internal/anchor"
	"github.com/relayfirst/relayfirst/internal/config"
	"github.com/relayfirst/relayfirst/internal/llm"
	"github.com/relayfirst/relayfirst/internal/merkle"
	"github.com/relayfirst/relayfirst/internal/mining"
	"github.com/relayfirst/relayfirst/internal/noderead"
	"github.com/relayfirst/relayfirst/internal/publish"
	"github.com/relayfirst/relayfirst/internal/receipt"
	"github.com/relayfirst/relayfirst/internal/scoring"
	"github.com/relayfirst/relayfirst/internal/sqlite"
	"github.com/relayfirst/relayfirst/internal/store"
	"github.com/relayfirst/relayfirst/internal/term"
	"github.com/relayfirst/relayfirst/internal/verification"
)

const usage = `relayfirst — Proof of Agent Work

Usage:
  relayfirst config <get|set|show>       Persist non-secret settings
  relayfirst mine [flags]                Mine into a local store, crediting points
  relayfirst status [flags]              Report points, receipts and anchors
  relayfirst receipts [flags]            List receipts, or --export them
  relayfirst anchor <root|proof|verify>  Compute an epoch's Merkle root, or prove
                                         a receipt is in it (read-only; no chain)
  relayfirst anchor manifest [--epoch]   Publish an epoch's receipt set + root, so an
                                         omission is discoverable
  relayfirst anchor audit --manifest <f> --root <r> [--require <receiptId>]
                                         Check a published set against a root, and flag
                                         receipts the caller names that are missing
  relayfirst verify <receipt.json>       Verify a receipt offline (no network);
                                         add --refetch to also re-check anchors
  relayfirst id <private-key-hex>        Print the agentId for a key
  relayfirst observations --subject <url>
                                         Show independent agents' observations of a
                                         source, grouped by claimed content hash
  relayfirst settle [--epoch <n>]         Settle an epoch's work into points (run once
                                         per epoch; idempotent)
  relayfirst claim [--agent 0x…] [--epoch <n>] [--to 0x…]
                                         Build a points-claim proof for an agent
                                         (read-only; no key, no chain). Settle first.
  relayfirst session <open|close|show|grant>
                                         Emit signed A2A session events, or sign a
                                         delegation grant authorizing a session key
  relayfirst version                     Print the version

Getting started (MVP.md §9.1):
  export RELAYFIRST_PRIVATE_KEY=0x...    # any 32-byte hex key; see below
  relayfirst config set provider local
  relayfirst config set source https://example.com
  relayfirst mine

NOT YET IMPLEMENTED: "relayfirst init". Generating and storing a wallet is blocked
here because writing key material to disk is a security decision that must be made
deliberately, not by an agent. Supply any EVM private key via the environment. Use
a key you generated yourself and keep a backup.

Config keys (all non-secret):
  provider | model | source | relay | semantic

Mine flags:
  --db <path>            SQLite database (default ./relayfirst.db)
  --key <hex>            Agent key (or RELAYFIRST_PRIVATE_KEY)
  --source <url>         Task source URL; repeatable; falls back to config
  --relay <url>          Relay node to publish to; repeatable; falls back to config
  --interval <duration>  Pause between iterations (default 5s)
  --chain <id>           EVM chain id for the agent id (default 8453)
  --once                 Run a single iteration and exit

Semantic extraction (ADR-0002):
  --semantic <text>      Natural-language field description; repeatable
  --provider <name>      local | openai | anthropic (default local)
  --model <id>           Model id for a real provider

Receipts flags:
  --export               Write every receipt to --out as canonical JSON
  --out <dir>            Export directory (default ./relayfirst-receipts)

Anchor flags (S7 — computes values only; it never contacts a chain):
  --epoch <n>            Epoch to anchor (default the current epoch)
  --show-ids             Include the receipt ids the root was built from
  --receipt <id>         Receipt to prove, verify or check
  --index <n>            The receipt's leaf index (from "anchor proof")
  --width <n>            Padded leaf count (from "anchor proof")
  --sibling <hash>       Sibling hash; repeatable (from "anchor proof")
  --root <hash>          The root to check against; REQUIRED by "anchor check" and
                         deliberately not read from the proof, or the check would
                         be circular

Anchor actions:
  root    Compute an epoch's Merkle root from your stored receipts
  proof   Emit an inclusion proof for one receipt
  verify  Check a proof by recomputing the root from your store
  check   Check a proof against a --root you supply, using NO local store. This is
          the anchoring property: someone holding only the published root and the
          proof can confirm inclusion after every server is gone.

Anchoring is read-only by design. Submitting a root is a transaction signed by your
own funded account, so it is a deliberate human action: deploy contracts/
RelayAnchor.sol and call submitRoot, then anyone can call verifyProof. See
docs/stages/S7-report.md for the exact steps.

Keys are NEVER written to the config file. The agent key comes from --key or
RELAYFIRST_PRIVATE_KEY; provider keys come from OPENAI_API_KEY / ANTHROPIC_API_KEY.
A command-line flag would leak a key into shell history and the process table.
--provider local needs no key at all, so a first run works offline.

Points are non-transferable and carry no promised return (MVP.md §6.1).

The verifier is fully offline by default: it never contacts a RelayFirst server, and it makes
no network call at all. That is the point — see MVP.md §11 criterion ②.

Verify flags:
  --refetch              ALSO re-fetch each anchor and check its content hash still matches.
                         Off by default because this verifies the *claim*, not the receipt:
                         it needs the network, and content legitimately changes, so an honest
                         receipt can fail it later (MVP.md §5.4). The default path stays
                         offline so criterion ② is not weakened. Output always reports which
                         mode ran, in the "mode" field.
`

const version = "0.5.0-s6"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// printFirstScreen is the interactive first screen: a wordmark and the three-step
// start.
//
// It exists because acceptance criterion ① is "a STRANGER produces points in ten
// minutes" — a stranger is a person, and a wall of usage text is a worse first screen
// than a short, obvious path. The non-interactive path is untouched: a script gets the
// same usage text it always did.
func printFirstScreen() {
	bold := func(s string) string { return term.Paint(s, term.Bold, true) }
	dim := func(s string) string { return term.Paint(s, term.Dim, true) }
	cyan := func(s string) string { return term.Paint(s, term.Cyan, true) }

	fmt.Println()
	fmt.Println(cyan(logo))
	fmt.Printf("  %s\n\n", dim("Proof of Agent Work — prove your agent did real work, earn non-transferable points"))
	fmt.Printf("  %s\n", bold("Start in three steps"))
	fmt.Printf("    1. %s\n", dim("export RELAYFIRST_PRIVATE_KEY=0x…   # any 32-byte key you own"))
	fmt.Printf("    2. %s\n", dim("relayfirst config set source https://example.com"))
	fmt.Printf("    3. %s\n", dim("relayfirst mine"))
	fmt.Printf("\n  %s\n", dim("points are non-transferable, unpriced, and carry no promised return (MVP.md §6.1)"))
	fmt.Printf("  %s\n\n", dim("run `relayfirst --help` for every command"))
}

// logo is the CLI wordmark, shared in shape with the node's banner.
const logo = `██████╗ ███████╗██╗      █████╗ ██╗   ██╗
██╔══██╗██╔════╝██║     ██╔══██╗╚██╗ ██╔╝
██████╔╝█████╗  ██║     ███████║ ╚████╔╝
██╔══██╗██╔══╝  ██║     ██╔══██║  ╚██╔╝
██║  ██║███████╗███████╗██║  ██║   ██║
╚═╝  ╚═╝╚══════╝╚══════╝╚═╝  ╚═╝   ╚═╝`

func run(args []string) error {
	if len(args) == 0 {
		// With no arguments the CLI is a human reading the first screen. On a terminal
		// that is a branded intro with the three-step start; piped, it stays the exact
		// usage text it has always been, because a script or a `| grep` must not suddenly
		// receive logo art. Same rule as the node's banner (internal/term).
		if term.IsTTY(os.Stdout) {
			printFirstScreen()
		} else {
			fmt.Print(usage)
		}
		return nil
	}

	switch args[0] {
	case "verify":
		if len(args) < 2 {
			return errors.New("verify requires a path to a receipt JSON file")
		}
		return runVerify(args[1], args[2:])

	case "id":
		if len(args) < 2 {
			return errors.New("id requires a private key in hex")
		}
		return runID(args[1], 8453)

	case "mine":
		return runMine(args[1:])

	case "settle":
		return runSettle(args[1:])

	case "claim":
		return runClaim(args[1:])

	case "observations":
		return runObservations(args[1:])

	case "stats":
		// `stats` predates `status`. Kept as an alias rather than a second
		// implementation, because two commands that report the same thing would
		// eventually disagree and there would be no way to tell which was right.
		fmt.Fprintln(os.Stderr, "note: `stats` is now `status`; showing the same report")
		return runStatus(args[1:])

	case "config":
		return runConfig(args[1:])

	case "status":
		return runStatus(args[1:])

	case "receipts":
		return runReceipts(args[1:])

	case "anchor":
		return runAnchor(args[1:])

	case "verify-receipt":
		return runVerifyReceipt(args[1:])

	case "card":
		return runCard(args[1:])

	case "session":
		return runSession(args[1:])

	case "version", "--version", "-v":
		fmt.Println(version)
		return nil

	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil

	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// runVerify validates a receipt's structure and signature. By default it makes no
// network access at all.
//
// # Why re-fetching is opt-in (S1-10)
//
// The default path answers the narrower and more important question: "is this receipt
// authentic and well-formed?" It touches no network, which is what acceptance criterion ②
// (MVP.md §11) requires — a receipt must remain verifiable after every server is switched off.
// Making a network call on the default path would quietly weaken that criterion, so it is not
// done.
//
// `--refetch` answers the *other* question: "does the anchor evidence still describe what its
// source returns?" That is verification of the claim, not of the receipt, and it is time-bounded
// for a reason (MVP.md §5.4): content changes, so an old receipt can be honest and still fail
// this check. A wide window plus this check rejects honest work, and this check without a window
// is arbitrary — so it is a deliberate, explicit action rather than a default.
//
// Both paths always report which one ran, in the `mode` field. A caller that meant to re-fetch
// but forgot the flag must be able to tell, and "no error" must not be read as "the evidence
// holds" when nothing was fetched.
func runVerify(path string, flagArgs []string) error {
	f, err := parseFlags(flagArgs)
	if err != nil {
		return err
	}

	// Validate the flag set rather than ignoring unknown flags.
	//
	// This matters more here than elsewhere: the whole point of --refetch is that the caller
	// opted into a network check. A typo like `--refeth` would otherwise be accepted, silently
	// run the offline path, and report "valid" — leaving the caller believing their anchor
	// evidence had been checked when nothing was fetched.
	for name := range f.values {
		if name != "refetch" {
			return fmt.Errorf("verify: unknown flag --%s (supported: --refetch)", name)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read receipt: %w", err)
	}

	r, err := receipt.Unmarshal(raw)
	if err != nil {
		return err
	}

	if err := r.ValidateStructure(); err != nil {
		return reportVerifyFailure(err)
	}

	if r.Signature == "" {
		return errors.New("receipt has no signature")
	}
	if err := r.Validate(nil); err != nil {
		return reportVerifyFailure(err)
	}

	out := map[string]any{
		"valid":       true,
		"schema":      r.Schema,
		"receiptId":   r.ReceiptID,
		"agentId":     r.AgentID,
		"epoch":       r.Epoch,
		"taskType":    string(r.Task.Type),
		"resultValue": r.Result.Value,
		"anchors":     len(r.Anchors),
	}

	if !f.has("refetch") {
		out["mode"] = "offline"
		out["note"] = "signature and structure verified offline; anchors not re-fetched (pass --refetch to check the claim)"
		return printJSON(out)
	}

	// Opt-in: contact each anchor's source and compare the content hash.
	//
	// A fetch failure is reported as a failure rather than swallowed, because treating an
	// unreachable source as agreement would make the whole check avoidable by taking the
	// source offline — the same reasoning as mining.AnchorConsistency.
	checker := mining.NewAnchorConsistency(anchorFetcher())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out["mode"] = "refetch"
	if err := checker.Check(ctx, r); err != nil {
		out["anchorConsistency"] = false
		out["note"] = err.Error()
		// The receipt is still authentic — re-fetching tests the claim, not the signature.
		// Exiting non-zero would conflate "forged" with "drifted", so this reports the
		// finding in the JSON and in the exit code, with `valid` left true on purpose.
		if perr := printJSON(out); perr != nil {
			return perr
		}
		return errors.New("anchor re-fetch found the evidence no longer matches its source")
	}

	out["anchorConsistency"] = true
	out["note"] = "signature and structure verified offline; anchors re-fetched and still match"
	return printJSON(out)
}

// reportVerifyFailure turns a validation error into a message that names which
// side is behind (H2, S9-0j).
//
// # Why this is not just an error pass-through
//
// An out-of-date verifier and a forged receipt both "fail", and reporting them
// identically sends an operator looking for a forger when the actual fix is to
// upgrade the verifier. The receipt package distinguishes the two error types;
// this is where that distinction reaches a human, which is the only place it
// matters.
func reportVerifyFailure(err error) error {
	if receipt.IsUnsupported(err) {
		return fmt.Errorf(
			"this receipt is from a version this build cannot check — upgrade the verifier.\n"+
				"  This is NOT a claim that the receipt is forged; it is a claim that it cannot be judged here.\n"+
				"  %w", err)
	}
	return err
}

// runID prints the canonical agent id derived from a private key.
func runID(privKeyHex string, chainID uint64) error {
	id, err := receipt.DeriveAgentID(privKeyHex, chainID)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

// flags is a minimal flag parser.
//
// The standard library's flag package is fine, but the CLI mixes repeated flags
// (--source) with positional arguments, and a four-line parser is clearer here
// than configuring a FlagSet to tolerate that.
type flags struct {
	values   map[string]string
	repeated map[string][]string
}

// repeatableFlags lists the flags that may be given more than once.
//
// It has to be an explicit list rather than a heuristic, because the parser
// cannot tell a repeated flag from a value: `--semantic a --semantic b` and
// `--model x` are syntactically identical. A flag missing from this set is
// silently overwritten, which is how `--semantic` was quietly dropped on its
// first outing — the CLI accepted it, reported nothing, and produced positional
// extract tasks that cost no inference at all.
var repeatableFlags = map[string]bool{
	"source":   true,
	"semantic": true,
	"relay":    true,
	"sibling":  true,
	"require":  true,
}

func parseFlags(args []string) (*flags, error) {
	f := &flags{values: map[string]string{}, repeated: map[string][]string{}}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			return nil, fmt.Errorf("unexpected argument %q", arg)
		}

		name := strings.TrimPrefix(arg, "--")
		value := "true"

		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name, value = name[:eq], name[eq+1:]
		} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			value = args[i+1]
			i++
		}

		if repeatableFlags[name] {
			f.repeated[name] = append(f.repeated[name], value)
			continue
		}
		f.values[name] = value
	}
	return f, nil
}

func (f *flags) get(name, fallback string) string {
	if v, ok := f.values[name]; ok {
		return v
	}
	return fallback
}

func (f *flags) has(name string) bool {
	_, ok := f.values[name]
	return ok
}

func (f *flags) sources() []string { return f.repeated["source"] }

func (f *flags) semantics() []string { return f.repeated["semantic"] }

func (f *flags) relays() []string { return f.repeated["relay"] }

// intArg reads an integer flag, reporting a clear error rather than defaulting.
//
// It exists alongside the map accessors so a flag whose absence would silently
// change behaviour (a proof index, a tree width) fails loudly instead of becoming
// zero.
func (f *flags) intArg(name string) (int, error) {
	raw := f.get(name, "")
	if raw == "" {
		return 0, fmt.Errorf("--%s is required", name)
	}
	var n int
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil {
		return 0, fmt.Errorf("--%s must be an integer, got %q", name, raw)
	}
	return n, nil
}

// resolverFor builds the semantic field resolver, if one is warranted.
//
// A nil resolver is not an error: positional extract tasks need none, and every
// non-extract task ignores it. Refusing to start without a provider would make
// plain probing need configuration it does not use.
//
// provider and model are passed in already resolved (flag, then config, then
// default) rather than read from flags here, so this function cannot disagree with
// what runMine reports to the user.
func resolverFor(f *flags, provider, model string) (mining.Resolver, error) {
	name := strings.ToLower(strings.TrimSpace(provider))
	if name == "" {
		name = "local"
	}

	cfg := map[string]string{"model": model}

	switch name {
	case "local":
		// No key, no network, deterministic answers. This is what makes semantic
		// tasks runnable offline and in tests.
		cfg["default"] = "NOT_FOUND"
	case "openai", "anthropic":
		// Keys come from the environment only. Passing a key as a flag would put
		// it in shell history and the process table, and the config file refuses to
		// store one (see internal/config).
		if name == "openai" {
			cfg["apiKey"] = os.Getenv("OPENAI_API_KEY")
		} else {
			cfg["apiKey"] = os.Getenv("ANTHROPIC_API_KEY")
		}
		if cfg["apiKey"] == "" {
			return nil, fmt.Errorf(
				"--provider %s needs %s in the environment; it is deliberately not a flag or a config value",
				name, strings.ToUpper(name)+"_API_KEY")
		}
		cfg["baseURL"] = f.get("base-url", "")
	default:
		return nil, fmt.Errorf("unknown --provider %q (want local, openai or anthropic)", name)
	}

	p, err := llm.NewProvider(name, cfg)
	if err != nil {
		return nil, err
	}
	return llm.NewFieldResolver(p)
}

// runMine runs the mining loop until interrupted.
func runMine(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}

	// Config supplies defaults so the 10-minute path does not require retyping
	// flags. Explicit flags always win, and a missing config file is simply an
	// empty one.
	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		// A malformed config is reported rather than ignored: silently falling back
		// would make a user's saved sources appear to vanish.
		return cfgErr
	}

	dbPath := f.get("db", "./relayfirst.db")

	// The key comes from the flag or the environment, never from the config file
	// (which has no field for it and refuses to store one).
	key := keyFromFlags(f)
	if key == "" {
		return errors.New("no private key: pass --key or set RELAYFIRST_PRIVATE_KEY\n" +
			"  (the key is never read from the config file; see `relayfirst config show`)")
	}

	sources := f.sources()
	if len(sources) == 0 {
		sources = cfg.Sources
	}
	if len(sources) == 0 {
		return errors.New("at least one --source URL is required\n" +
			"  set a default with: relayfirst config set source https://example.com")
	}

	chainID := uint64(8453)
	if raw := f.get("chain", ""); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &chainID); err != nil {
			return fmt.Errorf("invalid --chain %q: %w", raw, err)
		}
	}

	interval := mining.DefaultInterval
	if raw := f.get("interval", ""); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("invalid --interval %q: %w", raw, err)
		}
		interval = d
	}

	agentID, err := receipt.DeriveAgentID(key, chainID)
	if err != nil {
		return err
	}

	generator, err := mining.NewGenerator(sources)
	if err != nil {
		return err
	}

	semantics := f.semantics()
	if len(semantics) == 0 {
		semantics = cfg.Semantics
	}
	generator.SemanticFields = semantics

	// The provider resolves flags, then config, then a safe default. "local" is
	// the default because a first run must work with no key and no network.
	provider := f.get("provider", "")
	if provider == "" {
		provider = cfg.Provider
	}
	if provider == "" {
		provider = "local"
	}
	model := f.get("model", "")
	if model == "" {
		model = cfg.Model
	}
	// Name the provider in the spec so a semantic extract's work block reports the
	// provider that was actually used, not "none".
	generator.Provider = provider

	resolver, err := resolverFor(f, provider, model)
	if err != nil {
		return err
	}

	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	receipts := store.NewReceiptStore(db)
	ledgers := store.NewScoringLedgers(db)

	// The sink both persists and credits. Without it the miner produces work that
	// no ledger recognises, which is what left the points economy inert before.
	//
	// The verdict source is selectable because the choice materially changes what a point
	// means. `verifier` reads a verifier's recorded conclusion; `self-check` reproduces
	// the pre-S4 behaviour, where the miner's own signature validation stood in for
	// verification. The default is self-check so an existing single-machine workflow keeps
	// working, and the active choice is printed at startup rather than left implicit.
	verdicts, verdictsLabel := verdictSource(f, db)

	var sink mining.ReceiptSink = &mining.ScoringSink{
		Inner:     receipts,
		Receipts:  receipts,
		Artifacts: ledgers.Artifacts,
		Work:      ledgers.Work,
		Points:    ledgers.Points,
		Verdicts:  verdicts,
		OnVerdict: reportVerdict,
		// A scoring failure does not fail the run (the receipt is stored and can be
		// re-scored), but it must be visible: otherwise "the ledger rejected this
		// credit" looks exactly like "this receipt earned nothing".
		OnError: reportScoringError,
	}

	// Whether stdout is a terminal is decided once here and reused by the banner and the
	// progress line, so the two cannot disagree about which mode the run is in.
	interactive := term.IsTTY(os.Stdout)

	// Live progress state. It is created before the runner so the progress hook can
	// read the same ledgers the sink writes to — which is what makes the displayed
	// numbers the real ones rather than a separate tally that could drift.
	live := &liveProgress{
		AgentID:     agentID,
		Points:      ledgers.Points,
		Receipts:    receipts,
		EpochOf:     scoring.EpochOf,
		StartedAt:   time.Now(),
		Interactive: interactive,
	}

	// Publishing wraps the sink rather than replacing it, so a receipt is always
	// stored and scored locally first. With no relay configured the miner is purely
	// local, which is a legitimate way to run.
	relays := f.relays()
	if len(relays) == 0 {
		relays = cfg.Relays
	}
	if len(relays) > 0 {
		pub, err := publish.New(relays...)
		if err != nil {
			return err
		}
		sink = &publish.Sink{
			Inner:       sink,
			Publisher:   pub,
			ArtifactKey: scoring.ArtifactKey,
			OnOutcome:   reportDelivery,
		}
	}

	loop := &mining.Loop{
		AgentID:     agentID,
		PrivKeyHex:  key,
		Generator:   generator,
		Deps:        mining.Deps{Fetcher: anchorFetcher(), Resolver: resolver},
		EpochLength: scoring.EpochLength,
	}

	runner, err := mining.NewRunner(mining.RunnerConfig{
		Loop:        loop,
		Sink:        sink,
		ArtifactKey: scoring.ArtifactKey,
		Interval:    interval,
		OnResult:    live.Report,
	})
	if err != nil {
		return err
	}

	if f.has("once") {
		// A single iteration, for smoke testing.
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return runOnce(ctx, loop, sink)
	}

	// On a terminal the startup block is a short branded banner; piped, it stays the
	// plain lines it has always been, because the log of a run is something a person and
	// a script both read and only one of them wants a wordmark.
	if interactive {
		d := func(s string) string { return term.Paint(s, term.Dim, true) }
		b := func(s string) string { return term.Paint(s, term.Bold, true) }
		c := func(s string) string { return term.Paint(s, term.Cyan, true) }
		fmt.Println()
		fmt.Println(c(logo))
		fmt.Printf("  %s\n\n", d("Proof of Agent Work"))
		fmt.Printf("  %-12s %s\n", b("mining as"), c(agentID))
		fmt.Printf("  %-12s %s\n", b("database"), dbPath)
		fmt.Printf("  %-12s %d\n", b("sources"), len(sources))
		fmt.Printf("  %-12s %s\n", b("interval"), interval)
		fmt.Printf("  %-12s %s\n", b("verdicts"), verdictsLabel)
		if len(generator.SemanticFields) > 0 {
			fmt.Printf("  %-12s %d field(s) via %s (extract tasks cost inference)\n",
				b("semantic"), len(generator.SemanticFields), provider)
		}
		if earned := ledgers.Points.Balance(agentID); earned > 0 {
			fmt.Printf("  %-12s %.4f points\n", b("earned"), earned)
		}
		fmt.Printf("\n  %s\n\n", d("press ctrl-c to stop"))
	} else {
		fmt.Printf("mining as %s\n", agentID)
		fmt.Printf("  database: %s\n", dbPath)
		fmt.Printf("  sources:  %d\n", len(sources))
		fmt.Printf("  interval: %s\n", interval)
		// Report which verdict source is in use: it decides what a point means, so an
		// operator watching the numbers needs to know which evidence produced them.
		fmt.Printf("  verdicts: %s\n", verdictsLabel)
		if len(generator.SemanticFields) > 0 {
			fmt.Printf("  semantic: %d field(s) via %s (extract tasks cost inference)\n",
				len(generator.SemanticFields), provider)
		}
		// Report points earned before this run, so a restart does not look like it
		// wiped the balance.
		if earned := ledgers.Points.Balance(agentID); earned > 0 {
			fmt.Printf("  earned so far: %.4f points\n", earned)
		}
		fmt.Println("press ctrl-c to stop")
	}

	// Cancel on interrupt so the loop can shut down between iterations rather
	// than mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = runner.Run(ctx)
	if errors.Is(err, context.Canceled) {
		produced, failed := runner.Stats()
		// Close the in-place line before the summary, or the two would share a row.
		clearLiveLine()
		fmt.Printf("\nstopped: %d receipts produced, %d attempts failed\n", produced, failed)
		fmt.Printf("points: %.4f total (%d receipt(s) credited)\n",
			ledgers.Points.Balance(agentID), countCredits(ledgers.Points.Entries(), agentID))
		return nil
	}
	return err
}

// providerName reports the provider name behind a resolver, for display only.
//
// It returns the configured name rather than introspecting the resolver: the
// adapter (llm.FieldResolver) wraps a provider and exposes no Name of its own, so
// a type assertion would silently fall through to a generic label whenever a real
// provider was in use — reporting the least useful string in exactly the case
// where the operator most wants to know.
func providerName(configured string) string {
	if strings.TrimSpace(configured) == "" {
		return "local"
	}
	return configured
}

// runOnce performs a single mining iteration and reports the outcome.
func runOnce(_ context.Context, loop *mining.Loop, sink mining.ReceiptSink) error {
	res := loop.MineOnce()
	if res.Err != nil {
		return fmt.Errorf("mining failed: %w", res.Err)
	}

	key, keyErr := scoring.ArtifactKey(res.Receipt)
	if keyErr != nil {
		key = ""
	}
	if err := sink.Save(res.Receipt, key, time.Now()); err != nil {
		return fmt.Errorf("persist: %w", err)
	}

	return printJSON(map[string]any{
		"produced":  true,
		"receiptId": res.Receipt.ReceiptID,
		"agentId":   res.Receipt.AgentID,
		"taskType":  string(res.Task),
		"attempts":  res.Attempts,
		"artifact":  key,
	})
}

// runObservations shows the cross-verification view for a subject (S10-2).
//
// # Why this exists as a command
//
// The "price / reachability data for a trading bot" use case needs a one-page demo: "N
// independent agents observed this source; do they agree". The node has the endpoint
// and nothing surfaced it, so the demo could not be run. This is that surface, and it is
// deliberately read-only and node-optional in spirit — it can point at any node.
//
// It does NOT claim the observations are true. It groups them by claimed content hash and
// reports how many DISTINCT agents back each group, which is the honest form of "they
// agree": agreement is not correctness, and two agents can collude (the node's own note
// says so, and it is repeated in the output).
func runObservations(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}

	subject := f.get("subject", "")
	if strings.TrimSpace(subject) == "" {
		return errors.New("observations needs --subject <url>; the node indexes observations by subject")
	}
	// --relay is a repeatable flag, so it lives in f.repeated, not f.values; reading it
	// with f.get would silently return the default and ignore what the caller passed.
	relay := ""
	if rs := f.relays(); len(rs) > 0 {
		relay = rs[0]
	}
	if relay == "" {
		relay = os.Getenv("RELAYFIRST_RELAY")
	}
	if relay == "" {
		relay = "http://localhost:8080"
	}

	client, err := noderead.New(relay)
	if err != nil {
		return err
	}

	// Pull the subject's observations, then ask for the cross-verification view of one of
	// them. The route is keyed by receipt id, so a subject alone is not enough.
	obs, err := client.Observations(subject, 50)
	if err != nil {
		return err
	}
	if len(obs) == 0 {
		return printJSON(map[string]any{
			"subject": subject,
			"count":   0,
			"note":    "no observations for this subject on this node",
		})
	}

	_, groups, err := client.Evidence(obs[0].ReceiptID)
	if err != nil {
		return err
	}

	type groupOut struct {
		ContentHash    string   `json:"contentHash"`
		DistinctAgents int      `json:"distinctAgents"`
		Observations   int      `json:"observations"`
		ReceiptIDs     []string `json:"receiptIds"`
	}
	outs := make([]groupOut, 0, len(groups))
	for _, g := range groups {
		outs = append(outs, groupOut{
			ContentHash:    g.ContentHash,
			DistinctAgents: g.DistinctAgents,
			Observations:   g.Observations,
			ReceiptIDs:     g.ReceiptIDs,
		})
	}

	verdict := "no observations"
	if len(groups) == 1 {
		verdict = fmt.Sprintf("%d independent agents agree", groups[0].DistinctAgents)
	} else if len(groups) > 1 {
		verdict = fmt.Sprintf("%d groups disagree — the observers saw different content (real change, or worth investigating)", len(groups))
	}

	return printJSON(map[string]any{
		"subject":  subject,
		"relay":    relay,
		"observed": len(obs),
		"groups":   outs,
		"verdict":  verdict,
		"note": "each observation is a signed receipt re-verifiable offline (relayfirst verify <receipt.json>); " +
			"agreement is not correctness — the node does not verify, and distinct agents can still collude. " +
			"\"distinctAgents\" is what to read, not \"observations\".",
	})
}

// runSettle finalizes an epoch: it turns accumulated WORK into POINTS (D1).
//
// # Why settlement is an explicit command and not automatic
//
// Settlement is a once-per-epoch step over a CLOSED epoch. Doing it on the mining path
// would settle a moving total, and the number would change as more work arrived — the
// opposite of a settlement. So the trigger is a deliberate command an operator or a
// scheduler runs, and it is idempotent per epoch (see ScoringSink.Finalize), which
// makes re-running it safe after a crash. See docs/notes/settlement-trigger.md.
func runSettle(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}

	dbPath := f.get("db", "./relayfirst.db")
	epoch := scoring.EpochOf(time.Now())
	if raw := f.get("epoch", ""); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &epoch); err != nil {
			return fmt.Errorf("--epoch must be a number, got %q", raw)
		}
	}

	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ledgers := store.NewScoringLedgers(db)
	sink := &mining.ScoringSink{Work: ledgers.Work, Points: ledgers.Points}

	settled, err := sink.Finalize(epoch, time.Now())
	if err != nil {
		return err
	}

	// Build the epoch's BALANCE root from each agent's CUMULATIVE total through this
	// epoch (MVP.md §6.2b). Not this epoch's award: a claim proves "earned N by epoch
	// N" and SETS the on-chain total to N (contracts/RelayPoints.sol), so N must be
	// cumulative or a claim would erase every earlier epoch the agent did not claim.
	// Reading from the points ledger (which now holds this epoch too) makes the root
	// and the credited points the same numbers.
	cumulative, err := store.NewPointsLedger(db).CumulativeMicro(epoch)
	if err != nil {
		return err
	}
	balanceRoot, err := balanceRootFromCumulative(epoch, cumulative)
	if err != nil {
		return err
	}

	// Report what was settled, in a stable order, so an operator can see each agent's
	// emitted points rather than only a total.
	agents := make([]string, 0, len(settled))
	for a := range settled {
		agents = append(agents, a)
	}
	sort.Strings(agents)

	type row struct {
		AgentID string  `json:"agentId"`
		Points  float64 `json:"points"`
	}
	rows := make([]row, 0, len(agents))
	var total float64
	for _, a := range agents {
		rows = append(rows, row{AgentID: a, Points: settled[a]})
		total += settled[a]
	}

	return printJSON(map[string]any{
		"epoch":         epoch,
		"budget":        scoring.EpochBudget(epoch),
		"settledAgents": len(rows),
		"settledTotal":  total,
		"balanceRoot":   balanceRoot.Hex(),
		"points":        rows,
		"note": "settled points are derived from accumulated work and are idempotent per " +
			"epoch: re-running this command writes nothing new. Where the total is below the " +
			"budget, the per-agent cap withheld the surplus (that direction is safe). " +
			"balanceRoot is the Merkle root a points claim is verified against (MVP.md §6.2b).",
	})
}

// runClaim produces a points-claim proof for an agent (IMP-4, MVP.md §6.2b).
//
// # Why this needs no key
//
// It reads the local ledger and builds a Merkle proof. Producing a proof is not signing,
// so no private key is involved and nothing here can spend an identity. The on-chain
// claim is a separate, later step (TGE) that DOES need a wallet signature; this command
// only produces the proof that step will consume.
//
// # It proves the CUMULATIVE total, not one epoch's
//
// contracts/RelayPoints.sol SETS the total from the proof, and a claim that skipped an
// epoch must not lose that epoch's points, so the leaf's total is cumulative through the
// requested epoch. A per-epoch total would erase everything earned earlier.
//
// # The agent defaults to the local key, if there is one
//
// With --key (or RELAYFIRST_PRIVATE_KEY) the agent is derived from it, matching by
// address rather than by a name the ledger would have to trust. With no key and no
// --agent, it defaults to the single agent the ledger knows, and refuses if there are
// several, because guessing which identity to prove would be a way to claim the wrong
// one.
func runClaim(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}

	db, err := store.Open(f.get("db", "./relayfirst.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	epoch := scoring.EpochOf(time.Now())
	if raw := f.get("epoch", ""); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &epoch); err != nil {
			return fmt.Errorf("--epoch must be a number, got %q", raw)
		}
	}

	ledger := store.NewPointsLedger(db)
	cumulative, err := ledger.CumulativeMicro(epoch)
	if err != nil {
		return err
	}

	agent, err := resolveClaimAgent(f, cumulative)
	if err != nil {
		return err
	}

	micro, ok := cumulative[agent]
	if !ok || micro <= 0 {
		return fmt.Errorf("agent %s has no settled points through epoch %d; run `relayfirst settle --epoch %d` first (or it did not mine)", agent, epoch, epoch)
	}

	agentHash, err := merkle.AgentID(agent)
	if err != nil {
		return err
	}

	// Rebuild the whole tree so the proof is against the same leaves the root was built
	// from. Merkle.BalanceProof orders the leaves canonically, which is what makes the
	// proof verify against the root `settle` printed.
	totals := make(map[merkle.Hash]int64, len(cumulative))
	for a, m := range cumulative {
		if m <= 0 {
			continue
		}
		h, err := merkle.AgentID(a)
		if err != nil {
			return fmt.Errorf("agent id %q: %w", a, err)
		}
		totals[h] = m
	}
	proof, err := merkle.BalanceProof(epoch, totals, agentHash)
	if err != nil {
		return err
	}

	out := map[string]any{
		"agentId":    agent,
		"agentHash":  agentHash.Hex(),
		"epoch":      epoch,
		"totalMicro": micro,
		"total":      float64(micro) / scoring.MicroPerPoint,
		"root":       proof.Root.Hex(),
		"leaf":       proof.Leaf.Hex(),
		"index":      proof.Index,
		"proof":      hashStrings(proof.Siblings),
		"note": "this is a claim PROOF over the cumulative total through this epoch; it is " +
			"not a claim. Producing it needs no key. Submitting it on-chain is a later, " +
			"signed step (TGE).",
	}
	if to := f.get("to", ""); to != "" {
		out["to"] = to
	}
	return printJSON(out)
}

// resolveClaimAgent picks which agent a claim is for, from --agent, --key, or the sole
// agent in the ledger. It refuses to guess when there are several.
func resolveClaimAgent(f *flags, cumulative map[string]int64) (string, error) {
	if raw := f.get("agent", ""); raw != "" {
		if _, err := agentid.Parse(raw); err != nil {
			return "", fmt.Errorf("--agent %q: %w", raw, err)
		}
		return raw, nil
	}

	if key := keyFromFlags(f); key != "" {
		return agentIDFromKey(key, 8453)
	}

	agents := make([]string, 0, len(cumulative))
	for a := range cumulative {
		agents = append(agents, a)
	}
	sort.Strings(agents)
	switch len(agents) {
	case 0:
		return "", errors.New("no settled agents in this store; nothing to claim (run `relayfirst settle` first)")
	case 1:
		return agents[0], nil
	default:
		return "", fmt.Errorf("this store has %d agents; pass --agent or --key to say which one to claim", len(agents))
	}
}

// hashStrings renders hashes as lowercase 0x-hex strings for JSON output.
func hashStrings(hs []merkle.Hash) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Hex())
	}
	return out
}

// reportVerdict prints the outcome of scoring one receipt.
//
// Seeing this line is how an operator knows the economy is actually moving. A
// miner that only prints "ok probe" cannot be distinguished from one that is
// producing worthless work.
func reportVerdict(r *receipt.Receipt, v scoring.Verdict) {
	clearLiveLine()
	// This reports WORK, not points (D1). A receipt contributes work; points appear
	// when the epoch settles (`relayfirst settle`). Printing "+points" here would
	// promise points the run has not yet, and may never, emit.
	if v.Work > 0 {
		fmt.Printf("  + %s → +%.4f work (points at epoch settlement)\n", shortID(r.ReceiptID), v.Work)
		return
	}
	// A zero award is informative rather than noisy: it is the dedup ledger
	// working, and an operator should be able to see that.
	reason := v.Reason
	if reason == "" {
		reason = "no credit"
	}
	fmt.Printf("  · %s → %s\n", shortID(r.ReceiptID), reason)
}

// reportScoringError surfaces a scoring step that failed for one stored receipt.
//
// Live feedback is not decoration here: a credit that never landed looked identical
// to a receipt that earned nothing, so a miner could watch a run produce work and
// never learn that the ledger rejected every credit. The line goes through
// clearLiveLine first so it does not collide with the in-place progress row.
func reportScoringError(r *receipt.Receipt, stage string, err error) {
	clearLiveLine()
	fmt.Printf("  ! %s: scoring %s failed: %v\n", shortID(r.ReceiptID), stage, err)
	fmt.Printf("    the receipt is stored and can be re-scored; points were NOT credited\n")
}

// reportDelivery prints the outcome of publishing one receipt.
//
// A per-relay line rather than a single "published" line: with fan-out, "some node
// took it" and "every node took it" are different situations, and an operator
// watching for a dead node needs to see which one it was.
func reportDelivery(r *receipt.Receipt, o publish.Outcome) {
	clearLiveLine()
	if o.Failed == 0 {
		fmt.Printf("  → relayed to %d node(s)\n", o.Acked)
		return
	}
	fmt.Printf("  → relayed to %d/%d node(s)\n", o.Acked, len(o.Results))
	for _, res := range o.Results {
		if res.Err != nil {
			fmt.Printf("      ! %s: %v\n", res.Relay, res.Err)
		}
	}
}

// reportProgress prints a one-line summary per iteration.
//
// Live feedback is not decoration: an operator watching a miner needs to see that
// work is happening, and a silent loop is indistinguishable from a hung one.
//
// It is retained for the single-iteration (`--once`) path, where the cumulative
// `liveProgress` summary would be noise.
func reportProgress(res mining.RunResult) {
	if res.Err != nil {
		fmt.Printf("  ! %s\n", res.Err)
		return
	}
	fmt.Printf("  ok %s  %s  (%d attempt(s))\n",
		res.Task, shortID(res.Receipt.ReceiptID), res.Attempts)
}

// liveProgress prints a running summary after each iteration (S6-4).
//
// # Why the numbers come from the ledger, not from a counter here
//
// The displayed points are read back from the same points ledger the scoring sink
// writes to. A local tally would be cheaper and would eventually disagree with the
// ledger — and a miner is precisely the person who must be able to trust the
// number, since they cannot see the ledger directly.
//
// # Why this is a struct rather than a closure
//
// Go's loop-scoped variables make a closure over mutable state easy to get subtly
// wrong, and this runs on every iteration. An explicit type also gives the tests
// something to call directly.
type liveProgress struct {
	AgentID  string
	Points   scoring.PointsLedger
	Receipts *store.ReceiptStore
	EpochOf  func(time.Time) uint64

	StartedAt time.Time

	// Tasks counts completed iterations. It is only used for display, so a plain
	// field is fine — the authoritative counts come from the ledger and the store.
	Tasks int

	// Interactive selects an in-place single line (a terminal) over one line per
	// iteration (a pipe). It is a field rather than a global so the two behaviours are
	// testable side by side, and so the choice is made once by the caller that already
	// knows whether stdout is a terminal.
	Interactive bool
}

// Report prints the running summary for one completed iteration.
func (p *liveProgress) Report(res mining.RunResult) {
	if p == nil {
		return
	}
	if res.Err != nil {
		fmt.Printf("  ! %s\n", res.Err)
		return
	}

	p.Tasks++

	epoch := p.EpochOf(time.Now())
	epochPoints := p.Points.EpochBalance(p.AgentID, epoch)
	lifetime := p.Points.Balance(p.AgentID)

	// The anchor count comes from a single query rather than by loading every stored receipt.
	// The old approach grew with the agent's whole history on a loop that runs every few
	// seconds, so a long-running miner got steadily slower for no reason.
	anchors := 0
	if p.Receipts != nil {
		if n, err := p.Receipts.CountAnchorsByAgent(p.AgentID); err == nil {
			anchors = n
		}
	}

	if p.Interactive {
		// One line that rewrites itself, so a long run does not scroll the numbers away.
		// The carriage return returns to the start of the line; the padding clears any
		// tail left by a previous, longer line so digits do not smear.
		line := fmt.Sprintf("  %s %-7s  |  epoch %d  points %s / %s  tasks %d  anchors %d",
			term.Paint("●", term.Green, true), res.Task, epoch,
			term.Paint(fmt.Sprintf("%.4f", epochPoints), term.Cyan, true),
			fmt.Sprintf("%.4f", lifetime), p.Tasks, anchors)
		const pad = 8
		fmt.Printf("\r%s%s", line, strings.Repeat(" ", pad))
		liveLineOpen = true
		return
	}
	fmt.Printf("  ok %-7s %s  |  epoch %d  points epoch: %.4f  lifetime: %.4f  tasks: %d  anchors: %d\n",
		res.Task, shortID(res.Receipt.ReceiptID), epoch,
		epochPoints, lifetime, p.Tasks, anchors)
}

// liveLineOpen records that an in-place progress line is currently on screen, so a
// message printed by any other reporter can end that line first instead of appending
// onto it. It is a package variable because the verdict and delivery reporters are
// free functions the runner calls directly; the alternative is threading a handle
// through every callback for one carriage return.
var liveLineOpen bool

// clearLiveLine ends an in-place progress line before a normal message is printed.
//
// Without it, a line that is rewritten with `\r` has no terminating newline, so the
// next message — a points award, a delivery note — is appended to the same row and the
// two run together. The fix is a single newline, emitted only when a live line is open,
// so a piped run (which never opens one) is unchanged.
func clearLiveLine() {
	if liveLineOpen {
		fmt.Println()
		liveLineOpen = false
	}
}

// anchorFetcher returns the fetcher the mining loop uses to capture evidence.
func anchorFetcher() *anchor.Fetcher { return anchor.NewFetcher() }

// runStats reports what the store holds.
func runStats(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}

	db, err := store.Open(f.get("db", "./relayfirst.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	ledgers := store.NewScoringLedgers(db)
	receipts := store.NewReceiptStore(db)
	entries := ledgers.Points.Entries()

	var total float64
	for _, e := range entries {
		total += e.Points
	}

	// An empty object is a poor answer for "what do I have"; an explicit zero
	// lets a caller tell "nothing mined" apart from "stats failed".
	out := map[string]any{
		"receipts":          receipts.Count(),
		"distinctArtifacts": ledgers.Artifacts.Len(),
		"creditedAgents":    ledgers.Points.AgentCount(),
		"creditedReceipts":  len(entries),
		"totalPoints":       total,
	}

	// When a key is available, report that agent's balance specifically, which is
	// the number a miner actually cares about.
	key := f.get("key", "")
	if key == "" {
		key = os.Getenv("RELAYFIRST_PRIVATE_KEY")
	}
	if strings.TrimSpace(key) != "" {
		chainID := uint64(8453)
		if raw := f.get("chain", ""); raw != "" {
			_, _ = fmt.Sscanf(raw, "%d", &chainID)
		}
		if agentID, err := receipt.DeriveAgentID(key, chainID); err == nil {
			out["agentId"] = agentID
			out["yourBalance"] = ledgers.Points.Balance(agentID)
			out["yourCredited"] = countCredits(entries, agentID)
		}
	}

	// The status of the points economy belongs in the output, not only in prose.
	// If this is 0 the miner is working but earning nothing, which is the failure
	// this whole change exists to prevent.
	out["note"] = "points are non-transferable and unpriced; epoch budget decays from B0 (MVP.md §6)"
	return printJSON(out)
}

// countCredits reports how many entries belong to one agent.
func countCredits(entries []scoring.Entry, agentID string) int {
	n := 0
	for _, e := range entries {
		if e.AgentID == agentID {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- config

// runConfig gets or sets persisted, non-secret settings.
//
// It exists so the 10-minute path does not require retyping flags, and so a first
// run can be configured once rather than every time.
func runConfig(args []string) error {
	if len(args) == 0 {
		return errors.New("config requires an action: get, set, or show")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	switch args[0] {
	case "set":
		if len(args) < 3 {
			return errors.New("config set requires a key and a value, e.g. config set provider local")
		}
		if err := cfg.Set(args[1], strings.Join(args[2:], " ")); err != nil {
			return err
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		path, _ := config.Path()
		fmt.Printf("set %s\n", args[1])
		fmt.Printf("  file: %s\n", path)
		// State the secret policy at the moment it matters, not only in the docs.
		fmt.Println("  note: config holds no keys; RELAYFIRST_PRIVATE_KEY / OPENAI_API_KEY / ANTHROPIC_API_KEY stay in the environment")
		return nil

	case "get":
		if len(args) < 2 {
			return errors.New("config get requires a key")
		}
		v, err := cfg.Get(args[1])
		if err != nil {
			return err
		}
		fmt.Println(v)
		return nil

	case "show":
		return printJSON(cfg)

	default:
		return fmt.Errorf("unknown config action %q (want get, set or show)", args[0])
	}
}

// ---------------------------------------------------------------- status

// agentOut is one agent's line in the status report.
//
// A distinct-artifact count is global by construction (invariant A6), so it is reported
// once rather than per agent; this is the per-agent half.
type agentOut struct {
	AgentID        string  `json:"agentId"`
	Epoch          uint64  `json:"epoch"`
	PointsLifetime float64 `json:"pointsLifetime"`
	PointsEpoch    float64 `json:"pointsThisEpoch"`
	Receipts       int     `json:"receipts"`
	Credited       int     `json:"creditedReceipts"`
	Anchors        int     `json:"anchors"`
}

// runStatus reports the user's contribution in one place.
//
// MVP.md §1.1 describes the target user as a mercenary: they want to know what they
// have. A status command that reports only receipt counts cannot answer that, so
// points and balance are reported first.
func runStatus(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}

	// Config supplies a default key path, but the key itself is never read from
	// the config file.
	cfg, _ := config.Load()

	db, err := store.Open(f.get("db", "./relayfirst.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	receipts := store.NewReceiptStore(db)
	ledgers := store.NewScoringLedgers(db)

	all, err := receipts.All(0)
	if err != nil {
		return err
	}

	// Group by agent so the output is meaningful on a machine that has mined under
	// more than one identity, without assuming which.
	type agentStat struct {
		Receipts     int
		Anchors      int
		Points       float64
		Credited     int
		ArtifactsSet map[string]struct{}
	}
	byAgent := map[string]*agentStat{}

	for _, r := range all {
		s, ok := byAgent[r.AgentID]
		if !ok {
			s = &agentStat{ArtifactsSet: map[string]struct{}{}}
			byAgent[r.AgentID] = s
		}
		s.Receipts++
	}

	for agent, s := range byAgent {
		agentReceipts, err := receipts.ByAgentAll(agent)
		if err != nil {
			return err
		}
		s.Anchors = receipts.AnchorsFor(agentReceipts)
		s.Points = ledgers.Points.Balance(agent)
		s.Credited = countCredits(ledgers.Points.Entries(), agent)
	}

	epoch := scoring.EpochOf(time.Now())
	out := map[string]any{
		"epoch":             epoch,
		"epochEndsAt":       epochEndsAt(epoch),
		"receipts":          receipts.Count(),
		"distinctArtifacts": ledgers.Artifacts.Len(),
		"creditedAgents":    ledgers.Points.AgentCount(),
		"totalPoints":       ledgerTotal(ledgers.Points.Entries()),
		"note":              "points are non-transferable, unpriced, and carry no promised return (MVP.md §6.1)",
	}

	agents := make([]agentOut, 0, len(byAgent))
	for agent, s := range byAgent {
		agents = append(agents, agentOut{
			AgentID:        agent,
			Epoch:          epoch,
			PointsLifetime: s.Points,
			PointsEpoch:    ledgers.Points.EpochBalance(agent, epoch),
			Receipts:       s.Receipts,
			Credited:       s.Credited,
			Anchors:        s.Anchors,
		})
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].AgentID < agents[j].AgentID })
	if len(agents) > 0 {
		out["agents"] = agents
	}

	// Report configuration gaps as advice rather than as errors: a first run has
	// none of this, and that is a normal starting state.
	var advices []string
	if len(cfg.Sources) == 0 && len(f.sources()) == 0 {
		advices = append(advices, "no task sources configured: run `relayfirst config set source https://example.com`")
	}
	if cfg.Provider == "" && f.get("provider", "") == "" {
		advices = append(advices, "no inference provider configured: `relayfirst config set provider local` works offline")
	}
	if len(agents) == 0 {
		advices = append(advices, "no receipts yet: run `relayfirst mine --once --source https://example.com`")
	}
	if len(advices) > 0 {
		out["next"] = advices
	}

	// The pending estimate is computed ONLY for the terminal view and is deliberately
	// NOT added to `out`. The piped JSON is frozen (U1): scripts and the docs read
	// these fields, and a new one would change that contract. It is also here rather
	// than in the renderer because it is a query, and the renderer is meant to only
	// draw what it is handed.
	var pending map[string]float64
	if term.IsTTY(os.Stdout) {
		totals, err := ledgers.Work.Totals(epoch)
		if err != nil {
			return err
		}
		allocated, err := scoring.Allocate(epoch, totals)
		if err != nil {
			return err
		}
		// Not capped: this is a projection of what the settle step would pay, and the
		// cap is applied there. Showing the uncapped share here would overstate a
		// whale's pending number, so apply the same cap the settle applies.
		pending = scoring.CapAllocation(epoch, allocated)
		if pending == nil {
			pending = map[string]float64{}
		}
	}

	// On a terminal, render a readable dashboard; piped, emit the JSON that scripts and
	// the docs already depend on. The JSON object above is the SAME data either way — the
	// human view is a rendering of it, never a second query, so the two cannot disagree.
	if term.IsTTY(os.Stdout) {
		printStatusDashboard(out, agents, advices, pending)
		return nil
	}
	return printJSON(out)
}

// printStatusDashboard renders `status` for a human.
//
// It reads from the same `out` map that the JSON path emits, so a field can never
// appear in one and not the other. Colour is used for the numbers a miner looks at.
//
// `pending` is the one input not in `out`: this epoch's UNSETTLED estimate, in points,
// keyed by agent. It is passed separately precisely because it must not enter the
// frozen JSON, and it is labelled "pending" to keep it distinct from settled points.
func printStatusDashboard(out map[string]any, agents []agentOut, advices []string, pending map[string]float64) {
	bold := func(s string) string { return term.Paint(s, term.Bold, true) }
	dim := func(s string) string { return term.Paint(s, term.Dim, true) }
	cyan := func(s string) string { return term.Paint(s, term.Cyan, true) }

	epoch, _ := out["epoch"].(uint64)
	receipts, _ := out["receipts"].(int)
	artifacts, _ := out["distinctArtifacts"].(int)
	total, _ := out["totalPoints"].(float64)

	fmt.Printf("\n  %s\n\n", bold("RelayFirst — status"))
	fmt.Printf("  %-16s %s   %s\n", bold("epoch"), cyan(fmt.Sprint(epoch)),
		dim("ends "+fmt.Sprint(out["epochEndsAt"])))
	fmt.Printf("  %-16s %d\n", bold("receipts"), receipts)
	fmt.Printf("  %-16s %d\n", bold("distinct artifacts"), artifacts)
	fmt.Printf("  %-16s %s\n\n", bold("total points"), cyan(fmt.Sprintf("%.4f", total)))

	if len(agents) == 0 {
		fmt.Printf("  %s\n\n", dim("no receipts yet — run `relayfirst mine --once --source https://example.com`"))
	} else {
		fmt.Printf("  %s\n", bold("by agent"))
		for _, a := range agents {
			fmt.Printf("    %s\n", shortID(a.AgentID))
			fmt.Printf("      settled %s lifetime · %s this epoch\n",
				cyan(fmt.Sprintf("%.4f", a.PointsLifetime)), fmt.Sprintf("%.4f", a.PointsEpoch))
			// The pending line is this epoch's unsettled estimate. It is shown as a
			// separate, labelled number because it is a projection over current work,
			// not a settled balance: it will change as more work arrives, and only a
			// settlement (and its Merkle root) makes it final.
			if est, ok := pending[a.AgentID]; ok {
				fmt.Printf("      pending %s this epoch %s\n",
					cyan(fmt.Sprintf("%.4f", est)), dim("(unsettled estimate)"))
			}
			fmt.Printf("      work    %d receipt(s), %d credited, %d anchored\n", a.Receipts, a.Credited, a.Anchors)
		}
		fmt.Println()
	}

	for _, adv := range advices {
		fmt.Printf("  %s %s\n", term.Paint("next:", term.Yellow, true), adv)
	}
	fmt.Printf("\n  %s\n\n", dim("points are non-transferable, unpriced, and carry no promised return (MVP.md §6.1)"))
}

// epochEndsAt reports when the current epoch ends, so a miner knows when their
// per-epoch budget resets.
func epochEndsAt(epoch uint64) string {
	_, end := scoring.EpochBounds(epoch)
	return end.UTC().Format(time.RFC3339)
}

// ledgerTotal sums every credited entry.
func ledgerTotal(entries []scoring.Entry) float64 {
	var total float64
	for _, e := range entries {
		total += e.Points
	}
	return total
}

// balanceRootFromCumulative builds an epoch's balance Merkle root from agents'
// CUMULATIVE points through that epoch (MVP.md §6.2b): the root a points claim is
// verified against.
//
// The totals are already integers in micro-points (the points ledger stores
// micro_points), so no float rounding happens here -- which is the point, because the
// leaf carries a uint256 total that must match on-chain exactly.
//
// An agent with no points through this epoch is absent from the input and has no leaf:
// it has no claim to make, and a zero leaf would be an unclaimable entry.
func balanceRootFromCumulative(epoch uint64, cumulativeMicro map[string]int64) (merkle.Hash, error) {
	totals := make(map[merkle.Hash]int64, len(cumulativeMicro))
	for agent, micro := range cumulativeMicro {
		if micro <= 0 {
			continue
		}
		id, err := merkle.AgentID(agent)
		if err != nil {
			return merkle.Hash{}, fmt.Errorf("settle: agent id %q: %w", agent, err)
		}
		totals[id] = micro
	}
	return merkle.BalanceRootFromTotals(epoch, totals)
}

// ---------------------------------------------------------------- receipts

// runReceipts lists or exports receipts.
//
// Export exists because "I can take my contribution with me" is the basis of trust
// in a system whose whole claim is that it does not need to be trusted (MVP.md
// §9.2). The exported files are the canonical receipt bytes, so `relayfirst verify`
// works on them with no RelayFirst software beyond this binary and no network.
func runReceipts(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}

	db, err := store.Open(f.get("db", "./relayfirst.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	receipts := store.NewReceiptStore(db)

	agentID := ""
	if key := keyFromFlags(f); key != "" {
		id, err := receipt.DeriveAgentID(key, 8453)
		if err != nil {
			return err
		}
		agentID = id
	}

	var all []*receipt.Receipt
	if agentID != "" {
		all, err = receipts.ByAgentAll(agentID)
	} else {
		all, err = receipts.All(0)
	}
	if err != nil {
		return err
	}

	if !f.has("export") {
		// List mode: a summary a user can read without leaving the terminal.
		type row struct {
			ReceiptID string `json:"receiptId"`
			Epoch     uint64 `json:"epoch"`
			Task      string `json:"task"`
			Anchors   int    `json:"anchors"`
		}
		rows := make([]row, 0, len(all))
		for _, r := range all {
			rows = append(rows, row{
				ReceiptID: r.ReceiptID,
				Epoch:     r.Epoch,
				Task:      string(r.Task.Type),
				Anchors:   len(r.Anchors),
			})
		}
		return printJSON(map[string]any{"count": len(rows), "receipts": rows})
	}

	dir := f.get("out", "./relayfirst-receipts")
	written, err := store.ExportReceipts(dir, all)
	if err != nil {
		return err
	}

	return printJSON(map[string]any{
		"exported":  written,
		"directory": dir,
		"verify":    fmt.Sprintf("relayfirst verify %s/<receiptId>.json", dir),
		"note":      "each file is the canonical signed bytes; verification needs no network and no RelayFirst server",
	})
}

// ---------------------------------------------------------------- anchor (S7)

// runAnchor computes an epoch's Merkle root, or a proof for one receipt.
//
// # Why this is read-only
//
// It prints a root and a proof. It never submits a transaction and never contacts
// a chain: there is no funded account in this environment, and deploying or
// anchoring is a deliberate human action (see contracts/ and the S7 report).
// Producing the values is the part that must be exactly right; submitting them is
// the part that costs money and belongs to the operator.
func runAnchor(args []string) error {
	if len(args) == 0 {
		return errors.New("anchor requires an action: root or proof")
	}

	f, err := parseFlags(args[1:])
	if err != nil {
		return err
	}

	db, err := store.Open(f.get("db", "./relayfirst.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	receipts := store.NewReceiptStore(db)
	all, err := receipts.All(0)
	if err != nil {
		return err
	}

	switch args[0] {
	case "root":
		return anchorRoot(f, receipts, all)
	case "proof":
		return anchorProof(f, receipts, all)
	case "verify":
		return anchorVerify(f, receipts, all)
	case "check":
		return anchorCheck(f)
	case "manifest":
		return anchorManifest(f, receipts)
	case "audit":
		return anchorAudit(f)
	default:
		return fmt.Errorf("unknown anchor action %q (want root, proof, verify, check, manifest or audit)", args[0])
	}
}

// anchorCheck verifies a proof against a root supplied directly, with no store access.
//
// # Why this is the point of anchoring
//
// The other verification path (`anchor verify`) recomputes the expected root from the local
// receipt store. That is useful for an operator who has the store, but it is not what anchoring
// is for: the whole claim is that someone holding only the published root and a proof can confirm
// their receipt was included — after every server is gone, with no RelayFirst software beyond
// this binary.
//
// Requiring the store made that impossible. This command takes the root and the proof as inputs
// and needs nothing else, which is exactly the offline property criterion ② describes.
//
// # Why the root is not taken from the proof
//
// The proof carries a root field, but using it would make verification circular: a forged proof
// could simply carry the root it happens to fold to, and the check would always pass. The root
// must come from the caller — from the chain, from a published document, from whatever source
// they trust — so the check is "does this proof belong to *that* root".
func anchorCheck(f *flags) error {
	receiptID := f.get("receipt", "")
	if receiptID == "" {
		return errors.New("anchor check requires --receipt <receiptId>")
	}

	rootHex := f.get("root", "")
	if rootHex == "" {
		return errors.New("anchor check requires --root <merkleRoot>, the root to check against " +
			"(it is deliberately not read from the proof, or the check would be circular)")
	}

	rawSiblings := f.repeated["sibling"]
	if len(rawSiblings) == 0 {
		// A single-leaf epoch legitimately has no siblings, so an empty list is allowed only when
		// the width says so. The width check below catches a genuinely missing one.
		if f.get("width", "") != "1" {
			return errors.New("anchor check requires --sibling <hash> for each proof step " +
				"(use the values `anchor proof` printed)")
		}
	}

	index, err := f.intArg("index")
	if err != nil {
		return fmt.Errorf("anchor check requires --index <n>: %w", err)
	}
	width, err := f.intArg("width")
	if err != nil {
		return fmt.Errorf("anchor check requires --width <power of two>: %w", err)
	}

	leaf, err := merkle.IDFromHex(receiptID)
	if err != nil {
		return err
	}
	root, err := merkle.IDFromHex(rootHex)
	if err != nil {
		return fmt.Errorf("invalid --root %q: %w", rootHex, err)
	}

	siblings := make([]merkle.Hash, 0, len(rawSiblings))
	for _, s := range rawSiblings {
		h, err := merkle.IDFromHex(s)
		if err != nil {
			return fmt.Errorf("invalid --sibling %q: %w", s, err)
		}
		siblings = append(siblings, h)
	}

	proof := merkle.Proof{
		Leaf:     leaf,
		Index:    index,
		Siblings: siblings,
		Root:     root,
		Width:    width,
	}

	ok := merkle.Verify(proof)
	out := map[string]any{
		"verified": ok,
		"receipt":  receiptID,
		"root":     root.Hex(),
		"mode":     "against a supplied root, no local store consulted",
	}
	if !ok {
		// An unwell-formed proof is a different problem from a wrong one, and an operator needs to
		// know which: a malformed proof means the arguments are wrong, not that the receipt was
		// excluded.
		if !proof.IsWellFormed() {
			out["reason"] = "the proof is not well formed (width must be a power of two, " +
				"index within width, and siblings exactly log2(width) long)"
		} else {
			out["reason"] = "the proof does not fold to the supplied root"
		}
		return printJSON(out)
	}
	out["note"] = "this check used no RelayFirst server and no local receipt store; " +
		"anyone with the same root and proof will reach the same answer"
	return printJSON(out)
}

// anchorManifest publishes an epoch's receipt set and its root.
//
// # This is the "discoverable" half of omission (incentive.md §10.10)
//
// A receipt root alone proves that a receipt IS in an epoch, not that one is missing.
// But if the operator publishes the SET the root was built from -- every receipt id, in
// order -- then anyone can (a) recompute the root from the set and check it equals the
// anchored one, and (b) see whether their own receipt is in the set at all. A receipt
// that is absent from a set whose root matches is that much harder to hide: it cannot be
// dropped without changing the root, and a changed root does not match the anchor.
//
// # What it does NOT do
//
// It does not FORCE an operator to include anyone. Enforcing inclusion is a separate,
// harder problem (a challenge or dispute path), and it is not solved here. This only
// makes an omission findable and attributable -- the honest direction, stated plainly
// rather than dressed up as enforcement.
//
// The ids are sorted, because that is the order the tree is built in; publishing them in
// another order would make the recomputation fail for an honest operator.
func anchorManifest(f *flags, rs *store.ReceiptStore) error {
	epoch, err := f.epochArg()
	if err != nil {
		return err
	}

	leaves, ids, err := epochReceipts(rs, epoch)
	if err != nil {
		return err
	}
	root := merkle.Root(leaves)

	return printJSON(map[string]any{
		"epoch":    epoch,
		"root":     root.Hex(),
		"width":    nextPow2For(len(ids)),
		"count":    len(ids),
		"receipts": ids,
		"note": "publish this alongside the root. Anyone can recompute the root from " +
			"`receipts` in this order and confirm the anchor matches; a missing receipt " +
			"changes the root. This makes an omission DISCOVERABLE, not impossible.",
	})
}

// anchorAudit checks a published manifest, and any receipts the caller names, against a
// root they supply.
//
// # Why the root comes from the caller
//
// It takes --root rather than reading it from the manifest, for the same reason `anchor
// check` does: otherwise a forged manifest could carry the root it happens to fold to.
// The root is what the caller trusts -- from the chain, from a published document.
//
// # What it reports
//
//   - whether the manifest's receipt set recomputes to the supplied root (the operator
//     is lying if it does not);
//   - for each --require <receiptId>, whether that receipt is in the published set. A
//     receipt the caller KNOWS they earned but which is absent, while the root still
//     matches, is the evidence of an omission. The command reports it as "missing" and
//     exits non-zero, and it does not pretend that is a proof of anything beyond the
//     set differing from expectation.
func anchorAudit(f *flags) error {
	rootHex := f.get("root", "")
	if rootHex == "" {
		return errors.New("anchor audit requires --root <merkleRoot>, the root to check the manifest against")
	}
	wantRoot, err := merkle.IDFromHex(rootHex)
	if err != nil {
		return fmt.Errorf("--root: %w", err)
	}

	manifestPath := f.get("manifest", "")
	if manifestPath == "" {
		return errors.New("anchor audit requires --manifest <path> (the JSON `anchor manifest` published)")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var man struct {
		Epoch    uint64   `json:"epoch"`
		Root     string   `json:"root"`
		Receipts []string `json:"receipts"`
	}
	if err := json.Unmarshal(raw, &man); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if len(man.Receipts) == 0 {
		return errors.New("the manifest lists no receipts")
	}

	// Recompute the root from the published set, in the order published.
	leaves := make([]merkle.Hash, 0, len(man.Receipts))
	for _, id := range man.Receipts {
		h, err := merkle.IDFromHex(id)
		if err != nil {
			return fmt.Errorf("manifest receipt id %q: %w", id, err)
		}
		leaves = append(leaves, h)
	}
	computed := merkle.Root(leaves)
	rootMatches := computed == wantRoot

	present := make(map[string]bool, len(man.Receipts))
	for _, id := range man.Receipts {
		present[strings.ToLower(strings.TrimPrefix(id, "0x"))] = true
	}

	var missing []string
	for _, req := range f.repeated["require"] {
		key := strings.ToLower(strings.TrimPrefix(req, "0x"))
		if !present[key] {
			missing = append(missing, req)
		}
	}
	sort.Strings(missing)

	out := map[string]any{
		"epoch":          man.Epoch,
		"rootSupplied":   wantRoot.Hex(),
		"rootRecomputed": computed.Hex(),
		"rootMatches":    rootMatches,
		"manifestCount":  len(man.Receipts),
		"note": "rootMatches=false means the published set does not produce the anchored root. " +
			"A receipt listed in --require but absent from an otherwise-matching manifest is " +
			"evidence of an omission, but not a proof of intent.",
	}
	if len(missing) > 0 {
		out["missing"] = missing
	}

	if err := printJSON(out); err != nil {
		return err
	}
	if !rootMatches || len(missing) > 0 {
		// A non-zero exit so a script can gate on it: the manifest is either inconsistent
		// with the root, or it leaves out a receipt the caller says they earned.
		return fmt.Errorf("audit failed: rootMatches=%v, missing=%d", rootMatches, len(missing))
	}
	return nil
}

// It exists so a user can check a proof without a chain and without trusting
// anyone: the root is recomputed locally from stored receipts, and then the proof
// is folded against it. That is the same check the Solidity contract performs, done
// off-chain and for free.
func anchorVerify(f *flags, rs *store.ReceiptStore, all []*receipt.Receipt) error {
	receiptID := f.get("receipt", "")
	if receiptID == "" {
		return errors.New("anchor verify requires --receipt <receiptId>")
	}

	rawSiblings := f.repeated["sibling"]
	if len(rawSiblings) == 0 {
		return errors.New("anchor verify requires at least one --sibling <hash> (use the values anchor proof printed)")
	}
	index, err := f.intArg("index")
	if err != nil {
		return fmt.Errorf("anchor verify requires --index <n>: %w", err)
	}
	width, err := f.intArg("width")
	if err != nil {
		return fmt.Errorf("anchor verify requires --width <power of two>: %w", err)
	}

	leaf, err := merkle.IDFromHex(receiptID)
	if err != nil {
		return err
	}
	siblings := make([]merkle.Hash, 0, len(rawSiblings))
	for _, s := range rawSiblings {
		h, err := merkle.IDFromHex(s)
		if err != nil {
			return fmt.Errorf("invalid --sibling %q: %w", s, err)
		}
		siblings = append(siblings, h)
	}

	// Recompute the expected root from the store, so this command does not simply
	// trust the root the proof carries.
	epoch := uint64(0)
	found := false
	for _, r := range all {
		if r.ReceiptID == receiptID {
			epoch = r.Epoch
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("receipt %s is not in this store, so its epoch root cannot be recomputed", receiptID)
	}

	leaves, _, err := epochReceipts(rs, epoch)
	if err != nil {
		return err
	}
	expected := merkle.Root(leaves)

	proof := merkle.Proof{
		Leaf:     leaf,
		Index:    index,
		Siblings: siblings,
		Root:     expected,
		Width:    width,
	}

	ok := merkle.Verify(proof)
	out := map[string]any{
		"verified":       ok,
		"epoch":          epoch,
		"receipt":        receiptID,
		"recomputedRoot": expected.Hex(),
	}
	if !ok {
		out["reason"] = "the proof does not fold to the root recomputed from this store's receipts"
		return printJSON(out)
	}
	out["note"] = "the proof is consistent with this store; anyone can repeat this with the same receipts"
	return printJSON(out)
}

// epochReceipts returns one epoch's receipt ids as Merkle leaves, in a deterministic order.
//
// Ordering is by receipt id so that any party recomputing the root from the same receipt set
// arrives at the same tree. Ordering by insertion time would work for the writer and fail for
// everyone else, which defeats the purpose of an anchor.
//
// The selection happens in SQL rather than by filtering every receipt in Go. That matters
// beyond efficiency: the filtering decides which receipts belong to a root, so a mistake there
// would silently change an anchored tree. One query is easier to reason about than a loop.
func epochReceipts(rs *store.ReceiptStore, epoch uint64) ([]merkle.Hash, []string, error) {
	rows, err := rs.ByEpoch(epoch)
	if err != nil {
		return nil, nil, err
	}

	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ReceiptID)
	}
	// ByEpoch already sorts in SQL; sorting again keeps this function correct on its own
	// terms rather than depending on a caller's ordering guarantee.
	sort.Strings(ids)

	leaves := make([]merkle.Hash, 0, len(ids))
	for _, id := range ids {
		h, err := merkle.IDFromHex(id)
		if err != nil {
			return nil, nil, fmt.Errorf("receipt id %q is not usable as a leaf: %w", id, err)
		}
		leaves = append(leaves, h)
	}
	return leaves, ids, nil
}

func anchorRoot(f *flags, rs *store.ReceiptStore, all []*receipt.Receipt) error {
	epoch, err := f.epochArg()
	if err != nil {
		return err
	}

	leaves, ids, err := epochReceipts(rs, epoch)
	if err != nil {
		return err
	}

	root := merkle.Root(leaves)

	out := map[string]any{
		"epoch":    epoch,
		"root":     root.Hex(),
		"receipts": len(leaves),
		"width":    nextPow2For(len(leaves)),
		"note": "recompute this yourself from the same receipt ids, sorted; a root you " +
			"cannot reproduce is a root you should not trust",
	}
	if len(leaves) == 0 {
		// An empty epoch has a well-defined root, but anchoring it would be a
		// meaningless on-chain write, so say so rather than implying it is normal.
		out["warning"] = "this epoch has no receipts; its root is the empty-tree hash and should not be anchored"
	}
	// The ids are included so a verifier has exactly the input the root was built
	// from, in the order used.
	if f.has("show-ids") {
		out["receiptIds"] = ids
	}
	return printJSON(out)
}

func anchorProof(f *flags, rs *store.ReceiptStore, all []*receipt.Receipt) error {
	receiptID := f.get("receipt", "")
	if receiptID == "" {
		return errors.New("anchor proof requires --receipt <receiptId>")
	}

	// Find the receipt to learn its epoch, so the caller does not have to state it
	// and cannot state it wrongly.
	var epoch uint64
	found := false
	for _, r := range all {
		if r.ReceiptID == receiptID {
			epoch = r.Epoch
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("receipt %s is not in this store", receiptID)
	}

	leaves, _, err := epochReceipts(rs, epoch)
	if err != nil {
		return err
	}

	index := -1
	for i, l := range leaves {
		if strings.EqualFold(l.Hex(), receiptID) {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("receipt %s is not among the sorted leaves of epoch %d", receiptID, epoch)
	}

	proof, err := merkle.Prove(leaves, index)
	if err != nil {
		return err
	}

	// A proof is only meaningful if it verifies, so the command refuses to emit one
	// that does not. A self-check here is cheap and catches an ordering or shape bug
	// before a user pastes the proof somewhere it matters.
	if !merkle.Verify(proof) {
		return errors.New("internal error: generated proof does not verify; do not use it")
	}

	siblings := make([]string, 0, len(proof.Siblings))
	for _, s := range proof.Siblings {
		siblings = append(siblings, s.Hex())
	}

	return printJSON(map[string]any{
		"epoch":    epoch,
		"receipt":  receiptID,
		"root":     proof.Root.Hex(),
		"index":    proof.Index,
		"width":    proof.Width,
		"siblings": siblings,
		"note": "verify this offline with `relayfirst anchor verify`, or on-chain with " +
			"RelayAnchor.verifyProof(operator, epoch, leaf, index, siblings)",
	})
}

// epochArg reads --epoch, defaulting to the current epoch.
func (f *flags) epochArg() (uint64, error) {
	raw := f.get("epoch", "")
	if raw == "" {
		return scoring.EpochOf(time.Now()), nil
	}
	var n uint64
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil {
		return 0, fmt.Errorf("invalid --epoch %q: %w", raw, err)
	}
	return n, nil
}

// nextPow2For mirrors the Merkle tree's padding width, for reporting.
func nextPow2For(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// ---------------------------------------------------------------- verify-receipt (S4)

// runVerifyReceipt re-runs a stored receipt's task and reports the verdict.
//
// # Why this is separate from `verify`
//
// `verify` answers "is this receipt authentic?" — structure and signature, offline, and
// it needs no store. This command answers a different and stronger question: "does the
// claimed result actually reproduce?" That requires re-executing the task, so it needs
// the executors, a fetcher, and the stored receipt.
//
// Keeping them apart matters because they fail for different reasons and a caller needs
// to know which one happened. A receipt can be perfectly authentic and still be
// fabricated; that is the entire premise of adversarial verification.
//
// # Why the anti-collusion ratio cap is relaxed here, explicitly
//
// The cap exists so an agent cannot manufacture agreement at scale without producing
// anything itself — a sybil-defence for the protocol. A human running one deliberate
// check on their own receipt is not that threat, and enforcing the cap would make this
// command unusable for a verifier key that has not also mined.
//
// So the relaxation is deliberate and reported in the output, rather than achieved by
// quietly passing a permissive policy. A deployment performing verification at scale
// should use the ratio policy instead; see docs/stages/S4-report.md.
func runVerifyReceipt(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}

	receiptID := f.get("receipt", "")
	verifyAll := f.has("all")

	if receiptID == "" && !verifyAll {
		return errors.New("verify-receipt requires --receipt <receiptId>, or --all to verify a whole epoch")
	}
	if receiptID != "" && verifyAll {
		return errors.New("verify-receipt takes either --receipt or --all, not both")
	}

	verifierKey := keyFromFlags(f)
	if verifierKey == "" {
		return errors.New("verify-receipt needs a verifier identity: pass --key or set RELAYFIRST_PRIVATE_KEY")
	}

	db, err := store.Open(f.get("db", "./relayfirst.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	receipts := store.NewReceiptStore(db)

	verifierID, err := receipt.DeriveAgentID(verifierKey, 8453)
	if err != nil {
		return err
	}

	// The window protects honest work: content changes, so re-checking an old receipt
	// could reject a producer who was right at the time.
	window := verificationWindow(f, receipts)

	recomputer := &executorRecomputer{
		deps: mining.Deps{Fetcher: anchorFetcher(), Resolver: resolverForTask(f)},
	}

	stakes := verification.NewDurableStakes(sqlite.NewCommitmentLedger(db))
	updater := &receiptVerifierUpdater{store: receipts, verdicts: verification.NewRecordingVerdicts(sqlite.NewVerdictStore(db))}

	activity := store.NewActivity(receipts)

	// Anchor consistency is configured by default: MVP.md §5.5 requires re-fetching the
	// anchor, and skipping it would accept signed evidence nobody checked. `--no-anchors`
	// exists for a caller with no network, and the output says so.
	var anchors verification.AnchorChecker
	anchorsLabel := "checked"
	if f.has("no-anchors") {
		anchorsLabel = "NOT CHECKED (--no-anchors): the receipt's anchor evidence was not re-fetched"
	} else {
		anchors = mining.NewAnchorConsistency(anchorFetcher())
	}

	// The verifier is built once and used for every receipt in the run, so an epoch sweep
	// shares the same commitments, verdict store and window as a single check.
	v, err := verification.New(verification.Config{
		// AllowAll, deliberately: see the note on this command. The ratio cap is a protocol
		// sybil-defence, and a deliberate manual check is not the threat it guards against.
		// The relaxation is reported in the output rather than left implicit.
		Policy:     verification.AllowAll{Verifier: verifierID},
		Stakes:     stakes,
		Receipts:   updater,
		Recomputer: recomputer,
		Anchors:    anchors,
		Activity:   activity,
		Window:     window,
	})
	if err != nil {
		return err
	}

	if verifyAll {
		return verifyReceiptEpoch(f, receipts, v, verifierID, window, anchorsLabel)
	}

	r, ok, err := receipts.Load(receiptID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("receipt %s is not in this store", receiptID)
	}

	// The producer may not be the verifier. Refusing here means an operator gets a clear
	// message instead of a verification that would have been meaningless.
	if verifierID == r.AgentID {
		return fmt.Errorf("the receipt's producer cannot verify it; use a different key")
	}

	out, err := v.Verify(context.Background(), r)
	if err != nil {
		return err
	}

	return printJSON(map[string]any{
		"receipt":        receiptID,
		"verified":       out.Verified,
		"verifier":       out.VerifierID,
		"recomputedHash": out.RecomputedHash,
		"claimedHash":    r.Result.Hash,
		"status":         string(r.Verification.Status),
		"window":         describeWindow(window),
		"anchors":        anchorsLabel,
		"reason":         out.Reason,
		"note":           "a rejection means the claimed result did not reproduce; it does not by itself identify who is at fault",
	})
}

// verifyReceiptEpoch verifies every receipt in one epoch, reporting per receipt and in summary.
//
// # Why this exists
//
// A single check is fine for a spot inspection, but an operator auditing an epoch wants the
// whole set. Doing that by hand means one command per receipt, and the failure mode of that
// is predictable: someone checks a handful, finds them fine, and assumes the rest are too.
//
// # Why the summary distinguishes "checked and rejected" from "not checked"
//
// A receipt that cannot be checked — outside its window, or its producer is the verifier — is
// not the same as one that failed. Collapsing them into one number would let an epoch where
// nothing was verifiable look like an epoch where everything passed.
func verifyReceiptEpoch(
	f *flags,
	receipts *store.ReceiptStore,
	v *verification.Verifier,
	verifierID string,
	window verification.Window,
	anchorsLabel string,
) error {
	epoch, err := f.epochArg()
	if err != nil {
		return err
	}

	rows, err := receipts.ByEpoch(epoch)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("epoch %d holds no receipts in this store", epoch)
	}

	type row struct {
		ReceiptID string `json:"receiptId"`
		Producer  string `json:"producer"`
		Verified  bool   `json:"verified"`
		Status    string `json:"status"`
		Reason    string `json:"reason,omitempty"`
		Skipped   string `json:"skipped,omitempty"`
	}

	results := make([]row, 0, len(rows))
	verified, rejected, skipped := 0, 0, 0

	for _, r := range rows {
		// A producer cannot verify its own receipt. In an epoch sweep this is expected to
		// happen whenever the operator also mined that epoch, so it is reported as skipped
		// rather than aborting the sweep.
		if verifierID == r.AgentID {
			skipped++
			results = append(results, row{
				ReceiptID: r.ReceiptID,
				Producer:  r.AgentID,
				Status:    string(r.Verification.Status),
				Skipped:   "the verifier is this receipt's producer",
			})
			continue
		}

		out, err := v.Verify(context.Background(), r)
		if err != nil {
			// A window or activity refusal is a skip, not a rejection: the receipt was not
			// judged, so counting it as failed would misreport the epoch.
			skipped++
			results = append(results, row{
				ReceiptID: r.ReceiptID,
				Producer:  r.AgentID,
				Status:    string(r.Verification.Status),
				Skipped:   err.Error(),
			})
			continue
		}

		if out.Verified {
			verified++
		} else {
			rejected++
		}
		results = append(results, row{
			ReceiptID: r.ReceiptID,
			Producer:  r.AgentID,
			Verified:  out.Verified,
			Status:    string(r.Verification.Status),
			Reason:    out.Reason,
		})
	}

	return printJSON(map[string]any{
		"epoch":    epoch,
		"verifier": verifierID,
		"window":   describeWindow(window),
		"anchors":  anchorsLabel,
		"summary": map[string]any{
			"total":    len(rows),
			"verified": verified,
			"rejected": rejected,
			"skipped":  skipped,
			"note": "skipped means the receipt was not judged (outside its window, or the " +
				"verifier is its producer); it is not the same as rejected",
		},
		"results": results,
	})
}

// verificationWindow returns the window the CLI uses.
//
// The epoch window is the accurate choice and the default here, because a CLI user can
// see which epoch a receipt belongs to and act within it. `--no-window` exists for
// inspecting an old receipt deliberately, and reports that it did so.
func verificationWindow(f *flags, _ *store.ReceiptStore) verification.Window {
	if f.has("no-window") {
		return verification.AlwaysOpen{}
	}
	return verification.NewEpochWindow(scoring.EpochLength)
}

// describeWindow reports which window is active.
//
// The concrete types carry Describe, but the interface does not, because a caller-provided
// window need not implement it. A type assertion keeps a custom window usable while still
// naming the built-in ones, which matters because "which window is active" changes whether
// a rejection means anything.
func describeWindow(w verification.Window) string {
	if d, ok := w.(interface{ Describe() string }); ok {
		return d.Describe()
	}
	return "custom window"
}

// resolverForTask builds a resolver from flags alone, for re-execution.
//
// A verifier has no config to fall back on: it is re-running someone else's task, so the
// only provider it may use is the one named on the command line. Defaulting to "local"
// keeps an offline check possible without a key.
func resolverForTask(f *flags) mining.Resolver {
	r, err := resolverFor(f, f.get("provider", "local"), f.get("model", ""))
	if err != nil {
		// A resolver that cannot be built means a semantic task cannot be re-executed.
		// Returning nil makes the executor fail that one task with a clear message rather
		// than aborting the whole command; positional tasks are unaffected.
		return nil
	}
	return r
}

// executorRecomputer re-runs a receipt's task through the real executors.
//
// This is what verification IS in this system: the same deterministic re-execution a
// client already performs, aimed at a stored receipt.
type executorRecomputer struct {
	deps mining.Deps
}

// Recompute implements verification.Recomputer.
func (e *executorRecomputer) Recompute(ctx context.Context, r *receipt.Receipt) (receipt.Result, error) {
	res, _, err := mining.Run(ctx, e.deps, r.Task.Type, r.Task.Spec)
	return res, err
}

// receiptVerifierUpdater writes a verdict onto the receipt and into the verdict store.
//
// Both writes matter: the receipt needs the status so a later reader can see it, and the
// verdict store is what scoring reads. Writing only one would leave either the receipt or
// the score out of date.
type receiptVerifierUpdater struct {
	store    *store.ReceiptStore
	verdicts *verification.RecordingVerdicts
}

// UpdateVerification persists r's verification block.
func (u *receiptVerifierUpdater) UpdateVerification(r *receipt.Receipt) error {
	if u.verdicts != nil && r.Verification.VerifierID != nil {
		var verifiedAt time.Time
		if r.Verification.VerifiedAt != nil {
			verifiedAt = time.Unix(*r.Verification.VerifiedAt, 0).UTC()
		}
		var hash string
		if r.Verification.RecomputedHash != nil {
			hash = *r.Verification.RecomputedHash
		}

		err := u.verdicts.Record(sqlite.Verdict{
			ReceiptID:      r.ReceiptID,
			AgentID:        r.AgentID,
			VerifierID:     *r.Verification.VerifierID,
			Status:         string(r.Verification.Status),
			RecomputedHash: hash,
			VerifiedAt:     verifiedAt,
		})
		if err != nil {
			return err
		}
	}
	return u.store.SaveVerification(r)
}

// verdictSource picks how scoring decides whether a receipt is verified (S4-0).
//
// # Why the choice is explicit rather than defaulted silently
//
// The two options produce the same number of points from very different evidence. Under
// `self-check` the score trusts the submitter's own signature validation — which cannot
// tell honest work from a well-formed fabrication. Under `verifier` the score reads a
// second party's conclusion, and a freshly mined receipt earns nothing until that
// conclusion exists.
//
// A miner whose output silently changed meaning would be a confusing thing to debug, so
// the label is returned and printed at startup.
func verdictSource(f *flags, db *sqlite.DB) (mining.VerdictSource, string) {
	switch strings.ToLower(strings.TrimSpace(f.get("verdicts", "self-check"))) {
	case "", "self-check", "selfcheck":
		src := mining.SelfCheckVerdicts{}
		return src, src.Describe()

	case "verifier", "verdicts":
		src := verification.NewDurableVerdicts(sqlite.NewVerdictStore(db))
		return src, src.Describe()

	default:
		// An unrecognised value is reported by the caller as a configuration error; here
		// the safe fallback is the weaker source, and the label says so.
		src := mining.SelfCheckVerdicts{}
		return src, "unrecognised --verdicts value; falling back to " + src.Describe()
	}
}

// keyFromFlags reads the credential from the flag or the environment.
//
// It is factored out so every subcommand resolves it the same way, and so there is
// exactly one place to audit for "is this ever printed?". It is not.
func keyFromFlags(f *flags) string {
	key := f.get("key", "")
	if key == "" {
		key = os.Getenv("RELAYFIRST_PRIVATE_KEY")
	}
	return strings.TrimSpace(key)
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "…"
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
