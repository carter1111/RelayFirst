//go:build ignore

// ingest_bench measures the write path of the node's message store.
//
//	go run internal/devtools/ingest_bench.go                 # measure, print, compare to baseline
//	go run internal/devtools/ingest_bench.go -update         # rewrite the baseline
//
// # Why this exists, and what it is for
//
// RelayFirst has never measured its own ingest throughput. The WuKongIM note was corrected
// (b6ee030) precisely because it had borrowed a number from another project's benchmark, and the
// correction left a gap: the cost of single-connection, row-at-a-time SQLite was KNOWN to be
// unquantified. This closes it with a measurement of our own.
//
// # What it measures, and why the mix matters
//
// The realistic ingest load is not uniform: most messages are fresh and a minority are duplicates,
// because a sender whose acknowledgement was lost retries (S5-3). The duplicate path is the
// dedup check — `ON CONFLICT DO NOTHING` — so a benchmark of only unique inserts would measure a
// path nobody hits in production and would miss the contention that makes dedup interesting.
//
// So the workload is a configured share of duplicate deliveries, defaulting to 10%.
//
// # Why it writes to a FILE and not :memory:
//
// The in-memory database skips the durability work that is the entire question. WAL fsync behaviour
// is what single-connection serialization actually costs, so a memory-backed benchmark would produce
// a number that cannot be compared to production at all. This uses a temp file on the same
// filesystem, and reports the journal mode it observed so a run that silently fell back to the
// rollback journal is visible rather than merely faster.
//
// # Why the output is a committed baseline rather than a threshold
//
// A gate needs a number to compare against, and the honest source of that number is a measurement
// on a known machine. So the baseline records the environment alongside the figures: a throughput
// delta that came from a faster laptop is not a regression, and a gate that could not tell the
// difference would fail for the wrong reason and be turned off.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/relayfirst/relayfirst/internal/sqlite"
)

// baselinePath is where the committed figures live.
const baselinePath = "testdata/ingest-baseline.json"

// baseline is the committed measurement plus the environment it was taken in.
//
// # Why the environment is stored rather than only the number
//
// A throughput figure without its machine is not a measurement, it is a rumour — the same defect
// this whole exercise exists to fix. Recording the machine makes a comparison interpretable: a
// large delta on the same machine is a regression, and the same delta on a different one is
// probably the machine.
type baseline struct {
	// MeasuredAt is when the baseline was taken.
	MeasuredAt string `json:"measuredAt"`

	// Environment is what the number is conditional on.
	Environment struct {
		CPU         string `json:"cpu"`
		NumCPU      int    `json:"numCPU"`
		GoVersion   string `json:"goVersion"`
		SQLite      string `json:"sqliteDriver"`
		JournalMode string `json:"journalMode"`
		OS          string `json:"os"`
		Arch        string `json:"arch"`
	} `json:"environment"`

	// Config is the workload that produced the figures, so a comparison cannot be made against a
	// different workload by accident.
	Config struct {
		Operations   int `json:"operations"`
		DuplicatePct int `json:"duplicatePercent"`
		PayloadBytes int `json:"payloadBytes"`
		MaxOpenConns int `json:"maxOpenConns"`

		// BatchSize is how many inserts share one transaction. It is part of the recorded config
		// because a comparison across different batch sizes is not a regression test, and the
		// before/after figures this file exists to produce differ exactly here.
		BatchSize int `json:"batchSize"`
	} `json:"config"`

	// Results are the measured figures.
	Results struct {
		WritesPerSecond float64 `json:"writesPerSecond"`
		P50Millis       float64 `json:"p50Millis"`
		P99Millis       float64 `json:"p99Millis"`
		MaxMillis       float64 `json:"maxMillis"`
		Stored          int     `json:"stored"`
		Duplicates      int     `json:"duplicates"`
	} `json:"results"`
}

func main() {
	update := flag.Bool("update", false, "rewrite the committed baseline with this run's figures")
	ops := flag.Int("ops", 20000, "number of ingest operations")
	dupPct := flag.Int("dup-pct", 10, "percentage of operations that repeat an earlier id")
	payload := flag.Int("payload", 2048, "payload size in bytes")
	batchSize := flag.Int("batch", 1, "inserts per transaction; 1 measures the row-at-a-time path")
	flag.Parse()

	result, err := run(*ops, *dupPct, *payload, *batchSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ingest_bench: %v\n", err)
		os.Exit(2)
	}

	if *update {
		if err := writeBaseline(result); err != nil {
			fmt.Fprintf(os.Stderr, "ingest_bench: %v\n", err)
			os.Exit(2)
		}
		fmt.Printf("wrote %s\n", baselinePath)
		return
	}

	compare(result)
}

