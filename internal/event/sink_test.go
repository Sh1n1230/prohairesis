package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newSink(t *testing.T) *Sink {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PROHAIRESIS_HOME", filepath.Join(dir, "state"))
	return Open(filepath.Join(dir, "session"), "s-1")
}

func lines(t *testing.T, path string) []Event {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []Event
	for _, ln := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if ln == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(ln), &e); err != nil {
			t.Fatalf("line is not readable JSON: %v\n%s", err, ln)
		}
		out = append(out, e)
	}
	return out
}

func TestSequenceContinuesAcrossProcesses(t *testing.T) {
	s := newSink(t)
	for i := 0; i < 3; i++ {
		s.Append(New(Tool, "s-1", Actor{Adapter: "test"}))
	}
	// A second sink stands in for the next hook invocation: every event is
	// written by a process that has no memory of the last one.
	again := Open(filepath.Dir(s.Path), "s-1")
	again.Append(New(Tool, "s-1", Actor{Adapter: "test"}))

	got := lines(t, s.Path)
	for i, e := range got {
		if e.Seq != i+1 {
			t.Fatalf("event %d has seq %d; sequence numbers must not repeat or skip", i, e.Seq)
		}
	}
	if len(got) != 4 {
		t.Fatalf("wrote %d events, want 4", len(got))
	}
}

// Parallel tool calls mean parallel hooks. Two of them must not produce one
// mangled line or two events with the same number.
func TestConcurrentAppendsStayIntact(t *testing.T) {
	s := newSink(t)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Open(filepath.Dir(s.Path), "s-1").Append(New(Tool, "s-1", Actor{Adapter: "test"}))
		}()
	}
	wg.Wait()

	got := lines(t, s.Path)
	if len(got) != 24 {
		t.Fatalf("wrote %d events, want 24", len(got))
	}
	seen := map[int]bool{}
	for _, e := range got {
		if seen[e.Seq] {
			t.Fatalf("sequence number %d was used twice", e.Seq)
		}
		seen[e.Seq] = true
	}
}

// An event too large to write in one piece is shortened and says so. Silently
// shortening it would leave a record that reads as complete and is not.
func TestOversizeEventIsMarkedTruncated(t *testing.T) {
	s := newSink(t)
	e := New(Tool, "s-1", Actor{Adapter: "test"})
	e.Action = &Action{Kind: KindWrite, Tool: "Write"}
	for i := 0; i < 500; i++ {
		e.Action.Paths = append(e.Action.Paths, strings.Repeat("a", 40))
	}
	s.Append(e)

	got := lines(t, s.Path)
	if len(got) != 1 {
		t.Fatalf("wrote %d events, want 1", len(got))
	}
	if !got[0].Truncated {
		t.Fatal("an event that lost paths is not marked truncated")
	}
	b, _ := os.ReadFile(s.Path)
	if len(b) > MaxBytes {
		t.Fatalf("wrote %d bytes for one event, over the %d-byte ceiling", len(b), MaxBytes)
	}
}

// The failure this has to survive is not exotic: the state directory is gone, or
// belongs to someone else. The observation is lost either way -- what must not
// happen is losing the fact that it was lost.
func TestAnUnwritableLogRecordsTheLossElsewhere(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	t.Setenv("PROHAIRESIS_HOME", state)

	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A file where the session directory should be: MkdirAll cannot proceed.
	s := Open(filepath.Join(blocked, "session"), "s-1")
	s.Append(New(Tool, "s-1", Actor{Adapter: "test"}))

	n, _, err := LossCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("LossCount() = %d, want 1: a lost observation that leaves no trace "+
			"is indistinguishable from a quiet session", n)
	}
	if _, err := os.Stat(filepath.Join(state, LossFile)); err != nil {
		t.Fatalf("the loss went nowhere durable: %v", err)
	}
}

// The loss record must not become a second event log holding what the first one
// refuses to hold.
func TestLossRecordCarriesNoPayload(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROHAIRESIS_HOME", filepath.Join(dir, "state"))
	RecordLoss("s-1", "events", os.ErrPermission)

	b, err := os.ReadFile(filepath.Join(dir, "state", LossFile))
	if err != nil {
		t.Fatal(err)
	}
	var l Loss
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatal(err)
	}
	if l.Schema != LossSchema || l.Reason == "" || l.Stage != "events" {
		t.Fatalf("loss record is malformed: %s", b)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"action", "argv", "command", "paths", "event"} {
		if _, present := raw[forbidden]; present {
			t.Fatalf("the loss record carries %q; it must carry the fact of a loss "+
				"and nothing about the observation", forbidden)
		}
	}
}

// Reading a log has to report its own holes. Everything downstream treats
// silence as "nothing happened", and a gap is the one case where that is wrong.
func TestReadReportsGapsAndUnreadableLines(t *testing.T) {
	dir := t.TempDir()
	body := `{"schema":"harness.event.v1","seq":1,"type":"tool"}
{"schema":"harness.event.v1","seq":4,"type":"tool"}
{"schema":"harness.event.v1","seq":5,"typ`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	log, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if log.Gaps != 2 {
		t.Fatalf("Gaps = %d, want 2", log.Gaps)
	}
	if log.Unreadable != 1 {
		t.Fatalf("Unreadable = %d, want 1", log.Unreadable)
	}
}
