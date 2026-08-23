package event

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The published schema is a contract other implementations are invited to adopt,
// so it is written first and the Go types follow it -- not the other way round.
// That only stays true if something checks. This is the Go half: every field the
// types produce survives a round trip through the golden file, and every field
// the golden file carries is understood by the types.
//
// The other half runs in CI, where a JSON Schema validator checks the same file
// against schema/harness.event.v1.json. It lives there rather than here so that
// the shipped binary keeps its one genuinely useful property: no dependencies at
// all.

func goldenPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "tests", "golden", name)
}

func TestGoldenEventsRoundTrip(t *testing.T) {
	b, err := os.ReadFile(goldenPath(t, "event.v1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimRight(b, "\n"), []byte("\n"))
	if len(lines) < 5 {
		t.Fatalf("the golden file has %d events; it is meant to cover the shapes "+
			"this layer emits", len(lines))
	}

	seenKinds := map[Kind]bool{}
	seenTypes := map[Type]bool{}
	for i, line := range lines {
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("line %d does not parse: %v", i+1, err)
		}
		if e.Schema != Schema {
			t.Fatalf("line %d declares schema %q, want %q", i+1, e.Schema, Schema)
		}
		out, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		// Compared as values rather than bytes: key order is not part of the
		// contract, and a field silently dropped by the types is.
		if !sameJSON(t, line, out) {
			t.Fatalf("line %d does not survive a round trip.\nfrom: %s\n  to: %s",
				i+1, line, out)
		}
		seenTypes[e.Type] = true
		if e.Action != nil {
			seenKinds[e.Action.Kind] = true
		}
	}

	for _, want := range []Type{SessionStart, SessionEnd, Tool} {
		if !seenTypes[want] {
			t.Errorf("the golden file has no %q event", want)
		}
	}
	// Opaque above all: it is the shape most likely to be quietly dropped by a
	// later change, and the one whose absence would mean this layer had started
	// claiming coverage it does not have.
	for _, want := range []Kind{KindRead, KindWrite, KindExec, KindNetwork, KindOpaque} {
		if !seenKinds[want] {
			t.Errorf("the golden file has no %q action", want)
		}
	}
}

// Fields that do not exist cannot be filled in by someone who assumes they
// should be. Classification and enforcement belong to a later phase and a later
// schema version; reserving space for them here would be an invitation.
func TestTheSchemaHasNoRoomForAVerdict(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "schema", "harness.event.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"boundary_class", "decision", "enforcement", "allowed"} {
		if _, present := doc.Properties[forbidden]; present {
			t.Errorf("schema defines %q; this layer records and does not decide, and a "+
				"field waiting to be filled is how that stops being true", forbidden)
		}
	}
	for _, required := range []string{"schema", "ts", "seq", "session_id", "type", "actor"} {
		if _, present := doc.Properties[required]; !present {
			t.Errorf("schema is missing %q", required)
		}
	}
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &y); err != nil {
		return false
	}
	xs, _ := json.Marshal(x)
	ys, _ := json.Marshal(y)
	return bytes.Equal(xs, ys)
}
