//go:build ignore

// epochprobe prints the emission parameters for the current wall clock.
//
// It exists to make the epoch-anchoring question concrete rather than
// theoretical: run it and see what budget the live economy is actually working
// with.
//
//	go run internal/devtools/epochprobe.go
//
// Background: EpochOf is a pure function of the unix timestamp, so without the
// genesis anchor the epoch index is ~20,410 today. EpochBudget applies
// Decay^floor(n/DecayPeriod) to that index, which drives the budget to
// effectively zero and, through BudgetFactor = 1 - earned/cap, stops any agent
// being credited more than once per epoch. See docs/notes/epoch-anchoring.md.
//
// This is a diagnostic, not part of the build.
package main

import (
	"fmt"
	"time"

	"github.com/relayfirst/relayfirst/internal/scoring"
)

func main() {
	now := time.Now()
	e := scoring.EpochOf(now)

	fmt.Printf("now        = %s\n", now.UTC().Format(time.RFC3339))
	fmt.Printf("epoch      = %d\n", e)
	fmt.Printf("B(epoch)   = %.6e\n", scoring.EpochBudget(e))
	fmt.Printf("cap(epoch) = %.6e   (B * %.0f%%)\n", scoring.PerAgentCap(e), scoring.PerAgentCapFraction*100)

	// What the parameters would be at genesis, for comparison.
	fmt.Printf("\nepoch 0    = B0 = %.0f, cap = %.0f (peragent)\n",
		scoring.EpochBudget(0), scoring.PerAgentCap(0))

	// How many days until B(n) rounds to zero at float64 resolution.
	for _, d := range []int{1, 30, 365, 730, 1825} {
		future := now.AddDate(0, 0, d)
		fe := scoring.EpochOf(future)
		fmt.Printf("+%5d days  epoch %d  B = %.6e\n", d, fe, scoring.EpochBudget(fe))
	}
}
