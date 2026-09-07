package verify

import "testing"

func findings(severity string, n int) []Finding {
	out := make([]Finding, n)
	for i := range out {
		out[i] = Finding{Severity: severity, Message: string(rune('a' + i))}
	}
	return out
}

// The arithmetic is security-checker's, reproduced here because that scoring
// lives in a private repository this one cannot depend on. If the two ever
// disagree, an agent learning one output shape has been told a lie -- so the
// published table is asserted rather than trusted.
func TestScoringMatchesThePublishedTable(t *testing.T) {
	cases := []struct {
		name  string
		cats  []Category
		score int
		rank  string
	}{
		{"nothing found", []Category{{Category: "a"}}, 100, "A"},
		{"one high", []Category{{Category: "a", Findings: findings(SeverityHigh, 1)}}, 90, "A"},
		{"one critical", []Category{{Category: "a", Findings: findings(SeverityCritical, 1)}}, 80, "B"},
		{"three medium", []Category{{Category: "a", Findings: findings(SeverityMedium, 3)}}, 91, "A"},
		{"capped at forty", []Category{{Category: "a", Findings: findings(SeverityHigh, 40)}}, 60, "C"},
		{"two capped categories", []Category{
			{Category: "a", Findings: findings(SeverityHigh, 40)},
			{Category: "b", Findings: findings(SeverityCritical, 40)},
		}, 20, "D"},
		{"the floor holds", []Category{
			{Category: "a", Findings: findings(SeverityHigh, 40)},
			{Category: "b", Findings: findings(SeverityHigh, 40)},
			{Category: "c", Findings: findings(SeverityHigh, 40)},
		}, 0, "D"},
	}
	for _, c := range cases {
		got, rank := score(c.cats)
		if got != c.score || rank != c.rank {
			t.Errorf("%s: scored %d/%s, want %d/%s", c.name, got, rank, c.score, c.rank)
		}
	}
}

// A severity nobody here recognizes still costs something. Scoring it zero would
// let a new vocabulary quietly mean "clean".
func TestAnUnknownSeverityStillCosts(t *testing.T) {
	cats := []Category{{Category: "a", Findings: []Finding{{Severity: "SPICY"}}}}
	if got, _ := score(cats); got != 99 {
		t.Fatalf("an unknown severity scored %d, want 99", got)
	}
}

// This is the number most likely to be misread. Nothing ran, so nothing passed --
// and the record has to make that distinguishable from a project that passed.
func TestASkippedCheckIsNotAPass(t *testing.T) {
	r := Result{Categories: []Category{{Category: "a", Skipped: true, Reason: "not installed"}}}
	r.TotalScore, r.Rank = score(r.Categories)

	if r.TotalScore != 100 {
		t.Fatalf("a skipped check deducted %d; it has no findings to deduct for", 100-r.TotalScore)
	}
	if r.Failed() {
		t.Fatal("a skipped check was reported as a failure")
	}
	if r.Ran() != 0 {
		t.Fatal("a skipped check was counted as having run")
	}
}

func TestFailedIsFindingsAndNotAThreshold(t *testing.T) {
	r := Result{Categories: []Category{{Category: "a", Findings: findings(SeverityLow, 1)}}}
	r.TotalScore, r.Rank = score(r.Categories)
	if r.TotalScore != 99 || r.Rank != "A" {
		t.Fatalf("scored %d/%s, want 99/A", r.TotalScore, r.Rank)
	}
	// Rank A and still failed: the exit code reports what the repository's own
	// tools said, not a threshold this program picked.
	if !r.Failed() {
		t.Fatal("a run with a finding did not report as failed")
	}
}
