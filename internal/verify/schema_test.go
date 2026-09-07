package verify

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The published schema is a contract another implementation is invited to adopt,
// so the golden file is the fixed point and the Go types have to keep agreeing
// with it. The JSON Schema half of this check runs in CI, where a validator is
// available; this half runs everywhere and catches the failure that matters
// most -- a field the types silently stopped producing.

func TestGoldenResultsRoundTrip(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "golden", "verify.v1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n"))
	if len(lines) < 4 {
		t.Fatalf("the golden file has %d records; it is meant to cover the shapes "+
			"this layer emits", len(lines))
	}

	sawSkip, sawFinding, sawRedacted, sawTruncated := false, false, false, false
	for i, line := range lines {
		var r Result
		if err := json.Unmarshal(line, &r); err != nil {
			t.Fatalf("record %d does not parse: %v", i+1, err)
		}
		if r.Schema != Schema {
			t.Fatalf("record %d declares schema %q, want %q", i+1, r.Schema, Schema)
		}

		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
		// Compared as values: key order is not part of the contract, and a field
		// dropped by the types is.
		var from, to any
		if err := json.Unmarshal(line, &from); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(buf.Bytes(), &to); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(from, to) {
			t.Fatalf("record %d does not survive a round trip.\nfrom: %s\n  to: %s",
				i+1, line, bytes.TrimRight(buf.Bytes(), "\n"))
		}

		// The score in the file is the score the code computes. Without this, the
		// golden file could quietly become a record of arithmetic nobody does.
		cats := append([]Category(nil), r.Categories...)
		if got, rank := score(cats); got != r.TotalScore || rank != r.Rank {
			t.Fatalf("record %d stores %d/%s but the code computes %d/%s",
				i+1, r.TotalScore, r.Rank, got, rank)
		}
		if got := Fingerprint(r.Categories); !r.Redacted && got != r.Fingerprint {
			t.Fatalf("record %d stores fingerprint %q but the code computes %q",
				i+1, r.Fingerprint, got)
		}

		sawRedacted = sawRedacted || r.Redacted
		for _, c := range r.Categories {
			sawSkip = sawSkip || c.Skipped
			sawFinding = sawFinding || len(c.Findings) > 0
			sawTruncated = sawTruncated || c.Truncated
		}
	}

	for what, seen := range map[string]bool{
		"a skipped check": sawSkip, "a finding": sawFinding,
		"a redacted record": sawRedacted, "a truncated category": sawTruncated,
	} {
		if !seen {
			t.Errorf("the golden file covers no example of %s", what)
		}
	}
}

// A redacted record keeps the identity of a failure and drops the text that
// produced it, so its fingerprint is deliberately not recomputable from what is
// stored. Asserting that here keeps the next reader from "fixing" it.
func TestARedactedRecordCannotBeRehashed(t *testing.T) {
	r := Result{Categories: []Category{{
		Category: "quality",
		Findings: []Finding{{Severity: SeverityHigh, Message: "secret leaked", Location: "a.py:1"}},
	}}}
	r.Fingerprint = Fingerprint(r.Categories)

	stored := r.Redact()
	if Fingerprint(stored.Categories) == stored.Fingerprint {
		t.Fatal("the redacted record still contains what its fingerprint was taken from")
	}
	if stored.Fingerprint != r.Fingerprint {
		t.Fatal("redaction changed the identity of the failure")
	}
}
