// Package notice builds the only text prohairesis is allowed to put into an
// agent's context.
//
// The layer above this one is pull, not push: `verify`, `undo` and everything
// after them are commands an agent chooses to run, and nothing here summarizes,
// advises, or reports state. All that is injected is a pointer to the fact that
// those commands exist, because a capability nobody was told about is a
// capability nobody has.
//
// The budget is the discipline. Every phase of this project will want to inject
// "just one more line", and the sum of those is a tax on the context window paid
// by the agent's actual work. So the ceiling is enforced here, in the code that
// produces the text, rather than written down somewhere as an intention.
package notice

import "strings"

// Budget is the hard ceiling on injected text, in bytes.
//
// The design states the ceiling in tokens -- under 50 -- and tokens are not
// something this program can count without a tokenizer it has no business
// shipping. Four bytes per token is the conservative English approximation, so
// 200 bytes is the same limit expressed in a unit that can be checked, and
// checked cheaply, on every injection.
const Budget = 200

// SessionStart is the line handed to an agent when a session begins.
//
// It says two things and no more: what is recording, and which two commands
// exist. It deliberately does not say what the checks are, whether they last
// passed, or what the agent should do -- that is either state the agent can ask
// for or advice this layer has no standing to give.
func SessionStart(hasChecks bool) string {
	if hasChecks {
		return Fit("prohairesis: recording this session. `prohairesis verify` runs the " +
			"checks this repo declares; `prohairesis undo` restores the tree.")
	}
	return Fit("prohairesis: recording this session. `prohairesis undo` restores the " +
		"tree to any checkpoint.")
}

// Fit enforces the budget.
//
// Nothing this package produces today comes near it -- a test asserts that -- so
// this is the guard that keeps the next line somebody adds from being the one
// that quietly doubles the cost. Truncation is visible on purpose: a notice that
// was cut should look cut.
func Fit(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= Budget {
		return s
	}
	return s[:Budget-3] + "..."
}
