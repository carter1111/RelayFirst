// Command compliance_audit runs the A5 points-claims guard over the repository's user-facing
// text and prints what it matched.
//
// # Why this exists as a program and not only as a test
//
// The test in internal/compliance asserts the same thing, but it only reports pass or fail.
// When it fails, a reviewer needs to see *what* matched and *where* — and they need to be able
// to run that check against a file that is not yet committed, or against a pasted string,
// without editing the test. This program is the human-facing half of S8-9.
//
// # Usage
//
//	go run internal/devtools/compliance_audit.go            # audit the default surfaces
//	go run internal/devtools/compliance_audit.go FILE...     # audit specific files
//	go run internal/devtools/compliance_audit.go -v FILE...  # also show excused disclaimers
//
// Exit code is 1 when anything needs review, so it can gate a release step.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/relayfirst/relayfirst/internal/compliance"
)

// defaultTargets are the surfaces a user actually reads: the CLI's own output strings and the
// onboarding guide. Internal design notes are deliberately excluded — MVP.md §6.1 is where the
// A5 rule is *defined*, so it must be free to discuss value as a subject.
//
// The SBT contract is included (S11-7) because it is a user-facing surface in the way that
// matters most for A5: it is where a points balance could acquire a price. A contract comment
// promising a return, or a name implying one, would be the exact claim A5 forbids — and it
// would be on-chain, permanent, and readable by every wallet that displays the badge.
var defaultTargets = []string{
	filepath.Join("cmd", "relayfirst", "main.go"),
	filepath.Join("docs", "getting-started.md"),
	"README.md",
	filepath.Join("contracts", "RelayPoints.sol"),
}

func main() {
	verbose := flag.Bool("v", false, "also print matches that were excused as approved disclaimers")
	flag.Parse()

	targets := flag.Args()
	if len(targets) == 0 {
		targets = defaultTargets
	}

	scanned := 0
	matched := 0
	review := 0

	for _, path := range targets {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			fmt.Printf("skip  %s (not present)\n", path)
			continue
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: read %s: %v\n", path, err)
			os.Exit(2)
		}
		scanned++

		findings := compliance.Scan(path, string(data))
		matched += len(findings)

		for _, f := range findings {
			if f.Excused {
				if *verbose {
					fmt.Printf("ok    %s:%d  %q  (excused: %q)\n", f.Source, f.Line, f.Text, f.Excuse)
				}
				continue
			}
			review++
			fmt.Printf("FAIL  %s:%d  %q  — %s\n", f.Source, f.Line, f.Text, f.Claim.Why)
		}
	}

	fmt.Printf("\nscanned %d file(s), matched %d phrase(s)\n", scanned, matched)

	// A scan that matched nothing is not a pass; it means the guard is not looking at real
	// text. Saying so explicitly keeps this tool from reporting false confidence.
	if matched == 0 {
		fmt.Println("WARNING: matched zero phrases — the guard may not be running over real text")
	}

	if review > 0 {
		fmt.Printf("%d finding(s) need review\n", review)
		os.Exit(1)
	}
	fmt.Println("no A5 violations found (points are unpriced and non-transferable everywhere they are described)")
}
