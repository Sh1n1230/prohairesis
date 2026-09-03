package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// Timeout is a backstop, not a policy.
//
// Whoever runs `verify` almost always has a shorter deadline of their own -- an
// agent's shell tool has one, a human has a terminal they can interrupt. This
// exists so that the one case neither of those covers, a check that hangs in a
// script nobody is watching, ends rather than lasting until the machine is
// rebooted.
const Timeout = 10 * time.Minute

// Run executes the checks and normalizes what they said.
//
// It reports no error. A check that could not run is a skipped category with the
// reason recorded, because "mypy is not installed on this machine" and "this
// project fails type checking" are different facts and collapsing them would
// make the second unreadable. This mirrors the graceful degradation the rest of
// the project follows: report and skip, never error, never block.
func Run(root, homeDir string, checks []Check) Result {
	// Categories is an empty slice rather than nil so that a repository which
	// declares nothing still serializes as the shape the schema publishes. A
	// `null` where an array was promised is a contract broken over a detail.
	r := Result{Schema: Schema, TS: time.Now().UTC().Truncate(time.Millisecond),
		Categories: []Category{}}
	n := normalizer{root: root, home: homeDir}

	for _, c := range checks {
		r.Categories = append(r.Categories, runOne(n, root, c))
	}
	r.TotalScore, r.Rank = score(r.Categories)
	r.Fingerprint = Fingerprint(r.Categories)
	return r
}

func runOne(n normalizer, root string, c Check) Category {
	cat := Category{Category: c.Category, Source: c.Source, Counts: map[string]int{}, Findings: []Finding{}}

	if len(c.Argv) == 0 {
		cat.Skipped, cat.Reason = true, "nothing to run"
		return cat
	}
	if _, err := exec.LookPath(c.Argv[0]); err != nil {
		cat.Skipped = true
		cat.Reason = c.Argv[0] + " is not installed on this machine"
		return cat
	}

	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	// A static scanner flags this line, and it is right to: the program being run
	// comes from a file in the repository rather than from a constant. That is
	// the feature, not an oversight -- this layer's whole premise is that the
	// repository already declared how it checks itself. What bounds it is
	// discovery, not this call: the target names are a closed list that excludes
	// anything effectful, and `docs/ENFORCEMENT-HONESTY.md` states plainly that
	// `verify` runs code from a repository you may not have read. See ADR 0004.
	// nosemgrep: dangerous-exec-command
	cmd := exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
	cmd.Dir = root
	cmd.Env = os.Environ()
	// Stdin stays nil, which is /dev/null: a check that stops to ask a question
	// would otherwise wait for an answer nobody is there to give.
	//
	// The wait delay is the other half of the timeout. Without it, a check whose
	// children outlive it holds the output pipe open and the deadline buys
	// nothing: the wait, not the process, is what hangs.
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	out = bound(out)

	switch {
	case err == nil:
		return cat
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		// Not a finding about the code. The check did not reach a verdict, so
		// reporting one would be inventing it.
		cat.Skipped = true
		cat.Reason = fmt.Sprintf("did not finish within %s", Timeout)
		return cat
	}

	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		// The check could not be started at all -- a script the shell refused, a
		// binary that vanished between the lookup and the call. Nothing ran, so
		// nothing failed.
		cat.Skipped = true
		cat.Reason = err.Error()
		return cat
	}

	cat.Findings, cat.Truncated = n.findings(c, string(out), ee.ExitCode())
	return cat
}
