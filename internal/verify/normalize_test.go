package verify

import (
	"strings"
	"testing"
)

func norm() normalizer {
	return normalizer{root: "/home/someone/work/project", home: "/home/someone"}
}

// The one output format nearly every tool agrees on has to be read correctly,
// because everything downstream -- the location an agent is pointed at, the
// fingerprint, the recurrence count built on it later -- is derived from it.
func TestALocationIsReadFromTheFormatToolsAgreeOn(t *testing.T) {
	cases := map[string]struct{ loc, msg string }{
		"src/a.py:12:5: E501 line too long":              {"src/a.py:12:5", "E501 line too long"},
		"internal/x/y.go:42:2: declared and not used: x": {"internal/x/y.go:42:2", "declared and not used: x"},
		"  --> src/main.rs:3:5":                          {"src/main.rs:3:5", ""},
		"scripts/check.sh:8: bad substitution":           {"scripts/check.sh:8", "bad substitution"},
		"/home/someone/work/project/src/b.ts:1:1: oops":  {"src/b.ts:1:1", "oops"},
		"/home/someone/elsewhere/c.ts:1:1: outside":      {"~/elsewhere/c.ts:1:1", "outside"},
	}
	for line, want := range cases {
		got, _ := norm().findings(Check{Source: "Makefile"}, line, 1)
		if len(got) != 1 {
			t.Errorf("%q produced %d findings, want 1", line, len(got))
			continue
		}
		if got[0].Location != want.loc || got[0].Message != want.msg {
			t.Errorf("%q -> {%q, %q}, want {%q, %q}",
				line, got[0].Location, got[0].Message, want.loc, want.msg)
		}
	}
}

// A clock reading is not a location. Reading one as a location would put a
// nonsense path in front of an agent and, worse, make every run a new failure.
func TestATimestampIsNotMistakenForALocation(t *testing.T) {
	out := "10:00:00: everything is on fire\nerror: 3: still on fire"
	got, _ := norm().findings(Check{Source: "Makefile"}, out, 1)
	if len(got) != 1 || !strings.HasPrefix(got[0].Message, "exit 1:") {
		t.Fatalf("got %+v, want a single fallback finding", got)
	}
}

// This is the whole point of normalizing before hashing. The same failure, twice,
// with a different timestamp, a different pid, a different temporary directory
// and a different duration, has to be one failure -- otherwise "the same failure
// three times in a row" is never detectable and the layer built on it is dead.
func TestTheSameFailureTwiceHasOneFingerprint(t *testing.T) {
	first := "src/a.py:3:1: failed at 2026-09-03T02:11:05Z (pid 4821) in /var/folders/kt/abc123/T/run (0.42s)"
	second := "src/a.py:3:1: failed at 2026-09-04T19:02:44Z (pid 91) in /var/folders/zz/xy9q88/T/run (1.07s)"

	a, _ := norm().findings(Check{Source: "Makefile"}, first, 1)
	b, _ := norm().findings(Check{Source: "Makefile"}, second, 1)

	fa := Fingerprint([]Category{{Category: "make:test", Findings: a}})
	fb := Fingerprint([]Category{{Category: "make:test", Findings: b}})
	if fa != fb || fa == "" {
		t.Fatalf("two runs of one failure fingerprinted %q and %q\n  %q\n  %q",
			fa, fb, a[0].Message, b[0].Message)
	}
}

// The other half of the same requirement: scrubbing must not be so aggressive
// that two genuinely different failures collapse into one.
func TestADifferentFailureHasADifferentFingerprint(t *testing.T) {
	a, _ := norm().findings(Check{Source: "Makefile"}, "src/a.py:3:1: undefined name x", 1)
	b, _ := norm().findings(Check{Source: "Makefile"}, "src/a.py:3:1: undefined name y", 1)

	fa := Fingerprint([]Category{{Category: "make:test", Findings: a}})
	fb := Fingerprint([]Category{{Category: "make:test", Findings: b}})
	if fa == fb {
		t.Fatal("two different failures share a fingerprint")
	}
}

// A tool that prints its findings in a different order has not found different
// things.
func TestOutputOrderIsNotPartOfTheFailure(t *testing.T) {
	one := []Finding{{Severity: SeverityHigh, Message: "a", Location: "x.py:1"},
		{Severity: SeverityHigh, Message: "b", Location: "y.py:2"}}
	two := []Finding{one[1], one[0]}

	if Fingerprint([]Category{{Category: "c", Findings: one}}) !=
		Fingerprint([]Category{{Category: "c", Findings: two}}) {
		t.Fatal("reordering the output changed the fingerprint")
	}
}

// Green has no fingerprint. An empty hash would be a real-looking identifier for
// the absence of a failure, and something downstream would eventually count it.
func TestPassingProducesNoFingerprint(t *testing.T) {
	if got := Fingerprint([]Category{{Category: "go:test"}}); got != "" {
		t.Fatalf("a passing run produced fingerprint %q", got)
	}
}

// When no line names a location, the exit status is most of what distinguishes
// one failure from another.
func TestTheFallbackCarriesTheExitStatus(t *testing.T) {
	got, _ := norm().findings(Check{Source: "Makefile"}, "make: *** [test] Error 2\n", 2)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	if !strings.HasPrefix(got[0].Message, "exit 2:") {
		t.Fatalf("fallback message %q does not carry the exit status", got[0].Message)
	}
	if got[0].Location != "Makefile" {
		t.Fatalf("fallback location %q, want the file that declared the check", got[0].Location)
	}
}

// A linter with hundreds of complaints must not be able to push the document
// past what an agent can read. The cap is a bound on the record, and it says so.
func TestFindingsAreBoundedAndSaySo(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxFindings*3; i++ {
		b.WriteString("src/a.py:")
		b.WriteString(strings.Repeat("1", 1+i%3))
		b.WriteString(":1: distinct ")
		b.WriteString(strings.Repeat("x", i+1))
		b.WriteString("\n")
	}
	got, truncated := norm().findings(Check{Source: "Makefile"}, b.String(), 1)
	if len(got) != maxFindings || !truncated {
		t.Fatalf("kept %d findings, truncated=%v; want %d and true", len(got), truncated, maxFindings)
	}
}

func TestOutputIsShortenedFromTheMiddle(t *testing.T) {
	big := []byte(strings.Repeat("a", maxOutputKept*2))
	big[0] = 'S'
	big[len(big)-1] = 'E'
	got := bound(big)
	if len(got) > maxOutputKept+64 {
		t.Fatalf("shortened output is %d bytes, over the ceiling", len(got))
	}
	// Both ends survive, because linters put findings first and test runners put
	// them last, and keeping one end would lose half the tools.
	if got[0] != 'S' || got[len(got)-1] != 'E' {
		t.Fatal("shortening dropped one end of the output")
	}
}
