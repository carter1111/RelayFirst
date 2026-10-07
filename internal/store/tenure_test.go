package store_test

import (
	"testing"

	"github.com/relayfirst/relayfirst/internal/store"
	"github.com/relayfirst/relayfirst/internal/tenure"
)

func TestTenureLedger_RecordsIdempotentlyAndFolds(t *testing.T) {
	db := openStore(t)
	ledgers := store.NewTenureLedger(db)

	// Three consecutive qualifying epochs.
	for e := uint64(1); e <= 3; e++ {
		if _, err := ledgers.Record("node-a", e, true, at()); err != nil {
			t.Fatalf("Record epoch %d: %v", e, err)
		}
	}
	// Re-recording the same epoch must not change anything.
	changed, err := ledgers.Record("node-a", 3, true, at())
	if err != nil {
		t.Fatalf("re-Record: %v", err)
	}
	if changed {
		t.Error("re-recording the same (node, epoch) must be a no-op")
	}

	state, err := ledgers.Tenure("node-a", 3)
	if err != nil {
		t.Fatalf("Tenure: %v", err)
	}
	if state.Tenure != 3 {
		t.Errorf("tenure = %d, want 3", state.Tenure)
	}

	tier, err := ledgers.Tier("node-a", 3)
	if err != nil {
		t.Fatalf("Tier: %v", err)
	}
	if tier != 1.0 {
		t.Errorf("tier = %v, want 1.0 at tenure 3", tier)
	}
}

func TestTenureLedger_TwoMissesResetAcrossEpochs(t *testing.T) {
	db := openStore(t)
	ledgers := store.NewTenureLedger(db)

	// Five qualifying, then two misses in a row.
	flags := []bool{true, true, true, true, true, false, false}
	for i, q := range flags {
		if _, err := ledgers.Record("node-b", uint64(i+1), q, at()); err != nil {
			t.Fatalf("Record epoch %d: %v", i+1, err)
		}
	}

	state, err := ledgers.Tenure("node-b", uint64(len(flags)))
	if err != nil {
		t.Fatalf("Tenure: %v", err)
	}
	if state.Tenure != 0 {
		t.Errorf("tenure = %d, want 0 after two consecutive misses", state.Tenure)
	}

	// As of the epoch after the FIRST miss, tenure had only stepped down to 4: the
	// reset is a function of what comes after, so a maxEpoch in the middle of the
	// history must not see the future.
	mid, err := ledgers.Tenure("node-b", 6)
	if err != nil {
		t.Fatalf("Tenure(mid): %v", err)
	}
	if mid.Tenure != 4 {
		t.Errorf("tenure as of the first miss = %d, want 4", mid.Tenure)
	}
}

func TestTenureLedger_AllTenuresAndPool(t *testing.T) {
	db := openStore(t)
	ledgers := store.NewTenureLedger(db)

	// gold: 12 in a row. bronze: 3. short: 1 (ineligible).
	for e := uint64(1); e <= 12; e++ {
		if _, err := ledgers.Record("gold", e, true, at()); err != nil {
			t.Fatalf("Record gold: %v", err)
		}
	}
	for e := uint64(1); e <= 3; e++ {
		if _, err := ledgers.Record("bronze", e, true, at()); err != nil {
			t.Fatalf("Record bronze: %v", err)
		}
	}
	if _, err := ledgers.Record("short", 1, true, at()); err != nil {
		t.Fatalf("Record short: %v", err)
	}

	all, err := ledgers.AllTenures(12)
	if err != nil {
		t.Fatalf("AllTenures: %v", err)
	}
	if all["gold"] != 12 || all["bronze"] != 3 || all["short"] != 1 {
		t.Errorf("AllTenures = %v, want gold 12, bronze 3, short 1", all)
	}

	shares := tenure.PoolShare(all)
	if _, ok := shares["short"]; ok {
		t.Error("an ineligible node must not receive a pool share")
	}
	if shares["gold"] <= shares["bronze"] {
		t.Errorf("gold %v must outweigh bronze %v", shares["gold"], shares["bronze"])
	}
}