// run performs the measurement.
func run(ops, dupPct, payloadBytes, batchSize int) (baseline, error) {
	if ops <= 0 {
		return baseline{}, fmt.Errorf("-ops must be positive")
	}
	if dupPct < 0 || dupPct > 90 {
		return baseline{}, fmt.Errorf("-dup-pct must be between 0 and 90")
	}
	if batchSize < 1 {
		return baseline{}, fmt.Errorf("-batch must be at least 1")
	}

	dir, err := os.MkdirTemp("", "relayfirst-ingest-")
	if err != nil {
		return baseline{}, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	dbPath := filepath.Join(dir, "ingest.db")
	db, err := sqlite.Open(dbPath)
	if err != nil {
		return baseline{}, fmt.Errorf("open store: %w", err)
	}
	defer db.Close()

	store := sqlite.NewMessageStore(db)

	// Observe the journal mode rather than assuming it. A run that quietly fell back to the
	// rollback journal would be slower for a reason unrelated to our code, and reporting it makes
	// that visible instead of mysterious.
	journal, err := journalMode(db)
	if err != nil {
		return baseline{}, err
	}

	body := make([]byte, payloadBytes)
	for i := range body {
		body[i] = byte('a' + i%26)
	}

	// Pre-generate the ids so the timing loop measures storage rather than id construction. The
	// duplicate share reuses an id already issued, which is what a retry looks like.
	ids := make([]string, ops)
	const uniqueCount = 1 << 20
	for i := range ids {
		if i > 0 && (i*100/ops) < dupPct {
			// Reuse an earlier id: a retry of something already sent.
			ids[i] = ids[i/2]
		} else {
			ids[i] = fmt.Sprintf("0x%064x", (i*2654435761)%uniqueCount)
		}
	}

	latencies := make([]time.Duration, 0, ops)
	stored, duplicates := 0, 0

	start := time.Now()
	if batchSize <= 1 {
		for i := 0; i < ops; i++ {
			agentID := fmt.Sprintf("agent:eip155:8453:0x%040x", i%64)

			t0 := time.Now()
			wasStored, err := store.Put(sqlite.Message{
				ID:         ids[i],
				AgentID:    agentID,
				Kind:       "receipt",
				Payload:    body,
				ReceivedAt: time.Now(),
			})
			latencies = append(latencies, time.Since(t0))
			if err != nil {
				return baseline{}, fmt.Errorf("put %d: %w", i, err)
			}
			if wasStored {
				stored++
			} else {
				duplicates++
			}
		}
	} else {
		// Latency here is PER MESSAGE as observed by the caller, which includes its share of the
		// commit rather than only its own insert. Reporting the per-insert time instead would show a
		// number that no caller experiences and would flatter the batched path.
		batcher := store.NewBatcher(batchSize)
		for i := 0; i < ops; i++ {
			agentID := fmt.Sprintf("agent:eip155:8453:0x%040x", i%64)

			t0 := time.Now()
			res, err := batcher.Add(sqlite.Message{
				ID:         ids[i],
				AgentID:    agentID,
				Kind:       "receipt",
				Payload:    body,
				ReceivedAt: time.Now(),
			})
			latencies = append(latencies, time.Since(t0))
			if err != nil {
				return baseline{}, fmt.Errorf("add %d: %w", i, err)
			}
			if res.Stored {
				stored++
			} else {
				duplicates++
			}
		}
		if err := batcher.Flush(); err != nil {
			return baseline{}, fmt.Errorf("final flush: %w", err)
		}
	}
	elapsed := time.Since(start)

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	var b baseline
	b.MeasuredAt = time.Now().UTC().Format(time.RFC3339)
	b.Environment.CPU = cpuModel()
	b.Environment.NumCPU = numCPU()
	b.Environment.GoVersion = goVersion()
	b.Environment.SQLite = sqliteDriverVersion()
	b.Environment.JournalMode = journal
	b.Environment.OS = osName()
	b.Environment.Arch = arch()
	b.Config.Operations = ops
	b.Config.DuplicatePct = dupPct
	b.Config.PayloadBytes = payloadBytes
	b.Config.MaxOpenConns = 1
	b.Config.BatchSize = batchSize
	b.Results.WritesPerSecond = float64(ops) / elapsed.Seconds()
	b.Results.P50Millis = millis(percentile(latencies, 0.50))
	b.Results.P99Millis = millis(percentile(latencies, 0.99))
	b.Results.MaxMillis = millis(latencies[len(latencies)-1])
	b.Results.Stored = stored
	b.Results.Duplicates = duplicates

	return b, nil
}

// percentile returns the value at p within a sorted slice, using nearest-rank.
//
// # Why nearest-rank rather than interpolation
//
// The point of p99 here is to catch a bad tail, and interpolation would smooth the tail toward a
// number that no request actually observed. Nearest-rank reports a latency that really happened.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func millis(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }

// compare prints the run and, when a baseline exists, the delta.
//
// # Why a missing baseline is not an error
//
// The first run on a new machine has nothing to compare against, and failing there would make the
// tool unusable exactly when a contributor needs it. So it reports that there is no baseline and
// prints the command to create one.
func compare(result baseline) {
	fmt.Printf("ingest benchmark\n")
	fmt.Printf("  machine      %s (%d cpus)\n", result.Environment.CPU, result.Environment.NumCPU)
	fmt.Printf("  go           %s\n", result.Environment.GoVersion)
	fmt.Printf("  sqlite       %s, journal=%s\n", result.Environment.SQLite, result.Environment.JournalMode)
	fmt.Printf("  workload     %d ops, %d%% duplicates, %d-byte payloads, batch=%d\n",
		result.Config.Operations, result.Config.DuplicatePct, result.Config.PayloadBytes,
		result.Config.BatchSize)
	fmt.Printf("  stored %d, duplicates %d\n", result.Results.Stored, result.Results.Duplicates)
	fmt.Printf("  throughput   %.0f writes/s\n", result.Results.WritesPerSecond)
	fmt.Printf("  latency      p50=%.3fms p99=%.3fms max=%.3fms\n",
		result.Results.P50Millis, result.Results.P99Millis, result.Results.MaxMillis)

	base, err := readBaseline()
	if err != nil {
		fmt.Printf("\nno baseline at %s; create one with -update\n", baselinePath)
		return
	}

	// A comparison across different workloads or machines is not a regression test, and saying so
	// is better than printing a misleading delta.
	if base.Config != result.Config {
		fmt.Printf("\nbaseline workload differs; not comparing (baseline %d ops / %d%% dups / batch=%d, "+
			"this run %d / %d%% / batch=%d)\n",
			base.Config.Operations, base.Config.DuplicatePct, base.Config.BatchSize,
			result.Config.Operations, result.Config.DuplicatePct, result.Config.BatchSize)
		return
	}
	if base.Environment.CPU != result.Environment.CPU || base.Environment.NumCPU != result.Environment.NumCPU {
		fmt.Printf("\nbaseline machine differs (%s/%d vs %s/%d); comparing but the delta may be the machine\n",
			base.Environment.CPU, base.Environment.NumCPU, result.Environment.CPU, result.Environment.NumCPU)
	}

	delta := (result.Results.WritesPerSecond - base.Results.WritesPerSecond) / base.Results.WritesPerSecond * 100
	fmt.Printf("\nbaseline     %.0f writes/s (measured %s)\n", base.Results.WritesPerSecond, base.MeasuredAt)
	fmt.Printf("change       %+.1f%%\n", delta)
}

// writeBaseline records this run as the baseline.
func writeBaseline(result baseline) error {
	if err := os.MkdirAll(filepath.Dir(baselinePath), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(baselinePath, append(raw, '\n'), 0o644)
}

// readBaseline loads the committed figures.
func readBaseline() (baseline, error) {
	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		return baseline{}, err
	}
	var b baseline
	if err := json.Unmarshal(raw, &b); err != nil {
		return baseline{}, fmt.Errorf("parse %s: %w", baselinePath, err)
	}
	return b, nil
}

// journalMode reports the journal mode SQLite is actually using.
func journalMode(db *sqlite.DB) (string, error) {
	var mode string
	if err := db.Handle().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return "", fmt.Errorf("read journal_mode: %w", err)
	}
	return strings.ToLower(mode), nil
}

