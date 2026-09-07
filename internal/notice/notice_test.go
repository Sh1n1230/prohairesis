package notice

import (
	"strings"
	"testing"
)

// The budget is the only thing standing between this project and a context
// window slowly filling with its own announcements. Asserting it here means the
// truncation guard in Fit stays unreachable, which is the point: text that has
// to be cut is text that should have been shorter.
func TestEveryNoticeFitsTheBudget(t *testing.T) {
	for _, hasChecks := range []bool{true, false} {
		line := SessionStart(hasChecks)
		if len(line) > Budget {
			t.Errorf("notice is %d bytes, over the %d-byte budget:\n  %s",
				len(line), Budget, line)
		}
		if strings.HasSuffix(line, "...") {
			t.Errorf("notice was truncated, so it was written too long to begin with:\n  %s", line)
		}
	}
}

// One line. A notice that grows into a paragraph has become a summary, and a
// summary is the meta-state layer pushing itself into a context window it was
// supposed to wait to be asked for.
func TestANoticeIsOneLine(t *testing.T) {
	for _, hasChecks := range []bool{true, false} {
		if strings.Contains(SessionStart(hasChecks), "\n") {
			t.Errorf("notice spans more than one line: %q", SessionStart(hasChecks))
		}
	}
}

// Pointing at a command that will answer "nothing is declared here" spends
// context to buy a wasted tool call.
func TestVerifyIsOnlyMentionedWhereThereIsSomethingToVerify(t *testing.T) {
	if strings.Contains(SessionStart(false), "verify") {
		t.Error("a repository with no declared checks was pointed at verify")
	}
	if !strings.Contains(SessionStart(true), "verify") {
		t.Error("a repository with declared checks was not told the command exists")
	}
}

// This layer names facilities. It does not advise, plan, or evaluate -- the
// moment it does, the environment has started managing the agent, which is the
// thing this project exists to stop doing.
func TestANoticeCarriesNoStrategy(t *testing.T) {
	banned := []string{"you should", "try ", "consider", "recommend", "must ", "please"}
	for _, hasChecks := range []bool{true, false} {
		line := strings.ToLower(SessionStart(hasChecks))
		for _, b := range banned {
			if strings.Contains(line, b) {
				t.Errorf("notice tells the agent what to do (%q): %s", b, line)
			}
		}
	}
}

func TestFitCutsWhatWillNotFit(t *testing.T) {
	got := Fit(strings.Repeat("x", Budget*2))
	if len(got) != Budget {
		t.Fatalf("truncated notice is %d bytes, want %d", len(got), Budget)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatal("a notice that was cut does not look cut")
	}
}
