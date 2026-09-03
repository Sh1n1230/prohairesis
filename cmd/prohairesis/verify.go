package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/Sh1n1230/prohairesis/internal/session"
	"github.com/Sh1n1230/prohairesis/internal/verify"
)

// cmdVerify runs the checks this repository declares and prints the normalized
// result.
//
// This is the one command in the project written for the agent rather than for a
// person. Everything about it follows from that: one output shape whichever tool
// produced the finding, a `--json` form that needs no parsing of prose, and an
// exit code that means what it means everywhere else here.
//
// What it is not is a gate. Nothing is refused, nothing is blocked, and the exit
// code is a report on what the repository's own tools said -- not a judgement
// this program formed. The distinction is the reason this layer is allowed to
// exist at all: a verification contract that can stop work is a permission
// prompt with extra steps.
func cmdVerify(args []string) int {
	_, asJSON := has(args, "json")

	repo, err := session.DescribeRepo(cwd())
	if err != nil {
		fmt.Fprintln(os.Stderr, "prohairesis: "+err.Error())
		return 2
	}

	checks := verify.Discover(repo.Root)
	result := verify.Run(repo.Root, homeDir(), checks)
	record(repo, &result)

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			fmt.Fprintln(os.Stderr, "prohairesis: "+err.Error())
			return 2
		}
	} else {
		printResult(checks, result)
	}

	if result.Failed() {
		return 1
	}
	return 0
}

// record files the run against the current session, if there is one.
//
// Running outside a session is normal and stays silent: reading whether a
// repository passes is useful on its own, and refusing to answer without a
// session would make this command need a setup step it does not need.
func record(repo session.Repo, r *verify.Result) {
	s, err := session.CurrentFor(repo)
	if err != nil {
		if !errors.Is(err, session.ErrNoSession) {
			fmt.Fprintln(os.Stderr, "prohairesis: "+err.Error())
		}
		return
	}
	// Annotated before it is either written or printed, so that the copy an
	// agent reads and the copy on disk name the same tree.
	r.SessionID = s.ID
	if c, ok := s.LastCheckpoint(); ok {
		r.CheckpointSeq = c.Seq
	}
	dir, err := s.EventDir()
	if err == nil {
		err = verify.Record(dir, *r)
	}
	if err != nil {
		// Worth saying out loud rather than swallowing: a run that was not
		// recorded is a run the history will not know happened.
		fmt.Fprintln(os.Stderr, "prohairesis: this run was not recorded: "+err.Error())
	}
}

func printResult(checks []verify.Check, r verify.Result) {
	if len(checks) == 0 {
		fmt.Println("no checks are declared in this repository")
		fmt.Println()
		fmt.Println("prohairesis runs what a repository already declares and invents nothing.")
		fmt.Println("It looks for, in this order:")
		fmt.Println("  scripts/run_quality_checks.sh")
		fmt.Println("  a Makefile with a check, verify, lint, typecheck or test target")
		fmt.Println("  package.json scripts, Cargo.toml, go.mod")
		return
	}

	for _, c := range r.Categories {
		switch {
		case c.Skipped:
			fmt.Printf("  skip  %-16s %s\n", c.Category, c.Reason)
		case len(c.Findings) == 0:
			fmt.Printf("  pass  %-16s %s\n", c.Category, c.Source)
		default:
			fmt.Printf("  FAIL  %-16s %s  (-%d)\n", c.Category, summarize(c.Counts), c.Deduction)
		}
	}

	for _, c := range r.Categories {
		if c.Skipped || len(c.Findings) == 0 {
			continue
		}
		fmt.Println()
		fmt.Println(c.Category)
		for _, f := range c.Findings {
			where := f.Location
			if where == "" {
				where = "-"
			}
			fmt.Printf("  %-8s %s  %s\n", f.Severity, where, f.Message)
		}
		if c.Truncated {
			fmt.Println("  ... more findings than are worth printing; the score already caps here")
		}
	}

	fmt.Println()
	fmt.Printf("score %d/100  rank %s  (%d of %d checks ran)\n",
		r.TotalScore, r.Rank, r.Ran(), len(r.Categories))
	if r.Ran() == 0 {
		// A perfect score from an empty run is the one number here that could
		// be read as an answer when it is the absence of one.
		fmt.Println("nothing ran, so nothing passed; the score above measures nothing")
	}
	if r.Fingerprint != "" {
		fmt.Printf("failure %s\n", r.Fingerprint[:12])
	}
}

func summarize(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for _, k := range keys {
		if out != "" {
			out += " "
		}
		out += fmt.Sprintf("%s %d", k, counts[k])
	}
	return out
}