// Environment probes for the ingest benchmark.
//
// # Why these exist rather than being inlined
//
// A throughput number is only interpretable with the machine it came from, so the probes are part
// of the measurement rather than decoration. Each returns a value that is honest about failing:
// where a probe cannot answer, it says so instead of returning an empty string that would look like
// a machine with no name.

// cpuModel returns the CPU model, or a description of why it is unknown.
//
// These run `sysctl` and `lscpu` rather than reading /proc or a Go API because Go has no portable
// CPU-model accessor. A missing utility is a normal condition, not an error.
func cpuModel() string {
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				return s
			}
		}
	case "linux":
		if out, err := exec.Command("lscpu").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if name, ok := strings.CutPrefix(line, "Model name:"); ok {
					if s := strings.TrimSpace(name); s != "" {
						return s
					}
				}
			}
		}
	}
	return "unknown (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
}

func numCPU() int { return runtime.NumCPU() }

func goVersion() string { return runtime.Version() }

func osName() string { return runtime.GOOS }

func arch() string { return runtime.GOARCH }

// buildInfo returns the module build information, reporting whether it was available.
//
// A binary built without module info (GOFLAGS=-buildvcs=false in some setups, or a stripped build)
// cannot answer dependency questions, and that is reported rather than guessed.
func buildInfo() (*debug.BuildInfo, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil, false
	}
	return info, true
}

// sqliteDriverVersion returns the modernc.org/sqlite version.
//
// It reads the module version from the binary's build info rather than hardcoding a string, so a
// dependency bump changes the recorded environment instead of leaving a stale claim next to new
// numbers.
func sqliteDriverVersion() string {
	info, ok := buildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if dep.Path == "modernc.org/sqlite" {
			return dep.Version
		}
	}
	return "unknown"
}
