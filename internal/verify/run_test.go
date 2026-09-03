package verify

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, root string) Result {
	t.Helper()
	return Run(root, t.TempDir(), Discover(root))
}

// A machine without the tool installed is not a project that fails. Collapsing
// the two would make the second unreadable, and would make this layer report a
// verdict nobody reached.
func TestAMissingToolIsSkippedRatherThanFailed(t *testing.T) {
	root := t.TempDir()
	r := Run(root, "", []Check{{
		Category: "make:test",
		Source:   "Makefile",
		Argv:     []string{"a-program-that-does-not-exist-anywhere", "test"},
	}})

	if len(r.Categories) != 1 || !r.Categories[0].Skipped {
		t.Fatalf("got %+v, want one skipped category", r.Categories)
	}
	if r.Categories[0].Reason == "" {
		t.Fatal("a skip with no reason is a hole in the record")
	}
	if r.Failed() {
		t.Fatal("a missing tool was reported as a failing project")
	}
}

func TestAPassingCheckHasNoFindingsAndNoFingerprint(t *testing.T) {
	root := t.TempDir()
	write(t, root, "scripts/run_quality_checks.sh", "#!/bin/sh\necho fine\nexit 0\n")

	r := run(t, root)
	if r.Failed() || r.Fingerprint != "" || r.TotalScore != 100 {
		t.Fatalf("a passing repository produced %+v", r)
	}
	if r.Ran() != 1 {
		t.Fatalf("%d checks ran, want 1", r.Ran())
	}
}

// The end-to-end shape of the thing an agent reads, from a repository that
// declares one check and fails it.
func TestAFailingCheckIsNormalizedIntoTheSharedShape(t *testing.T) {
	root := t.TempDir()
	write(t, root, "scripts/run_quality_checks.sh",
		"#!/bin/sh\necho 'src/a.py:3:1: undefined name x'\nexit 1\n")

	r := run(t, root)
	if !r.Failed() {
		t.Fatal("a failing check did not report as failed")
	}
	if len(r.Categories) != 1 || r.Categories[0].Category != "quality" {
		t.Fatalf("got categories %+v", r.Categories)
	}
	f := r.Categories[0].Findings
	if len(f) != 1 || f[0].Location != "src/a.py:3:1" || f[0].Message != "undefined name x" {
		t.Fatalf("got findings %+v", f)
	}
	if r.TotalScore != 90 || r.Rank != "A" {
		t.Fatalf("scored %d/%s, want 90/A", r.TotalScore, r.Rank)
	}
	if r.Fingerprint == "" {
		t.Fatal("a failure with no fingerprint cannot be recognized when it repeats")
	}
}

// Running the same failing check twice must produce the same fingerprint, in the
// real path rather than only in the normalizer's unit test: the whole recurrence
// idea rests on this being true of actual subprocess output.
func TestTheSameFailingRepositoryFingerprintsTheSameTwice(t *testing.T) {
	root := t.TempDir()
	write(t, root, "scripts/run_quality_checks.sh",
		"#!/bin/sh\necho \"src/a.py:3:1: failed at $(date -u +%H:%M:%S) pid $$\"\nexit 1\n")

	first, second := run(t, root), run(t, root)
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("two runs of one failure fingerprinted differently:\n  %s\n  %s",
			first.Categories[0].Findings[0].Message, second.Categories[0].Findings[0].Message)
	}
}

// The record is what a later phase derives attempts and recurrence from, and it
// is also a file that outlives the session. So it keeps the identity of a failure
// and drops the text that produced it -- the same decision ADR 0002 made for
// command lines, for the same reason: a failing test may print anything at all,
// including a secret.
func TestTheStoredRecordKeepsTheIdentityAndNotTheText(t *testing.T) {
	root := t.TempDir()
	write(t, root, "scripts/run_quality_checks.sh",
		"#!/bin/sh\necho 'src/a.py:3:1: AWS_SECRET_ACCESS_KEY=hunter2 leaked'\nexit 1\n")

	r := run(t, root)
	dir := t.TempDir()
	if err := Record(dir, r); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, File))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hunter2") {
		t.Fatal("the stored record kept the text of a finding")
	}

	var stored Result
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Redacted {
		t.Fatal("a record with the text removed did not say so")
	}
	if stored.Fingerprint != r.Fingerprint {
		t.Fatal("the stored record lost the identity of the failure")
	}
	if stored.Categories[0].Findings[0].Location != "src/a.py:3:1" {
		t.Fatalf("the stored record lost the location: %+v", stored.Categories[0].Findings)
	}
}

func TestTheRecordIsAppendOnly(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := Record(dir, Result{Schema: Schema}); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(filepath.Join(dir, File))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("three runs left %d lines", n)
	}
}
