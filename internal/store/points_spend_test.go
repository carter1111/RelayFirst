package store_test

import (
	"testing"
	"time"

	"github.com/relayfirst/relayfirst/internal/scoring"
	"github.com/relayfirst/relayfirst/internal/store"
)

func seedCredit(t *testing.T, db *store.DB, agent string, micro int64) {
	t.Helper()
	points := float64(micro) / scoring.MicroPerPoint
	// One credit entry with a per-agent unique receipt id, so seeding is idempotent-ish
	// and the amount is exactly the micro value.
	id := "seed:" + agent + ":" + itoa64(micro)
	if _, err := store.NewPointsLedger(db).Credit(id, agent, 7, points, time.Unix(1791090000, 0)); err != nil {
		t.Fatalf("seed credit: %v", err)
	}
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestBurn_ReducesUsableAndPayout: spending points burns them, and the burn reduces both
// the usable balance and (per the 2026-10-08 decision) the TGE payout, which is built
// from the same cumulative-usable number.
func TestBurn_ReducesUsableAndPayout(t *testing.T) {
	db := openStore(t)
	spend := store.NewPointsSpend(db)

	const agent = "agent:eip155:8453:0x00000000000000000000000000000000000000a1"
	seedCredit(t, db, agent, 1_000_000_000) // 1000 points

	usable, err := spend.UsableMicro(agent)
	if err != nil {
		t.Fatalf("usable: %v", err)
	}
	if usable != 1_000_000_000 {
		t.Fatalf("usable before burn = %d, want 1000000000", usable)
	}

	if err := spend.Burn("burn:1", agent, 7, 400_000_000, "namespace", time.Unix(1791090000, 0)); err != nil {
		t.Fatalf("burn: %v", err)
	}

	usable, _ = spend.UsableMicro(agent)
	if usable != 600_000_000 {
		t.Errorf("usable after burn = %d, want 600000000", usable)
	}

	// The balance root source must reflect the burn: payout = earned - burned.
	cum, err := spend.CumulativeUsableMicro(7)
	if err != nil {
		t.Fatalf("cumulative usable: %v", err)
	}
	if cum[agent] != 600_000_000 {
		t.Errorf("payout balance = %d, want 600000000 (earned minus burned)", cum[agent])
	}
}

// TestBurn_RefusesOverdraft: a burn larger than the balance must fail, not go negative.
func TestBurn_RefusesOverdraft(t *testing.T) {
	db := openStore(t)
	spend := store.NewPointsSpend(db)

	const agent = "agent:eip155:8453:0x00000000000000000000000000000000000000a1"
	seedCredit(t, db, agent, 100_000_000) // 100 points

	if err := spend.Burn("burn:over", agent, 7, 200_000_000, "namespace", time.Now()); err == nil {
		t.Error("burning more than the balance must be refused")
	}
	usable, _ := spend.UsableMicro(agent)
	if usable != 100_000_000 {
		t.Errorf("a refused burn must not change the balance, got %d", usable)
	}
}

// TestBurn_IsIdempotentAndReasonIsClosed: a repeated burn id is a no-op (a retry must not
// double-spend), and an undefined reason is refused.
func TestBurn_IsIdempotentAndReasonIsClosed(t *testing.T) {
	db := openStore(t)
	spend := store.NewPointsSpend(db)

	const agent = "agent:eip155:8453:0x00000000000000000000000000000000000000a1"
	seedCredit(t, db, agent, 1_000_000_000)

	if err := spend.Burn("burn:x", agent, 7, 100_000_000, "discount", time.Now()); err != nil {
		t.Fatalf("first burn: %v", err)
	}
	// Same id again: no double-spend, no error.
	if err := spend.Burn("burn:x", agent, 7, 100_000_000, "discount", time.Now()); err != nil {
		t.Fatalf("a retry of the same burn id must be a no-op, got %v", err)
	}
	usable, _ := spend.UsableMicro(agent)
	if usable != 900_000_000 {
		t.Errorf("balance = %d, want 900000000 (one burn, not two)", usable)
	}

	// An undefined reason is refused, so no caller can invent a sink.
	if err := spend.Burn("burn:y", agent, 7, 1_000_000, "lol", time.Now()); err == nil {
		t.Error("an undefined burn reason must be refused")
	}
}

// TestBurn_ThroughEpochExcludesLaterBurns: a claim for an earlier epoch must not be
// changed by a burn that happened after it, or the epoch's root would drift as burns
// arrive.
func TestBurn_ThroughEpochExcludesLaterBurns(t *testing.T) {
	db := openStore(t)
	spend := store.NewPointsSpend(db)

	const agent = "agent:eip155:8453:0x00000000000000000000000000000000000000a1"
	// Credit at epochs 5 and 9 by seeding two entries.
	pl := store.NewPointsLedger(db)
	if _, err := pl.Credit("c5", agent, 5, 100, time.Now()); err != nil {
		t.Fatalf("credit e5: %v", err)
	}
	if _, err := pl.Credit("c9", agent, 9, 100, time.Now()); err != nil {
		t.Fatalf("credit e9: %v", err)
	}
	// A burn in epoch 9.
	if err := spend.Burn("b9", agent, 9, store.MicroFromPoints(50), "discount", time.Now()); err != nil {
		t.Fatalf("burn e9: %v", err)
	}

	// As of epoch 5, the burn (epoch 9) is not counted.
	at5, _ := spend.UsableMicroThroughEpoch(agent, 5)
	if at5 != store.MicroFromPoints(100) {
		t.Errorf("balance through epoch 5 = %d, want 100 points (later burn excluded)", at5)
	}
	// As of epoch 9, both credits and the burn count.
	at9, _ := spend.UsableMicroThroughEpoch(agent, 9)
	if at9 != store.MicroFromPoints(150) {
		t.Errorf("balance through epoch 9 = %d, want 150 points (200 - 50)", at9)
	}
}
