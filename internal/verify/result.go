package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// Schema is the contract this package emits. The JSON Schema in schema/ is
// authoritative; the golden record keeps the two from drifting.
const Schema = "harness.verify.v1"

// Severity levels, in the vocabulary `~/security-checker` already uses.
//
// prohairesis itself only ever emits High. That is not laziness, it is the
// honest answer: this layer knows that the repository declared a check and that
// the check did not pass, and it does not know how bad that is. The rest of the
// ladder exists because a source that does grade its own findings -- a security
// scanner -- writes into this same shape, and folding its CRITICAL into "High"
// would discard a judgement someone actually made.
const (
	SeverityCritical = "CRITICAL"
	SeverityHigh     = "HIGH"
	SeverityMedium   = "MEDIUM"
	SeverityLow      = "LOW"
)

// Finding is one thing a check reported.
type Finding struct {
	Severity string `json:"severity"`
	// Message is absent from the persisted copy of a result. See record.go.
	Message string `json:"message,omitempty"`
	// Location is repository-relative, in the same form the event log uses.
	Location string `json:"location,omitempty"`
}

// Category is one check's normalized result.
type Category struct {
	Category string `json:"category"`
	Source   string `json:"source,omitempty"`
	// Skipped means the check did not run. It is not a pass and is never scored
	// as one: a machine without the tool installed is not a project that passes.
	Skipped bool   `json:"skipped"`
	Reason  string `json:"skipped_reason,omitempty"`
	// Counts is findings by severity, the same summary security-checker prints.
	Counts    map[string]int `json:"counts"`
	Findings  []Finding      `json:"findings"`
	Deduction int            `json:"deduction"`
	// Truncated means findings were dropped to keep the document bounded. The
	// score is computed from what was kept, which for a category already at the
	// cap changes nothing -- but a reader is told rather than left to assume.
	Truncated bool `json:"truncated,omitempty"`
}

// Result is one verification run. Schema: harness.verify.v1.
type Result struct {
	Schema string    `json:"schema"`
	TS     time.Time `json:"ts"`
	// TotalScore and Rank are the security-checker arithmetic, unchanged, so
	// that one normalization serves both sources.
	TotalScore int    `json:"total_score"`
	Rank       string `json:"rank"`
	// Fingerprint identifies *this failure*, stably across runs. Empty when
	// there was nothing to fail: green has no fingerprint.
	Fingerprint string     `json:"fingerprint,omitempty"`
	Categories  []Category `json:"categories"`
	// Redacted marks the copy that kept no finding text. See record.go.
	Redacted bool `json:"redacted,omitempty"`
	// SessionID and CheckpointRef tie a run to the tree it ran against. Absent
	// when the run happened outside a session, which is allowed: reading whether
	// a repository passes needs no session.
	SessionID     string `json:"session_id,omitempty"`
	CheckpointSeq int    `json:"checkpoint_seq,omitempty"`
}

// penalty is security-checker's deduction table, reproduced rather than
// imported: that scoring lives in a private repository, and a public tool cannot
// depend on a file most of its users do not have. The arithmetic is asserted
// against the published table in score_test.go.
var penalty = map[string]int{
	SeverityCritical: 20,
	SeverityHigh:     10,
	SeverityMedium:   3,
	SeverityLow:      1,
}

// categoryCap bounds one category's deduction, so that a linter with a thousand
// complaints cannot drown out a failing test suite.
const categoryCap = 40

// score fills in counts, deductions, the total and the rank.
func score(cats []Category) (int, string) {
	total := 100
	for i := range cats {
		c := &cats[i]
		c.Counts = map[string]int{}
		c.Deduction = 0
		if c.Skipped {
			continue
		}
		for _, f := range c.Findings {
			c.Counts[f.Severity]++
			p, ok := penalty[f.Severity]
			if !ok {
				// An unrecognized severity still costs something. Scoring it
				// zero would let an unknown vocabulary silently mean "clean".
				p = 1
			}
			c.Deduction += p
		}
		if c.Deduction > categoryCap {
			c.Deduction = categoryCap
		}
		total -= c.Deduction
	}
	if total < 0 {
		total = 0
	}
	return total, rank(total)
}

func rank(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 70:
		return "B"
	case score >= 50:
		return "C"
	}
	return "D"
}

// Fingerprint identifies a failure by what was found, not by how it was printed.
//
// It hashes the normalized tuples -- category, severity, scrubbed message,
// location -- and never the raw output. Raw output carries timestamps, process
// ids, durations and temporary paths, so a fingerprint taken from it would be
// different on every run, and "the same failure three times in a row" would
// never be detectable. The tuples are sorted so that a tool reordering its
// output is not a different failure.
//
// It is approximate, and the direction of the error is chosen: scrubbing can
// merge two failures that differ only in a number. Whatever is built on this
// must present a count as evidence, never act on it.
func Fingerprint(cats []Category) string {
	seen := map[string]bool{}
	var lines []string
	for _, c := range cats {
		for _, f := range c.Findings {
			key := strings.Join([]string{c.Category, f.Severity, f.Message, f.Location}, "\x1f")
			if seen[key] {
				continue
			}
			seen[key] = true
			lines = append(lines, key)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// Failed reports whether any check that ran produced a finding. It is what the
// exit code is derived from, and it is deliberately not "the score is below
// some number": a threshold here would be this layer inventing a definition of
// correctness that no repository declared.
func (r Result) Failed() bool {
	for _, c := range r.Categories {
		if !c.Skipped && len(c.Findings) > 0 {
			return true
		}
	}
	return false
}

// Ran counts the categories that actually executed. A result where nothing ran
// scores 100, and saying so plainly is the only thing that keeps that number
// from reading as a pass.
func (r Result) Ran() int {
	n := 0
	for _, c := range r.Categories {
		if !c.Skipped {
			n++
		}
	}
	return n
}
