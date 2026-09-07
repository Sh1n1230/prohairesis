package claudecode

import (
	"encoding/json"
	"testing"
)

func TestContextIsNamedRatherThanPrintedLoose(t *testing.T) {
	out := SessionStartContext("hello")
	var got struct {
		Specific struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Specific.Event != "SessionStart" || got.Specific.Context != "hello" {
		t.Fatalf("got %+v", got.Specific)
	}
}

// A session with nothing to point at must cost the agent nothing at all -- not
// an empty envelope, not a blank line.
func TestNothingToSayProducesNoOutput(t *testing.T) {
	if out := SessionStartContext(""); out != nil {
		t.Fatalf("empty text produced %q", out)
	}
}
