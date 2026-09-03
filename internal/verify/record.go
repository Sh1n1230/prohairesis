package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// File is the append-only record of verification runs in a session directory.
const File = "verify.jsonl"

// Redact returns the copy of a result that is safe to keep.
//
// Everything survives except the text of a finding. That single omission is the
// whole decision, and it follows the one already made for command lines in
// ADR 0002: a tool's output is arbitrary program output, and a failing test that
// prints an environment variable would otherwise put a secret in a file that
// outlives the session. What is kept -- category, severity, location and the
// fingerprint computed from the text before it was dropped -- is exactly what
// recurrence detection needs and no more.
//
// The consequence is stated rather than hidden: the fingerprint in a stored
// record cannot be recomputed from that record. It is an identity, not a digest
// of what is next to it.
func (r Result) Redact() Result {
	out := r
	out.Redacted = true
	out.Categories = make([]Category, len(r.Categories))
	for i, c := range r.Categories {
		c.Findings = append(make([]Finding, 0, len(c.Findings)), c.Findings...)
		for j := range c.Findings {
			c.Findings[j].Message = ""
		}
		out.Categories[i] = c
	}
	return out
}

// Record appends a run to a session's verification log.
//
// It is what makes a fingerprint mean anything: an identifier for "this
// failure" is decoration until there is a previous run to compare it against.
// Nothing reads this file yet -- the layer that derives attempts and recurrence
// from it is a later phase -- but the record has to start being written before
// that phase can be built on real sessions rather than on fixtures.
func Record(dir string, r Result) error {
	if dir == "" {
		return fmt.Errorf("no session directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r.Redact()); err != nil {
		return err
	}

	path := filepath.Join(dir, File)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// The same advisory lock the event log uses: two checks finishing at once
	// must not interleave their lines. Every writer of this file is this program.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	_, err = f.Write(buf.Bytes())
	return err
}
